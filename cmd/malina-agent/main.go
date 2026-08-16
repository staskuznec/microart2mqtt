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
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/staskuznec/microart2mqtt/internal/microart"
	"github.com/staskuznec/microart2mqtt/internal/update"
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

	// Общий сигнал «применить настройки заново»: его шлют и SIGUSR1, и веб-
	// страница после сохранения. Буфер 1 + неблокирующая отправка — лишние
	// триггеры схлопываются.
	reload := make(chan struct{}, 1)
	trigger := func() {
		select {
		case reload <- struct{}{}:
		default:
		}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	go func() {
		for range sig {
			trigger()
		}
	}()

	ag := &agent{path: *cfgPath, api: *apiURL}

	// Проверка новых версий: раз в сутки спрашиваем GitHub и показываем на
	// странице, что вышло обновление. Ставить сами ничего не будем — решение
	// за человеком, устройство не должно менять свой бинарник без спроса.
	ag.updates = update.New(version, slog.Default())
	go ag.updates.Run(ctx)

	// Веб-страница агента поднимается один раз на всё время работы. Порт берём
	// из первого чтения конфига (при смене порта нужен рестарт агента).
	startCfg, _ := LoadConfig(*cfgPath)
	ag.startWeb(*cfgPath, startCfg, trigger)

	for {
		ag.runOnce(ctx, reload)
		select {
		case <-ctx.Done():
			ag.shutdown()
			return
		default:
			// применяем настройки заново — следующая итерация цикла
		}
	}
}

// agent держит одно подключение к брокеру и цикл опроса.
type agent struct {
	path    string
	api     string
	updates *update.Checker

	mu         sync.Mutex
	client     mqtt.Client
	base       string
	qos        byte
	retain     bool
	lastValues map[string]string
	lastFull   time.Time
}

// runOnce поднимает подключение и опрашивает до SIGUSR1 или остановки.
func (a *agent) runOnce(ctx context.Context, reload <-chan struct{}) {
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

	a.lastValues = make(map[string]string, 128)
	a.lastFull = time.Time{}

	interval := cfg.PollInterval()
	slog.Info("агент запущен", "broker", cfg.BrokerURL(), "base", cfg.BaseTopic,
		"interval", interval)

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
			slog.Info("применяю настройки заново")
			// Если новыми настройками агент выключают — пометим offline, пока
			// соединение живо (чистый disconnect LWT не пришлёт).
			if ncfg, err := LoadConfig(a.path); err == nil && !ncfg.Enabled {
				a.setAvailability(offline)
			}
			return
		case <-ticker.C:
			a.poll(api, cfg)
		}
	}
}

func (a *agent) waitReload(ctx context.Context, reload <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-reload:
	case <-t.C:
	}
}

// devices — разделы API, которые публикуем целиком.
var devices = []string{"map", "bat", "mppt"}

// poll делает один цикл: тянет сырой JSON каждого раздела и публикует ВСЕ поля.
// Ничего описывать не надо — что устройство отдаёт, то и уходит в топики.
func (a *agent) poll(api *microart.MicroArt, cfg Config) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("паника в опросе, продолжаю", "panic", r)
		}
	}()

	// topic -> строковое значение за этот цикл.
	values := make(map[string]string, 128)
	// раздельные сырые JSON для топиков <base>/<device>.
	rawByDevice := make(map[string]string, len(devices))
	reached := false // хоть один раздел ответил

	for _, dev := range devices {
		raw, err := api.RawJSON(dev)
		if err != nil {
			// map/bat обязательны, mppt может отсутствовать — не шумим по нему.
			if dev != "mppt" {
				slog.Warn("read_json не ответил", "device", dev, "err", err)
			}
			continue
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			slog.Debug("не разобрать JSON раздела", "device", dev, "err", err)
			continue
		}
		fields, err := microart.Fields(tree)
		if err != nil || len(fields) == 0 {
			continue
		}
		reached = true
		rawByDevice[dev] = strings.TrimSpace(string(raw))
		for _, f := range fields {
			topic := dev + "/" + topicPath(f.Path)
			values[topic] = f.Value
		}
	}

	if !reached {
		a.setAvailability(offline)
		return
	}
	a.setAvailability(online)
	a.publish(cfg, values, rawByDevice)
}

// publish шлёт изменившиеся значения; раз в republish — весь набор заново.
func (a *agent) publish(cfg Config, values map[string]string, rawByDevice map[string]string) {
	full := cfg.RepublishInterval() <= 0 || a.lastFull.IsZero() ||
		time.Since(a.lastFull) >= cfg.RepublishInterval()

	changed, failed := 0, 0
	for topic, text := range values {
		if !full && a.lastValues[topic] == text {
			continue
		}
		if a.pub(a.base+"/"+topic, text, a.retain) {
			a.lastValues[topic] = text
			changed++
		} else {
			delete(a.lastValues, topic)
			failed++
		}
	}

	// Заодно кладём сырой JSON каждого раздела целиком — удобно тем, кто хочет
	// разобрать сам. Только при полном цикле, чтобы не гонять большие пейлоады.
	if full {
		for dev, raw := range rawByDevice {
			if !a.pub(a.base+"/"+dev, raw, a.retain) {
				failed++
			}
		}
	}

	if failed > 0 {
		a.lastFull = time.Time{}
	} else if full {
		a.lastFull = time.Now()
	}
	slog.Debug("опубликовано", "changed", changed, "full", full, "known", len(a.lastValues))
}

// topicPath превращает путь поля API в хвост топика: точки -> слэши, а
// недопустимые в топиках символы (+ # пробел) заменяем на «_».
func topicPath(p string) string {
	p = strings.ReplaceAll(p, ".", "/")
	return strings.Map(func(r rune) rune {
		switch r {
		case '+', '#', ' ', '\t':
			return '_'
		default:
			return r
		}
	}, p)
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
