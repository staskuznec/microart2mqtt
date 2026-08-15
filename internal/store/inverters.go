package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound — записи с таким идентификатором нет.
var ErrNotFound = errors.New("запись не найдена")

// Inverter — одна «Малина» с подключённым МАП.
type Inverter struct {
	ID              int64
	Name            string // попадает в топик: <base>/<name>/...
	URL             string
	PollIntervalSec int // 0 — брать из общих настроек
	HTTPTimeoutSec  int // 0 — брать из общих настроек
	Enabled         bool
	Position        int

	Metrics []Metric // заполняется только там, где нужно
}

// PollInterval возвращает интервал опроса с учётом общих настроек.
func (i Inverter) PollInterval(def time.Duration) time.Duration {
	if i.PollIntervalSec > 0 {
		return time.Duration(i.PollIntervalSec) * time.Second
	}
	return def
}

// HTTPTimeout возвращает таймаут запроса с учётом общих настроек.
func (i Inverter) HTTPTimeout(def time.Duration) time.Duration {
	if i.HTTPTimeoutSec > 0 {
		return time.Duration(i.HTTPTimeoutSec) * time.Second
	}
	return def
}

// Validate проверяет поля формы.
func (i Inverter) Validate() error {
	name := strings.TrimSpace(i.Name)
	switch {
	case name == "":
		return fmt.Errorf("имя не может быть пустым")
	case strings.ContainsAny(name, "/+# "):
		return fmt.Errorf("имя попадает в топик: без пробелов и символов / + #")
	}
	url := strings.TrimSpace(i.URL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("адрес должен начинаться с http:// или https://")
	}
	return nil
}

// Inverters возвращает все инверторы в порядке отображения, вместе с метриками.
func (s *Store) Inverters(ctx context.Context) ([]Inverter, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, poll_interval_sec, http_timeout_sec, enabled, position
		FROM inverters ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("store: список инверторов: %w", err)
	}
	defer rows.Close()

	var out []Inverter
	for rows.Next() {
		var inv Inverter
		if err := rows.Scan(&inv.ID, &inv.Name, &inv.URL, &inv.PollIntervalSec,
			&inv.HTTPTimeoutSec, &inv.Enabled, &inv.Position); err != nil {
			return nil, fmt.Errorf("store: список инверторов: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: список инверторов: %w", err)
	}

	for i := range out {
		m, err := s.Metrics(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Metrics = m
	}
	return out, nil
}

// Inverter возвращает один инвертор вместе с метриками.
func (s *Store) Inverter(ctx context.Context, id int64) (Inverter, error) {
	var inv Inverter
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, url, poll_interval_sec, http_timeout_sec, enabled, position
		FROM inverters WHERE id = ?`, id).
		Scan(&inv.ID, &inv.Name, &inv.URL, &inv.PollIntervalSec,
			&inv.HTTPTimeoutSec, &inv.Enabled, &inv.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return inv, ErrNotFound
	}
	if err != nil {
		return inv, fmt.Errorf("store: инвертор %d: %w", id, err)
	}

	inv.Metrics, err = s.Metrics(ctx, id)
	if err != nil {
		return inv, err
	}
	return inv, nil
}

// SaveInverter создаёт или обновляет инвертор и возвращает его идентификатор.
func (s *Store) SaveInverter(ctx context.Context, inv Inverter) (int64, error) {
	if err := inv.Validate(); err != nil {
		return 0, err
	}
	name := strings.TrimSpace(inv.Name)
	url := strings.TrimRight(strings.TrimSpace(inv.URL), "/")

	if inv.ID == 0 {
		// Новый встаёт в конец списка.
		var next int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), 0) + 1 FROM inverters`).Scan(&next); err != nil {
			return 0, fmt.Errorf("store: позиция инвертора: %w", err)
		}

		res, err := s.db.ExecContext(ctx, `
			INSERT INTO inverters (name, url, poll_interval_sec, http_timeout_sec, enabled, position)
			VALUES (?, ?, ?, ?, ?, ?)`,
			name, url, inv.PollIntervalSec, inv.HTTPTimeoutSec, inv.Enabled, next)
		if err != nil {
			return 0, wrapUnique(err, name)
		}
		return res.LastInsertId()
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE inverters
		SET name = ?, url = ?, poll_interval_sec = ?, http_timeout_sec = ?, enabled = ?
		WHERE id = ?`,
		name, url, inv.PollIntervalSec, inv.HTTPTimeoutSec, inv.Enabled, inv.ID)
	if err != nil {
		return 0, wrapUnique(err, name)
	}
	return inv.ID, nil
}

// SetInverterEnabled включает или выключает опрос инвертора.
func (s *Store) SetInverterEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE inverters SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return fmt.Errorf("store: переключение инвертора %d: %w", id, err)
	}
	return nil
}

// DeleteInverter удаляет инвертор вместе с его метриками.
func (s *Store) DeleteInverter(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM inverters WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: удаление инвертора %d: %w", id, err)
	}
	return nil
}

func wrapUnique(err error, name string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("имя %q уже занято", name)
	}
	return err
}
