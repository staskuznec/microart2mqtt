#!/bin/sh
# Управление агентом microart2mqtt на «Малине».
#
# Единая точка для веб-страницы и загрузочного хука. Веб (nginx+php-fpm) ходит
# сюда через sudo — на «Малине» www-data имеет NOPASSWD: ALL, поэтому вся
# работа с процессом и файлами вынесена в этот скрипт, а PHP лишь его дёргает.
#
#   agent-ctl.sh boot            запуск при загрузке (из myfolders.sh)
#   agent-ctl.sh start|stop|restart
#   agent-ctl.sh reload          перечитать настройки (SIGUSR1)
#   agent-ctl.sh status          running|stopped и версия
#   agent-ctl.sh update <url>    скачать новый бинарник, проверить, заменить
#
# POSIX sh: на «Малине» это Debian jessie, bash есть, но не обязателен.
set -u

DIR=/settings/microart-mqtt
BIN="$DIR/malina-agent"
CFG=/settings/web-data/mqtt.json
LOG=/var/log/malina-agent.log
PIDFILE=/var/run/malina-agent.pid
NAME=malina-agent

log() { echo "$(date '+%F %T') agent-ctl: $*"; }

is_running() {
    pidof "$NAME" >/dev/null 2>&1
}

start() {
    [ -x "$BIN" ] || { log "нет бинарника $BIN"; return 1; }
    if is_running; then
        log "уже запущен"
        return 0
    fi
    # start-stop-daemon есть в Debian; если вдруг нет — запускаем напрямую.
    if command -v start-stop-daemon >/dev/null 2>&1; then
        start-stop-daemon --start --background --make-pidfile --pidfile "$PIDFILE" \
            --exec "$BIN" -- --config "$CFG"
    else
        "$BIN" --config "$CFG" >>"$LOG" 2>&1 &
        echo $! > "$PIDFILE"
    fi
    log "запущен"
}

stop() {
    if command -v start-stop-daemon >/dev/null 2>&1; then
        start-stop-daemon --stop --oknodo --retry TERM/5/KILL/5 \
            --pidfile "$PIDFILE" --name "$NAME" 2>/dev/null
    fi
    # Подстраховка: добить по имени, если pidfile разошёлся с реальностью.
    kill -TERM "$(pidof "$NAME" 2>/dev/null)" 2>/dev/null || true
    rm -f "$PIDFILE"
    log "остановлен"
}

reload() {
    if is_running; then
        kill -USR1 "$(pidof "$NAME")" 2>/dev/null && log "перечитывает настройки"
    else
        # Не запущен, но настройки просят работать — поднимем.
        start
    fi
}

update() {
    url="${1:-}"
    [ -n "$url" ] || { log "update: не задан URL"; return 2; }

    tmp="$(mktemp /tmp/malina-agent.XXXXXX)" || return 1
    sums="$(mktemp /tmp/malina-sums.XXXXXX)" || return 1
    trap 'rm -f "$tmp" "$sums"' EXIT

    log "скачиваю $url"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$tmp" || { log "скачать не удалось"; return 1; }
    else
        wget -qO "$tmp" "$url" || { log "скачать не удалось"; return 1; }
    fi

    # Сверка суммы, если рядом с бинарником лежит SHA256SUMS (как в наших релизах).
    sumurl="$(dirname "$url")/SHA256SUMS"
    asset="$(basename "$url")"
    if command -v sha256sum >/dev/null 2>&1; then
        if curl -fsSL "$sumurl" -o "$sums" 2>/dev/null || wget -qO "$sums" "$sumurl" 2>/dev/null; then
            want="$(awk -v a="$asset" '$2==a || $2=="*"a {print $1}' "$sums")"
            if [ -n "$want" ]; then
                have="$(sha256sum "$tmp" | awk '{print $1}')"
                [ "$want" = "$have" ] || { log "контрольная сумма не сошлась"; return 1; }
                log "контрольная сумма сошлась"
            fi
        fi
    fi

    # Проверяем, что скачанное вообще запускается на этом железе.
    chmod +x "$tmp"
    if ! "$tmp" -version >/dev/null 2>&1; then
        log "новый бинарник не запускается — отмена"
        return 1
    fi
    newver="$("$tmp" -version 2>/dev/null)"

    mkdir -p "$DIR"
    # Прежний бинарник сохраняем: откат в одно движение.
    [ -f "$BIN" ] && cp -f "$BIN" "$BIN.old"
    mv -f "$tmp" "$BIN"
    chmod +x "$BIN"
    sync
    trap - EXIT
    rm -f "$sums"

    log "обновлён до $newver, перезапускаю"
    restart
}

restart() { stop; start; }

status() {
    if is_running; then
        printf 'running %s\n' "$("$BIN" -version 2>/dev/null || echo '?')"
    else
        printf 'stopped %s\n' "$([ -x "$BIN" ] && "$BIN" -version 2>/dev/null || echo 'not-installed')"
    fi
}

case "${1:-}" in
    boot)    start ;;
    start)   start ;;
    stop)    stop ;;
    restart) restart ;;
    reload)  reload ;;
    update)  shift; update "$@" ;;
    status)  status ;;
    *) echo "usage: $0 {boot|start|stop|restart|reload|status|update <url>}"; exit 2 ;;
esac
