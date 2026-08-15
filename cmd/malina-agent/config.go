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
// агент перечитывает по SIGUSR1.
//
// Метрик здесь нет намеренно: агент публикует ВСЁ, что отдаёт API малины —
// каждое поле становится топиком автоматически. Описывать ничего не нужно,
// как у zigbee2mqtt/tasmota; подписчик берёт то, что ему интересно.
type Config struct {
	Enabled   bool   `json:"enabled"`
	Broker    string `json:"broker"` // host:port, tcp://…, tls://…
	Username  string `json:"username"`
	Password  string `json:"password"`
	ClientID  string `json:"client_id"`  // пусто -> из имени хоста
	BaseTopic string `json:"base_topic"` // например microart/inv1
	QoS       int    `json:"qos"`
	Retain    bool   `json:"retain"`
	Interval  int    `json:"interval_sec"`  // как часто опрашивать localhost
	Republish int    `json:"republish_sec"` // как часто слать всё заново
	WebPort   int    `json:"web_port"`      // порт веб-страницы агента
}

const (
	defaultInterval  = 5 * time.Second
	defaultRepublish = 10 * time.Minute
	defaultBaseTopic = "microart/inv1"
	minInterval      = 1 * time.Second
	defaultWebPort   = 8091
)

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

// LoadConfig читает и подставляет значения по умолчанию.
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
	if c.WebPort <= 0 {
		c.WebPort = defaultWebPort
	}
	return c, nil
}

// WebAddr — адрес, на котором агент слушает свою веб-страницу.
func (c Config) WebAddr() string {
	port := c.WebPort
	if port <= 0 {
		port = defaultWebPort
	}
	return fmt.Sprintf(":%d", port)
}

// Save записывает конфиг в файл (агент правит его сам из веб-страницы).
func (c Config) Save(path string) error {
	c.BaseTopic = strings.Trim(strings.TrimSpace(c.BaseTopic), "/")
	if c.BaseTopic == "" {
		c.BaseTopic = defaultBaseTopic
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := dirOf(path); dir != "" {
		_ = os.MkdirAll(dir, 0o775)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o664); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return ""
}
