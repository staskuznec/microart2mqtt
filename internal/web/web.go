// Package web — HTTP-интерфейс демона.
//
// Здесь настраивается всё: брокер, инверторы и метрики. Страница открывается
// и сама по себе, и вкладкой внутри панели MimiSetup через iframe, поэтому
// вёрстка простая и без внешних файлов.
package web

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/staskuznec/microart2mqtt/internal/app"
	"github.com/staskuznec/microart2mqtt/internal/logging"
	"github.com/staskuznec/microart2mqtt/internal/store"
	"github.com/staskuznec/microart2mqtt/internal/update"
)

// healthTimeout — сколько ждём базу при проверке состояния. Проверка обязана
// отвечать быстро: по ней systemd и мониторинг судят, жив ли демон.
const healthTimeout = 2 * time.Second

// Server — веб-интерфейс.
type Server struct {
	db      *store.Store
	app     *app.App
	log     *logging.Ring
	updates *update.Checker
	version string
	base    string

	pages map[string]*template.Template
}

// New собирает сервер. basePath — подкаталог, если демон стоит за веб-сервером.
func New(db *store.Store, application *app.App, ring *logging.Ring,
	updates *update.Checker, version, basePath string) (*Server, error) {

	s := &Server{
		db:      db,
		app:     application,
		log:     ring,
		updates: updates,
		version: version,
		base:    strings.TrimRight(basePath, "/"),
		pages:   make(map[string]*template.Template, len(allPages)),
	}

	// Каждая страница определяет свой "content", поэтому в один набор шаблоны
	// не складываются — собираем по одному дереву на страницу. Разбор при
	// старте, а не на каждый запрос: ошибку в шаблоне надо ловить сразу, а не
	// когда человек откроет страницу.
	for name, content := range allPages {
		tpl, err := template.New(name).Funcs(funcs).Parse(layout)
		if err != nil {
			return nil, fmt.Errorf("шаблон обвязки: %w", err)
		}
		if _, err := tpl.Parse(content); err != nil {
			return nil, fmt.Errorf("шаблон %s: %w", name, err)
		}
		s.pages[name] = tpl
	}
	return s, nil
}

// Handler отдаёт маршруты веб-интерфейса.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.pageOverview)

	mux.HandleFunc("GET /inverters", s.pageInverters)
	mux.HandleFunc("GET /inverters/new", s.pageInverterForm)
	mux.HandleFunc("GET /inverters/{id}", s.pageInverterForm)
	mux.HandleFunc("POST /inverters/save", s.saveInverter)
	mux.HandleFunc("POST /inverters/{id}/toggle", s.toggleInverter)
	mux.HandleFunc("POST /inverters/{id}/delete", s.deleteInverter)

	mux.HandleFunc("GET /inverters/{id}/metrics", s.pageMetrics)
	mux.HandleFunc("GET /inverters/{id}/metrics/new", s.pageMetricForm)
	mux.HandleFunc("GET /inverters/{id}/metrics/{mid}", s.pageMetricForm)
	mux.HandleFunc("POST /inverters/{id}/metrics/save", s.saveMetric)
	mux.HandleFunc("POST /inverters/{id}/metrics/preset", s.addPreset)
	mux.HandleFunc("POST /inverters/{id}/metrics/copy", s.copyMetrics)
	mux.HandleFunc("POST /metrics/{mid}/delete", s.deleteMetric)

	mux.HandleFunc("GET /inverters/{id}/discover", s.pageDiscover)
	mux.HandleFunc("POST /inverters/{id}/metrics/add", s.addDiscovered)

	mux.HandleFunc("GET /settings", s.pageSettings)
	mux.HandleFunc("POST /settings", s.saveSettings)

	mux.HandleFunc("GET /log", s.pageLog)
	mux.HandleFunc("POST /update/check", s.checkUpdate)

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/state", s.apiState)

	if s.base == "" {
		return mux
	}

	// За прокси демон живёт в подкаталоге: снимаем префикс перед разбором пути
	// и уводим голый /mqtt на /mqtt/, иначе относительные ссылки ломаются.
	outer := http.NewServeMux()
	outer.Handle(s.base+"/", http.StripPrefix(s.base, mux))
	outer.HandleFunc(s.base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.base+"/", http.StatusMovedPermanently)
	})
	return outer
}

// page — общие поля всех страниц.
type page struct {
	Title   string
	Nav     string
	Base    string
	Version string
	Update  update.Info
	Error   string
	Notice  string
}

func (s *Server) page(title, nav string) page {
	return page{
		Title:   title,
		Nav:     nav,
		Base:    s.base,
		Version: s.version,
		Update:  s.updates.Info(),
	}
}

// render отрисовывает страницу по имени, разобранному при старте.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	tpl, ok := s.pages[name]
	if !ok {
		http.Error(w, "неизвестная страница "+name, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "layout", data); err != nil {
		// Заголовки уже ушли, отдать 500 нельзя — остаётся журнал.
		slog.Error("отрисовка страницы", "err", err)
	}
}

// redirect уводит обратно, добавляя сообщение в адрес.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path, errMsg, notice string) {
	u := s.base + path
	switch {
	case errMsg != "":
		u += "?err=" + urlEscape(errMsg)
	case notice != "":
		u += "?ok=" + urlEscape(notice)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func urlEscape(s string) string {
	return strings.NewReplacer(" ", "%20", "\"", "%22", "?", "%3F", "&", "%26",
		"#", "%23", "+", "%2B", "\n", " ").Replace(s)
}

// messages достаёт сообщения из адреса.
func messages(r *http.Request) (errMsg, notice string) {
	q := r.URL.Query()
	return q.Get("err"), q.Get("ok")
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		http.Error(w, "база недоступна", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "host"
	}
	return h
}
