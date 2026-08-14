package microart

import "testing"

func TestFieldsFlattensNestedResponse(t *testing.T) {
	bat := BatteryResponse{
		MinuteData: BatteryMinuteData{C100Remain: "88.0", UaccAvg: "52.43"},
		SecondData: BatterySecondData{SunP: "1450"},
	}

	fields, err := Fields(bat)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}

	got := make(map[string]Field, len(fields))
	for _, f := range fields {
		got[f.Path] = f
	}

	// Путь должен совпадать с тем, что понимает FieldFrom: иначе поле,
	// выбранное в вебе, не найдётся при опросе.
	for path, want := range map[string]string{
		"minute_data.C_100_remain": "88.0",
		"minute_data.Uacc_avg":     "52.43",
		"second_data.Sun_P":        "1450",
	} {
		f, ok := got[path]
		if !ok {
			t.Errorf("поле %s не найдено", path)
			continue
		}
		if f.Value != want {
			t.Errorf("поле %s: значение %q, ожидалось %q", path, f.Value, want)
		}
		if !f.Numeric {
			t.Errorf("поле %s: должно распознаться как числовое", path)
		}
	}
}

func TestFieldsSkipsEmpty(t *testing.T) {
	// Полей, которых «Малина» не прислала, в списке быть не должно: метрику из
	// пустоты делать нечего.
	fields, err := Fields(BatteryResponse{MinuteData: BatteryMinuteData{C100Remain: "50"}})
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	for _, f := range fields {
		if f.Value == "" {
			t.Errorf("в списке пустое поле %s", f.Path)
		}
		if f.Path == "minute_data.Iavg" {
			t.Errorf("поле %s не приходило от устройства, его быть не должно", f.Path)
		}
	}
}

func TestFieldsPathsMatchFieldFrom(t *testing.T) {
	// Главная связка: то, что показал Fields, обязано читаться через FieldFrom.
	bat := BatteryResponse{MinuteData: BatteryMinuteData{C100Remain: "77.5"}}

	fields, err := Fields(bat)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) == 0 {
		t.Fatal("Fields ничего не вернул")
	}

	for _, f := range fields {
		got, err := FieldFrom(bat, f.Path)
		if err != nil {
			t.Errorf("FieldFrom(%s): %v", f.Path, err)
			continue
		}
		if got != f.Value {
			t.Errorf("FieldFrom(%s) = %q, а Fields показал %q", f.Path, got, f.Value)
		}
	}
}

func TestFieldFromRejectsEmptyPath(t *testing.T) {
	if _, err := FieldFrom(BatteryResponse{}, ""); err == nil {
		t.Error("пустой путь должен считаться ошибкой")
	}
	if _, err := FieldFrom(nil, "minute_data.C_100_remain"); err == nil {
		t.Error("пустой ответ должен считаться ошибкой")
	}
}
