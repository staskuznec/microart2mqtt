package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Ключи настроек. Значения вводятся в веб-интерфейсе, конфигов на диске нет.
const (
	KeyMQTTAddr      = "mqtt.addr"       // host:port брокера
	KeyMQTTUser      = "mqtt.user"       //
	KeyMQTTPassword  = "mqtt.password"   //
	KeyMQTTClientID  = "mqtt.client_id"  // пусто — соберётся из имени хоста
	KeyMQTTBaseTopic = "mqtt.base_topic" // корень топиков, по умолчанию microart
	KeyMQTTQoS       = "mqtt.qos"        //
	KeyMQTTRetain    = "mqtt.retain"     //
	KeyMQTTTLS       = "mqtt.tls"        // подключаться по tls://

	KeyPollInterval = "poll.interval_sec"     // интервал по умолчанию
	KeyHTTPTimeout  = "poll.http_timeout_sec" //
	KeyRepublish    = "poll.republish_sec"    // как часто слать всё заново
	KeyOfflineAfter = "poll.offline_after"    // неудач подряд до offline
)

// Значения по умолчанию. Малина усредняет данные с шагом 5 секунд, а
// minute_data обновляется раз в минуту — опрашивать чаще смысла нет.
const (
	DefaultPollInterval = 30 * time.Second
	DefaultHTTPTimeout  = 5 * time.Second
	DefaultRepublish    = 10 * time.Minute
	DefaultOfflineAfter = 3
	DefaultBaseTopic    = "microart"
)

// Config — то, что демон поднимает при старте.
//
// Плоская структура, а не дерево: это набор полей одной формы в вебе, и так
// его проще и заполнять, и сохранять одной транзакцией.
type Config struct {
	MQTTAddr      string
	MQTTUser      string
	MQTTPassword  string
	MQTTClientID  string
	MQTTBaseTopic string
	MQTTQoS       int
	MQTTRetain    bool
	MQTTTLS       bool

	PollInterval time.Duration
	HTTPTimeout  time.Duration
	Republish    time.Duration
	OfflineAfter int
}

// Ready сообщает, что настроек достаточно для запуска. Пока это не так, веб
// показывает мастер первого запуска.
func (c Config) Ready() bool { return c.MQTTAddr != "" }

// BrokerURL собирает адрес для клиента MQTT.
func (c Config) BrokerURL() string {
	addr := c.MQTTAddr
	if strings.Contains(addr, "://") {
		return addr
	}
	if !strings.Contains(addr, ":") {
		if c.MQTTTLS {
			addr += ":8883"
		} else {
			addr += ":1883"
		}
	}
	if c.MQTTTLS {
		return "tls://" + addr
	}
	return "tcp://" + addr
}

// Validate проверяет то, что можно проверить без сети.
func (c Config) Validate() error {
	if c.MQTTAddr == "" {
		return fmt.Errorf("не задан адрес брокера")
	}
	if c.MQTTQoS < 0 || c.MQTTQoS > 2 {
		return fmt.Errorf("QoS должен быть 0, 1 или 2")
	}
	if strings.ContainsAny(c.MQTTBaseTopic, "+#") {
		return fmt.Errorf("корень топиков не должен содержать + и #")
	}
	if c.OfflineAfter < 1 {
		return fmt.Errorf("число неудач до offline должно быть не меньше 1")
	}
	return nil
}

// Config читает настройки, подставляя значения по умолчанию.
func (s *Store) Config(ctx context.Context) (Config, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Config{}, fmt.Errorf("store: чтение настроек: %w", err)
	}
	defer rows.Close()

	raw := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Config{}, fmt.Errorf("store: чтение настроек: %w", err)
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return Config{}, fmt.Errorf("store: чтение настроек: %w", err)
	}

	c := Config{
		MQTTAddr:      raw[KeyMQTTAddr],
		MQTTUser:      raw[KeyMQTTUser],
		MQTTPassword:  raw[KeyMQTTPassword],
		MQTTClientID:  raw[KeyMQTTClientID],
		MQTTBaseTopic: orString(raw[KeyMQTTBaseTopic], DefaultBaseTopic),
		MQTTQoS:       orInt(raw[KeyMQTTQoS], 0),
		MQTTRetain:    orBool(raw[KeyMQTTRetain], true),
		MQTTTLS:       orBool(raw[KeyMQTTTLS], false),

		PollInterval: orDuration(raw[KeyPollInterval], DefaultPollInterval),
		HTTPTimeout:  orDuration(raw[KeyHTTPTimeout], DefaultHTTPTimeout),
		Republish:    orDuration(raw[KeyRepublish], DefaultRepublish),
		OfflineAfter: orInt(raw[KeyOfflineAfter], DefaultOfflineAfter),
	}
	return c, nil
}

// SaveConfig сохраняет настройки одной транзакцией.
func (s *Store) SaveConfig(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: сохранение настроек: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	values := map[string]string{
		KeyMQTTAddr:      strings.TrimSpace(c.MQTTAddr),
		KeyMQTTUser:      c.MQTTUser,
		KeyMQTTPassword:  c.MQTTPassword,
		KeyMQTTClientID:  strings.TrimSpace(c.MQTTClientID),
		KeyMQTTBaseTopic: strings.Trim(strings.TrimSpace(c.MQTTBaseTopic), "/"),
		KeyMQTTQoS:       strconv.Itoa(c.MQTTQoS),
		KeyMQTTRetain:    strconv.FormatBool(c.MQTTRetain),
		KeyMQTTTLS:       strconv.FormatBool(c.MQTTTLS),

		KeyPollInterval: strconv.Itoa(int(c.PollInterval.Seconds())),
		KeyHTTPTimeout:  strconv.Itoa(int(c.HTTPTimeout.Seconds())),
		KeyRepublish:    strconv.Itoa(int(c.Republish.Seconds())),
		KeyOfflineAfter: strconv.Itoa(c.OfflineAfter),
	}

	for k, v := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("store: сохранение %s: %w", k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: сохранение настроек: %w", err)
	}
	return nil
}

func orString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orInt(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func orBool(v string, def bool) bool {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func orDuration(v string, def time.Duration) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
}
