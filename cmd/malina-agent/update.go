package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// updateURL — откуда берём новый бинарник. URL зашит: кнопка в вебе просто
// запускает обновление, вводить ничего не нужно. Файл собирается в GitHub
// Actions (release.yml) под ARMv7.
const (
	updateURL  = "https://github.com/staskuznec/microart2mqtt/releases/latest/download/malina-agent-linux-armv7"
	sumsURL    = "https://github.com/staskuznec/microart2mqtt/releases/latest/download/SHA256SUMS"
	updateName = "malina-agent-linux-armv7"
)

// selfUpdate скачивает новый бинарник, сверяет контрольную сумму, проверяет,
// что он запускается, заменяет текущий файл и перезапускает процесс тем же
// путём (exec). Вся вёрстка внутри бинарника, поэтому веб обновляется вместе с
// агентом. При успехе функция НЕ возвращается — процесс заменяется.
func (a *agent) selfUpdate() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не определить свой путь: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)
	dir := filepath.Dir(self)

	client := &http.Client{Timeout: 60 * time.Second}

	slog.Info("самообновление: скачиваю", "url", updateURL)
	newBin, err := download(client, updateURL)
	if err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}

	// Сверка суммы: скачиваем SHA256SUMS и ищем строку для нашего файла.
	if sums, err := download(client, sumsURL); err == nil {
		if want := sumFor(sums, updateName); want != "" {
			got := sha256hex(newBin)
			if !strings.EqualFold(want, got) {
				return fmt.Errorf("контрольная сумма не сошлась")
			}
			slog.Info("самообновление: сумма сошлась")
		}
	}

	// Пишем во временный файл рядом с текущим, проверяем запуск, затем заменяем.
	tmp := filepath.Join(dir, ".malina-agent.new")
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("запись временного файла: %w", err)
	}
	if out, err := exec.Command(tmp, "-version").Output(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("новый бинарник не запускается: %w", err)
	} else {
		slog.Info("самообновление: новая версия", "version", strings.TrimSpace(string(out)))
	}

	// Прежний бинарник сохраняем для отката, затем ставим новый на его место.
	_ = os.Rename(self, self+".old")
	if err := os.Rename(tmp, self); err != nil {
		// Пытаемся вернуть старый на место.
		_ = os.Rename(self+".old", self)
		return fmt.Errorf("замена бинарника: %w", err)
	}
	_ = os.Chmod(self, 0o755)

	slog.Info("самообновление: перезапускаюсь", "path", self)
	// Заменяем образ процесса новым бинарником. pid сохраняется — pidfile от
	// start-stop-daemon остаётся валидным.
	if err := syscall.Exec(self, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("перезапуск: %w", err)
	}
	return nil // не достигается
}

func download(c *http.Client, url string) ([]byte, error) {
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ответ %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // не больше 64 МБ
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sumFor находит в файле SHA256SUMS сумму для нужного имени.
func sumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && (f[1] == name || f[1] == "*"+name) {
			return f[0]
		}
	}
	return ""
}
