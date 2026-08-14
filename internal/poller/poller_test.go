package poller

import (
	"testing"

	"github.com/staskuznec/microart2mqtt/internal/store"
)

func TestConvertNumber(t *testing.T) {
	p := func(n int) *int { return &n }

	cases := []struct {
		name      string
		metric    store.Metric
		in        string
		wantText  string
		wantValue any
	}{
		{
			name:      "без округления",
			metric:    store.Metric{Kind: store.KindNumber},
			in:        "52.4321",
			wantText:  "52.4321",
			wantValue: 52.4321,
		},
		{
			name:      "округление до одного знака",
			metric:    store.Metric{Kind: store.KindNumber, Precision: p(1)},
			in:        "88.06",
			wantText:  "88.1",
			wantValue: 88.1,
		},
		{
			name:      "округление до целого",
			metric:    store.Metric{Kind: store.KindNumber, Precision: p(0)},
			in:        "1450.7",
			wantText:  "1451",
			wantValue: float64(1451),
		},
		{
			// Не число — публикуем как строку: это честнее, чем подставлять 0.
			name:      "нечисловое значение",
			metric:    store.Metric{Kind: store.KindNumber},
			in:        "ЭКО",
			wantText:  "ЭКО",
			wantValue: "ЭКО",
		},
		{
			name:      "строковая метрика не трогается",
			metric:    store.Metric{Kind: store.KindString, Precision: p(1)},
			in:        "52.4321",
			wantText:  "52.4321",
			wantValue: "52.4321",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value, text := convert(c.metric, c.in)
			if text != c.wantText {
				t.Errorf("текст %q, ожидалось %q", text, c.wantText)
			}
			if value != c.wantValue {
				t.Errorf("значение %#v, ожидалось %#v", value, c.wantValue)
			}
		})
	}
}

func TestConvertTrimsTrailingZeros(t *testing.T) {
	// Значение уходит в топик как текст, и «88» читается лучше, чем «88.000000».
	// Заодно это делает сравнение с прошлым значением устойчивым.
	p := 3
	_, text := convert(store.Metric{Kind: store.KindNumber, Precision: &p}, "88.0000")
	if text != "88" {
		t.Errorf("текст %q, ожидалось 88", text)
	}
}
