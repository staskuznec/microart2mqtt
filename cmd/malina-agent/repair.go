package main

// Починка устройств, застрявших на версиях 0.2.1–0.2.3.
//
// В тех версиях перезапуск после обновления брал свой путь через
// os.Executable() уже ПОСЛЕ того, как работающий файл переименовали в .old, и
// поднимал обратно старую версию. Новый бинарник при этом ложился на диск
// исправно — крутился по-прежнему старый. Сам себя такой агент починить не
// может: кнопка «Обновить» в нём сломана ровно тем же местом.
//
// Зацепка: перед установкой он ЗАПУСКАЕТ скачанный файл как
// «<каталог>/.malina-agent.new -version», чтобы проверить, что тот рабочий, и
// делает это от root. То есть в этот момент новая версия уже выполняется на
// устройстве. Ею и чиним: ставим себя на штатное место, обновляем копию в
// /boot и через несколько секунд перезапускаем службу. Для человека это
// по-прежнему одно нажатие «Обновить» — карту вынимать не нужно.
//
// Срабатывает только когда нас запустил СТАРЫЙ агент: исправленная версия при
// такой же проверке выставляет probeEnv, и починка себя не трогает.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// probeEnv выставляет исправленная версия, когда проверяет скачанный файл.
	// Его отсутствие и означает «нас запустил старый агент».
	probeEnv = "MICROART2MQTT_PROBE"
	// tmpName — имя временного файла, одинаковое во всех выпущенных версиях.
	tmpName = ".malina-agent.new"
	binName = "malina-agent"
	service = "microart-mqtt"
	// repairLog виден в браузере: http://<ip>/mqtt-update.txt
	repairLog = "/settings/html/mqtt-update.txt"
	// restartDelay — даём старому агенту дописать ответ браузеру и доделать
	// свою (уже неважную) возню с файлами.
	restartDelay = 8 * time.Second
)

// maybeRepair вызывается из ветки -version, после того как версия напечатана.
func maybeRepair() {
	if os.Getenv(probeEnv) != "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if filepath.Base(self) != tmpName {
		return
	}

	log := openRepairLog(filepath.Dir(self))
	defer func() {
		if log != nil {
			_ = log.Close()
		}
	}()
	say := func(format string, a ...any) {
		if log == nil {
			return
		}
		fmt.Fprintf(log, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	}

	say("запущен старой версией на проверку — чиню установку (версия %s)", version)

	data, err := os.ReadFile(self)
	if err != nil {
		say("не прочитать себя: %v", err)
		return
	}

	// 1. Ставим себя на штатное место. Работающий старый процесс от этого не
	// пострадает: rename меняет запись в каталоге, а его инод остаётся жив.
	dir := filepath.Dir(self)
	target := filepath.Join(dir, binName)
	if err := replaceFile(target, data); err != nil {
		say("не поставить %s: %v", target, err)
		return
	}
	say("новая версия установлена: %s", target)

	// 2. Копия в /boot, иначе установщик с карты на следующей загрузке вернёт
	// версию, записанную в образ.
	for _, m := range bootRefresh(data) {
		say("%s", m)
	}

	// 3. Перезапуск службы: только он поднимет новый бинарник, потому что
	// перезапуск в старой версии сломан.
	if err := scheduleRestart(dir); err != nil {
		say("не удалось назначить перезапуск: %v — перезагрузите устройство", err)
		return
	}
	say("служба будет перезапущена через %s", restartDelay)
}

// openRepairLog пишет в файл, который виден в браузере. Если каталога веба нет
// (например, агент поставлен куда-то ещё), кладём журнал рядом с бинарником —
// молча терять отчёт о починке нельзя.
func openRepairLog(dir string) *os.File {
	for _, p := range []string{repairLog, filepath.Join(dir, "update.log")} {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			return f
		}
	}
	return nil
}

// replaceFile пишет рядом и переименовывает: если запись оборвётся, на месте
// останется прежний рабочий файл, а не обрубок.
func replaceFile(path string, data []byte) error {
	tmp := path + ".fix"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o755)
}

// bootRefresh кладёт бинарник в набор на карте (/boot смонтирован ro, поэтому
// перемонтируем на запись и возвращаем обратно). Установщик оттуда запускается
// на каждой загрузке, и если там останется старая версия — она вернётся.
//
// Возвращает строки для журнала: молча не делаем ничего, но и не считаем
// неудачу здесь фатальной — на работу уже установленного агента она не влияет.
func bootRefresh(data []byte) []string {
	var out []string
	for _, d := range []string{"/boot/MICROART", "/boot/microart"} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		name := ""
		for _, n := range []string{"AGENT", "malina-agent", "agent"} {
			if _, err := os.Stat(filepath.Join(d, n)); err == nil {
				name = n
				break
			}
		}
		if name == "" {
			continue
		}
		path := filepath.Join(d, name)

		wasRO := bootIsReadOnly()
		if wasRO {
			if err := remountBoot("rw"); err != nil {
				out = append(out, fmt.Sprintf("/boot: не перемонтировать на запись: %v", err))
				continue
			}
		}
		err := replaceFile(path, data)
		syscall.Sync()
		if wasRO {
			if rerr := remountBoot("ro"); rerr != nil {
				out = append(out, fmt.Sprintf("/boot: не вернуть в режим чтения: %v", rerr))
			}
		}
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		out = append(out, "обновлён набор на карте: "+path)
	}
	return out
}

func bootIsReadOnly() bool {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return true // не знаем — считаем, что ro, попытка перемонтировать безвредна
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[1] == "/boot" {
			for _, o := range strings.Split(f[3], ",") {
				if o == "rw" {
					return false
				}
			}
			return true
		}
	}
	return true
}

func remountBoot(mode string) error {
	return exec.Command("mount", "-o", "remount,"+mode, "/boot").Run()
}

// scheduleRestart запускает отсоединённый перезапуск с задержкой. Отдельным
// процессом и с setsid — потому что нас самих вот-вот прибьют вместе со старым
// агентом, а вывод не должен попасть в stdout: старый агент читает оттуда
// строку с версией.
func scheduleRestart(dir string) error {
	script := fmt.Sprintf(
		"sleep %d; systemctl restart %s >/dev/null 2>&1 || %s restart >/dev/null 2>&1 || true",
		int(restartDelay.Seconds()), service, filepath.Join(dir, "agent-ctl.sh"))
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// nil = /dev/null: занятый stdout заставил бы старого агента ждать нас.
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
