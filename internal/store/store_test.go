package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, context.Context) {
	t.Helper()

	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, ctx
}

func TestConfigDefaultsOnEmptyBase(t *testing.T) {
	db, ctx := open(t)

	cfg, err := db.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Ready() {
		t.Error("пустая база не должна считаться настроенной")
	}
	if cfg.MQTTBaseTopic != DefaultBaseTopic {
		t.Errorf("корень топиков %q, ожидался %q", cfg.MQTTBaseTopic, DefaultBaseTopic)
	}
	if cfg.PollInterval != DefaultPollInterval {
		t.Errorf("интервал %s, ожидался %s", cfg.PollInterval, DefaultPollInterval)
	}
	if !cfg.MQTTRetain {
		t.Error("ретейн по умолчанию должен быть включён")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	db, ctx := open(t)

	want := Config{
		MQTTAddr:      "192.168.1.10:1883",
		MQTTUser:      "user",
		MQTTPassword:  "secret",
		MQTTBaseTopic: "solar",
		MQTTQoS:       1,
		MQTTRetain:    false,
		PollInterval:  15 * time.Second,
		HTTPTimeout:   3 * time.Second,
		Republish:     5 * time.Minute,
		OfflineAfter:  2,
	}
	if err := db.SaveConfig(ctx, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := db.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got != want {
		t.Errorf("прочитано %+v, сохранялось %+v", got, want)
	}
	if !got.Ready() {
		t.Error("с адресом брокера конфиг должен считаться готовым")
	}
}

func TestBrokerURL(t *testing.T) {
	cases := []struct {
		addr string
		tls  bool
		want string
	}{
		{"192.168.1.10:1883", false, "tcp://192.168.1.10:1883"},
		{"192.168.1.10", false, "tcp://192.168.1.10:1883"},
		{"192.168.1.10", true, "tls://192.168.1.10:8883"},
		{"ws://host:9001", false, "ws://host:9001"},
	}
	for _, c := range cases {
		got := Config{MQTTAddr: c.addr, MQTTTLS: c.tls}.BrokerURL()
		if got != c.want {
			t.Errorf("BrokerURL(%q, tls=%v) = %q, ожидалось %q", c.addr, c.tls, got, c.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	base := Config{MQTTAddr: "host:1883", OfflineAfter: 3}
	if err := base.Validate(); err != nil {
		t.Errorf("корректный конфиг забракован: %v", err)
	}

	bad := base
	bad.MQTTAddr = ""
	if err := bad.Validate(); err == nil {
		t.Error("пустой адрес брокера должен быть ошибкой")
	}

	bad = base
	bad.MQTTQoS = 5
	if err := bad.Validate(); err == nil {
		t.Error("QoS 5 должен быть ошибкой")
	}

	bad = base
	bad.MQTTBaseTopic = "solar/#"
	if err := bad.Validate(); err == nil {
		t.Error("шаблон в корне топиков должен быть ошибкой")
	}
}

func TestInverterCRUD(t *testing.T) {
	db, ctx := open(t)

	id, err := db.SaveInverter(ctx, Inverter{Name: "inv1", URL: "http://10.0.0.1/", Enabled: true})
	if err != nil {
		t.Fatalf("SaveInverter: %v", err)
	}

	inv, err := db.Inverter(ctx, id)
	if err != nil {
		t.Fatalf("Inverter: %v", err)
	}
	if inv.URL != "http://10.0.0.1" {
		t.Errorf("хвостовой слэш должен срезаться, получено %q", inv.URL)
	}

	// Имя попадает в топик и обязано быть уникальным.
	if _, err := db.SaveInverter(ctx, Inverter{Name: "inv1", URL: "http://10.0.0.2", Enabled: true}); err == nil {
		t.Error("повторное имя должно быть ошибкой")
	}

	if err := db.SetInverterEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetInverterEnabled: %v", err)
	}
	if inv, _ := db.Inverter(ctx, id); inv.Enabled {
		t.Error("инвертор должен был выключиться")
	}

	if err := db.DeleteInverter(ctx, id); err != nil {
		t.Fatalf("DeleteInverter: %v", err)
	}
	if _, err := db.Inverter(ctx, id); err != ErrNotFound {
		t.Errorf("после удаления ожидался ErrNotFound, получено %v", err)
	}
}

func TestInverterValidate(t *testing.T) {
	cases := map[string]Inverter{
		"пустое имя":      {Name: "", URL: "http://h"},
		"пробел в имени":  {Name: "inv 1", URL: "http://h"},
		"слэш в имени":    {Name: "inv/1", URL: "http://h"},
		"адрес без схемы": {Name: "inv1", URL: "10.0.0.1"},
	}
	for name, inv := range cases {
		if err := inv.Validate(); err == nil {
			t.Errorf("%s: должно быть ошибкой", name)
		}
	}
}

func TestMetricsCascadeAndDuplicates(t *testing.T) {
	db, ctx := open(t)

	id, err := db.SaveInverter(ctx, Inverter{Name: "inv1", URL: "http://10.0.0.1", Enabled: true})
	if err != nil {
		t.Fatalf("SaveInverter: %v", err)
	}

	added, err := db.AddMetrics(ctx, id, DefaultMetrics())
	if err != nil {
		t.Fatalf("AddMetrics: %v", err)
	}
	if added != len(DefaultMetrics()) {
		t.Errorf("добавлено %d метрик, ожидалось %d", added, len(DefaultMetrics()))
	}

	// Повторное применение набора не должно ни падать, ни плодить дубли:
	// его накладывают поверх уже настроенного.
	again, err := db.AddMetrics(ctx, id, DefaultMetrics())
	if err != nil {
		t.Fatalf("повторный AddMetrics: %v", err)
	}
	if again != 0 {
		t.Errorf("повторно добавлено %d метрик, ожидалось 0", again)
	}

	// Удаление инвертора должно уносить и метрики.
	if err := db.DeleteInverter(ctx, id); err != nil {
		t.Fatalf("DeleteInverter: %v", err)
	}
	left, err := db.Metrics(ctx, id)
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("после удаления инвертора осталось %d метрик", len(left))
	}
}

func TestMetricValidateAndTopic(t *testing.T) {
	ok := Metric{Name: "soc", Device: DeviceBat, Target: "minute_data.C_100_remain"}
	if err := ok.Validate(); err != nil {
		t.Errorf("корректная метрика забракована: %v", err)
	}
	if ok.FlatTopic() != "soc" {
		t.Errorf("пустой топик должен подменяться именем, получено %q", ok.FlatTopic())
	}

	withTopic := ok
	withTopic.Topic = "/battery/soc/"
	if got := withTopic.FlatTopic(); got != "battery/soc" {
		t.Errorf("топик %q, ожидалось battery/soc", got)
	}

	bad := ok
	bad.Device = "invalid"
	if err := bad.Validate(); err == nil {
		t.Error("неизвестный источник должен быть ошибкой")
	}

	bad = ok
	bad.Target = ""
	if err := bad.Validate(); err == nil {
		t.Error("пустое поле API должно быть ошибкой")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("первое открытие: %v", err)
	}
	if _, err := db.SaveInverter(ctx, Inverter{Name: "inv1", URL: "http://h", Enabled: true}); err != nil {
		t.Fatalf("SaveInverter: %v", err)
	}
	_ = db.Close()

	// Повторное открытие не должно ни падать на миграциях, ни терять данные.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer func() { _ = db2.Close() }()

	list, err := db2.Inverters(ctx)
	if err != nil {
		t.Fatalf("Inverters: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("после переоткрытия инверторов %d, ожидался 1", len(list))
	}
}
