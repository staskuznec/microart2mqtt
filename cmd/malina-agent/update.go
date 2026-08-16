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

// selfUpdate скачивает новый бинарник, сверяет сумму, проверяет запуск и
// заменяет текущий файл. Процесс при этом НЕ перезапускается — это отдельный
// шаг (restartSelf), потому что перезапуск обрывает текущий HTTP-запрос:
// браузер получал бы пустой ответ, а nginx на «Малине» рисует на такой обрыв
// свою страницу «404», хотя обновление на самом деле прошло.
func (a *agent) selfUpdate() error {
	// Путь берём тот, что запомнили при старте. Спрашивать os.Executable()
	// здесь нельзя: он читает /proc/self/exe — ссылку на ИНОД работающего
	// файла, а мы этот файл ниже переименовываем в .old. После переименования
	// он вернул бы путь к .old, и перезапуск поднял бы старую версию.
	self := a.selfPath
	if self == "" {
		return fmt.Errorf("не определить свой путь")
	}
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
	// probeEnv помечает, что файл запускает уже исправленная версия: чинить
	// установку (см. repair.go) в этом случае не нужно.
	probe := exec.Command(tmp, "-version")
	probe.Env = append(os.Environ(), probeEnv+"=1")
	if out, err := probe.Output(); err != nil {
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

	// Обновляем и копию на карте: установщик оттуда отрабатывает на каждой
	// загрузке, и если оставить там прежнюю версию, она вернётся после
	// перезагрузки.
	for _, m := range bootRefresh(newBin) {
		slog.Info("самообновление: " + m)
	}

	slog.Info("самообновление: новая версия установлена, готов к перезапуску", "path", self)
	return nil
}

// restartSelf заменяет образ процесса новым бинарником. Вызывается уже ПОСЛЕ
// того, как ответ ушёл в браузер, с небольшой задержкой — иначе запрос
// оборвётся на полуслове.
//
// pid при exec сохраняется, поэтому systemd перезапуска даже не замечает.
func (a *agent) restartSelf() {
	self := a.selfPath
	if self == "" {
		slog.Error("самообновление: не определить свой путь")
		return
	}

	slog.Info("самообновление: перезапускаюсь", "path", self)
	if err := syscall.Exec(self, os.Args, os.Environ()); err != nil {
		// Не вышло — не страшно: под systemd с Restart=always нас поднимут
		// заново после выхода.
		slog.Error("самообновление: exec не удался, выхожу для рестарта службой", "err", err)
		os.Exit(0)
	}
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

// latestVersion сообщает версию из релиза, если она новее установленной.
// Проверку ведёт фоновый Checker (раз в сутки); сами ничего не ставим —
// решение за человеком.
func (a *agent) latestVersion() (string, bool) {
	if a.updates == nil {
		return "", false
	}
	info := a.updates.Info()
	return info.Latest, info.HasUpdate
}
