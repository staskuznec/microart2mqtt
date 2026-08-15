#!/usr/bin/env python3
"""Включить SSH в образе «Малины» и задать известный пароль пользователю pi.

МикроАрт заглушает ssh масками systemd (ssh.service и ssh.socket -> /dev/null) и
не даёт известного пароля. Скрипт правит образ офлайн, без монтирования:

  1. снимает обе маски — удаляет записи каталога /etc/systemd/system,
     расширяя rec_len предыдущей записи (данные не двигаются, размер тот же);
  2. задаёт паролю pi известное значение — замена $6$-хэша байт-в-байт, той же
     длины (см. ext4_setpass).

По умолчанию только показывает, что будет сделано. Запись — с флагом --apply.
Каждый шаг проверяется до и после; старые значения печатаются для отката.

    python3 tools/malina_enable_ssh.py <образ> [пароль]            # проверка
    python3 tools/malina_enable_ssh.py <образ> [пароль] --apply    # запись

metadata_csum в этом образе выключен, поэтому контрольные суммы каталога и
инодов пересчитывать не нужно — правки затрагивают только сами записи.
"""
import struct
import subprocess
import sys

sys.path.insert(0, "tools")
from ext4extract import Ext4, PARTS


def parse_dir(block):
    """Записи одного блока каталога: (pos, inode, rec_len, name_len, ftype, name)."""
    out = []
    pos = 0
    while pos < len(block) - 8:
        inode, rec, nl, ft = struct.unpack_from("<IHBB", block, pos)
        if rec < 8:
            break
        name = block[pos + 8:pos + 8 + nl].decode("latin-1") if nl else ""
        out.append([pos, inode, rec, nl, ft, name])
        pos += rec
    return out


def plan_unmask(fs, dir_path, names):
    """Готовит правки rec_len для удаления записей names из однослойного каталога."""
    ino = fs.resolve(dir_path)
    d = fs.inode(ino)
    if struct.unpack_from("<I", d, 0x20)[0] & 0x1000:
        raise SystemExit("каталог %s htree-индексирован — удаление не поддержано" % dir_path)
    blocks = fs.extents(d)
    if len(blocks) != 1:
        raise SystemExit("каталог %s из %d блоков — ожидался один" % (dir_path, len(blocks)))
    logical, physical = blocks[0]
    block = fs.block(physical)
    entries = parse_dir(block)

    present = {e[5] for e in entries}
    for n in names:
        if n not in present:
            raise SystemExit("в %s нет записи %r — возможно, уже снято" % (dir_path, n))

    # Пройти записи, склеивая удаляемые в предыдущую оставшуюся.
    edits = {}          # pos предыдущей записи -> новый rec_len
    kept_prev = None
    removed = []
    for e in entries:
        pos, inode, rec, nl, ft, name = e
        if name in names:
            if kept_prev is None:
                raise SystemExit("удаляемая запись оказалась первой — не бывает для ssh.*")
            kept_prev[2] += rec          # предыдущая поглощает место удаляемой
            edits[kept_prev[0]] = kept_prev[2]
            removed.append((name, inode))
        else:
            kept_prev = e

    phys_base = PARTS["root"] + physical * fs.block_size
    return {
        "dir": dir_path, "phys_base": phys_base, "edits": edits,
        "removed": removed, "block_size": fs.block_size,
    }


def sha512_crypt(password, salt):
    out = subprocess.run(["openssl", "passwd", "-6", "-salt", salt, password],
                         capture_output=True, text=True, check=True).stdout.strip()
    if not out.startswith("$6$") or len(out) != 98:
        raise SystemExit("openssl вернул неожиданный хэш длины %d" % len(out))
    return out


def plan_password(fs, user, password):
    ino = fs.resolve("/etc/shadow")
    d = fs.inode(ino)
    data = fs.read_file(ino)
    bs = fs.block_size
    bmap = {l: p for l, p in fs.extents(d)}
    lines = data.decode("latin-1").split("\n")
    idx = next((i for i, l in enumerate(lines) if l.startswith(user + ":")), -1)
    if idx < 0:
        raise SystemExit("нет пользователя %r в /etc/shadow" % user)
    fields = lines[idx].split(":")
    old = fields[1]
    if not old.startswith("$6$") or len(old) != 98:
        raise SystemExit("хэш %r не $6$/98 — замена другой длины небезопасна" % old)
    salt = old.split("$")[2][:8]
    new = sha512_crypt(password, salt)
    hash_off = sum(len(l) + 1 for l in lines[:idx]) + len(fields[0]) + 1

    def phys(g):
        lb, within = divmod(g, bs)
        if lb not in bmap:
            raise SystemExit("дыра в /etc/shadow на блоке %d" % lb)
        return PARTS["root"] + bmap[lb] * bs + within

    return {"user": user, "old": old, "new": new, "hash_off": hash_off, "phys": phys}


def main():
    args = [a for a in sys.argv[1:] if a != "--apply"]
    apply = "--apply" in sys.argv
    if not args:
        raise SystemExit(__doc__)
    img = args[0]
    password = args[1] if len(args) > 1 else "microart"

    fs = Ext4(img, PARTS["root"])
    unmask = plan_unmask(fs, "/etc/systemd/system", ["ssh.service", "ssh.socket"])
    pw = plan_password(fs, "pi", password)

    print("=== 1. снять маски ssh ===")
    for name, inode in unmask["removed"]:
        print("  удаляю запись %-12s (inode %d, была -> /dev/null)" % (name, inode))
    for pos, rec in unmask["edits"].items():
        print("  запись на off=%d: rec_len -> %d" % (pos, rec))

    print("\n=== 2. пароль pi ===")
    print("  пароль:    ", password)
    print("  старый хэш:", pw["old"], "(для отката)")
    print("  новый хэш: ", pw["new"])

    # Проверка места хэша до записи.
    with open(img, "rb") as f:
        got = bytearray()
        for i in range(98):
            f.seek(pw["phys"](pw["hash_off"] + i)); got += f.read(1)
    if got.decode("latin-1") != pw["old"]:
        raise SystemExit("\nПРОВЕРКА: на месте хэша не старое значение — стоп, ничего не тронуто")
    print("\nпроверка: место хэша совпадает со старым значением ✓")

    if not apply:
        print("\n[холостой прогон] запись НЕ выполнялась. Повторите с --apply.")
        return

    with open(img, "r+b") as f:
        for pos, rec in unmask["edits"].items():
            f.seek(unmask["phys_base"] + pos + 4)   # поле rec_len (u16) в записи
            f.write(struct.pack("<H", rec))
        for i, ch in enumerate(pw["new"].encode("latin-1")):
            f.seek(pw["phys"](pw["hash_off"] + i)); f.write(bytes([ch]))
        f.flush()

    # Сверка после записи.
    fs2 = Ext4(img, PARTS["root"])
    left = {e[5] for e in parse_dir(fs2.block(fs2.extents(fs2.inode(
        fs2.resolve("/etc/systemd/system")))[0][1]))}
    if "ssh.service" in left or "ssh.socket" in left:
        raise SystemExit("маски всё ещё на месте — что-то не так")
    lines2 = fs2.read_file(fs2.resolve("/etc/shadow")).decode("latin-1").split("\n")
    pi2 = next(l for l in lines2 if l.startswith("pi:")).split(":")[1]
    if pi2 != pw["new"]:
        raise SystemExit("пароль не записался")

    print("\nОК: маски сняты, пароль записан и проверен. Можно прошивать.")


if __name__ == "__main__":
    main()
