#!/usr/bin/env python3
"""Вписать в образ «Малины» консольный вход и автозапуск агента.

SSH на этой прошивке не поднять — МикроАрт вырезал бинарник sshd. Единственный
надёжный вход — консоль на мониторе, а агент должен стартовать сам. И то и
другое делается одной припиской в /usr/sbin/myfolders.sh: этот скрипт точно
выполняется при загрузке (его тянет myinit.service). Вставляем ПЕРЕД финальным
'exit 0' — иначе строки не выполнятся:

  * автологин pi на tty1 (шелл на мониторе, без пароля);
  * запуск /boot/microart/install-on-malina.sh, если он там лежит
    (установка/обновление агента с раздела /boot, который виден в macOS).

Правка идемпотентна и в пределах блока файла (размер меняется, метаданные — нет;
metadata_csum в образе выключен).

    python3 tools/malina_bootstrap.py <образ>            # показать план
    python3 tools/malina_bootstrap.py <образ> --apply    # записать
"""
import os
import struct
import subprocess
import sys
import tempfile

sys.path.insert(0, "tools")
from ext4extract import Ext4, parts

HOOK = "/usr/sbin/myfolders.sh"
MARKER = "# microart2mqtt"
# Вставляемый текст — только ASCII: файл образа кодируется побайтно (latin-1),
# а шеллу комментарий на латинице роли не играет.
#
# Ставим В НАЧАЛО myfolders.sh (сразу после shebang), до вызова my_init.sh:
# тот может зависнуть или перезагрузиться, и строки после него не выполнятся.
#
# /boot смонтирован ro — писать туда нельзя. Лог кладём в корень их веб-сервера
# (/settings/html, раздел на запись): он открывается в браузере по
# http://<ip>/mqtt-install.txt — видно без шелла и без снятия карты.
# Установщик читаем с /boot (оттуда чтение работает и на ro).
BLOCK = (
    MARKER + ": console autologin + agent bootstrap (added offline)\n"
    "setsid /sbin/agetty --autologin pi --noclear tty1 linux >/dev/null 2>&1 &\n"
    # Установщик кладётся в образ инструментом fat_put.py именами 8.3, поэтому
    # на смонтированном vfat он виден как /boot/MICROART/INSTALL.SH. Если файлы
    # скопировали руками, имена будут длинными — проверяем оба варианта.
    # Установщик с карты запускаем, только если агента нет, он негоден или
    # служба выключена: иначе он на каждой загрузке возвращал бы версию из
    # образа поверх той, что поставили кнопкой «Обновить» в вебе. Тот же текст
    # умеет вписывать сам агент (bootguard.go) — для уже прошитых устройств.
    "if ! /settings/microart-mqtt/malina-agent -version >/dev/null 2>&1 || \\\n"
    "   ! systemctl is-enabled microart-mqtt >/dev/null 2>&1; then\n"
    "for i in /boot/MICROART/INSTALL.SH /boot/microart/install-on-malina.sh; do\n"
    "  [ -f \"$i\" ] && { sh \"$i\" >/settings/html/mqtt-install.txt 2>&1 & break; }\n"
    "done\n"
    "fi\n"
)


def inode_abs(fs, ino, root):
    g = (ino - 1) // fs.inodes_per_group
    i = (ino - 1) % fs.inodes_per_group
    return root + fs.group_desc(g) * fs.block_size + i * fs.inode_size


def main():
    args = [a for a in sys.argv[1:] if a != "--apply"]
    apply = "--apply" in sys.argv
    if not args:
        raise SystemExit(__doc__)
    img = args[0]

    root = parts(img)["root"]
    fs = Ext4(img, root)
    ino = fs.resolve(HOOK)
    d = fs.inode(ino)
    content = fs.read_file(ino).decode("latin-1")

    # Убираем наши прежние строки (любой версии), чтобы вставить свежие и не
    # накапливать дубли. Опознаём по характерным подстрокам.
    #
    # Список должен покрывать КАЖДУЮ строку, которую мы когда-либо вписывали.
    # Раньше в нём не было тела цикла, и повторные прогоны оставляли висячие
    # «done» — а это синтаксическая ошибка, из-за которой всё, что идёт в их
    # скрипте дальше, на загрузке просто не выполнялось.
    sig = ("# microart2mqtt", "agetty --autologin pi", "install-on-malina.sh",
           "mkdir -p /boot/microart", "mqtt-install.txt",
           "/settings/microart-mqtt/malina-agent -version",
           "systemctl is-enabled microart-mqtt",
           "# Установщик с карты нужен", "# выключена. Иначе", "# той, что поставили")
    kept, dropped_prev = [], False
    for ln in content.split("\n"):
        if any(s in ln for s in sig):
            dropped_prev = True
            continue
        # Закрывающие «done»/«fi» от наших конструкций удаляем только когда они
        # идут сразу за удалённой строкой: у них в скрипте свои циклы и условия.
        if dropped_prev and ln.strip() in ("done", "fi"):
            continue
        dropped_prev = False
        kept.append(ln)
    cleaned = "\n".join(kept)

    # Вставляем сразу после строки shebang (#!/bin/sh), до всей логики.
    nl = cleaned.find("\n")
    if nl < 0:
        raise SystemExit("в %s нет переносов строк — неожиданный формат" % HOOK)
    new = cleaned[:nl + 1] + BLOCK + cleaned[nl + 1:]
    data = new.encode("latin-1")

    if new == content:
        print("Уже прописано в актуальном виде — образ готов.")
        return

    blocks = fs.extents(d)
    if len(blocks) != 1:
        raise SystemExit("%s из %d блоков — ожидался один" % (HOOK, len(blocks)))
    if len(data) > fs.block_size:
        raise SystemExit("не влезает в блок: %d > %d" % (len(data), fs.block_size))
    physical = blocks[0][1]

    print("Файл:", HOOK)
    print("Размер: %d -> %d байт (запас в блоке: %d)" %
          (len(content), len(data), fs.block_size - len(data)))
    print("Вставка в начало myfolders.sh (после shebang):")
    for line in BLOCK.strip("\n").split("\n"):
        print("   ", line)

    # Разбираем получившийся скрипт через «sh -n»: сломанный myfolders.sh
    # рушит не только наш агент, а всю загрузку устройства.
    with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as tf:
        tf.write(new)
        probe = tf.name
    try:
        r = subprocess.run(["sh", "-n", probe], capture_output=True, text=True)
    finally:
        os.unlink(probe)
    if r.returncode != 0:
        raise SystemExit("получившийся myfolders.sh не разбирается как sh:\n" + r.stderr)
    print("Проверка синтаксиса (sh -n): пройдена")

    if not apply:
        print("\n[холостой прогон] запись НЕ выполнялась. Повторите с --apply.")
        return

    block = bytearray(fs.block_size)     # добиваем нулями, чтобы не осталось
    block[:len(data)] = data             # хвоста старого текста
    with open(img, "r+b") as f:
        f.seek(root + physical * fs.block_size)
        f.write(bytes(block))
        f.seek(inode_abs(fs, ino, root) + 0x4)  # i_size_lo
        f.write(struct.pack("<I", len(data)))
        f.flush()

    # Сверка.
    fs2 = Ext4(img, root)
    got = fs2.read_file(fs2.resolve(HOOK)).decode("latin-1")
    if MARKER not in got or not got.rstrip().endswith("exit 0"):
        raise SystemExit("сверка не прошла — проверьте образ")
    print("\nОК: консоль (автологин pi) и запуск агента прописаны. Можно прошивать.")


if __name__ == "__main__":
    main()
