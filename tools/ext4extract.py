#!/usr/bin/env python3
"""Чтение файлов из образа «Малины» без монтирования.

macOS не умеет монтировать ext4, а вытаскивать нужное поиском по байтам —
гадание: границы файла так не определить. Поэтому здесь минимальный
разборщик ext4 только на чтение: суперблок, дескрипторы групп, иноды,
дерево экстентов и линейные каталоги. Этого хватает для образа Raspbian.

    python3 tools/ext4extract.py <образ> ls   <раздел> <путь>
    python3 tools/ext4extract.py <образ> cat  <раздел> <путь>
    python3 tools/ext4extract.py <образ> dump <раздел> <путь> <куда>
"""
import os
import struct
import sys

ROOT_INO = 2


class Ext4:
    def __init__(self, img, offset):
        self.f = open(img, "rb")
        self.off = offset

        sb = self.pread(offset + 1024, 1024)
        magic = struct.unpack_from("<H", sb, 0x38)[0]
        if magic != 0xEF53:
            raise SystemExit("это не ext2/3/4: magic 0x%04x" % magic)

        self.inodes_count = struct.unpack_from("<I", sb, 0)[0]
        self.block_size = 1024 << struct.unpack_from("<I", sb, 0x18)[0]
        self.blocks_per_group = struct.unpack_from("<I", sb, 0x20)[0]
        self.inodes_per_group = struct.unpack_from("<I", sb, 0x28)[0]
        self.inode_size = struct.unpack_from("<H", sb, 0x58)[0]
        self.first_data_block = struct.unpack_from("<I", sb, 0x14)[0]
        incompat = struct.unpack_from("<I", sb, 0x60)[0]
        self.desc_size = struct.unpack_from("<H", sb, 0xFE)[0] or 32
        self.is_64bit = bool(incompat & 0x80)
        if not self.is_64bit:
            self.desc_size = 32

    def pread(self, pos, size):
        self.f.seek(pos)
        return self.f.read(size)

    def block(self, num, count=1):
        return self.pread(self.off + num * self.block_size, self.block_size * count)

    def group_desc(self, group):
        # Дескрипторы идут сразу за суперблоком.
        gd_block = self.first_data_block + 1
        pos = self.off + gd_block * self.block_size + group * self.desc_size
        d = self.pread(pos, self.desc_size)
        lo = struct.unpack_from("<I", d, 0x8)[0]          # inode_table_lo
        hi = struct.unpack_from("<I", d, 0x28)[0] if self.desc_size >= 0x2C else 0
        return lo | (hi << 32)

    def inode(self, ino):
        group = (ino - 1) // self.inodes_per_group
        index = (ino - 1) % self.inodes_per_group
        table = self.group_desc(group)
        pos = self.off + table * self.block_size + index * self.inode_size
        return self.pread(pos, self.inode_size)

    def size_of(self, ino_data):
        lo = struct.unpack_from("<I", ino_data, 0x4)[0]
        hi = struct.unpack_from("<I", ino_data, 0x6C)[0]
        return lo | (hi << 32)

    def mode_of(self, ino_data):
        return struct.unpack_from("<H", ino_data, 0)[0]

    def extents(self, ino_data, blocks=None):
        """Собирает список блоков файла, разбирая дерево экстентов."""
        if blocks is None:
            blocks = []
        body = ino_data[0x28:0x28 + 60]
        self._walk_extent(body, blocks)
        return blocks

    def _walk_extent(self, node, blocks):
        magic, entries, _max, depth, _gen = struct.unpack_from("<HHHHI", node, 0)
        if magic != 0xF30A:
            return
        for i in range(entries):
            e = node[12 + i * 12: 24 + i * 12]
            if depth == 0:
                first, length, start_hi, start_lo = struct.unpack("<IHHI", e)
                start = start_lo | (start_hi << 32)
                for b in range(length):
                    blocks.append((first + b, start + b))
            else:
                _first, leaf_lo, leaf_hi, _unused = struct.unpack("<IIHH", e)
                child = leaf_lo | (leaf_hi << 32)
                self._walk_extent(self.block(child), blocks)

    def read_file(self, ino):
        data_inode = self.inode(ino)
        size = self.size_of(data_inode)

        # Короткая символьная ссылка хранит цель прямо в inode, без блоков.
        if (self.mode_of(data_inode) & 0xF000) == 0xA000 and size < 60:
            return data_inode[0x28:0x28 + size]

        flags = struct.unpack_from("<I", data_inode, 0x20)[0]
        if not (flags & 0x80000):  # EXT4_EXTENTS_FL
            raise SystemExit("файл без экстентов (старый ext2-стиль) — не поддержано")

        parts = {}
        for logical, physical in self.extents(data_inode):
            parts[logical] = self.block(physical)

        out = bytearray()
        for i in range(max(parts) + 1 if parts else 0):
            out += parts.get(i, b"\x00" * self.block_size)
        return bytes(out[:size])

    def readdir(self, ino):
        """Возвращает [(имя, инод, тип)] — линейный обход блоков каталога."""
        data = self.read_file(ino)
        out = []
        pos = 0
        while pos < len(data) - 8:
            child, rec_len, name_len, ftype = struct.unpack_from("<IHBB", data, pos)
            if rec_len < 8:
                break
            if child and name_len:
                name = data[pos + 8: pos + 8 + name_len].decode("utf-8", "replace")
                if name not in (".", ".."):
                    out.append((name, child, ftype))
            pos += rec_len
        return out

    def resolve(self, path):
        ino = ROOT_INO
        for part in [p for p in path.strip("/").split("/") if p]:
            found = None
            for name, child, _t in self.readdir(ino):
                if name == part:
                    found = child
                    break
            if found is None:
                raise SystemExit("нет пути: %s (не найдено %r)" % (path, part))
            ino = found
        return ino


# Смещения разделов из shrunk-образа МикроАрт. У карты рабочего устройства
# разметка может быть другой (корень расширен под размер карты), поэтому это
# лишь запасной вариант: parts() читает настоящую таблицу разделов.
PARTS = {"root": 137216 * 512, "settings": 8525824 * 512}


def parts(path):
    """Смещения разделов в байтах из MBR образа или карты (/dev/rdiskN).

    Разметка Малины одинакова: p1 — FAT32 /boot, p2 — ext4 корень,
    p3 — ext4 /settings. Берём Linux-разделы по порядку.
    """
    try:
        with open(path, "rb") as f:
            mbr = f.read(512)
    except OSError:
        return dict(PARTS)

    if len(mbr) < 512 or mbr[510:512] != b"\x55\xaa":
        return dict(PARTS)

    linux = []
    for i in range(4):
        e = mbr[446 + i * 16: 446 + (i + 1) * 16]
        if e[4] == 0x83:                                  # тип ext2/3/4
            lba = struct.unpack("<I", e[8:12])[0]
            if lba:
                linux.append(lba * 512)
    if len(linux) < 2:
        return dict(PARTS)
    return {"root": linux[0], "settings": linux[1]}


def main():
    if len(sys.argv) < 5:
        raise SystemExit(__doc__)
    img, cmd, part, path = sys.argv[1:5]
    fs = Ext4(img, parts(img)[part])

    if cmd == "ls":
        ino = fs.resolve(path)
        for name, child, ftype in sorted(fs.readdir(ino)):
            kind = "DIR " if ftype == 2 else "file"
            size = "" if ftype == 2 else "%8d" % fs.size_of(fs.inode(child))
            print("  %s %8s  %s" % (kind, size, name))
    elif cmd == "cat":
        sys.stdout.buffer.write(fs.read_file(fs.resolve(path)))
    elif cmd == "dump":
        dest = sys.argv[5]
        os.makedirs(os.path.dirname(dest) or ".", exist_ok=True)
        with open(dest, "wb") as out:
            out.write(fs.read_file(fs.resolve(path)))
        print("  %s -> %s (%d байт)" % (path, dest, os.path.getsize(dest)))
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main()
