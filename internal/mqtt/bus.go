// Package mqtt — клиент брокера: подключение с LWT, публикация и топики.
package mqtt

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/staskuznec/microart2mqtt/internal/store"
)

// Статусы доступности — обычная для MQTT пара значений.
const (
	Online  = "online"
	Offline = "offline"
)

const (
	// publishTimeout — сколько ждём подтверждения публикации.
	publishTimeout = 5 * time.Second
	// connectTimeout — сколько ждём первого подключения, прежде чем уйти
	// подключаться в фоне.
	connectTimeout = 5 * time.Second
)

// Status — состояние подключения для веб-страницы.
type Status struct {
	Configured bool
	Connected  bool
	Broker     string
	ClientID   string
	Since      time.Time
	LastError  string
}

// Bus публикует значения в брокер.
type Bus struct {
	client paho.Client
	log    *slog.Logger

	base      string
	qos       byte
	retain    bool
	broker    string
	client_id string

	// generation растёт на каждом подключении. По её смене поллеры понимают,
	// что связь переустанавливалась, и переотправляют всё: ретейн на новом
	// (или перезапущенном) брокере мог не сохраниться.
	generation atomic.Uint64

	connected atomic.Bool
	since     atomic.Int64
	lastErr   atomic.Value // string
}

// New создаёт клиента и подключается к брокеру.
//
// LWT вешается на <base>/bridge/availability: если демон отвалится, брокер сам
// разошлёт offline. Доступность каждого инвертора публикуется отдельно — LWT в
// MQTT один на соединение, а соединение у нас общее.
//
// Ошибка первого подключения не фатальна: клиент продолжает переподключаться
// в фоне, а инверторы можно опрашивать и до появления брокера.
func New(cfg store.Config, log *slog.Logger) (*Bus, error) {
	b := &Bus{
		log:       log,
		base:      cfg.MQTTBaseTopic,
		qos:       byte(cfg.MQTTQoS),
		retain:    cfg.MQTTRetain,
		broker:    cfg.BrokerURL(),
		client_id: clientID(cfg.MQTTClientID),
	}
	b.lastErr.Store("")

	opts := paho.NewClientOptions().
		AddBroker(b.broker).
		SetClientID(b.client_id).
		SetKeepAlive(30*time.Second).
		SetConnectTimeout(connectTimeout).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(time.Minute).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		SetCleanSession(true).
		SetOrderMatters(false).
		SetWill(b.BridgeTopic("availability"), Offline, byte(cfg.MQTTQoS), true)

	if cfg.MQTTUser != "" {
		opts.SetUsername(cfg.MQTTUser)
		opts.SetPassword(cfg.MQTTPassword)
	}
	if cfg.MQTTTLS {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	opts.SetOnConnectHandler(func(paho.Client) {
		b.generation.Add(1)
		b.connected.Store(true)
		b.since.Store(time.Now().Unix())
		b.lastErr.Store("")
		log.Info("подключились к брокеру", "broker", b.broker, "client_id", b.client_id)
		// Ретейн на bridge/availability перетирает offline от прошлого LWT.
		_ = b.Publish(b.BridgeTopic("availability"), Online, true)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		b.connected.Store(false)
		b.lastErr.Store(err.Error())
		log.Error("соединение с брокером потеряно", "err", err)
	})

	b.client = paho.NewClient(opts)

	token := b.client.Connect()
	if !token.WaitTimeout(connectTimeout) {
		b.lastErr.Store("таймаут подключения")
		return b, fmt.Errorf("брокер %s не ответил за %s", b.broker, connectTimeout)
	}
	if err := token.Error(); err != nil {
		b.lastErr.Store(err.Error())
		return b, fmt.Errorf("подключение к брокеру %s: %w", b.broker, err)
	}
	return b, nil
}

// clientID подставляет имя хоста, если идентификатор не задан: два демона в
// одной сети с одинаковым client_id выбивали бы друг друга.
func clientID(configured string) string {
	if configured != "" {
		return configured
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "microart2mqtt"
	}
	return "microart2mqtt-" + host
}

// Topic собирает топик инвертора: <base>/<inverter>/<suffix>.
func (b *Bus) Topic(inverter, suffix string) string {
	return fmt.Sprintf("%s/%s/%s", b.base, inverter, suffix)
}

// BridgeTopic собирает топик самого демона: <base>/bridge/<suffix>.
func (b *Bus) BridgeTopic(suffix string) string {
	return fmt.Sprintf("%s/bridge/%s", b.base, suffix)
}

// Base возвращает корень топиков.
func (b *Bus) Base() string { return b.base }

// Publish отправляет значение в топик. Возвращает ошибку, чтобы вызывающий не
// считал значение доставленным: иначе фильтр изменений «запомнит» то, что на
// самом деле не ушло, и топик останется пустым до планового рефреша.
func (b *Bus) Publish(topic string, payload any, retain bool) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("клиент брокера не создан")
	}
	if !b.client.IsConnected() {
		return fmt.Errorf("нет соединения с брокером")
	}

	token := b.client.Publish(topic, b.qos, retain, payload)
	// При QoS 0 токен завершается сразу; ждём коротко, чтобы не копить горутины.
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("публикация в %s не подтверждена за %s", topic, publishTimeout)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("публикация в %s: %w", topic, err)
	}
	return nil
}

// PublishValue публикует значение с ретейном из настроек.
func (b *Bus) PublishValue(topic string, payload any) error {
	return b.Publish(topic, payload, b.retain)
}

// PublishJSON сериализует значение в JSON и публикует.
func (b *Bus) PublishJSON(topic string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("сериализация %s: %w", topic, err)
	}
	return b.PublishValue(topic, data)
}

// IsConnected сообщает, есть ли сейчас соединение с брокером.
func (b *Bus) IsConnected() bool {
	return b != nil && b.client != nil && b.client.IsConnected()
}

// Generation возвращает номер текущего подключения к брокеру.
func (b *Bus) Generation() uint64 {
	if b == nil {
		return 0
	}
	return b.generation.Load()
}

// Status отдаёт состояние подключения веб-странице.
func (b *Bus) Status() Status {
	if b == nil {
		return Status{}
	}
	s := Status{
		Configured: true,
		Connected:  b.IsConnected(),
		Broker:     b.broker,
		ClientID:   b.client_id,
	}
	if sec := b.since.Load(); sec > 0 {
		s.Since = time.Unix(sec, 0)
	}
	if v, ok := b.lastErr.Load().(string); ok {
		s.LastError = v
	}
	return s
}

// Close помечает мост offline и корректно отключается.
func (b *Bus) Close() {
	if b == nil || b.client == nil {
		return
	}
	if b.client.IsConnected() {
		_ = b.Publish(b.BridgeTopic("availability"), Offline, true)
	}
	b.client.Disconnect(500) // мс на дослать очередь
}
