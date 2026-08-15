// Package poller опрашивает одну «Малину» по HTTP и публикует значения в MQTT.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/staskuznec/microart2mqtt/internal/microart"
	"github.com/staskuznec/microart2mqtt/internal/mqtt"
	"github.com/staskuznec/microart2mqtt/internal/store"
)

// Status — что показывать про инвертор в вебе.
type Status struct {
	Name      string
	URL       string
	Available bool
	Fails     int
	LastOK    time.Time
	LastError string
	Values    map[string]string // имя метрики -> последнее опубликованное значение
}

// Poller опрашивает один инвертор в своём темпе.
type Poller struct {
	name         string
	url          string
	api          *microart.MicroArt
	bus          *mqtt.Bus
	metrics      []store.Metric
	interval     time.Duration
	republish    time.Duration
	offlineAfter int
	log          *slog.Logger

	mu           sync.RWMutex
	lastValues   map[string]string
	lastFullSend time.Time
	lastGen      uint64
	lastOK       time.Time
	lastErr      string
	fails        int
	available    bool
	started      bool
}

// New создаёт поллер для инвертора.
func New(inv store.Inverter, cfg store.Config, bus *mqtt.Bus, log *slog.Logger) (*Poller, error) {
	api, err := microart.NewMicroArt(microart.MicroArtOption{
		BaseURL: inv.URL,
		Timeout: inv.HTTPTimeout(cfg.HTTPTimeout),
	})
	if err != nil {
		return nil, fmt.Errorf("клиент MicroArt для %s: %w", inv.Name, err)
	}

	// В опрос идут только включённые метрики.
	enabled := make([]store.Metric, 0, len(inv.Metrics))
	for _, m := range inv.Metrics {
		if m.Enabled {
			enabled = append(enabled, m)
		}
	}

	return &Poller{
		name:         inv.Name,
		url:          inv.URL,
		api:          api,
		bus:          bus,
		metrics:      enabled,
		interval:     inv.PollInterval(cfg.PollInterval),
		republish:    cfg.Republish,
		offlineAfter: cfg.OfflineAfter,
		log:          log.With("inverter", inv.Name),
		lastValues:   make(map[string]string, len(enabled)),
	}, nil
}

// Name возвращает имя инвертора.
func (p *Poller) Name() string { return p.name }

// Available сообщает, отвечал ли инвертор на последнем опросе.
func (p *Poller) Available() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.available
}

// Status отдаёт состояние инвертора веб-странице.
func (p *Poller) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()

	values := make(map[string]string, len(p.lastValues))
	for k, v := range p.lastValues {
		values[k] = v
	}
	return Status{
		Name:      p.name,
		URL:       p.url,
		Available: p.available,
		Fails:     p.fails,
		LastOK:    p.lastOK,
		LastError: p.lastErr,
		Values:    values,
	}
}

// Run опрашивает инвертор до отмены контекста.
func (p *Poller) Run(ctx context.Context) {
	defer p.api.Close()

	p.log.Info("запускаем опрос", "interval", p.interval, "metrics", len(p.metrics))
	p.pollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.log.Info("останавливаем опрос")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

// pollOnce делает один цикл: забирает данные и публикует изменения.
// Паника в разборе ответа не должна ронять весь демон.
func (p *Poller) pollOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("паника в цикле опроса, продолжаем", "panic", r)
		}
	}()

	if ctx.Err() != nil || len(p.metrics) == 0 {
		return
	}

	start := time.Now()
	mapResp, batResp, mpptResp, err := p.fetch()
	if err != nil {
		p.onFailure(err)
		return
	}

	values := make(map[string]any, len(p.metrics))
	raw := make(map[string]string, len(p.metrics))
	for _, m := range p.metrics {
		src := sourceFor(m, mapResp, batResp, mpptResp)
		if src == nil {
			continue
		}
		s, err := microart.FieldFrom(src, m.Target)
		if err != nil {
			p.log.Warn("поле не найдено в ответе API", "metric", m.Name, "target", m.Target, "err", err)
			continue
		}
		if strings.TrimSpace(s) == "" {
			// «Малина» не прислала это поле для своей конфигурации. Публиковать
			// пустое значение в ретейн-топик хуже, чем не публиковать вовсе:
			// подписчик получит пустоту вместо прежнего значения.
			p.log.Debug("поле пустое, пропускаем", "metric", m.Name, "target", m.Target)
			continue
		}
		val, text := convert(m, s)
		values[m.Name] = val
		raw[m.Name] = text
	}

	if len(values) == 0 {
		p.onFailure(fmt.Errorf("ни одно поле не удалось прочитать"))
		return
	}

	p.onSuccess()
	p.publish(values, raw)

	p.log.Debug("опрос завершён", "values", len(values), "took", time.Since(start).Truncate(time.Millisecond))
}

// fetch забирает нужные ответы API — по одному разу на цикл, независимо от
// того, сколько метрик из них читается.
func (p *Poller) fetch() (mapResp *microart.MapResponse, batResp *microart.BatteryResponse, mpptResp *microart.MPPTResponse, err error) {
	var needMap, needBat, needMPPT bool
	for _, m := range p.metrics {
		switch m.Device {
		case store.DeviceMap:
			needMap = true
		case store.DeviceBat:
			needBat = true
		case store.DeviceMPPT:
			needMPPT = true
		}
	}

	if needMap {
		mapResp, err = p.api.GetDeviceInfo()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read_json.php?device=map: %w", err)
		}
	}
	if needBat {
		bat, batErr := p.api.GetBatteryInfo()
		if batErr != nil {
			return nil, nil, nil, fmt.Errorf("read_json.php?device=bat: %w", batErr)
		}
		batResp = &bat
	}
	if needMPPT {
		mpptResp, err = p.api.GetMpptInfo()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read_json.php?device=mppt: %w", err)
		}
	}
	return mapResp, batResp, mpptResp, nil
}

func sourceFor(m store.Metric, mapResp *microart.MapResponse, batResp *microart.BatteryResponse, mpptResp *microart.MPPTResponse) any {
	switch m.Device {
	case store.DeviceMap:
		if mapResp == nil {
			return nil
		}
		return mapResp
	case store.DeviceBat:
		if batResp == nil {
			return nil
		}
		return *batResp
	case store.DeviceMPPT:
		if mpptResp == nil {
			return nil
		}
		return mpptResp
	}
	return nil
}

// publish отправляет JSON-стейт и плоские топики. Плоские значения шлём только
// изменившиеся: каждая публикация — это работа для брокера и всех подписчиков.
func (p *Poller) publish(values map[string]any, raw map[string]string) {
	p.mu.Lock()
	full := p.republish <= 0 || p.lastFullSend.IsZero() || time.Since(p.lastFullSend) >= p.republish
	if gen := p.bus.Generation(); gen != p.lastGen {
		// Связь с брокером переустанавливалась — ретейн на той стороне мог не
		// сохраниться, отправляем всё заново.
		p.lastGen = gen
		full = true
	}
	known := make(map[string]string, len(p.lastValues))
	for k, v := range p.lastValues {
		known[k] = v
	}
	p.mu.Unlock()

	changed, failed := 0, 0
	sent := make(map[string]string, len(raw))
	for _, m := range p.metrics {
		text, ok := raw[m.Name]
		if !ok {
			continue
		}
		if !full && known[m.Name] == text {
			sent[m.Name] = text
			continue
		}
		if err := p.bus.PublishValue(p.bus.Topic(p.name, m.FlatTopic()), text); err != nil {
			// Не запоминаем значение: оно не доставлено и должно уйти снова.
			failed++
			continue
		}
		sent[m.Name] = text
		changed++
	}

	// JSON-стейт публикуем, если что-то поменялось или пришло время рефреша:
	// подписчику удобнее получать снимок целиком.
	if changed > 0 || full {
		values["ts"] = time.Now().Format(time.RFC3339)
		if err := p.bus.PublishJSON(p.bus.Topic(p.name, "state"), values); err != nil {
			failed++
		}
	}

	p.mu.Lock()
	p.lastValues = sent
	switch {
	case failed > 0:
		// Заставляем следующий цикл переотправить всё, что не дошло.
		p.lastFullSend = time.Time{}
	case full:
		p.lastFullSend = time.Now()
	}
	p.mu.Unlock()

	if failed > 0 {
		p.log.Warn("часть значений не ушла в брокер", "failed", failed, "published", changed)
	}
}

func (p *Poller) onSuccess() {
	p.mu.Lock()
	p.fails = 0
	p.lastOK = time.Now()
	p.lastErr = ""
	announce := !p.available || !p.started
	p.available = true
	p.started = true
	p.mu.Unlock()

	if announce {
		_ = p.bus.Publish(p.bus.Topic(p.name, "availability"), mqtt.Online, true)
		p.log.Info("инвертор доступен")
	}
}

func (p *Poller) onFailure(err error) {
	p.mu.Lock()
	p.fails++
	p.lastErr = err.Error()
	fails := p.fails
	announce := false
	if fails >= p.offlineAfter && (p.available || !p.started) {
		p.available = false
		p.started = true
		announce = true
		// Значения устарели: следующий успешный опрос отправит всё заново.
		p.lastValues = make(map[string]string, len(p.metrics))
		p.lastFullSend = time.Time{}
	}
	p.mu.Unlock()

	p.log.Warn("опрос не удался", "fails", fails, "err", err)
	if announce {
		_ = p.bus.Publish(p.bus.Topic(p.name, "availability"), mqtt.Offline, true)
		p.log.Error("инвертор помечен offline", "fails", fails)
	}
}

// MarkOffline выставляет offline — вызывается при остановке демона.
func (p *Poller) MarkOffline() {
	_ = p.bus.Publish(p.bus.Topic(p.name, "availability"), mqtt.Offline, true)
}

// convert приводит значение к виду из настроек: число для графиков, строка как есть.
func convert(m store.Metric, s string) (value any, text string) {
	if !m.IsNumber() {
		return s, s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Не число — публикуем как строку, это честнее, чем подставлять 0.
		return s, s
	}
	if m.Precision != nil {
		pow := math.Pow10(*m.Precision)
		f = math.Round(f*pow) / pow
	}
	return f, strconv.FormatFloat(f, 'f', -1, 64)
}
