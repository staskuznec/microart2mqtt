#!/bin/sh
# Установка и обновление демона microart2mqtt.
#
#   curl -fsSL https://github.com/staskuznec/microart2mqtt/releases/latest/download/install.sh | sudo sh
#
# Ссылка на релиз, а не на raw в ветке: во-первых, так берётся выпущенная
# версия, а не то, что сейчас в main; во-вторых, GitHub считает скачивания
# файлов релиза, и по ним видно, сколько раз устанавливали.
#
# Скрипт ставит бинарник в /opt/microart2mqtt, заводит службу systemd и
# запускает её. Повторный запуск обновляет: база с настройками, инверторами и
# метриками лежит отдельно, в /var/lib/microart2mqtt, и не трогается.
#
# POSIX sh намеренно: на сервере умного дома bash может и не стоять.
set -eu

REPO="staskuznec/microart2mqtt"
BIN_DIR="${BIN_DIR:-/opt/microart2mqtt}"
STATE_DIR="${STATE_DIR:-/var/lib/microart2mqtt}"
SERVICE="microart2mqtt"
ADDR="${ADDR:-}"

# Корень веб-сервера, где лежит панель умного дома, и подкаталог, в котором
# рядом с ней встанет демон. Панель обычно в <корень>/MimiSetup — по ней корень
# и опознаётся.
WEB_ROOT="${WEB_ROOT:-}"
BASE_PATH="${BASE_PATH:-/microart}"

find_web_root() {
  [ -z "$WEB_ROOT" ] || { printf '%s' "$WEB_ROOT"; return 0; }
  for d in /home/html /var/www/html /home/sh2/web /var/www; do
    [ -d "$d/MimiSetup" ] && { printf '%s' "$d"; return 0; }
  done
  printf ''
}
WEB_ROOT=$(find_web_root)

say() { printf '%s\n' "$*"; }
die() { printf 'ошибка: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "нужны права root: запустите через sudo"

# --- диалог с человеком ----------------------------------------------------
#
# Скрипт обычно запускают через "curl … | sh", и тогда стандартный ввод занят
# самим скриптом: обычный read съел бы его текст вместо ответа. Поэтому все
# вопросы читаются напрямую с терминала.
if [ -r /dev/tty ] && [ -w /dev/tty ]; then
  INTERACTIVE=yes
else
  INTERACTIVE=no
fi

ask() { # ask "вопрос" "по умолчанию"
  _ans=""
  if [ "$INTERACTIVE" = yes ]; then
    printf '%s' "$1" > /dev/tty
    read -r _ans < /dev/tty || _ans=""
  fi
  [ -n "$_ans" ] || _ans="$2"
  printf '%s' "$_ans"
}

TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

# --- какая платформа -------------------------------------------------------
detect_asset() {
  _os=$(uname -s)
  [ "$_os" = "Linux" ] || die "поддерживается только Linux, а здесь $_os"

  case "$(uname -m)" in
    x86_64 | amd64) printf 'microart2mqtt-linux-amd64' ;;
    aarch64 | arm64) printf 'microart2mqtt-linux-arm64' ;;
    armv7* | armv6* | arm) printf 'microart2mqtt-linux-armv7' ;;
    *) die "неизвестная архитектура $(uname -m)" ;;
  esac
}
ASSET=$(detect_asset)

# --- скачиваем и сверяем ---------------------------------------------------
BASE="https://github.com/$REPO/releases/latest/download"

download() { # download <имя файла> <куда>
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$BASE/$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$BASE/$1"
  else
    die "нужен curl или wget"
  fi
}

say "Скачиваем $ASSET…"
download "$ASSET" "$TMP/$ASSET" || die "не удалось скачать $ASSET"

# Сверка суммы обязательна: скрипт скачивает исполняемый файл по сети и ставит
# его в систему. Без проверки это означало бы «запустить то, что пришло».
if download "SHA256SUMS" "$TMP/SHA256SUMS" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    _want=$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1}' "$TMP/SHA256SUMS")
    _have=$(sha256sum "$TMP/$ASSET" | awk '{print $1}')
    [ -n "$_want" ] || die "в SHA256SUMS нет строки для $ASSET"
    [ "$_want" = "$_have" ] || die "контрольная сумма не сошлась, установка отменена"
    say "Контрольная сумма сошлась."
  else
    say "Предупреждение: нет sha256sum, сумму проверить не удалось."
  fi
else
  say "Предупреждение: файл сумм не скачался, сумму проверить не удалось."
fi

chmod +x "$TMP/$ASSET"

# --- веб-сервер: демон рядом с панелью умного дома -------------------------
#
# На сервере уже стоит веб-сервер с панелью MimiSetup. Логично открывать демон
# оттуда же — http://сервер/microart/, — а не отдельным портом: один адрес,
# одна точка входа, и порт 8081 не надо помнить и открывать.
PROXY=no

setup_proxy() {
  if [ "${SKIP_PROXY:-}" = "1" ]; then
    return 0
  fi

  WEBSRV=""
  if command -v apache2ctl >/dev/null 2>&1 || command -v apachectl >/dev/null 2>&1; then
    WEBSRV="apache"
  elif command -v nginx >/dev/null 2>&1; then
    WEBSRV="nginx"
  else
    say "Веб-сервер не найден — демон будет доступен отдельным портом."
    return 0
  fi

  say ""
  say "Найден веб-сервер: $WEBSRV."
  say "Управление инверторами можно открыть рядом с панелью умного дома,"
  say "по адресу http://сервер$BASE_PATH/ — вместо отдельного порта 8081."
  ans=$(ask "Настроить? [Д/н]: " "д")
  case "$ans" in
    [нНnN]*) return 0 ;;
  esac

  if [ "$WEBSRV" = "apache" ]; then
    # conf-available — штатный механизм apache: конфиг применяется ко всем
    # виртуальным хостам, и чужие файлы при этом не правятся.
    a2enmod proxy proxy_http >/dev/null 2>&1 || true
    cat > /etc/apache2/conf-available/microart2mqtt.conf <<PROXYEOF
# Демон microart2mqtt рядом с панелью умного дома.
# Поставлено install.sh; удалить: a2disconf microart2mqtt
<Location $BASE_PATH/>
    ProxyPass        http://127.0.0.1:8081$BASE_PATH/
    ProxyPassReverse http://127.0.0.1:8081$BASE_PATH/
</Location>
PROXYEOF
    a2enconf microart2mqtt >/dev/null 2>&1 || true
    if apache2ctl configtest >/dev/null 2>&1 || apachectl configtest >/dev/null 2>&1; then
      systemctl reload apache2 2>/dev/null || service apache2 reload 2>/dev/null || true
      PROXY=yes
      say "Apache настроен."
    else
      say "Предупреждение: apache не принял конфиг, оставляем отдельный порт."
      a2disconf microart2mqtt >/dev/null 2>&1 || true
    fi
    return 0
  fi

  # nginx: вставлять location в чужой server-блок автоматически нельзя —
  # так проще всего уронить работающую панель. Кладём готовый кусок и
  # объясняем, куда его подключить.
  mkdir -p /etc/nginx/snippets
  cat > /etc/nginx/snippets/microart2mqtt.conf <<PROXYEOF
# Демон microart2mqtt рядом с панелью умного дома.
location $BASE_PATH/ {
    proxy_pass http://127.0.0.1:8081$BASE_PATH/;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
}
PROXYEOF
  say ""
  say "Для nginx готов кусок конфига: /etc/nginx/snippets/microart2mqtt.conf"
  say "Добавьте в нужный server-блок строку:"
  say "    include snippets/microart2mqtt.conf;"
  say "и выполните: nginx -t && systemctl reload nginx"
  say "Автоматически не вставляем — так проще всего уронить работающую панель."
  say ""
  ans=$(ask "Демон уже подключён к nginx этим сниппетом? [д/Н]: " "н")
  case "$ans" in
    [дДyY]*) PROXY=yes ;;
  esac
}

# add_panel_tab добавляет вкладку «Инверторы» в верхнее меню панели MimiSetup.
#
# Сам app.js не трогаем: он собран Sencha Cmd в один минифицированный файл, и
# правка в нём не переживёт обновления панели, а найти её потом невозможно.
# Вместо этого кладём рядом свой файл и подключаем его строкой в index.php —
# читаемой, восстановимой и заметной.
add_panel_tab() {
  PANEL="$WEB_ROOT/MimiSetup"
  [ -d "$PANEL" ] && [ -f "$PANEL/index.php" ] || return 0

  ans=$(ask "Добавить вкладку «Инверторы» в меню панели MimiSetup? [Д/н]: " "д")
  case "$ans" in
    [нНnN]*) return 0 ;;
  esac

  # Файл вкладки всегда перезаписываем: он наш, и в нём мог поменяться адрес.
  download "mimisetup-microart-tab.js" "$PANEL/microart-tab.js" 2>/dev/null || {
    say "Не удалось получить файл вкладки — пропускаем."
    return 0
  }

  # Адрес демона во вкладке. За прокси это подкаталог того же сервера, без
  # прокси — прямой порт: вкладка полезна в обоих случаях, просто во втором
  # адрес абсолютный.
  if [ "$PROXY" = yes ]; then
    TAB_URL="$BASE_PATH/"
  else
    TAB_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -n "$TAB_IP" ] || TAB_IP="127.0.0.1"
    TAB_URL="http://$TAB_IP:8081/"
  fi

  # Правим через временный файл: у sed -i разный синтаксис в GNU и BSD, и
  # полагаться на конкретный означает однажды получить пустой файл.
  sed "s#var DAEMON_URL = \"/microart/\";#var DAEMON_URL = \"$TAB_URL\";#" \
    "$PANEL/microart-tab.js" > "$PANEL/microart-tab.js.tmp" &&
    mv -f "$PANEL/microart-tab.js.tmp" "$PANEL/microart-tab.js"
  chmod 0644 "$PANEL/microart-tab.js" 2>/dev/null || true

  if grep -q "microart-tab.js" "$PANEL/index.php" 2>/dev/null; then
    say "Вкладка «Инверторы» в панели уже подключена."
    return 0
  fi

  # Резервная копия перед правкой чужого файла: восстановить должно быть проще,
  # чем разбираться, что сломалось.
  cp "$PANEL/index.php" "$PANEL/index.php.before-microart" 2>/dev/null || true

  # Строка вставляется сразу после подключения app.js — к этому моменту
  # фреймворк уже есть, а панель ещё не построена. Через awk, а не sed -i:
  # у последнего разный синтаксис в GNU и BSD.
  awk '{
    print
    if ($0 ~ /src="app\.js/ && !done) {
      print "<script type=\"text/javascript\" src=\"microart-tab.js\"></script>"
      done = 1
    }
  }' "$PANEL/index.php" > "$PANEL/index.php.tmp" 2>/dev/null

  if [ -s "$PANEL/index.php.tmp" ] && grep -q "microart-tab.js" "$PANEL/index.php.tmp"; then
    mv -f "$PANEL/index.php.tmp" "$PANEL/index.php"
    say "Вкладка «Инверторы» добавлена в меню панели."
    say "Обновление панели её снесёт — запустите этот скрипт повторно, и она вернётся."
  else
    rm -f "$PANEL/index.php.tmp"
    say "Не удалось дописать index.php панели. Добавьте вручную после подключения app.js:"
    say '    <script type="text/javascript" src="microart-tab.js"></script>'
  fi
}

setup_proxy || say "Предупреждение: настроить веб-сервер не удалось."
add_panel_tab || say "Предупреждение: вкладку в панель добавить не удалось."

# За прокси демону незачем слушать наружу: снаружи к нему ходят через панель.
if [ -z "$ADDR" ]; then
  if [ "$PROXY" = yes ]; then
    ADDR="127.0.0.1:8081"
  else
    ADDR="0.0.0.0:8081"
    BASE_PATH=""
  fi
fi

# --- пользователь и каталоги ----------------------------------------------
if ! id microart2mqtt >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin microart2mqtt 2>/dev/null \
    || adduser --system --no-create-home --shell /usr/sbin/nologin microart2mqtt 2>/dev/null \
    || say "предупреждение: не удалось завести пользователя, служба пойдёт от root"
fi

mkdir -p "$BIN_DIR" "$STATE_DIR"
chown microart2mqtt:microart2mqtt "$STATE_DIR" 2>/dev/null || true
chmod 0700 "$STATE_DIR"

# --- ставим ----------------------------------------------------------------
UPDATE=no
[ -f "$BIN_DIR/microart2mqtt" ] && UPDATE=yes

# Старый бинарник сохраняем: откат должен быть в одно движение.
if [ "$UPDATE" = yes ]; then
  cp -f "$BIN_DIR/microart2mqtt" "$BIN_DIR/microart2mqtt.old" 2>/dev/null || true
  systemctl stop "$SERVICE" 2>/dev/null || true
fi

install -m 0755 "$TMP/$ASSET" "$BIN_DIR/microart2mqtt"
ln -sf "$BIN_DIR/microart2mqtt" /usr/local/bin/microart2mqtt 2>/dev/null || true

# --- служба ----------------------------------------------------------------
cat > "/etc/systemd/system/$SERVICE.service" <<UNITEOF
[Unit]
Description=Мост MicroArt -> MQTT
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN_DIR/microart2mqtt --addr $ADDR --base-path $BASE_PATH --db $STATE_DIR/microart2mqtt.db
Restart=always
RestartSec=10s

StandardOutput=journal
StandardError=journal

User=microart2mqtt
Group=microart2mqtt

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectControlGroups=true
ReadWritePaths=$STATE_DIR
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
MemoryMax=128M

[Install]
WantedBy=multi-user.target
UNITEOF

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1 || true
systemctl restart "$SERVICE"

sleep 1
if ! systemctl is-active --quiet "$SERVICE"; then
  say ""
  say "Служба не запустилась. Последние строки журнала:"
  journalctl -u "$SERVICE" -n 20 --no-pager 2>/dev/null || true
  if [ "$UPDATE" = yes ] && [ -f "$BIN_DIR/microart2mqtt.old" ]; then
    say ""
    say "Откатиться на прежнюю версию:"
    say "  mv $BIN_DIR/microart2mqtt.old $BIN_DIR/microart2mqtt && systemctl restart $SERVICE"
  fi
  exit 1
fi

VERSION=$("$BIN_DIR/microart2mqtt" --version 2>/dev/null || echo "?")

say ""
if [ "$UPDATE" = yes ]; then
  say "Обновлено до версии $VERSION. Настройки и список инверторов сохранены."
else
  say "Установлена версия $VERSION."
fi
say ""
if [ "$PROXY" = yes ]; then
  say "Управление: http://<адрес сервера>$BASE_PATH/"
else
  say "Управление: http://<адрес сервера>:${ADDR##*:}/"
fi
say ""
say "Что дальше:"
say "  1. Откройте «Настройки» и укажите адрес MQTT-брокера."
say "  2. Добавьте инверторы — адрес «Малины» такой же, как в браузере."
say "  3. Метрики можно взять готовым набором или выбрать из живых полей API."
say ""
say "Журнал:      journalctl -u $SERVICE -f"
say "Перезапуск:  systemctl restart $SERVICE"
