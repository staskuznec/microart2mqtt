// Package app связывает хранилище, брокер и опрос инверторов.
//
// Всё настраивается в вебе и применяется без перезапуска: после правки
// настроек или списка инверторов веб дёргает Reload, и опрос поднимается
// заново по свежим данным.
package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/staskuznec/microart2mqtt/internal/mqtt"
	"github.com/staskuznec/microart2mqtt/internal/poller"
	"github.com/staskuznec/microart2mqtt/internal/store"
)

const (
	// bridgeStateInterval — как часто демон рассказывает о себе в bridge/state.
	bridgeStateInterval = time.Minute
	// stopTimeout — сколько ждём остановки поллеров при перенастройке.
	stopTimeout = 15 * time.Second
)

// App — работающая часть демона: брокер и поллеры инверторов.
type App struct {
	db      *store.Store
	log     *slog.Logger
	version string

	mu      sync.RWMutex
	bus     *mqtt.Bus
	pollers []*poller.Poller
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started time.Time
	lastErr string
}

// New создаёт приложение. Соединения поднимает Reload.
func New(db *store.Store, log *slog.Logger, version string) *App {
	return &App{db: db, log: log, version: version, started: time.Now()}
}

// Reload останавливает текущий опрос и поднимает его заново по свежим
// настройкам. Вызывается при старте и после каждой правки в вебе.
func (a *App) Reload(ctx context.Context) error {
	cfg, err := a.db.Config(ctx)
	if err != nil {
		return err
	}
	inverters, err := a.db.Inverters(ctx)
	if err != nil {
		return err
	}

	a.stop()

	if !cfg.Ready() {
		a.log.Info("настройки не заполнены, опрос не запускаем")
		a.mu.Lock()
		a.lastErr = "не задан адрес брокера"
		a.mu.Unlock()
		return nil
	}

	bus, err := mqtt.New(cfg, a.log)
	if err != nil {
		// Не фатально: клиент продолжит подключаться в фоне, а инверторы можно
		// опрашивать и до появления брокера.
		a.log.Warn("брокер пока недоступен, продолжаем в фоне", "err", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())

	var started []*poller.Poller
	enabled := make([]store.Inverter, 0, len(inverters))
	for _, inv := range inverters {
		if inv.Enabled {
			enabled = append(enabled, inv)
		}
	}

	for i, inv := range enabled {
		p, err := poller.New(inv, cfg, bus, a.log)
		if err != nil {
			a.log.Error("инвертор пропущен", "inverter", inv.Name, "err", err)
			continue
		}
		started = append(started, p)

		// Равномерно размазываем старты внутри интервала опроса, чтобы десять
		// «Малин» не опрашивались одновременно.
		delay := time.Duration(0)
		if len(enabled) > 1 {
			delay = time.Duration(i) * inv.PollInterval(cfg.PollInterval) / time.Duration(len(enabled))
		}

		a.wg.Add(1)
		go func(p *poller.Poller, delay time.Duration) {
			defer a.wg.Done()
			if delay > 0 {
				select {
				case <-runCtx.Done():
					return
				case <-time.After(delay):
				}
			}
			p.Run(runCtx)
		}(p, delay)
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.reportBridgeState(runCtx)
	}()

	a.mu.Lock()
	a.bus = bus
	a.pollers = started
	a.cancel = cancel
	a.lastErr = ""
	a.mu.Unlock()

	a.log.Info("опрос запущен", "inverters", len(started), "broker", cfg.BrokerURL())
	return nil
}

// stop останавливает поллеров и отключается от брокера.
func (a *App) stop() {
	a.mu.Lock()
	cancel := a.cancel
	bus := a.bus
	pollers := a.pollers
	a.cancel = nil
	a.bus = nil
	a.pollers = nil
	a.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()

	// Ждём, пока поллеры дожмут текущий цикл, но не бесконечно: за перенастройкой
	// стоит человек с открытой страницей.
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopTimeout):
		a.log.Warn("поллеры не остановились вовремя", "timeout", stopTimeout)
	}

	if bus != nil {
		for _, p := range pollers {
			p.MarkOffline()
		}
		bus.Close()
	}
}

// Close останавливает всё при завершении демона.
func (a *App) Close() { a.stop() }

// reportBridgeState публикует состояние самого демона: по нему видно, что мост
// жив и сколько инверторов отвечает.
func (a *App) reportBridgeState(ctx context.Context) {
	ticker := time.NewTicker(bridgeStateInterval)
	defer ticker.Stop()

	publish := func() {
		a.mu.RLock()
		bus := a.bus
		pollers := a.pollers
		a.mu.RUnlock()

		if bus == nil {
			return
		}
		online := 0
		for _, p := range pollers {
			if p.Available() {
				online++
			}
		}
		_ = bus.PublishJSON(bus.BridgeTopic("state"), map[string]any{
			"version":          a.version,
			"uptime_sec":       int(time.Since(a.started).Seconds()),
			"inverters":        len(pollers),
			"inverters_online": online,
			"ts":               time.Now().Format(time.RFC3339),
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

// MQTTStatus отдаёт состояние подключения к брокеру.
func (a *App) MQTTStatus() mqtt.Status {
	a.mu.RLock()
	bus := a.bus
	lastErr := a.lastErr
	a.mu.RUnlock()

	if bus == nil {
		return mqtt.Status{LastError: lastErr}
	}
	return bus.Status()
}

// PollerStatus отдаёт состояние инверторов, которые сейчас опрашиваются.
func (a *App) PollerStatus() map[string]poller.Status {
	a.mu.RLock()
	pollers := a.pollers
	a.mu.RUnlock()

	out := make(map[string]poller.Status, len(pollers))
	for _, p := range pollers {
		out[p.Name()] = p.Status()
	}
	return out
}

// Uptime — сколько демон работает.
func (a *App) Uptime() time.Duration { return time.Since(a.started) }
