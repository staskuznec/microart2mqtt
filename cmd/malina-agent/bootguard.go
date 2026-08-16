package main

// Защита от отката версии при перезагрузке.
//
// В /usr/sbin/myfolders.sh (их скрипт, отрабатывает на каждой загрузке) вписан
// наш блок, который запускает установщик с карты. Установщик кладёт бинарник
// из образа на штатное место — и версия, поставленная кнопкой «Обновить»,
// пропадает после первой же перезагрузки.
//
// Чинить это записью в /boot нельзя: порча загрузочного раздела — единственная
// беда, после которой пришлось бы ехать с картой. Зато myfolders.sh лежит на
// корневом разделе: он ext4 и перемонтируется на запись с работающей системы,
// ровно как это делает наш установщик.
//
// Поэтому обходимся правкой одного условия: установщик с карты запускается,
// только если агента нет, он не запускается или служба не включена. То есть
// карта остаётся аварийным образом — она вернёт рабочую версию, если на диске
// оказалось негодное, — но здоровую установку больше не трогает.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const bootHookPath = "/usr/sbin/myfolders.sh"

// hookOld — блок, который вписывает malina_bootstrap.py в образ (без защиты).
const hookOld = `for i in /boot/MICROART/INSTALL.SH /boot/microart/install-on-malina.sh; do
  [ -f "$i" ] && { sh "$i" >/settings/html/mqtt-install.txt 2>&1 & break; }
done`

// hookGuard — он же, но с проверкой. Тот же текст вписывает в образ
// malina_bootstrap.py: там он для новых прошивок, здесь — для уже прошитых.
const hookGuard = `# Установщик с карты нужен, только когда агента нет, он негоден или служба
# выключена. Иначе он на каждой загрузке возвращал бы версию из образа поверх
# той, что поставили кнопкой «Обновить» в вебе.
if ! /settings/microart-mqtt/malina-agent -version >/dev/null 2>&1 || \
   ! systemctl is-enabled microart-mqtt >/dev/null 2>&1; then
for i in /boot/MICROART/INSTALL.SH /boot/microart/install-on-malina.sh; do
  [ -f "$i" ] && { sh "$i" >/settings/html/mqtt-install.txt 2>&1 & break; }
done
fi`

// guardMark — по нему узнаём, что защита уже стоит.
const guardMark = "/settings/microart-mqtt/malina-agent -version"

// ourMark — наша метка в их скрипте. Нет её — устройство ставили не мы, не лезем.
const ourMark = "# microart2mqtt"

// ourLines — подстроки КАЖДОЙ строки, которую мы когда-либо вписывали. По ним
// вычищаем прежние вставки: в образах, собранных до исправления сборщика,
// накопились дубли блока с висячими «done», а это синтаксическая ошибка — всё,
// что идёт в их скрипте дальше, на загрузке не выполнялось.
var ourLines = []string{
	ourMark,
	"agetty --autologin pi",
	"install-on-malina.sh",
	"mkdir -p /boot/microart",
	"mqtt-install.txt",
	guardMark,
	"systemctl is-enabled microart-mqtt",
	"# Установщик с карты нужен",
	"# выключена. Иначе",
	"# той, что поставили",
}

// stripOurs убирает все наши прежние строки. Закрывающие «done»/«fi» удаляем
// только когда они идут сразу за удалённой строкой: у них в скрипте свои циклы
// и условия, трогать их нельзя.
func stripOurs(text string) string {
	var kept []string
	dropped := false
	for _, ln := range strings.Split(text, "\n") {
		if containsAny(ln, ourLines) {
			dropped = true
			continue
		}
		if dropped {
			switch strings.TrimSpace(ln) {
			case "done", "fi":
				continue
			}
		}
		dropped = false
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ensureBootHook приводит загрузочный хук к нужному виду: одна наша вставка,
// с проверкой установленного агента. Возвращает строку для журнала («» —
// делать нечего).
//
// Если нашей метки в файле нет, не трогаем ничего. Результат перед записью
// прогоняется через «sh -n»: сломанный myfolders.sh рушит не наш агент, а всю
// загрузку устройства.
func ensureBootHook(path string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", nil // нет файла — устройство поставлено иначе, не наше дело
	}
	text := string(src)
	if !strings.Contains(text, ourMark) {
		return "", nil
	}

	cleaned := stripOurs(text)
	nl := strings.Index(cleaned, "\n")
	if nl < 0 {
		return "", nil // нет переносов — неожиданный формат, не лезем
	}
	block := ourMark + ": console autologin + agent bootstrap (added offline)\n" +
		"setsid /sbin/agetty --autologin pi --noclear tty1 linux >/dev/null 2>&1 &\n" +
		hookGuard + "\n"
	updated := cleaned[:nl+1] + block + cleaned[nl+1:]
	if updated == text {
		return "", nil // уже в нужном виде
	}
	if err := shSyntaxOK(updated); err != nil {
		return "", fmt.Errorf("правка не прошла проверку синтаксиса, оставляю как было: %w", err)
	}

	// Сохраняем исходник рядом — на случай, если что-то пойдёт не так.
	backup := path + ".before-mqtt"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		_ = writeMaybeRO(backup, src, 0o755)
	}
	if err := writeMaybeRO(path, []byte(updated), 0o755); err != nil {
		return "", err
	}
	return "загрузочный хук приведён в порядок: версия переживёт перезагрузку", nil
}

// shSyntaxOK прогоняет текст через «sh -n»: разбор без выполнения.
func shSyntaxOK(text string) error {
	f, err := os.CreateTemp("", "hook-*.sh")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := exec.Command("sh", "-n", f.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// writeMaybeRO пишет файл, а если раздел смонтирован только на чтение —
// перемонтирует его на запись и возвращает обратно. Сначала пробуем записать:
// лишний раз дёргать корень на запись незачем.
func writeMaybeRO(path string, data []byte, mode os.FileMode) error {
	err := writeAtomic(path, data, mode)
	if err == nil {
		return nil
	}
	mp := mountPointFor(path)
	if rerr := remount(mp, "rw"); rerr != nil {
		return fmt.Errorf("%s: %w (и не перемонтировать %s на запись: %v)", path, err, mp, rerr)
	}
	err = writeAtomic(path, data, mode)
	if rerr := remount(mp, "ro"); rerr != nil && err == nil {
		return fmt.Errorf("записано, но %s не вернулся в режим чтения: %w", mp, rerr)
	}
	return err
}

// writeAtomic пишет рядом и переименовывает: оборвавшаяся запись не оставит
// обрубок вместо рабочего файла.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".new")
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, mode)
}

// mountPointFor — точка монтирования, в которой лежит путь. Нам по сути нужен
// только корень, но угадывать не будем: /usr может оказаться отдельным.
func mountPointFor(path string) string {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "/"
	}
	best := "/"
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		mp := f[1]
		if mp == "/" || !strings.HasPrefix(path, strings.TrimSuffix(mp, "/")+"/") {
			continue
		}
		if len(mp) > len(best) {
			best = mp
		}
	}
	return best
}

func remount(mp, mode string) error {
	return exec.Command("mount", "-o", "remount,"+mode, mp).Run()
}

// syncAll сбрасывает буферы на носитель — на карте это особенно важно.
func syncAll() { syscall.Sync() }
