package store

import (
	"context"
	"fmt"
)

// migrations — шаги схемы по порядку. Добавлять только в конец: номер шага
// запоминается в базе, и уже применённые не выполняются повторно.
var migrations = []string{
	// 1. Настройки: плоский набор ключ-значение. Это одна форма в вебе,
	// и так её проще сохранять одной транзакцией.
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,

	// 2. Инверторы: одна «Малина» — одна строка.
	`CREATE TABLE inverters (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		name              TEXT    NOT NULL UNIQUE,
		url               TEXT    NOT NULL,
		poll_interval_sec INTEGER NOT NULL DEFAULT 0,
		http_timeout_sec  INTEGER NOT NULL DEFAULT 0,
		enabled           INTEGER NOT NULL DEFAULT 1,
		position          INTEGER NOT NULL DEFAULT 0
	)`,

	// 3. Метрики привязаны к инвертору: удалили инвертор — ушли и они.
	// Имя уникально в пределах инвертора, оно же ключ в JSON-стейте.
	`CREATE TABLE metrics (
		id          INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		inverter_id INTEGER NOT NULL REFERENCES inverters(id) ON DELETE CASCADE,
		name        TEXT    NOT NULL,
		topic       TEXT    NOT NULL DEFAULT '',
		device      TEXT    NOT NULL,
		target      TEXT    NOT NULL,
		kind        TEXT    NOT NULL DEFAULT 'number',
		precision   INTEGER,
		enabled     INTEGER NOT NULL DEFAULT 1,
		position    INTEGER NOT NULL DEFAULT 0,
		UNIQUE (inverter_id, name)
	)`,

	`CREATE INDEX idx_metrics_inverter ON metrics(inverter_id, position)`,
}

// migrate приводит схему к последней версии.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY
	)`); err != nil {
		return fmt.Errorf("store: таблица версий схемы: %w", err)
	}

	var applied int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("store: версия схемы: %w", err)
	}

	for i := applied; i < len(migrations); i++ {
		version := i + 1

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: миграция %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: миграция %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: отметка миграции %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: фиксация миграции %d: %w", version, err)
		}
	}
	return nil
}
