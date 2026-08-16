package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Копия на карте должна освежаться, когда версия отличается, и оставаться
// нетронутой, когда там уже то же самое: лишняя запись на FAT загрузочного
// раздела — ровно тот риск, ради которого всё и городилось.
func TestRefreshCardCopy(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "MICROART")
	if err := os.MkdirAll(card, 0o755); err != nil {
		t.Fatal(err)
	}
	agentOnCard := filepath.Join(card, "AGENT")
	if err := os.WriteFile(agentOnCard, []byte("старая версия"), 0o755); err != nil {
		t.Fatal(err)
	}

	self := filepath.Join(dir, "malina-agent")
	fresh := []byte("новая версия")
	if err := os.WriteFile(self, fresh, 0o755); err != nil {
		t.Fatal(err)
	}

	cardDirs = []string{card}
	settleTime = 0
	t.Cleanup(func() {
		cardDirs = []string{"/boot/MICROART", "/boot/microart"}
		settleTime = 2 * time.Minute
	})

	refreshCardCopy(self)

	got, err := os.ReadFile(agentOnCard)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fresh) {
		t.Fatalf("на карте %q, ожидалось %q", got, fresh)
	}
	if _, err := os.Stat(filepath.Join(card, "AGENT.NEW")); err == nil {
		t.Error("на карте остался временный файл")
	}

	// Второй заход: содержимое совпадает — файл трогать не должны.
	before, err := os.Stat(agentOnCard)
	if err != nil {
		t.Fatal(err)
	}
	refreshCardCopy(self)
	after, err := os.Stat(agentOnCard)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("карта переписана, хотя версия та же")
	}
}

// Нет набора на карте — нечего и делать (устройство поставлено вручную).
func TestRefreshCardCopyWithoutBundle(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "malina-agent")
	if err := os.WriteFile(self, []byte("что-то"), 0o755); err != nil {
		t.Fatal(err)
	}
	cardDirs = []string{filepath.Join(dir, "нет-такого")}
	settleTime = 0
	t.Cleanup(func() {
		cardDirs = []string{"/boot/MICROART", "/boot/microart"}
		settleTime = 2 * time.Minute
	})
	refreshCardCopy(self) // не должно ни паниковать, ни создавать файлов
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("в каталоге появились лишние файлы: %d", len(ents))
	}
}
