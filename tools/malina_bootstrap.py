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
import struct
import sys

sys.path.insert(0, "tools")
from ext4extract import Ext4, PARTS

HOOK = "/usr/sbin/myfolders.sh"
MARKER = "# microart2mqtt"
# Вставляемый текст — только ASCII: файл образа кодируется побайтно (latin-1),
# а шеллу комментарий на латинице роли не играет.
BLOCK = (
    "\n" + MARKER + ": console autologin + agent bootstrap (added offline)\n"
    "setsid /sbin/agetty --autologin pi --noclear tty1 linux >/dev/null 2>&1 &\n"
    "[ -f /boot/microart/install-on-malina.sh ] && "
    "sh /boot/microart/install-on-malina.sh >/var/log/microart-install.log 2>&1 &\n"
)


def inode_abs(fs, ino):
    g = (ino - 1) // fs.inodes_per_group
    i = (ino - 1) % fs.inodes_per_group
    return PARTS["root"] + fs.group_desc(g) * fs.block_size + i * fs.inode_size


def main():
    args = [a for a in sys.argv[1:] if a != "--apply"]
    apply = "--apply" in sys.argv
    if not args:
        raise SystemExit(__doc__)
    img = args[0]

    fs = Ext4(img, PARTS["root"])
    ino = fs.resolve(HOOK)
    d = fs.inode(ino)
    content = fs.read_file(ino).decode("latin-1")

    if MARKER in content:
        print("Уже прописано — образ готов.")
        return

    idx = content.rfind("exit 0")
    new = content[:idx] + BLOCK + content[idx:] if idx >= 0 else content + BLOCK
    data = new.encode("latin-1")

    blocks = fs.extents(d)
    if len(blocks) != 1:
        raise SystemExit("%s из %d блоков — ожидался один" % (HOOK, len(blocks)))
    if len(data) > fs.block_size:
        raise SystemExit("не влезает в блок: %d > %d" % (len(data), fs.block_size))
    physical = blocks[0][1]

    print("Файл:", HOOK)
    print("Размер: %d -> %d байт (запас в блоке: %d)" %
          (len(content), len(data), fs.block_size - len(data)))
    print("Вставка перед 'exit 0':")
    for line in BLOCK.strip("\n").split("\n"):
        print("   ", line)

    if not apply:
        print("\n[холостой прогон] запись НЕ выполнялась. Повторите с --apply.")
        return

    block = bytearray(fs.block_size)     # добиваем нулями, чтобы не осталось
    block[:len(data)] = data             # хвоста старого текста
    with open(img, "r+b") as f:
        f.seek(PARTS["root"] + physical * fs.block_size)
        f.write(bytes(block))
        f.seek(inode_abs(fs, ino) + 0x4)  # i_size_lo
        f.write(struct.pack("<I", len(data)))
        f.flush()

    # Сверка.
    fs2 = Ext4(img, PARTS["root"])
    got = fs2.read_file(fs2.resolve(HOOK)).decode("latin-1")
    if MARKER not in got or not got.rstrip().endswith("exit 0"):
        raise SystemExit("сверка не прошла — проверьте образ")
    print("\nОК: консоль (автологин pi) и запуск агента прописаны. Можно прошивать.")


if __name__ == "__main__":
    main()
