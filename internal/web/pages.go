package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/staskuznec/microart2mqtt/internal/logging"
	"github.com/staskuznec/microart2mqtt/internal/mqtt"
	"github.com/staskuznec/microart2mqtt/internal/store"
	"github.com/staskuznec/microart2mqtt/internal/update"
)

// ---- Обзор -----------------------------------------------------------------

type overviewInverter struct {
	ID        int64
	Name      string
	URL       string
	Enabled   bool
	Available bool
	LastOK    time.Time
	LastError string
	Values    []namedValue
}

type namedValue struct {
	Name  string
	Value string
}

type overviewData struct {
	page
	MQTT           mqtt.Status
	BaseTopic      string
	Uptime         time.Duration
	Online         int
	Total          int
	Inverters      []overviewInverter
	InstallCommand string
}

func (s *Server) pageOverview(w http.ResponseWriter, r *http.Request) {
	// Результат проверки мог протухнуть, пока страницу не открывали.
	s.updates.EnsureFresh(r.Context())

	cfg, err := s.db.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inverters, err := s.db.Inverters(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses := s.app.PollerStatus()
	data := overviewData{
		page:           s.page("Обзор", "overview"),
		MQTT:           s.app.MQTTStatus(),
		BaseTopic:      cfg.MQTTBaseTopic,
		Uptime:         s.app.Uptime(),
		Total:          len(inverters),
		InstallCommand: update.InstallCommand,
	}
	data.Error, data.Notice = messages(r)

	for _, inv := range inverters {
		item := overviewInverter{ID: inv.ID, Name: inv.Name, URL: inv.URL, Enabled: inv.Enabled}
		if st, ok := statuses[inv.Name]; ok {
			item.Available = st.Available
			item.LastOK = st.LastOK
			item.LastError = st.LastError
			// Показываем в том же порядке, в каком метрики настроены.
			for _, m := range inv.Metrics {
				if v, ok := st.Values[m.Name]; ok {
					item.Values = append(item.Values, namedValue{Name: m.Name, Value: v})
				}
			}
		}
		if item.Available {
			data.Online++
		}
		data.Inverters = append(data.Inverters, item)
	}

	s.render(w, "overview", data)
}

// ---- Инверторы --------------------------------------------------------------

type inverterRow struct {
	store.Inverter
	Available bool
}

type invertersData struct {
	page
	Inverters []inverterRow
}

func (s *Server) pageInverters(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.Inverters(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses := s.app.PollerStatus()
	data := invertersData{page: s.page("Инверторы", "inverters")}
	data.Error, data.Notice = messages(r)
	for _, inv := range list {
		row := inverterRow{Inverter: inv}
		if st, ok := statuses[inv.Name]; ok {
			row.Available = st.Available
		}
		data.Inverters = append(data.Inverters, row)
	}

	s.render(w, "inverters", data)
}

type inverterFormData struct {
	page
	Inverter       store.Inverter
	BaseTopic      string
	DefaultPoll    string
	DefaultTimeout string
}

func (s *Server) pageInverterForm(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.db.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := inverterFormData{
		page:           s.page("Инвертор", "inverters"),
		Inverter:       store.Inverter{Enabled: true},
		BaseTopic:      cfg.MQTTBaseTopic,
		DefaultPoll:    cfg.PollInterval.String(),
		DefaultTimeout: cfg.HTTPTimeout.String(),
	}
	data.Error, data.Notice = messages(r)

	if idStr := r.PathValue("id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		inv, err := s.db.Inverter(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Inverter = inv
	}

	s.render(w, "inverter-form", data)
}

func (s *Server) saveInverter(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, "/inverters", "не удалось разобрать форму", "")
		return
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	inv := store.Inverter{
		ID:              id,
		Name:            r.FormValue("name"),
		URL:             r.FormValue("url"),
		PollIntervalSec: atoi(r.FormValue("poll_interval_sec")),
		HTTPTimeoutSec:  atoi(r.FormValue("http_timeout_sec")),
		Enabled:         r.FormValue("enabled") != "",
	}

	newID, err := s.db.SaveInverter(r.Context(), inv)
	if err != nil {
		s.redirect(w, r, "/inverters", err.Error(), "")
		return
	}

	notice := "Инвертор сохранён"
	if id == 0 && r.FormValue("preset") != "" {
		added, err := s.db.AddMetrics(r.Context(), newID, store.DefaultMetrics())
		if err != nil {
			s.redirect(w, r, "/inverters", "инвертор создан, но набор метрик не добавился: "+err.Error(), "")
			return
		}
		notice = "Инвертор создан, добавлено метрик: " + strconv.Itoa(added)
	}

	s.reload(r)
	s.redirect(w, r, "/inverters/"+strconv.FormatInt(newID, 10)+"/metrics", "", notice)
}

func (s *Server) toggleInverter(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inv, err := s.db.Inverter(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.db.SetInverterEnabled(r.Context(), id, !inv.Enabled); err != nil {
		s.redirect(w, r, "/inverters", err.Error(), "")
		return
	}

	s.reload(r)
	s.redirect(w, r, "/inverters", "", "")
}

func (s *Server) deleteInverter(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.db.DeleteInverter(r.Context(), id); err != nil {
		s.redirect(w, r, "/inverters", err.Error(), "")
		return
	}

	s.reload(r)
	s.redirect(w, r, "/inverters", "", "Инвертор удалён")
}

// ---- Настройки ---------------------------------------------------------------

type settingsData struct {
	page
	Config       store.Config
	Hostname     string
	PollSec      int
	TimeoutSec   int
	RepublishSec int
}

func (s *Server) pageSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.db.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := settingsData{
		page:         s.page("Настройки", "settings"),
		Config:       cfg,
		Hostname:     hostname(),
		PollSec:      int(cfg.PollInterval.Seconds()),
		TimeoutSec:   int(cfg.HTTPTimeout.Seconds()),
		RepublishSec: int(cfg.Republish.Seconds()),
	}
	data.Error, data.Notice = messages(r)

	s.render(w, "settings", data)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirect(w, r, "/settings", "не удалось разобрать форму", "")
		return
	}

	cfg := store.Config{
		MQTTAddr:      r.FormValue("mqtt_addr"),
		MQTTUser:      r.FormValue("mqtt_user"),
		MQTTPassword:  r.FormValue("mqtt_password"),
		MQTTClientID:  r.FormValue("mqtt_client_id"),
		MQTTBaseTopic: r.FormValue("mqtt_base_topic"),
		MQTTQoS:       atoi(r.FormValue("mqtt_qos")),
		MQTTRetain:    r.FormValue("mqtt_retain") != "",
		MQTTTLS:       r.FormValue("mqtt_tls") != "",

		PollInterval: time.Duration(atoi(r.FormValue("poll_interval_sec"))) * time.Second,
		HTTPTimeout:  time.Duration(atoi(r.FormValue("http_timeout_sec"))) * time.Second,
		Republish:    time.Duration(atoi(r.FormValue("republish_sec"))) * time.Second,
		OfflineAfter: atoi(r.FormValue("offline_after")),
	}

	if err := s.db.SaveConfig(r.Context(), cfg); err != nil {
		s.redirect(w, r, "/settings", err.Error(), "")
		return
	}

	s.reload(r)
	s.redirect(w, r, "/settings", "", "Настройки сохранены, опрос перезапущен")
}

// ---- Журнал --------------------------------------------------------------------

type logData struct {
	page
	Entries []logEntry
	Level   string
	Levels  []string
}

type logEntry struct {
	Time      time.Time
	LevelName string
	Message   string
	Attrs     []logging.Attr
}

func (s *Server) pageLog(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "info"
	}
	min, err := logging.ParseLevel(level)
	if err != nil {
		min = slog.LevelInfo
		level = "info"
	}

	data := logData{
		page:   s.page("Журнал", "log"),
		Level:  level,
		Levels: []string{"debug", "info", "warn", "error"},
	}
	data.Error, data.Notice = messages(r)

	for _, e := range s.log.Entries(300, min) {
		data.Entries = append(data.Entries, logEntry{
			Time:      e.Time,
			LevelName: logging.LevelName(e.Level),
			Message:   e.Message,
			Attrs:     e.Attrs,
		})
	}

	s.render(w, "log", data)
}

// ---- Обновления ------------------------------------------------------------------

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	info := s.updates.Check(r.Context())

	switch {
	case info.Error != "":
		s.redirect(w, r, "/", "проверка обновлений: "+info.Error, "")
	case info.HasUpdate:
		s.redirect(w, r, "/", "", "Вышла версия "+info.Latest)
	default:
		s.redirect(w, r, "/", "", "Установлена последняя версия")
	}
}

// ---- API ----------------------------------------------------------------------------

func (s *Server) apiState(w http.ResponseWriter, r *http.Request) {
	inverters, err := s.db.Inverters(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses := s.app.PollerStatus()
	type invState struct {
		Name      string            `json:"name"`
		URL       string            `json:"url"`
		Enabled   bool              `json:"enabled"`
		Available bool              `json:"available"`
		LastOK    string            `json:"last_ok,omitempty"`
		LastError string            `json:"last_error,omitempty"`
		Values    map[string]string `json:"values,omitempty"`
	}

	out := struct {
		Version   string     `json:"version"`
		UptimeSec int        `json:"uptime_sec"`
		MQTT      any        `json:"mqtt"`
		Inverters []invState `json:"inverters"`
	}{
		Version:   s.version,
		UptimeSec: int(s.app.Uptime().Seconds()),
		MQTT:      s.app.MQTTStatus(),
	}

	for _, inv := range inverters {
		st := invState{Name: inv.Name, URL: inv.URL, Enabled: inv.Enabled}
		if p, ok := statuses[inv.Name]; ok {
			st.Available = p.Available
			st.LastError = p.LastError
			st.Values = p.Values
			if !p.LastOK.IsZero() {
				st.LastOK = p.LastOK.Format(time.RFC3339)
			}
		}
		out.Inverters = append(out.Inverters, st)
	}
	sort.Slice(out.Inverters, func(i, j int) bool { return out.Inverters[i].Name < out.Inverters[j].Name })

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// reload поднимает опрос заново после правки. Ошибку только пишем в журнал:
// настройки уже сохранены, и человеку важнее увидеть их сохранёнными.
func (s *Server) reload(r *http.Request) {
	if err := s.app.Reload(r.Context()); err != nil {
		slog.Error("перезапуск опроса", "err", err)
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
