// Package logging — журнал демона.
//
// Кроме обычного вывода в stderr (его подхватывает systemd) записи оседают в
// кольцевом буфере в памяти: демон работает службой, и увидеть причину
// неполадки надо не выходя из браузера.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Форматы вывода.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ringSize — сколько последних записей держим в памяти. 500 строк это
// десятки килобайт: хватает, чтобы разобрать недавний сбой, и не растёт.
const ringSize = 500

// Entry — запись журнала для веб-страницы.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   []Attr
}

// Attr — поле записи.
type Attr struct {
	Key   string
	Value string
}

// Ring — последние записи журнала. Безопасен для одновременного доступа:
// пишут горутины опроса, читает веб.
type Ring struct {
	mu   sync.RWMutex
	buf  []Entry
	next int
	full bool
}

// NewRing создаёт буфер на ringSize записей.
func NewRing() *Ring {
	return &Ring{buf: make([]Entry, ringSize)}
}

// Add кладёт запись, вытесняя самую старую.
func (r *Ring) Add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Entries возвращает записи от новых к старым: не ниже minLevel и не больше
// limit штук.
func (r *Ring) Entries(limit int, minLevel slog.Level) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > len(r.buf) {
		limit = len(r.buf)
	}

	out := make([]Entry, 0, limit)
	// Идём назад от последней записанной позиции.
	count := len(r.buf)
	if !r.full {
		count = r.next
	}
	for i := 0; i < count && len(out) < limit; i++ {
		idx := (r.next - 1 - i + len(r.buf)*2) % len(r.buf)
		e := r.buf[idx]
		if e.Time.IsZero() || e.Level < minLevel {
			continue
		}
		out = append(out, e)
	}
	return out
}

// handler дублирует записи в кольцевой буфер, а всё остальное отдаёт
// обёрнутому обработчику.
type handler struct {
	inner slog.Handler
	ring  *Ring
	attrs []Attr
	group string
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make([]Attr, 0, len(h.attrs)+r.NumAttrs())
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, Attr{Key: h.prefixed(a.Key), Value: valueOf(a.Value)})
		return true
	})

	h.ring.Add(Entry{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Attrs:   attrs,
	})
	return h.inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	next := &handler{inner: h.inner.WithAttrs(as), ring: h.ring, group: h.group}
	next.attrs = append(append([]Attr{}, h.attrs...), toAttrs(h.prefix(), as)...)
	return next
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{
		inner: h.inner.WithGroup(name),
		ring:  h.ring,
		attrs: append([]Attr{}, h.attrs...),
		group: name,
	}
}

func (h *handler) prefix() string { return h.group }

func (h *handler) prefixed(key string) string {
	if h.group == "" {
		return key
	}
	return h.group + "." + key
}

func toAttrs(prefix string, as []slog.Attr) []Attr {
	out := make([]Attr, 0, len(as))
	for _, a := range as {
		key := a.Key
		if prefix != "" {
			key = prefix + "." + key
		}
		out = append(out, Attr{Key: key, Value: valueOf(a.Value)})
	}
	return out
}

func valueOf(v slog.Value) string {
	return fmt.Sprintf("%v", v.Any())
}

// New собирает журнал: вывод в w и кольцевой буфер для веба.
func New(w io.Writer, level, format string) (*slog.Logger, *Ring, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var inner slog.Handler
	switch strings.ToLower(format) {
	case FormatJSON:
		inner = slog.NewJSONHandler(w, opts)
	case FormatText, "":
		inner = slog.NewTextHandler(w, opts)
	default:
		return nil, nil, fmt.Errorf("неизвестный формат журнала %q: нужен %s или %s", format, FormatText, FormatJSON)
	}

	ring := NewRing()
	return slog.New(&handler{inner: inner, ring: ring}), ring, nil
}

// ParseLevel переводит имя уровня в slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("неизвестный уровень журнала %q: нужен debug, info, warn или error", s)
	}
}

// LevelName — короткое имя уровня для веб-страницы.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}
