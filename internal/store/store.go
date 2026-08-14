// Package store — хранилище демона поверх SQLite.
//
// Драйвер взят чистый на Go (modernc.org/sqlite): он не требует cgo, поэтому
// сборка под ARMv7 остаётся такой же однострочной, как и под хост.
//
// Конфигов на диске нет: рядом с бинарником лежит только файл базы, а брокер,
// инверторы и метрики задаются в веб-интерфейсе. Файл создаётся с правами
// 0600 — внутри пароль к брокеру.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// FileMode — права на файл базы. Внутри пароль к брокеру, читать его
// посторонним незачем.
const FileMode os.FileMode = 0o600

// Store — открытая база со всеми накатанными миграциями.
type Store struct {
	db   *sql.DB
	path string
}

// Open открывает базу по пути path, создавая её при необходимости, и приводит
// схему к последней версии.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: путь к базе %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: каталог для базы: %w", err)
	}

	// busy_timeout задаётся прямо в DSN, чтобы он действовал уже на первых
	// запросах — включая те, которыми накатываются миграции.
	dsn := "file:" + abs + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: открытие базы: %w", err)
	}

	// Запись идёт из веба, чтение — из горутин опроса. Один писатель избавляет
	// от «database is locked» на медленной флешке.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, path: abs}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Права выставляем после создания файла: SQLite создаёт его сам.
	if err := os.Chmod(abs, FileMode); err != nil {
		s.Close()
		return nil, fmt.Errorf("store: права на базу: %w", err)
	}
	return s, nil
}

// Path возвращает путь к файлу базы.
func (s *Store) Path() string { return s.path }

// Close закрывает базу.
func (s *Store) Close() error { return s.db.Close() }

// Ping проверяет, что база отвечает.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
