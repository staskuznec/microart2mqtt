package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.1", "1.0.0", false},
		{"1.10.0", "1.9.0", false}, // сравнение числами, а не строками
		{"1.9.0", "1.10.0", true},
		{"v1.0.0", "v1.0.1", true}, // префикс v допустим с обеих сторон
		{"1.0", "1.0.1", true},

		// На сборке из ветки предлагать обновление нечего: версии не сравнить.
		{"dev", "1.0.0", false},
		{"a1b2c3d", "1.0.0", false},
		{"1.0.0", "", false},
		{"1.0.0", "мусор", false},
	}

	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, ожидалось %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestNewerIgnoresSuffixes(t *testing.T) {
	if !Newer("1.0.0", "1.0.1-rc1") {
		t.Error("суффикс -rc1 не должен мешать сравнению")
	}
	if Newer("1.0.0-dirty", "1.0.0") {
		t.Error("одинаковые версии не должны считаться обновлением")
	}
}
