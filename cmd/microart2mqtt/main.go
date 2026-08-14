// Демон-мост между инверторами MicroArt и MQTT.
//
// Опрашивает «Малины» инверторов по HTTP (read_json.php) и публикует значения
// в топики брокера. Конфигов на диске нет: рядом с бинарником лежит только
// файл базы, а брокер, инверторы и метрики задаются в веб-интерфейсе.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/staskuznec/microart2mqtt/internal/app"
	"github.com/staskuznec/microart2mqtt/internal/logging"
	"github.com/staskuznec/microart2mqtt/internal/store"
	"github.com/staskuznec/microart2mqtt/internal/update"
	"github.com/staskuznec/microart2mqtt/internal/web"
)

// version подставляется при сборке через -ldflags.
var version = "dev"

const (
	// DefaultDBName — имя файла базы рядом с бинарником, если путь не задан.
	DefaultDBName = "microart2mqtt.db"

	// shutdownTimeout — сколько ждём, пока веб дообслужит запросы.
	shutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		// Журнал к этому моменту может быть ещё не настроен, поэтому пишем
		// прямо в stderr — иначе причина падения просто потеряется.
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr      = flag.String("addr", "127.0.0.1:8081", "адрес веб-интерфейса, host:port")
		dbPath    = flag.String("db", "", "путь к файлу базы (по умолчанию "+DefaultDBName+" рядом с бинарником)")
		basePath  = flag.String("base-path", "", "подкаталог, если демон стоит за веб-сервером: /microart")
		logLevel  = flag.String("log-level", "info", "уровень журнала: debug, info, warn, error")
		logFormat = flag.String("log-format", logging.FormatText, "формат журнала: text или json")
		showVer   = flag.Bool("version", false, "показать версию и выйти")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return nil
	}

	log, ring, err := logging.New(os.Stdout, *logLevel, *logFormat)
	if err != nil {
		return err
	}

	path, err := resolveDBPath(*dbPath)
	if err != nil {
		return err
	}

	// Контекст живёт до Ctrl+C или SIGTERM от systemd.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("закрытие базы", "err", err)
		}
	}()

	log.Info("запускаем microart2mqtt", "version", version, "db", path, "web", *addr)

	application := app.New(db, log, version)
	defer application.Close()

	// Настроек может ещё не быть — это нормально, веб для того и нужен.
	if err := application.Reload(ctx); err != nil {
		log.Error("первый запуск опроса", "err", err)
	}

	updates := update.New(version, log)
	go updates.Run(ctx)

	srv, err := web.New(db, application, ring, updates, version, *basePath)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("веб-интерфейс: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал остановки, завершаем работу")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("веб-интерфейс не остановился вовремя", "err", err)
	}
	return nil
}

// resolveDBPath возвращает путь к базе: заданный явно или рядом с бинарником.
func resolveDBPath(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("не удалось определить каталог демона: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), DefaultDBName), nil
}
