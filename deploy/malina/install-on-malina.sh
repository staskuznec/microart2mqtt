#!/bin/sh
# Установка/обновление агента microart2mqtt на «Малине».
#
# Ставим по-человечески, службой systemd. Корень у МикроАрт смонтирован ro,
# поэтому на время установки перемонтируем его на запись и возвращаем обратно —
# ровно так же, как это делает их собственная веб-морда (mw_partition.sh).
#
# Маскировка юнитов у них касается ТОЛЬКО их служб (ssh, getty, journald);
# нашему собственному юниту она не мешает.
#
# Что делает:
#   1. кладёт бинарник и agent-ctl в /settings/microart-mqtt (раздел на запись);
#   2. ставит и включает службу microart-mqtt.service (systemd);
#   3. проксирует агент через их nginx на /mqtt/ (во всех шаблонах конфига);
#   4. добавляет кнопку MQTT в конец меню на всех страницах веб-морды.
#
# Запускается любым способом: из бутстрапа при загрузке, из консоли, из веба.
# Идемпотентен. Рядом должны лежать malina-agent (ARM), agent-ctl.sh и
# microart-mqtt.service.
set -u

SRC="$(cd "$(dirname "$0")" && pwd)"
# Пути можно переопределить окружением — так установщик целиком прогоняется в
# песочнице на обычной машине. На устройстве используются значения по умолчанию.
DIR=${DIR:-/settings/microart-mqtt}
WEB=${WEB:-/settings/html}
CFG=${CFG:-/settings/web-data/mqtt.json}
NGINX_DIR=${NGINX_DIR:-/settings/nginx/sites-available}
UNIT=${UNIT:-/etc/systemd/system/microart-mqtt.service}
SERVICE=${SERVICE:-microart-mqtt}
PORT=${PORT:-8091}

say() { echo "install: $*"; }

# Агенту нужно секунду-другую, чтобы подняться; ждём до 10 с, а не гадаем.
wait_active() {
    i=0
    while [ $i -lt 10 ]; do
        systemctl is-active --quiet "$SERVICE" 2>/dev/null && return 0
        sleep 1
        i=$((i+1))
    done
    return 1
}

# Файлы рядом могут называться и длинно (копировали руками), и в 8.3 — так их
# кладёт в образ fat_put.py, и на vfat они видны заглавными. Ищем оба варианта.
pick() {
    for n in "$@"; do [ -f "$SRC/$n" ] && { printf '%s' "$SRC/$n"; return 0; }; done
    return 1
}
AGENT_BIN="$(pick malina-agent AGENT agent)" || {
    echo "install: рядом нет бинарника агента (malina-agent или AGENT)" >&2; exit 1; }
CTL_SRC="$(pick agent-ctl.sh AGENTCTL.SH)" || CTL_SRC=""
UNIT_SRC="$(pick microart-mqtt.service AGENT.SRV)" || UNIT_SRC=""

# Корень на запись/чтение. Возврат в ro делаем в любом случае — через trap,
# чтобы не оставить систему в rw при ошибке на середине.
root_rw() { mount -o remount,rw / 2>/dev/null; }
root_ro() { sync; mount -o remount,ro / 2>/dev/null; }
trap root_ro EXIT INT TERM

# --- бинарник и управляющий скрипт (на /settings, там запись и так есть) -----
mkdir -p "$DIR"
# Бинарник ставим, только если в бандле он НЕ старее установленного. Иначе
# получалось бы вот что: обновились кнопкой в вебе, устройство перезагрузилось,
# бутстрап снова запустил этот установщик — и версия с карты откатила свежую.
# Карта обновляется редко, веб — часто, поэтому побеждает та, что новее.
ver_of() { [ -x "$1" ] && "$1" -version 2>/dev/null | tr -d 'v \t\r\n' || echo ""; }
new_ver="$(ver_of "$AGENT_BIN")"
cur_ver="$(ver_of "$DIR/malina-agent")"

if [ -z "$cur_ver" ]; then
    install -m 0755 "$AGENT_BIN" "$DIR/malina-agent"
    say "агент установлен в $DIR (версия ${new_ver:-неизвестна})"
elif [ "$new_ver" = "$cur_ver" ]; then
    say "агент уже версии ${cur_ver} — оставляю как есть"
elif [ -n "$new_ver" ] && \
     [ "$(printf '%s\n%s\n' "$new_ver" "$cur_ver" | sort -V | tail -1)" = "$new_ver" ]; then
    install -m 0755 "$AGENT_BIN" "$DIR/malina-agent"
    say "агент обновлён с ${cur_ver} до ${new_ver}"
else
    say "на карте версия ${new_ver:-неизвестна}, установлена ${cur_ver} — не откатываю"
fi

[ -n "$CTL_SRC" ] && install -m 0755 "$CTL_SRC" "$DIR/agent-ctl.sh"

# --- настройки по умолчанию, если ещё нет -----------------------------------
if [ ! -f "$CFG" ]; then
    mkdir -p "$(dirname "$CFG")"
    cat > "$CFG" <<JSON
{
  "enabled": false,
  "broker": "",
  "username": "",
  "password": "",
  "base_topic": "microart/inv1",
  "qos": 0,
  "retain": true,
  "interval_sec": 5,
  "republish_sec": 600,
  "web_port": $PORT
}
JSON
    chmod 0664 "$CFG"
    say "создан $CFG (агент выключен — задайте брокер в вебе)"
fi

# --- служба systemd (нужен rw на корне) --------------------------------------
if root_rw; then
    say "корень перемонтирован на запись"
    if [ -n "$UNIT_SRC" ]; then
        install -m 0644 "$UNIT_SRC" "$UNIT"
        systemctl daemon-reload 2>/dev/null
        systemctl enable "$SERVICE" >/dev/null 2>&1
        systemctl restart "$SERVICE" 2>/dev/null
        if wait_active; then
            say "служба $SERVICE установлена и запущена (systemd)"
        elif [ -n "$cur_ver" ] && [ "$cur_ver" != "$new_ver" ]; then
            # Установленная версия (обновлялись через веб) не поднимается.
            # Возвращаем версию с карты: она заведомо рабочая, а иначе устройство
            # осталось бы без веб-интерфейса — то есть без способа починить его
            # удалённо, и пришлось бы ехать с картой.
            say "служба не поднялась на версии ${cur_ver} — откатываю к версии с карты (${new_ver:-неизвестна})"
            install -m 0755 "$AGENT_BIN" "$DIR/malina-agent"
            systemctl restart "$SERVICE" 2>/dev/null
            if wait_active; then
                say "откат удался, служба работает"
            else
                say "служба не запускается и после отката — смотрите http://<ip>/mqtt-agent.txt"
            fi
        else
            say "служба установлена, но не активна — смотрите http://<ip>/mqtt-agent.txt"
        fi
    else
        say "рядом нет файла службы — systemd-юнит не установлен"
    fi
    root_ro && say "корень возвращён в режим только чтения"
else
    say "не удалось перемонтировать корень на запись — ставлю без systemd"
    "$DIR/agent-ctl.sh" restart
fi

# --- проброс агента через nginx на /mqtt/ (порт 80 точно доступен) -----------
# У них есть шаблоны default_e / default_r (открытый / парольный режим доступа),
# и при переключении они делают cp default_* -> default + reload nginx, затирая
# наш location. Поэтому патчим ВСЕ файлы sites-available/default*.
for site in "$NGINX_DIR"/default "$NGINX_DIR"/default_e "$NGINX_DIR"/default_r; do
    [ -f "$site" ] || continue
    grep -q "location /mqtt/" "$site" && continue
    cp "$site" "$site.before-mqtt"
    awk '
        {print}
        /root[ \t]+\/var\/www\/html/ && !done {
            print "    location /mqtt/ { proxy_pass http://127.0.0.1:'"$PORT"'/; proxy_set_header Host $host; proxy_http_version 1.1; }"
            done=1
        }
    ' "$site.before-mqtt" > "$site"
    grep -q "location /mqtt/" "$site" || mv -f "$site.before-mqtt" "$site"
done
if nginx -t >/dev/null 2>&1; then
    for b in "$NGINX_DIR"/default*.before-mqtt; do [ -f "$b" ] && rm -f "$b"; done
    nginx -s reload >/dev/null 2>&1 || service nginx reload >/dev/null 2>&1 || true
    say "nginx: /mqtt/ -> агент во всех шаблонах"
else
    for b in "$NGINX_DIR"/default*.before-mqtt; do [ -f "$b" ] && mv -f "$b" "${b%.before-mqtt}"; done
    say "nginx: конфиг не принят, откатил (агент только на :$PORT)"
fi

# --- кнопка MQTT в навбар веб-морды (на ВСЕХ страницах с меню) ---------------
# Навбар у них свой на каждой странице. Ставим в КОНЕЦ меню: перед закрывающим
# </ul> списка nav-pills (ищем подсчётом вложенности <ul>/</ul>). Прежние наши
# пункты сносим по классу иконки — чтобы не плодить дубли и переставить в конец.
ITEM='                    <li role="presentation" class="normal"><a href="/mqtt/"><span class="glyphicon glyphicon-cloud-upload" aria-hidden="true"></span>&nbsp;MQTT</a></li>'
added=0
for page in "$WEB"/*.php; do
    [ -f "$page" ] || continue
    grep -q 'nav nav-pills' "$page" || continue
    cp "$page" "$page.before-mqtt"
    grep -v 'glyphicon-cloud-upload' "$page.before-mqtt" > "$page.tmp1"
    awk -v item="$ITEM" '
        {
            if ($0 ~ /<ul class="nav nav-pills">/) innav=1
            if (innav && !done) {
                t=$0; o=gsub(/<ul/,"",t)
                t=$0; c=gsub(/<\/ul>/,"",t)
                nd=depth+o-c
                if (c>0 && nd<=0) { print item; done=1; innav=0 }
                depth=nd
            }
            print
        }
    ' "$page.tmp1" > "$page"
    rm -f "$page.tmp1"
    if grep -q 'href="/mqtt/"' "$page"; then
        rm -f "$page.before-mqtt"; added=$((added+1))
    else
        mv -f "$page.before-mqtt" "$page"
    fi
done
say "кнопка MQTT добавлена на страниц: $added"

say "готово. Открывайте http://<ip>/mqtt/  (или кнопка MQTT в меню)"
say "служба:  systemctl status $SERVICE   |   лог: http://<ip>/mqtt-agent.txt"
