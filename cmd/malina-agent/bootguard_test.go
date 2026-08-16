package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// myfolders — кусок их скрипта с нашим блоком: ровно то, что вписывает в образ
// malina_bootstrap.py.
const myfolders = `#!/bin/sh
mkdir -p /var/map
# microart2mqtt: console autologin + agent bootstrap (added offline)
setsid /sbin/agetty --autologin pi --noclear tty1 linux >/dev/null 2>&1 &
` + hookOld + `
exit 0
`

func TestEnsureBootHook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "myfolders.sh")
	if err := os.WriteFile(path, []byte(myfolders), 0o755); err != nil {
		t.Fatal(err)
	}

	msg, err := ensureBootHook(path)
	if err != nil {
		t.Fatalf("правка не прошла: %v", err)
	}
	if msg == "" {
		t.Fatal("хук не был исправлен, хотя защиты в нём не было")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), guardMark) {
		t.Error("в хуке нет проверки установленного агента")
	}
	// Их собственные строки должны остаться нетронутыми.
	for _, keep := range []string{"mkdir -p /var/map", "agetty --autologin pi", "exit 0"} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("из хука пропало: %q", keep)
		}
	}
	if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Errorf("исправленный хук не разбирается как sh: %v\n%s", err, out)
	}
	if _, err := os.Stat(path + ".before-mqtt"); err != nil {
		t.Error("не сохранена копия исходного хука")
	}

	// Повторный вызов ничего не меняет.
	msg2, err := ensureBootHook(path)
	if err != nil || msg2 != "" {
		t.Errorf("повторная правка: msg=%q err=%v", msg2, err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Error("повторный вызов изменил файл")
	}
}

// Чужой файл без нашего блока не трогаем вовсе.
func TestEnsureBootHookLeavesForeignFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "myfolders.sh")
	orig := "#!/bin/sh\nmkdir -p /var/map\nexit 0\n"
	if err := os.WriteFile(path, []byte(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := ensureBootHook(path)
	if err != nil || msg != "" {
		t.Errorf("msg=%q err=%v — файл без нашего блока трогать нельзя", msg, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != orig {
		t.Error("чужой файл изменён")
	}
}

// Само условие из хука: когда он всё-таки пускает установщик с карты.
// Проверяем на настоящем тексте hookGuard, подменив в нём пути на временные.
func TestHookConditionDecidesWhenToRunInstaller(t *testing.T) {
	cases := []struct {
		name       string
		agent      string // "" — бинарника нет
		unitOK     bool
		wantRunner bool // должен ли запуститься установщик с карты
	}{
		{"агент работает, служба включена", "#!/bin/sh\necho 0.2.7\n", true, false},
		{"агента нет", "", true, true},
		{"агент не запускается", "#!/bin/sh\nexit 1\n", true, true},
		{"служба не включена", "#!/bin/sh\necho 0.2.7\n", false, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			agent := filepath.Join(dir, "malina-agent")
			if c.agent != "" {
				if err := os.WriteFile(agent, []byte(c.agent), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			// Заглушка systemctl: is-enabled отвечает согласно случаю.
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			code := "1"
			if c.unitOK {
				code = "0"
			}
			if err := os.WriteFile(filepath.Join(bin, "systemctl"),
				[]byte("#!/bin/sh\nexit "+code+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			// Берём настоящий hookGuard, подменяя путь к агенту и тело цикла.
			script := strings.ReplaceAll(hookGuard, "/settings/microart-mqtt/malina-agent", agent)
			script = strings.Replace(script, hookOld, "echo RUN", 1)

			f := filepath.Join(dir, "hook.sh")
			if err := os.WriteFile(f, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", f)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("хук не отработал: %v\n%s", err, out)
			}
			ran := strings.Contains(string(out), "RUN")
			if ran != c.wantRunner {
				t.Errorf("установщик с карты запущен=%v, ожидалось %v\nвывод: %s",
					ran, c.wantRunner, out)
			}
		})
	}
}
