package main

// Обновление аварийной копии агента на карте (/boot).
//
// Установщик с карты — наш последний рубеж: он поднимет агента, если на диске
// оказалось негодное (см. bootguard.go). Толку от этого рубежа мало, если там
// лежит сборка полугодовой давности, поэтому копию надо освежать.
//
// Но не в момент обновления. Копия на карте ценна ровно тем, что она заведомо
// работает; положив туда версию, которую только что поставили и ещё ни разу не
// видели в деле, мы бы получили аварийный образ, негодный ровно так же, как и
// основной. Поэтому ждём, пока новая версия проработает settleTime, и только
// тогда переписываем.
//
// /boot — раздел FAT, с которого устройство грузится. Его порча означает поездку
// с картой в руках, поэтому: пишем только когда версия и правда отличается,
// пишем во временный файл, читаем обратно и сверяем sha256, и лишь потом
// подменяем. Не вышло — оставляем как было, это не мешает работе агента.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// settleTime — сколько новая версия должна отработать, прежде чем мы сочтём её
// пригодной на роль аварийной копии. Переменная, а не константа, чтобы тест не
// ждал две минуты.
var settleTime = 2 * time.Minute

// cardDirs / cardNames — куда fat_put.py кладёт набор (имена 8.3 на vfat видны
// заглавными) и как он может называться, если файлы копировали руками.
var (
	cardDirs  = []string{"/boot/MICROART", "/boot/microart"}
	cardNames = []string{"AGENT", "malina-agent", "agent"}
)

// refreshCardCopy обновляет копию на карте, если она отличается от работающего
// бинарника. Запускается в фоне при старте агента.
func refreshCardCopy(selfPath string) {
	if selfPath == "" {
		return
	}
	time.Sleep(settleTime)

	path := findCardAgent()
	if path == "" {
		return // набора на карте нет — устройство поставлено иначе
	}

	self, err := os.ReadFile(selfPath)
	if err != nil {
		slog.Debug("копия на карте: не прочитать себя", "err", err)
		return
	}
	card, err := os.ReadFile(path)
	if err == nil && bytes.Equal(sum(self), sum(card)) {
		return // уже свежая, карту не трогаем
	}

	if err := putOnCard(path, self); err != nil {
		// Не страшно: на работу агента это не влияет, просто аварийная копия
		// осталась прежней.
		slog.Warn("копия на карте не обновлена", "path", path, "err", err)
		return
	}
	slog.Info("копия на карте обновлена", "path", path, "version", version)
}

func findCardAgent() string {
	for _, d := range cardDirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		for _, n := range cardNames {
			p := filepath.Join(d, n)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// putOnCard подменяет бинарник на карте. Сначала просто пробуем записать: если
// раздел смонтирован только на чтение — перемонтируем и вернём обратно.
func putOnCard(path string, data []byte) error {
	err := writeCard(path, data)
	if err == nil {
		return nil
	}
	mp := mountPointFor(path)
	if rerr := remount(mp, "rw"); rerr != nil {
		return fmt.Errorf("%w (и не перемонтировать %s на запись: %v)", err, mp, rerr)
	}
	err = writeCard(path, data)
	if rerr := remount(mp, "ro"); rerr != nil && err == nil {
		return fmt.Errorf("записано, но %s не вернулся в режим чтения: %w", mp, rerr)
	}
	return err
}

// writeCard пишет во временный файл, читает обратно и сверяет, и только потом
// подменяет. Имя временного файла — в формате 8.3: на карте FAT.
func writeCard(path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), "AGENT.NEW")
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	back, err := os.ReadFile(tmp)
	if err != nil || !bytes.Equal(sum(back), sum(data)) {
		_ = os.Remove(tmp)
		return fmt.Errorf("записанное на карту не совпало с исходным")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncAll()
	return nil
}

func sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
