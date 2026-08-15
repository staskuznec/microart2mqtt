#!/usr/bin/env python3
"""Подготовить образ «Малины» к работе: консольный вход, SSH и пароль pi.

МикроАрт делает из образа headless-appliance: маскирует getty (нет входа на
мониторе), ssh (нет входа по сети) и не даёт известного пароля. Скрипт правит
образ офлайн, без монтирования, и идемпотентен — можно гонять повторно.

Что делает:
  1. снимает маску getty@.service  -> вход на мониторе (tty1);
  2. вписывает автологин pi в drop-in getty@tty1 -> сразу шелл без пароля;
  3. снимает маски ssh.service/ssh.socket -> вход по сети;
  4. ставит паролю pi известное значение (для ssh и на всякий случай).

    python3 tools/malina_prep.py <образ> [пароль]           # показать план
    python3 tools/malina_prep.py <образ> [пароль] --apply   # записать

metadata_csum в образе выключен -> контрольные суммы каталогов/инодов
пересчитывать не нужно; все правки минимальны и обратимы.
"""
import struct
import subprocess
import sys

sys.path.insert(0, "tools")
from ext4extract import Ext4, PARTS

AUTOLOGIN = (
    "[Service]\n"
    "TTYVTDisallocate=no\n"
    "ExecStart=\n"
    "ExecStart=-/sbin/agetty --autologin pi --noclear %I $TERM\n"
)


class Editor:
    def __init__(self, img):
        self.img = img
        self.fs = Ext4(img, PARTS["root"])
        self.base = PARTS["root"]
        self.bs = self.fs.block_size
        self.actions = []   # человекочитаемый план
        self.writes = []    # (абсолютное_смещение, bytes)

    # --- адресация ---
    def inode_abs(self, ino):
        g = (ino - 1) // self.fs.inodes_per_group
        i = (ino - 1) % self.fs.inodes_per_group
        table = self.fs.group_desc(g)
        return self.base + table * self.bs + i * self.fs.inode_size

    def file_blocks(self, ino_data):
        return {l: p for l, p in self.fs.extents(ino_data)}

    def phys(self, bmap, global_off):
        lb, within = divmod(global_off, self.bs)
        if lb not in bmap:
            raise SystemExit("дыра в файле на блоке %d" % lb)
        return self.base + bmap[lb] * self.bs + within

    # --- операции ---
    def unmask(self, dirpath, names):
        ino = self.fs.resolve(dirpath)
        d = self.fs.inode(ino)
        if struct.unpack_from("<I", d, 0x20)[0] & 0x1000:
            raise SystemExit("каталог %s htree — не поддержано" % dirpath)
        blocks = self.fs.extents(d)
        if len(blocks) != 1:
            raise SystemExit("каталог %s из %d блоков" % (dirpath, len(blocks)))
        physical = blocks[0][1]
        block = bytearray(self.fs.block(physical))

        # разобрать записи
        ents = []
        pos = 0
        while pos < len(block) - 8:
            inode, rec, nl, ft = struct.unpack_from("<IHBB", block, pos)
            if rec < 8:
                break
            name = block[pos + 8:pos + 8 + nl].decode("latin-1") if nl else ""
            ents.append([pos, inode, rec, nl, ft, name])
            pos += rec

        present = {e[5] for e in ents}
        todo = [n for n in names if n in present]
        for n in names:
            if n not in present:
                self.actions.append("  [уже снято] %s" % n)
        if not todo:
            return

        # склеить удаляемые в предыдущую оставшуюся, правя rec_len
        kept = None
        newrec = {}   # pos -> rec_len
        for e in ents:
            pos, inode, rec, nl, ft, name = e
            if name in todo:
                if kept is None:
                    raise SystemExit("удаляемая запись первая — не бывает")
                kept[2] += rec
                newrec[kept[0]] = kept[2]
                self.actions.append("  снять маску %s (inode %d)" % (name, inode))
            else:
                kept = e

        base = self.base + physical * self.bs
        for pos, rec in newrec.items():
            self.writes.append((base + pos + 4, struct.pack("<H", rec)))

    def write_small_file(self, path, content):
        data = content.encode("latin-1")
        ino = self.fs.resolve(path)
        d = self.fs.inode(ino)
        cur = self.fs.read_file(ino)
        if cur == data:
            self.actions.append("  [уже настроено] %s" % path)
            return
        if len(data) > self.bs:
            raise SystemExit("%s: содержимое больше блока" % path)
        blocks = self.fs.extents(d)
        if len(blocks) != 1:
            raise SystemExit("%s: ожидался один блок" % path)
        physical = blocks[0][1]
        # содержимое блока
        self.writes.append((self.base + physical * self.bs, data))
        # новый размер файла (i_size_lo в иноде)
        self.writes.append((self.inode_abs(ino) + 0x4, struct.pack("<I", len(data))))
        self.actions.append("  переписать %s (%d -> %d байт, автологин pi)"
                            % (path, len(cur), len(data)))

    def set_password(self, user, password):
        ino = self.fs.resolve("/etc/shadow")
        d = self.fs.inode(ino)
        bmap = self.file_blocks(d)
        lines = self.fs.read_file(ino).decode("latin-1").split("\n")
        idx = next((i for i, l in enumerate(lines) if l.startswith(user + ":")), -1)
        if idx < 0:
            raise SystemExit("нет пользователя %r в shadow" % user)
        old = lines[idx].split(":")[1]
        if not old.startswith("$6$") or len(old) != 98:
            raise SystemExit("хэш %r не $6$/98 — небезопасно" % old)
        salt = old.split("$")[2][:8]
        want = sha512(password, salt)
        if old == want:
            self.actions.append("  [уже задан] пароль %s" % user)
            return
        off = sum(len(l) + 1 for l in lines[:idx]) + len(user) + 1
        # проверить, что на месте действительно старый хэш
        with open(self.img, "rb") as f:
            got = bytearray()
            for i in range(98):
                f.seek(self.phys(bmap, off + i)); got += f.read(1)
        if got.decode("latin-1") != old:
            raise SystemExit("место хэша не совпало — стоп")
        for i, ch in enumerate(want.encode("latin-1")):
            self.writes.append((self.phys(bmap, off + i), bytes([ch])))
        self.actions.append("  пароль %s -> задан (старый: %s)" % (user, old))

    def commit(self):
        with open(self.img, "r+b") as f:
            for off, b in self.writes:
                f.seek(off); f.write(b)
            f.flush()


def sha512(password, salt):
    out = subprocess.run(["openssl", "passwd", "-6", "-salt", salt, password],
                         capture_output=True, text=True, check=True).stdout.strip()
    if not out.startswith("$6$") or len(out) != 98:
        raise SystemExit("openssl вернул хэш длины %d" % len(out))
    return out


def main():
    args = [a for a in sys.argv[1:] if a != "--apply"]
    apply = "--apply" in sys.argv
    if not args:
        raise SystemExit(__doc__)
    img = args[0]
    password = args[1] if len(args) > 1 else "microart"

    ed = Editor(img)
    print("=== консоль ===")
    ed.unmask("/etc/systemd/system", ["getty@.service"])
    ed.write_small_file("/etc/systemd/system/getty@tty1.service.d/noclear.conf", AUTOLOGIN)
    print("=== ssh ===")
    ed.unmask("/etc/systemd/system", ["ssh.service", "ssh.socket"])
    print("=== пароль ===")
    ed.set_password("pi", password)

    print("\nПлан:")
    for a in ed.actions:
        print(a)
    if not ed.writes:
        print("\nВсё уже сделано — образ готов.")
        return
    if not apply:
        print("\n[холостой прогон] запись НЕ выполнялась. Повторите с --apply.")
        return

    ed.commit()

    # Сверка.
    fs = Ext4(img, PARTS["root"])
    def names(path):
        ino = fs.resolve(path); blk = fs.block(fs.extents(fs.inode(ino))[0][1]); out = []
        pos = 0
        while pos < fs.block_size - 8:
            inode, rec, nl, ft = struct.unpack_from("<IHBB", blk, pos)
            if rec < 8:
                break
            if inode and nl:
                out.append(blk[pos + 8:pos + 8 + nl].decode("latin-1"))
            pos += rec
        return out
    sysd = names("/etc/systemd/system")
    assert "getty@.service" not in sysd, "маска getty осталась"
    assert "ssh.service" not in sysd and "ssh.socket" not in sysd, "маска ssh осталась"
    drop = fs.read_file(fs.resolve("/etc/systemd/system/getty@tty1.service.d/noclear.conf")).decode("latin-1")
    assert "autologin pi" in drop, "автологин не записан"
    pi = next(l for l in fs.read_file(fs.resolve("/etc/shadow")).decode("latin-1").split("\n") if l.startswith("pi:"))
    assert pi.split(":")[1] == sha512(password, pi.split("$")[2][:8]), "пароль не сверился"
    print("\nОК: консоль включена (автологин pi), ssh включён, пароль задан. Можно прошивать.")


if __name__ == "__main__":
    main()
