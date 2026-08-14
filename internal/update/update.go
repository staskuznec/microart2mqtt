// Package update проверяет, вышла ли новая версия демона.
//
// Только проверка: скачиванием и заменой бинарника занимается install.sh,
// запущенный человеком. Демон, который снимает данные с инверторов, не должен
// подменять сам себя посреди дня — а вот сказать, что вышло обновление, он
// обязан, иначе о нём никто не узнает.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// repo — где живут релизы.
	repo = "staskuznec/microart2mqtt"

	// releasesURL — публичный API GitHub, без ключа и без учётной записи.
	releasesURL = "https://api.github.com/repos/" + repo + "/releases/latest"

	// checkInterval — как часто спрашиваем сами, в фоне. Раз в сутки: релизы
	// выходят реже, а у неавторизованного доступа к API есть предел обращений.
	checkInterval = 24 * time.Hour

	// freshFor — сколько результат считается свежим при заходе на «Обзор».
	freshFor = 15 * time.Minute

	requestTimeout = 15 * time.Second
)

// InstallCommand — строка, которой человек ставит обновление.
const InstallCommand = "curl -fsSL https://github.com/" + repo +
	"/releases/latest/download/install.sh | sudo sh"

// Info — что известно про версии.
type Info struct {
	Current   string
	Latest    string
	HasUpdate bool
	CheckedAt time.Time
	Error     string
}

// Checker спрашивает GitHub, не вышло ли новой версии.
type Checker struct {
	current string
	log     *slog.Logger
	client  *http.Client

	mu   sync.RWMutex
	info Info
}

// New создаёт проверяльщика для текущей версии.
func New(current string, log *slog.Logger) *Checker {
	return &Checker{
		current: current,
		log:     log,
		client:  &http.Client{Timeout: requestTimeout},
		info:    Info{Current: current},
	}
}

// Info возвращает последний известный результат.
func (c *Checker) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info
}

// Run проверяет при старте и дальше раз в сутки, пока жив контекст.
func (c *Checker) Run(ctx context.Context) {
	c.Check(ctx)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Check(ctx)
		}
	}
}

// EnsureFresh обновляет результат, если он успел устареть. Без этого человек,
// открывший страницу через час после старта, видел бы вчерашний ответ.
func (c *Checker) EnsureFresh(ctx context.Context) {
	c.mu.RLock()
	checked := c.info.CheckedAt
	c.mu.RUnlock()

	if time.Since(checked) > freshFor {
		c.Check(ctx)
	}
}

// Check ходит в GitHub и запоминает результат.
func (c *Checker) Check(ctx context.Context) Info {
	latest, err := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.info = Info{
		Current:   c.current,
		Latest:    latest,
		HasUpdate: err == nil && Newer(c.current, latest),
		CheckedAt: time.Now(),
	}
	if err != nil {
		c.info.Error = err.Error()
		c.log.Debug("проверка обновлений не удалась", "err", err)
	}
	return c.info
}

func (c *Checker) fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub ответил %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", fmt.Errorf("в ответе нет тега релиза")
	}
	return strings.TrimPrefix(body.TagName, "v"), nil
}

// Newer сравнивает версии вида 1.2.3. Отвечает false, если разобрать не вышло:
// на сборке из ветки (dev, хеш коммита) обновление предлагать нечего.
func Newer(current, latest string) bool {
	cur, okCur := parse(current)
	lat, okLat := parse(latest)
	if !okCur || !okLat {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int

	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" {
		return out, false
	}
	// Отбрасываем суффиксы вида -rc1 и +dirty.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}

	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
