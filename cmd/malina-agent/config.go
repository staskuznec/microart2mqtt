package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config — настройки агента. Живёт файлом /settings/web-data/mqtt.json на
// записываемом разделе «Малины»; его пишет страница «MQTT» в их вебморде, а
// агент перечитывает по SIGUSR1. Формат намеренно плоский — это одна форма.
type Config struct {
	Enabled   bool     `json:"enabled"`
	Broker    string   `json:"broker"` // host:port, tcp://…, tls://…
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	ClientID  string   `json:"client_id"`  // пусто -> из имени хоста
	BaseTopic string   `json:"base_topic"` // например microart/inv1
	QoS       int      `json:"qos"`
	Retain    bool     `json:"retain"`
	Interval  int      `json:"interval_sec"`  // как часто опрашивать localhost
	Republish int      `json:"republish_sec"` // как часто слать всё заново
	Metrics   []Metric `json:"metrics"`
}

// Metric — одно значение: откуда взять и в какой топик писать.
type Metric struct {
	Name      string `json:"name"`      // ключ в JSON-стейте
	Topic     string `json:"topic"`     // хвост топика; пусто -> = name
	Device    string `json:"device"`    // map | bat | mppt
	Target    string `json:"target"`    // путь в ответе API
	Kind      string `json:"kind"`      // number | string
	Precision *int   `json:"precision"` // округление; null -> как есть
}

const (
	defaultInterval  = 5 * time.Second
	defaultRepublish = 10 * time.Minute
	defaultBaseTopic = "microart/inv1"
	minInterval      = 1 * time.Second
)

// FlatTopic возвращает хвост топика метрики.
func (m Metric) FlatTopic() string {
	if t := strings.Trim(strings.TrimSpace(m.Topic), "/"); t != "" {
		return t
	}
	return m.Name
}

// IsNumber сообщает, публиковать ли значение числом.
func (m Metric) IsNumber() bool { return m.Kind != "string" }

// PollInterval — интервал опроса с нижней границей.
func (c Config) PollInterval() time.Duration {
	if c.Interval <= 0 {
		return defaultInterval
	}
	d := time.Duration(c.Interval) * time.Second
	if d < minInterval {
		return minInterval
	}
	return d
}

// RepublishInterval — период полной переотправки; <0 отключает фильтр изменений.
func (c Config) RepublishInterval() time.Duration {
	if c.Republish < 0 {
		return 0
	}
	if c.Republish == 0 {
		return defaultRepublish
	}
	return time.Duration(c.Republish) * time.Second
}

// BrokerURL приводит адрес к форме, понятной клиенту MQTT.
func (c Config) BrokerURL() string {
	a := strings.TrimSpace(c.Broker)
	if a == "" {
		return ""
	}
	if strings.Contains(a, "://") {
		return a
	}
	if !strings.Contains(a, ":") {
		a += ":1883"
	}
	return "tcp://" + a
}

// LoadConfig читает и проверяет файл настроек.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("чтение %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("разбор %s: %w", path, err)
	}
	if c.BaseTopic == "" {
		c.BaseTopic = defaultBaseTopic
	}
	c.BaseTopic = strings.Trim(c.BaseTopic, "/")
	if c.QoS < 0 || c.QoS > 2 {
		c.QoS = 0
	}
	if len(c.Metrics) == 0 {
		c.Metrics = DefaultMetrics()
	}
	return c, nil
}

// DefaultMetrics — набор по умолчанию, если в конфиге метрик нет.
// Те же поля, что у центрального демона: заряд, ток, сеть, нагрузка, солнце.
func DefaultMetrics() []Metric {
	p := func(n int) *int { return &n }
	return []Metric{
		{Name: "soc", Topic: "battery/soc", Device: "bat", Target: "minute_data.C_100_remain", Precision: p(1)},
		{Name: "battery_voltage", Topic: "battery/voltage", Device: "bat", Target: "minute_data.Uacc_avg", Precision: p(2)},
		{Name: "battery_current", Topic: "battery/current", Device: "bat", Target: "minute_data.Iavg", Precision: p(2)},
		{Name: "grid_voltage", Topic: "grid/voltage", Device: "map", Target: "_UNET", Precision: p(1)},
		{Name: "grid_power", Topic: "grid/power", Device: "map", Target: "_PNET", Precision: p(1)},
		{Name: "load_power", Topic: "load/power", Device: "map", Target: "_PLoad", Precision: p(1)},
		{Name: "solar_power", Topic: "solar/power", Device: "bat", Target: "second_data.Sun_P", Precision: p(1)},
		{Name: "status", Topic: "status", Device: "map", Target: "_Status_Char"},
	}
}
