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
// устройстве. Ею и чиним: ставим себя на штатное место и через несколько
// секунд перезапускаем службу. Для человека это по-прежнему одно нажатие
// «Обновить» — карту вынимать не нужно.
//
// Раздел /boot при этом НЕ трогаем. Во-первых, его порча — единственная беда,
// которая и правда потребовала бы ехать перешивать. Во-вторых, установщик с
// карты ставит свою версию на каждой загрузке, и это наша страховка: если
// новая версия окажется негодной, достаточно передёрнуть питание.
//
// Срабатывает только когда нас запустил СТАРЫЙ агент: исправленная версия при
// такой же проверке выставляет probeEnv, и починка себя не трогает.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// 2. Перезапуск службы: только он поднимет новый бинарник, потому что
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
