// malina-agent — агент на «Малине» инвертора МикроАрт.
//
// Крутится на самом устройстве, опрашивает локальный read_json.php (данные
// МикроАрт кладут в разделяемую память, PHP их оттуда отдаёт) и публикует в
// MQTT-брокер. Настройки — /settings/web-data/mqtt.json, их пишет страница
// «MQTT» в вебморде; агент перечитывает по SIGUSR1.
//
// Локальный HTTP выбран намеренно: не нужен cgo ради System V shm, а разбор
// ответов — тот же код, что у центрального демона (internal/microart).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/staskuznec/microart2mqtt/internal/microart"
)

var version = "dev"

const (
	// localAPI — read_json.php на самой малине. Раз в секунду это дёшево.
	localAPI = "http://127.0.0.1"
	// httpTimeout — таймаут локального запроса.
	httpTimeout = 5 * time.Second

	online  = "online"
	offline = "offline"
)

func main() {
	cfgPath := flag.String("config", "/settings/web-data/mqtt.json", "путь к файлу настроек")
	apiURL := flag.String("api", localAPI, "базовый URL read_json.php (по умолчанию localhost)")
	showVer := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// SIGUSR1 — перечитать конфиг (шлёт веб-страница после «Сохранить»).
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGUSR1)

	ag := &agent{path: *cfgPath, api: *apiURL}

	for {
		ag.runOnce(ctx, reload)
		select {
		case <-ctx.Done():
			ag.shutdown()
			return
		default:
			// перезапуск по SIGUSR1 — просто следующая итерация цикла
		}
	}
}

// agent держит одно подключение к брокеру и цикл опроса.
type agent struct {
	path string
	api  string

	mu         sync.Mutex
	client     mqtt.Client
	base       string
	qos        byte
	retain     bool
	lastValues map[string]string
	lastFull   time.Time
}

// runOnce поднимает подключение и опрашивает до SIGUSR1 или остановки.
func (a *agent) runOnce(ctx context.Context, reload <-chan os.Signal) {
	cfg, err := LoadConfig(a.path)
	if err != nil {
		slog.Error("не удалось прочитать конфиг, жду сигнала", "err", err)
		a.waitReload(ctx, reload, time.Minute)
		return
	}
	if !cfg.Enabled {
		slog.Info("агент выключен в настройках, жду сигнала")
		a.waitReload(ctx, reload, time.Minute)
		return
	}
	if cfg.BrokerURL() == "" {
		slog.Warn("не задан адрес брокера, жду сигнала")
		a.waitReload(ctx, reload, time.Minute)
		return
	}

	a.connect(cfg)
	defer a.disconnect()

	apiBase := a.api
	if apiBase == "" {
		apiBase = localAPI
	}
	api, err := microart.NewMicroArt(microart.MicroArtOption{BaseURL: apiBase, Timeout: httpTimeout})
	if err != nil {
		slog.Error("не удалось создать клиент API", "err", err)
		a.waitReload(ctx, reload, time.Minute)
		return
	}
	defer api.Close()

	a.lastValues = make(map[string]string, len(cfg.Metrics))
	a.lastFull = time.Time{}

	interval := cfg.PollInterval()
	slog.Info("агент запущен", "broker", cfg.BrokerURL(), "base", cfg.BaseTopic,
		"interval", interval, "metrics", len(cfg.Metrics))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	a.poll(api, cfg)
	for {
		select {
		case <-ctx.Done():
			// Останавливаемся штатно: чистый disconnect не шлёт LWT, поэтому
			// сами выставляем offline, пока соединение ещё живо.
			a.setAvailability(offline)
			return
		case <-reload:
			slog.Info("получен SIGUSR1 — перечитываю настройки")
			return
		case <-ticker.C:
			a.poll(api, cfg)
		}
	}
}

func (a *agent) waitReload(ctx context.Context, reload <-chan os.Signal, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-reload:
	case <-t.C:
	}
}

// poll делает один цикл: тянет нужные разделы API и публикует изменения.
func (a *agent) poll(api *microart.MicroArt, cfg Config) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("паника в опросе, продолжаю", "panic", r)
		}
	}()

	var needMap, needBat, needMPPT bool
	for _, m := range cfg.Metrics {
		switch m.Device {
		case "map":
			needMap = true
		case "bat":
			needBat = true
		case "mppt":
			needMPPT = true
		}
	}

	var mapResp *microart.MapResponse
	var batResp *microart.BatteryResponse
	var mpptResp *microart.MPPTResponse
	var err error

	if needMap {
		if mapResp, err = api.GetDeviceInfo(); err != nil {
			a.setAvailability(offline)
			slog.Warn("read_json map не ответил", "err", err)
			return
		}
	}
	if needBat {
		if b, e := api.GetBatteryInfo(); e != nil {
			a.setAvailability(offline)
			slog.Warn("read_json bat не ответил", "err", e)
			return
		} else {
			batResp = &b
		}
	}
	if needMPPT {
		if mpptResp, err = api.GetMpptInfo(); err != nil {
			slog.Debug("read_json mppt не ответил (нет контроллеров?)", "err", err)
		}
	}

	values := make(map[string]any, len(cfg.Metrics))
	raw := make(map[string]string, len(cfg.Metrics))
	for _, m := range cfg.Metrics {
		var src any
		switch m.Device {
		case "map":
			if mapResp != nil {
				src = mapResp
			}
		case "bat":
			if batResp != nil {
				src = *batResp
			}
		case "mppt":
			if mpptResp != nil {
				src = mpptResp
			}
		}
		if src == nil {
			continue
		}
		s, err := microart.FieldFrom(src, m.Target)
		if err != nil || strings.TrimSpace(s) == "" {
			continue
		}
		val, text := convert(m, s)
		values[m.Name] = val
		raw[m.Name] = text
	}
	if len(values) == 0 {
		a.setAvailability(offline)
		return
	}

	a.setAvailability(online)
	a.publish(cfg, values, raw)
}

// publish отправляет изменившиеся значения; раз в republish — весь набор.
func (a *agent) publish(cfg Config, values map[string]any, raw map[string]string) {
	full := cfg.RepublishInterval() <= 0 || a.lastFull.IsZero() ||
		time.Since(a.lastFull) >= cfg.RepublishInterval()

	changed, failed := 0, 0
	for _, m := range cfg.Metrics {
		text, ok := raw[m.Name]
		if !ok {
			continue
		}
		if !full && a.lastValues[m.Name] == text {
			continue
		}
		if a.pub(a.base+"/"+m.FlatTopic(), text, a.retain) {
			a.lastValues[m.Name] = text
			changed++
		} else {
			delete(a.lastValues, m.Name)
			failed++
		}
	}

	if changed > 0 || full {
		values["ts"] = time.Now().Format(time.RFC3339)
		if data := toJSON(values); data != nil {
			if !a.pub(a.base+"/state", data, a.retain) {
				failed++
			}
		}
	}
	if failed > 0 {
		a.lastFull = time.Time{}
	} else if full {
		a.lastFull = time.Now()
	}
	slog.Debug("опубликовано", "changed", changed, "full", full)
}

// --- MQTT ---

func (a *agent) connect(cfg Config) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.base = cfg.BaseTopic
	a.qos = byte(cfg.QoS)
	a.retain = cfg.Retain

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL()).
		SetClientID(clientID(cfg.ClientID)).
		SetKeepAlive(30*time.Second).
		SetConnectTimeout(5*time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		SetCleanSession(true).
		SetWill(cfg.BaseTopic+"/availability", offline, byte(cfg.QoS), true)
	if cfg.Username != "" {
		opts.SetUsername(cfg.Username).SetPassword(cfg.Password)
	}
	opts.SetOnConnectHandler(func(mqtt.Client) {
		slog.Info("подключились к брокеру", "broker", cfg.BrokerURL())
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, e error) {
		slog.Warn("связь с брокером потеряна", "err", e)
	})

	a.client = mqtt.NewClient(opts)
	t := a.client.Connect()
	if !t.WaitTimeout(5*time.Second) || t.Error() != nil {
		slog.Warn("брокер пока недоступен, переподключаюсь в фоне")
	}
}

func (a *agent) pub(topic string, payload any, retain bool) bool {
	a.mu.Lock()
	c := a.client
	qos := a.qos
	a.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return false
	}
	t := c.Publish(topic, qos, retain, payload)
	if !t.WaitTimeout(5*time.Second) || t.Error() != nil {
		return false
	}
	return true
}

func (a *agent) setAvailability(state string) {
	a.pub(a.base+"/availability", state, true)
}

func (a *agent) disconnect() {
	a.mu.Lock()
	c := a.client
	a.client = nil
	a.mu.Unlock()
	if c != nil && c.IsConnected() {
		c.Disconnect(300)
	}
}

func (a *agent) shutdown() {
	if a.base != "" {
		a.pub(a.base+"/availability", offline, true)
	}
	a.disconnect()
}

func clientID(configured string) string {
	if configured != "" {
		return configured
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "malina-agent"
	}
	return "malina-agent-" + h
}

// toJSON сериализует стейт; при ошибке возвращает nil (публикацию пропустим).
func toJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("не удалось собрать JSON стейта", "err", err)
		return nil
	}
	return b
}

// convert приводит значение к числу или строке по настройке метрики.
func convert(m Metric, s string) (any, string) {
	if !m.IsNumber() {
		return s, s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s, s
	}
	if m.Precision != nil {
		p := math.Pow10(*m.Precision)
		f = math.Round(f*p) / p
	}
	return f, strconv.FormatFloat(f, 'f', -1, 64)
}
