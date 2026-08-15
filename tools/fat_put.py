#!/usr/bin/env python3
"""Положить файлы в FAT32-раздел (/boot) образа или карты — без монтирования.

Нужен, чтобы собрать готовый к прошивке образ: бандл агента кладётся прямо в
/boot/MICROART, и после dd на карту ничего копировать руками не надо.
macOS монтировать образ в песочнице не даёт, а писать в FAT32 напрямую просто.

Имена — только 8.3 (заглавными): так не нужны длинные имена LFN, а Linux и так
их видит. Установщик на устройстве знает оба варианта написания.

    python3 tools/fat_put.py <образ> <каталог> <файл> [<файл> ...]
    python3 tools/fat_put.py <образ> --list <каталог>

Пример:
    python3 tools/fat_put.py disk.img MICROART exe/microart-boot/*
"""
import os
import struct
import sys

ATTR_DIR = 0x10
ATTR_ARCHIVE = 0x20
EOC = 0x0FFFFFF8


class Fat32:
    def __init__(self, path):
        self.f = open(path, "r+b")
        self.off = self._find_partition()

        bs = self._pread(self.off, 512)
        self.bps = struct.unpack_from("<H", bs, 0x0B)[0]
        self.spc = bs[0x0D]
        self.reserved = struct.unpack_from("<H", bs, 0x0E)[0]
        self.nfats = bs[0x10]
        self.fatsz = struct.unpack_from("<I", bs, 0x24)[0]
        self.root_cluster = struct.unpack_from("<I", bs, 0x2C)[0]
        total = struct.unpack_from("<I", bs, 0x20)[0]

        if self.bps == 0 or self.spc == 0 or self.fatsz == 0:
            raise SystemExit("это не FAT32")

        self.cluster_size = self.bps * self.spc
        self.fat_start = self.off + self.reserved * self.bps
        self.data_start = self.off + (self.reserved + self.nfats * self.fatsz) * self.bps
        self.clusters = (total - self.reserved - self.nfats * self.fatsz) // self.spc

    @staticmethod
    def _looks_like_fat32(sec):
        """Похож ли сектор на загрузочный сектор FAT32 (а не на MBR).

        Различать надо явно: 0x55AA в конце есть и там, и там. Смотрим на поля
        BPB — размер сектора, кластер степени двойки и ненулевой FAT32-размер.
        """
        if len(sec) < 512:
            return False
        bps = struct.unpack_from("<H", sec, 0x0B)[0]
        spc = sec[0x0D]
        fatsz32 = struct.unpack_from("<I", sec, 0x24)[0]
        return (bps in (512, 1024, 2048, 4096)
                and spc and (spc & (spc - 1)) == 0
                and fatsz32 > 0)

    def _find_partition(self):
        head = self._pread(0, 512)
        if self._looks_like_fat32(head):
            return 0                      # передан сам раздел, без таблицы
        if head[510:512] == b"\x55\xaa":  # это MBR — ищем FAT32-раздел
            for i in range(4):
                e = head[446 + i * 16: 446 + (i + 1) * 16]
                if e[4] in (0x0B, 0x0C):
                    return struct.unpack("<I", e[8:12])[0] * 512
        raise SystemExit("FAT32-раздел не найден")

    def _pread(self, pos, size):
        self.f.seek(pos)
        return self.f.read(size)

    def _pwrite(self, pos, data):
        self.f.seek(pos)
        self.f.write(data)

    # --- FAT ---
    def fat_get(self, n):
        return struct.unpack_from("<I", self._pread(self.fat_start + n * 4, 4))[0] & 0x0FFFFFFF

    def fat_set(self, n, val):
        # Пишем во все копии FAT, иначе fsck сочтёт таблицы расходящимися.
        for c in range(self.nfats):
            base = self.fat_start + c * self.fatsz * self.bps
            self._pwrite(base + n * 4, struct.pack("<I", val & 0x0FFFFFFF))

    def chain(self, start):
        out = []
        n = start
        while 2 <= n < EOC and len(out) < 1_000_000:
            out.append(n)
            n = self.fat_get(n)
        return out

    def free_chain(self, start):
        for n in self.chain(start):
            self.fat_set(n, 0)

    def alloc(self, count):
        got = []
        n = 3  # 0,1 служебные; 2 обычно корень
        while len(got) < count and n < self.clusters + 2:
            if self.fat_get(n) == 0 and n != self.root_cluster:
                got.append(n)
            n += 1
        if len(got) < count:
            raise SystemExit("на разделе /boot не хватает места")
        for i, c in enumerate(got):
            self.fat_set(c, got[i + 1] if i + 1 < len(got) else EOC | 0x7)
        return got

    def cluster_pos(self, n):
        return self.data_start + (n - 2) * self.cluster_size

    def read_cluster(self, n):
        return self._pread(self.cluster_pos(n), self.cluster_size)

    def write_cluster(self, n, data):
        buf = bytearray(self.cluster_size)
        buf[:len(data)] = data
        self._pwrite(self.cluster_pos(n), bytes(buf))

    # --- каталоги ---
    def dir_entries(self, dir_cluster):
        """[(индекс_в_цепочке, смещение, сырые 32 байта)] всех записей каталога."""
        out = []
        for ci, cl in enumerate(self.chain(dir_cluster)):
            data = self.read_cluster(cl)
            for off in range(0, len(data), 32):
                out.append((cl, off, data[off:off + 32]))
        return out

    def find(self, dir_cluster, name83):
        for cl, off, raw in self.dir_entries(dir_cluster):
            if raw[0] in (0x00, 0xE5) or raw[11] == 0x0F:  # свободна/удалена/LFN
                continue
            if raw[:11] == name83:
                cluster = (struct.unpack_from("<H", raw, 20)[0] << 16) | \
                          struct.unpack_from("<H", raw, 26)[0]
                size = struct.unpack_from("<I", raw, 28)[0]
                return cl, off, raw[11], cluster, size
        return None

    def _free_slot(self, dir_cluster):
        for cl, off, raw in self.dir_entries(dir_cluster):
            if raw[0] in (0x00, 0xE5):
                return cl, off
        # Каталог кончился — добавляем кластер в его цепочку.
        chain = self.chain(dir_cluster)
        new = self.alloc(1)[0]
        self.fat_set(chain[-1], new)
        self.write_cluster(new, b"")
        return new, 0

    def set_entry(self, dir_cluster, name83, attr, first_cluster, size):
        found = self.find(dir_cluster, name83)
        if found:
            cl, off = found[0], found[1]
        else:
            cl, off = self._free_slot(dir_cluster)

        e = bytearray(32)
        e[0:11] = name83
        e[11] = attr
        struct.pack_into("<H", e, 20, (first_cluster >> 16) & 0xFFFF)
        struct.pack_into("<H", e, 26, first_cluster & 0xFFFF)
        struct.pack_into("<I", e, 28, size)
        # Дата/время оставляем нулями: FAT это допускает, а часы на устройстве
        # всё равно не синхронизированы на момент прошивки.
        data = bytearray(self.read_cluster(cl))
        data[off:off + 32] = e
        self.write_cluster(cl, bytes(data))

    def ensure_dir(self, parent_cluster, name83):
        found = self.find(parent_cluster, name83)
        if found and found[2] & ATTR_DIR:
            return found[3]

        cl = self.alloc(1)[0]
        # Новый каталог начинается с записей "." и ".."
        buf = bytearray(self.cluster_size)
        dot = bytearray(32)
        dot[0:11] = b".          "
        dot[11] = ATTR_DIR
        struct.pack_into("<H", dot, 26, cl & 0xFFFF)
        struct.pack_into("<H", dot, 20, (cl >> 16) & 0xFFFF)
        buf[0:32] = dot
        dotdot = bytearray(32)
        dotdot[0:11] = b"..         "
        dotdot[11] = ATTR_DIR
        parent = 0 if parent_cluster == self.root_cluster else parent_cluster
        struct.pack_into("<H", dotdot, 26, parent & 0xFFFF)
        struct.pack_into("<H", dotdot, 20, (parent >> 16) & 0xFFFF)
        buf[32:64] = dotdot
        self.write_cluster(cl, bytes(buf))

        self.set_entry(parent_cluster, name83, ATTR_DIR, cl, 0)
        return cl

    def put_file(self, dir_cluster, name83, data):
        old = self.find(dir_cluster, name83)
        if old and old[3]:
            self.free_chain(old[3])          # переиспользуем место старого файла

        if not data:
            self.set_entry(dir_cluster, name83, ATTR_ARCHIVE, 0, 0)
            return

        need = (len(data) + self.cluster_size - 1) // self.cluster_size
        chain = self.alloc(need)
        for i, cl in enumerate(chain):
            self.write_cluster(cl, data[i * self.cluster_size:(i + 1) * self.cluster_size])
        self.set_entry(dir_cluster, name83, ATTR_ARCHIVE, chain[0], len(data))

    def free_space(self):
        free = sum(1 for n in range(3, self.clusters + 2) if self.fat_get(n) == 0)
        return free * self.cluster_size

    def update_fsinfo(self):
        """Пересчитать свободные кластеры в блоке FSInfo.

        Иначе fsck справедливо ругается, что счётчик в FSInfo разошёлся с FAT.
        Пишем настоящее число — так раздел остаётся полностью согласованным.
        """
        bs = self._pread(self.off, 512)
        fsinfo_sec = struct.unpack_from("<H", bs, 0x30)[0]
        if not fsinfo_sec:
            return
        pos = self.off + fsinfo_sec * self.bps
        sec = bytearray(self._pread(pos, self.bps))
        if sec[0:4] != b"RRaA" or sec[484:488] != b"rrAa":
            return
        free = sum(1 for n in range(2, self.clusters + 2) if self.fat_get(n) == 0)
        struct.pack_into("<I", sec, 488, free)        # свободных кластеров
        struct.pack_into("<I", sec, 492, 0xFFFFFFFF)  # подсказка «искать с начала»
        self._pwrite(pos, bytes(sec))

    def close(self):
        self.f.flush()
        os.fsync(self.f.fileno())
        self.f.close()


def name83(name):
    """Имя файла в формате 8.3, заглавными, дополненное пробелами."""
    name = os.path.basename(name).upper()
    if "." in name:
        base, ext = name.rsplit(".", 1)
    else:
        base, ext = name, ""
    keep = lambda s: "".join(c for c in s if c.isalnum() or c in "_-")
    base, ext = keep(base)[:8], keep(ext)[:3]
    if not base:
        raise SystemExit("не могу сделать имя 8.3 из %r" % name)
    return (base.ljust(8) + ext.ljust(3)).encode("ascii")


def main():
    if len(sys.argv) < 4:
        raise SystemExit(__doc__)
    img = sys.argv[1]

    fs = Fat32(img)
    try:
        if sys.argv[2] == "--list":
            d = fs.ensure_dir(fs.root_cluster, name83(sys.argv[3]))
            for cl, off, raw in fs.dir_entries(d):
                if raw[0] in (0x00, 0xE5) or raw[11] == 0x0F:
                    continue
                nm = raw[:11].decode("ascii", "replace")
                nm = nm[:8].strip() + ("." + nm[8:].strip() if nm[8:].strip() else "")
                size = struct.unpack_from("<I", raw, 28)[0]
                print("  %-12s %9d %s" % (nm, size, "DIR" if raw[11] & ATTR_DIR else ""))
            return

        dirname, files = sys.argv[2], sys.argv[3:]
        d = fs.ensure_dir(fs.root_cluster, name83(dirname))
        for spec in files:
            # Имя на разделе можно задать явно: путь=ИМЯ.EXT. Так имена
            # предсказуемы, и установщик на устройстве знает, что искать.
            path, _, want = spec.partition("=")
            if not os.path.isfile(path):
                continue
            with open(path, "rb") as src:
                data = src.read()
            target = name83(want or path)
            fs.put_file(d, target, data)
            shown = target.decode()
            shown = shown[:8].strip() + ("." + shown[8:].strip() if shown[8:].strip() else "")
            print("  %-24s -> /%s/%s (%d байт)" %
                  (os.path.basename(path), dirname.upper(), shown, len(data)))
        fs.update_fsinfo()
        print("свободно на /boot: %.1f МБ" % (fs.free_space() / 1024 / 1024))
    finally:
        fs.close()


if __name__ == "__main__":
    main()
