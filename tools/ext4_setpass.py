#!/usr/bin/env python3
"""Задать известный пароль пользователю прямо в образе (ext4, без монтирования).

Меняет только хэш в /etc/shadow, байт-в-байт, той же длины: у SHA-512 ($6$) с
8-символьной солью хэш всегда 98 символов, как и уже стоящий. Размер файла и
любые метаданные не меняются, поэтому правка безопасна и обратима.

    python3 tools/ext4_setpass.py <образ> <пользователь> <новый_пароль>

Перед записью убеждаемся, что на вычисленном месте лежит именно старый хэш;
после — перечитываем /etc/shadow и проверяем пароль. Старый хэш печатается для
отката.
"""
import subprocess
import sys

sys.path.insert(0, "tools")
from ext4extract import Ext4, PARTS


def sha512_crypt(password: str, salt: str) -> str:
    out = subprocess.run(
        ["openssl", "passwd", "-6", "-salt", salt, password],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    if not out.startswith("$6$") or len(out) != 98:
        raise SystemExit("openssl вернул неожиданный хэш длины %d" % len(out))
    return out


def block_map(fs: Ext4, ino_data):
    """logical block index -> physical block, по дереву экстентов."""
    m = {}
    for logical, physical in fs.extents(ino_data):
        m[logical] = physical
    return m


def main():
    if len(sys.argv) != 4:
        raise SystemExit(__doc__)
    img, user, password = sys.argv[1], sys.argv[2], sys.argv[3]

    fs = Ext4(img, PARTS["root"])
    ino = fs.resolve("/etc/shadow")
    ino_data = fs.inode(ino)
    data = fs.read_file(ino)
    bs = fs.block_size
    bmap = block_map(fs, ino_data)

    text = data.decode("latin-1")
    lines = text.split("\n")
    idx = next((i for i, ln in enumerate(lines) if ln.startswith(user + ":")), -1)
    if idx < 0:
        raise SystemExit("пользователь %r не найден в /etc/shadow" % user)

    fields = lines[idx].split(":")
    old_hash = fields[1]
    if not old_hash.startswith("$6$") or len(old_hash) != 98:
        raise SystemExit(
            "хэш %r не $6$/98 символов — замена другой длины небезопасна, останавливаюсь"
            % old_hash
        )

    salt = old_hash.split("$")[2][:8]  # та же длина соли -> та же длина хэша
    new_hash = sha512_crypt(password, salt)
    assert len(new_hash) == len(old_hash) == 98

    # Глобальное смещение хэша внутри файла.
    line_start = sum(len(l) + 1 for l in lines[:idx])
    hash_off = line_start + len(fields[0]) + 1  # после "user:"

    # Проверка: на этом месте физически лежит именно старый хэш.
    def phys_pos(global_off):
        lb, within = divmod(global_off, bs)
        if lb not in bmap:
            raise SystemExit("логический блок %d не отображён (дыра в файле)" % lb)
        return PARTS["root"] + bmap[lb] * bs + within

    with open(img, "rb") as f:
        got = bytearray()
        for i in range(98):
            f.seek(phys_pos(hash_off + i))
            got += f.read(1)
    if got.decode("latin-1") != old_hash:
        raise SystemExit(
            "на вычисленном месте не старый хэш — прекращаю, ничего не тронуто.\n"
            "  ждали: %s\n  нашли: %s" % (old_hash, got.decode("latin-1"))
        )

    print("пользователь:      ", user)
    print("старый хэш (откат):", old_hash)
    print("новый хэш:         ", new_hash)

    # Запись байт-в-байт по физическим позициям (хэш может пересекать границу блока).
    with open(img, "r+b") as f:
        for i, ch in enumerate(new_hash.encode("latin-1")):
            f.seek(phys_pos(hash_off + i))
            f.write(bytes([ch]))
        f.flush()

    # Сверка: перечитываем через разборщик и проверяем пароль.
    fs2 = Ext4(img, PARTS["root"])
    lines2 = fs2.read_file(fs2.resolve("/etc/shadow")).decode("latin-1").split("\n")
    pi2 = next(ln for ln in lines2 if ln.startswith(user + ":"))
    stored = pi2.split(":")[1]
    check = sha512_crypt(password, salt)
    if stored != check:
        raise SystemExit("ПОСЛЕ ЗАПИСИ хэш не сходится: %s" % stored)
    if len(lines2) != len(lines):
        raise SystemExit("число строк в shadow изменилось — что-то не так")

    print("\nОК: пароль записан и проверен. Прочих строк shadow правка не задела.")


if __name__ == "__main__":
    main()
