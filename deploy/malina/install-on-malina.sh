#!/bin/sh
# Установка/обновление агента microart2mqtt на «Малине». Кладёт файлы на раздел
# /settings (он смонтирован на запись) и запускает агент. Автозапуск при
# загрузке обеспечивает приписка в /usr/sbin/myfolders.sh (см. malina_bootstrap.py),
# которая и вызывает этот скрипт, — поэтому здесь корень (ro) не трогаем.
#
# Запускается любым способом: из консоли, из бутстрапа при загрузке (лежит на
# /boot/microart/) или разово из веба. Идемпотентен.
#
#   sh install-on-malina.sh
#
# Рядом должны лежать: malina-agent (ARM), agent-ctl.sh, *.php.
set -u

SRC="$(cd "$(dirname "$0")" && pwd)"
DIR=/settings/microart-mqtt
WEB=/settings/html
CFG=/settings/web-data/mqtt.json
MENU="$WEB/load_menu.php"

say() { echo "install: $*"; }

[ -f "$SRC/malina-agent" ] || { echo "install: рядом нет malina-agent" >&2; exit 1; }

# --- бинарник и управляющий скрипт (на /settings, запись разрешена) ---------
mkdir -p "$DIR"
install -m 0755 "$SRC/malina-agent" "$DIR/malina-agent"
install -m 0755 "$SRC/agent-ctl.sh" "$DIR/agent-ctl.sh"
say "агент установлен в $DIR"

# --- страницы веб-морды -----------------------------------------------------
for f in "$SRC"/*.php; do
    [ -f "$f" ] && install -m 0644 "$f" "$WEB/$(basename "$f")"
done
say "страницы MQTT установлены в $WEB"

# --- настройки по умолчанию, если ещё нет -----------------------------------
if [ ! -f "$CFG" ]; then
    mkdir -p "$(dirname "$CFG")"
    cat > "$CFG" <<'JSON'
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
  "metrics": []
}
JSON
    chmod 0664 "$CFG"
    say "создан $CFG (агент выключен — задайте брокер в вебе)"
fi

# --- кнопка MQTT в навбар веб-морды (меню — в index.php, тоже на /settings) --
IDX="$WEB/index.php"
ITEM='                    <li role="presentation" class="normal"><a href="mqtt.php"><span class="glyphicon glyphicon-cloud-upload" aria-hidden="true"></span>&nbsp;MQTT</a></li>'
if [ -f "$IDX" ] && ! grep -q 'href="mqtt.php"' "$IDX"; then
    cp "$IDX" "$IDX.before-mqtt"
    # Вставляем пункт первым в списке — сразу после <ul class="nav nav-pills">.
    awk -v item="$ITEM" '
        {print}
        /<ul class="nav nav-pills">/ && !done {print item; done=1}
    ' "$IDX.before-mqtt" > "$IDX" 2>/dev/null
    if grep -q 'href="mqtt.php"' "$IDX"; then
        say "кнопка MQTT добавлена в меню"
    else
        cp "$IDX.before-mqtt" "$IDX"
        say "не удалось добавить кнопку в меню (разметка иная) — откатил index.php"
    fi
fi

# --- запуск -----------------------------------------------------------------
"$DIR/agent-ctl.sh" restart
say "готово. Раздел «MQTT» в веб-морде -> задайте брокер."
