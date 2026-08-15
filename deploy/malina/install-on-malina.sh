#!/bin/sh
# Установка/обновление агента microart2mqtt на «Малине». Кладёт бинарник на
# раздел /settings (он смонтирован на запись; корень ro не трогаем), добавляет
# кнопку MQTT в меню веб-морды и запускает агент. Автозапуск при загрузке даёт
# приписка в /usr/sbin/myfolders.sh (см. malina_bootstrap.py), которая и
# вызывает этот скрипт.
#
# Весь веб-интерфейс — внутри самого агента (порт 8091), поэтому PHP-страниц
# больше нет: обновили бинарник — обновился и интерфейс. Здесь только бинарник,
# управляющий скрипт и одна ссылка-кнопка в чужом меню.
#
# Запускается любым способом: из бутстрапа при загрузке, из консоли, из веба.
# Идемпотентен. Рядом должны лежать malina-agent (ARM) и agent-ctl.sh.
set -u

SRC="$(cd "$(dirname "$0")" && pwd)"
DIR=/settings/microart-mqtt
WEB=/settings/html
CFG=/settings/web-data/mqtt.json
IDX="$WEB/index.php"
PORT=8091

say() { echo "install: $*"; }

[ -f "$SRC/malina-agent" ] || { echo "install: рядом нет malina-agent" >&2; exit 1; }

# --- бинарник и управляющий скрипт (на /settings, запись разрешена) ----------
mkdir -p "$DIR"
install -m 0755 "$SRC/malina-agent" "$DIR/malina-agent"
install -m 0755 "$SRC/agent-ctl.sh" "$DIR/agent-ctl.sh"
say "агент установлен в $DIR"

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

# --- кнопка MQTT в навбар веб-морды -----------------------------------------
# Ссылка ведёт на веб-страницу агента (порт 8091 на этом же хосте). Адрес хоста
# берём из браузера через onclick — не нужно знать IP на момент установки.
ITEM='                    <li role="presentation" class="normal"><a href="#" onclick="window.location.href=(location.protocol+&#39;//&#39;+location.hostname+&#39;:'"$PORT"'/&#39;);return false;"><span class="glyphicon glyphicon-cloud-upload" aria-hidden="true"></span>&nbsp;MQTT</a></li>'
if [ -f "$IDX" ] && ! grep -q "location.hostname+':$PORT" "$IDX" 2>/dev/null && ! grep -q ":$PORT/" "$IDX"; then
    cp "$IDX" "$IDX.before-mqtt"
    awk -v item="$ITEM" '
        {print}
        /<ul class="nav nav-pills">/ && !done {print item; done=1}
    ' "$IDX.before-mqtt" > "$IDX" 2>/dev/null
    if grep -q ":$PORT/" "$IDX"; then
        say "кнопка MQTT добавлена в меню"
    else
        cp "$IDX.before-mqtt" "$IDX"
        say "не удалось добавить кнопку (иная разметка) — откатил index.php"
    fi
else
    say "кнопка MQTT уже в меню или index.php не найден"
fi

# --- запуск -----------------------------------------------------------------
"$DIR/agent-ctl.sh" restart
say "готово. Веб агента: http://<ip>:$PORT/  (или кнопка MQTT в меню)"
