package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Виды значений.
const (
	KindNumber = "number"
	KindString = "string"
)

// Источники данных — разделы API «Малины» (read_json.php?device=...).
const (
	DeviceMap  = "map"
	DeviceBat  = "bat"
	DeviceMPPT = "mppt"
)

// Metric — одно значение, которое публикуется в MQTT.
type Metric struct {
	ID         int64
	InverterID int64
	Name       string // ключ в JSON-стейте
	Topic      string // хвост плоского топика; пусто — берётся Name
	Device     string // map | bat | mppt
	Target     string // путь в ответе API: minute_data.C_100_remain
	Kind       string // number | string
	Precision  *int   // округление; nil — как есть
	Enabled    bool
	Position   int
}

// FlatTopic возвращает хвост плоского топика.
func (m Metric) FlatTopic() string {
	if t := strings.Trim(strings.TrimSpace(m.Topic), "/"); t != "" {
		return t
	}
	return m.Name
}

// IsNumber сообщает, публиковать ли значение числом.
func (m Metric) IsNumber() bool { return m.Kind != KindString }

// Validate проверяет поля формы.
func (m Metric) Validate() error {
	name := strings.TrimSpace(m.Name)
	switch {
	case name == "":
		return fmt.Errorf("имя не может быть пустым")
	case strings.ContainsAny(name, "/+# "):
		return fmt.Errorf("имя — ключ в JSON: без пробелов и символов / + #")
	}
	if strings.TrimSpace(m.Target) == "" {
		return fmt.Errorf("не указано поле API")
	}
	switch m.Device {
	case DeviceMap, DeviceBat, DeviceMPPT:
	default:
		return fmt.Errorf("источник должен быть map, bat или mppt")
	}
	switch m.Kind {
	case KindNumber, KindString, "":
	default:
		return fmt.Errorf("вид значения должен быть number или string")
	}
	if strings.ContainsAny(m.Topic, "+# ") {
		return fmt.Errorf("топик не должен содержать пробелов и символов + #")
	}
	if m.Precision != nil && (*m.Precision < 0 || *m.Precision > 6) {
		return fmt.Errorf("округление возможно от 0 до 6 знаков")
	}
	return nil
}

// Metrics возвращает метрики инвертора в порядке отображения.
func (s *Store) Metrics(ctx context.Context, inverterID int64) ([]Metric, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, inverter_id, name, topic, device, target, kind, precision, enabled, position
		FROM metrics WHERE inverter_id = ? ORDER BY position, id`, inverterID)
	if err != nil {
		return nil, fmt.Errorf("store: метрики инвертора %d: %w", inverterID, err)
	}
	defer rows.Close()

	var out []Metric
	for rows.Next() {
		var (
			m         Metric
			precision sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.InverterID, &m.Name, &m.Topic, &m.Device,
			&m.Target, &m.Kind, &precision, &m.Enabled, &m.Position); err != nil {
			return nil, fmt.Errorf("store: метрики инвертора %d: %w", inverterID, err)
		}
		if precision.Valid {
			p := int(precision.Int64)
			m.Precision = &p
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: метрики инвертора %d: %w", inverterID, err)
	}
	return out, nil
}

// Metric возвращает одну метрику.
func (s *Store) Metric(ctx context.Context, id int64) (Metric, error) {
	var (
		m         Metric
		precision sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, inverter_id, name, topic, device, target, kind, precision, enabled, position
		FROM metrics WHERE id = ?`, id).
		Scan(&m.ID, &m.InverterID, &m.Name, &m.Topic, &m.Device,
			&m.Target, &m.Kind, &precision, &m.Enabled, &m.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, fmt.Errorf("store: метрика %d: %w", id, err)
	}
	if precision.Valid {
		p := int(precision.Int64)
		m.Precision = &p
	}
	return m, nil
}

// SaveMetric создаёт или обновляет метрику.
func (s *Store) SaveMetric(ctx context.Context, m Metric) (int64, error) {
	if err := m.Validate(); err != nil {
		return 0, err
	}
	if m.Kind == "" {
		m.Kind = KindNumber
	}
	name := strings.TrimSpace(m.Name)
	topic := strings.Trim(strings.TrimSpace(m.Topic), "/")
	target := strings.TrimSpace(m.Target)

	var precision any
	if m.Precision != nil {
		precision = *m.Precision
	}

	if m.ID == 0 {
		var next int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), 0) + 1 FROM metrics WHERE inverter_id = ?`,
			m.InverterID).Scan(&next); err != nil {
			return 0, fmt.Errorf("store: позиция метрики: %w", err)
		}

		res, err := s.db.ExecContext(ctx, `
			INSERT INTO metrics (inverter_id, name, topic, device, target, kind, precision, enabled, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.InverterID, name, topic, m.Device, target, m.Kind, precision, m.Enabled, next)
		if err != nil {
			return 0, wrapUnique(err, name)
		}
		return res.LastInsertId()
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE metrics SET name = ?, topic = ?, device = ?, target = ?, kind = ?, precision = ?, enabled = ?
		WHERE id = ?`,
		name, topic, m.Device, target, m.Kind, precision, m.Enabled, m.ID)
	if err != nil {
		return 0, wrapUnique(err, name)
	}
	return m.ID, nil
}

// DeleteMetric удаляет метрику.
func (s *Store) DeleteMetric(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metrics WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: удаление метрики %d: %w", id, err)
	}
	return nil
}

// AddMetrics добавляет пачку метрик одному инвертору, пропуская те, чьи имена
// уже заняты. Используется набором по умолчанию и копированием с соседа.
func (s *Store) AddMetrics(ctx context.Context, inverterID int64, list []Metric) (added int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: добавление метрик: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), 0) FROM metrics WHERE inverter_id = ?`,
		inverterID).Scan(&next); err != nil {
		return 0, fmt.Errorf("store: позиция метрики: %w", err)
	}

	for _, m := range list {
		if err := m.Validate(); err != nil {
			return 0, fmt.Errorf("метрика %q: %w", m.Name, err)
		}
		if m.Kind == "" {
			m.Kind = KindNumber
		}
		var precision any
		if m.Precision != nil {
			precision = *m.Precision
		}
		next++

		// Имя уже занято — просто пропускаем: набор накладывается поверх
		// того, что человек уже настроил, и затирать его нельзя.
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO metrics (inverter_id, name, topic, device, target, kind, precision, enabled, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			inverterID, strings.TrimSpace(m.Name), strings.Trim(m.Topic, "/"),
			m.Device, strings.TrimSpace(m.Target), m.Kind, precision, true, next)
		if err != nil {
			return 0, fmt.Errorf("store: добавление метрики %q: %w", m.Name, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: добавление метрик: %w", err)
	}
	return added, nil
}
