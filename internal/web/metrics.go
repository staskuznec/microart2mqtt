package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/staskuznec/microart2mqtt/internal/microart"
	"github.com/staskuznec/microart2mqtt/internal/store"
)

type metricsData struct {
	page
	Inverter  store.Inverter
	Metrics   []store.Metric
	Values    map[string]string
	BaseTopic string
	Others    []store.Inverter // для копирования метрик
}

func (s *Server) pageMetrics(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}
	cfg, err := s.db.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	all, err := s.db.Inverters(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := metricsData{
		page:      s.page("Метрики", "inverters"),
		Inverter:  inv,
		Metrics:   inv.Metrics,
		BaseTopic: cfg.MQTTBaseTopic,
		Values:    map[string]string{},
	}
	data.Error, data.Notice = messages(r)

	if st, ok := s.app.PollerStatus()[inv.Name]; ok {
		data.Values = st.Values
	}
	for _, other := range all {
		if other.ID != inv.ID && len(other.Metrics) > 0 {
			data.Others = append(data.Others, other)
		}
	}

	s.render(w, "metrics", data)
}

type metricFormData struct {
	page
	Inverter store.Inverter
	Metric   store.Metric
}

func (s *Server) pageMetricForm(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}

	data := metricFormData{
		page:     s.page("Метрика", "inverters"),
		Inverter: inv,
		Metric:   store.Metric{InverterID: inv.ID, Device: store.DeviceMap, Kind: store.KindNumber, Enabled: true},
	}
	data.Error, data.Notice = messages(r)

	if midStr := r.PathValue("mid"); midStr != "" {
		mid, err := strconv.ParseInt(midStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		m, err := s.db.Metric(r.Context(), mid)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data.Metric = m
	}

	s.render(w, "metric-form", data)
}

func (s *Server) saveMetric(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.metricsRedirect(w, r, inv.ID, "не удалось разобрать форму", "")
		return
	}

	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	m := store.Metric{
		ID:         id,
		InverterID: inv.ID,
		Name:       r.FormValue("name"),
		Topic:      r.FormValue("topic"),
		Device:     r.FormValue("device"),
		Target:     r.FormValue("target"),
		Kind:       r.FormValue("kind"),
		Enabled:    r.FormValue("enabled") != "",
	}
	if p := strings.TrimSpace(r.FormValue("precision")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			s.metricsRedirect(w, r, inv.ID, "округление должно быть числом", "")
			return
		}
		m.Precision = &n
	}

	if _, err := s.db.SaveMetric(r.Context(), m); err != nil {
		s.metricsRedirect(w, r, inv.ID, err.Error(), "")
		return
	}

	s.reload(r)
	s.metricsRedirect(w, r, inv.ID, "", "Метрика сохранена")
}

func (s *Server) deleteMetric(w http.ResponseWriter, r *http.Request) {
	mid, err := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	m, err := s.db.Metric(r.Context(), mid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.db.DeleteMetric(r.Context(), mid); err != nil {
		s.metricsRedirect(w, r, m.InverterID, err.Error(), "")
		return
	}

	s.reload(r)
	s.metricsRedirect(w, r, m.InverterID, "", "Метрика удалена")
}

func (s *Server) addPreset(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}

	added, err := s.db.AddMetrics(r.Context(), inv.ID, store.DefaultMetrics())
	if err != nil {
		s.metricsRedirect(w, r, inv.ID, err.Error(), "")
		return
	}

	s.reload(r)
	s.metricsRedirect(w, r, inv.ID, "", plural(added, "Добавлена метрика", "Добавлено метрик", "Все метрики набора уже есть"))
}

func (s *Server) copyMetrics(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}
	from, err := strconv.ParseInt(r.FormValue("from"), 10, 64)
	if err != nil {
		s.metricsRedirect(w, r, inv.ID, "не выбран инвертор-источник", "")
		return
	}
	src, err := s.db.Inverter(r.Context(), from)
	if err != nil {
		s.metricsRedirect(w, r, inv.ID, "инвертор-источник не найден", "")
		return
	}

	added, err := s.db.AddMetrics(r.Context(), inv.ID, store.CopyMetrics(src.Metrics))
	if err != nil {
		s.metricsRedirect(w, r, inv.ID, err.Error(), "")
		return
	}

	s.reload(r)
	s.metricsRedirect(w, r, inv.ID, "", plural(added, "Скопирована метрика", "Скопировано метрик", "Все метрики уже есть"))
}

// ---- Подбор полей из живого API ------------------------------------------------

type discoverField struct {
	Path    string
	Value   string
	Suggest string
	Used    bool
	UsedAs  string
}

type discoverGroup struct {
	Device string
	Title  string
	Fields []discoverField
}

type discoverData struct {
	page
	Inverter store.Inverter
	Groups   []discoverGroup
	Failed   string
}

// pageDiscover ходит на «Малину» и показывает всё, что она сейчас отдаёт.
// Так метрика добавляется выбором из списка с живыми значениями, а не
// вслепую по названию поля из документации.
func (s *Server) pageDiscover(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}
	cfg, err := s.db.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := discoverData{page: s.page("Поля API", "inverters"), Inverter: inv}
	data.Error, data.Notice = messages(r)

	// Что уже настроено — показываем как занятое, чтобы не плодить дубли.
	used := make(map[string]string, len(inv.Metrics))
	for _, m := range inv.Metrics {
		used[m.Device+"|"+m.Target] = m.Name
	}

	api, err := microart.NewMicroArt(microart.MicroArtOption{
		BaseURL: inv.URL,
		Timeout: inv.HTTPTimeout(cfg.HTTPTimeout),
	})
	if err != nil {
		data.Failed = err.Error()
		s.render(w, "discover", data)
		return
	}
	defer func() { _ = api.Close() }()

	var failures []string
	add := func(device, title string, resp any, err error) {
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", title, err))
			return
		}
		fields, err := microart.Fields(resp)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", title, err))
			return
		}
		group := discoverGroup{Device: device, Title: title}
		for _, f := range fields {
			item := discoverField{Path: f.Path, Value: f.Value, Suggest: suggestName(f.Path)}
			if name, ok := used[device+"|"+f.Path]; ok {
				item.Used = true
				item.UsedAs = name
			}
			group.Fields = append(group.Fields, item)
		}
		if len(group.Fields) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}

	mapResp, mapErr := api.GetDeviceInfo()
	add(store.DeviceMap, "map — состояние МАП", mapResp, mapErr)

	batResp, batErr := api.GetBatteryInfo()
	add(store.DeviceBat, "bat — батарейный монитор", batResp, batErr)

	// MPPT есть не у всех: молчим, если контроллеров нет.
	if mpptResp, err := api.GetMpptInfo(); err == nil {
		add(store.DeviceMPPT, "mppt — контроллеры КЭС", mpptResp, nil)
	}

	if len(data.Groups) == 0 && len(failures) > 0 {
		data.Failed = "Малина не ответила. " + strings.Join(failures, "; ")
	}

	s.render(w, "discover", data)
}

// addDiscovered создаёт метрики из отмеченных полей.
func (s *Server) addDiscovered(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.inverterFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.metricsRedirect(w, r, inv.ID, "не удалось разобрать форму", "")
		return
	}

	picks := r.Form["pick"]
	if len(picks) == 0 {
		s.metricsRedirect(w, r, inv.ID, "не отмечено ни одного поля", "")
		return
	}

	list := make([]store.Metric, 0, len(picks))
	for _, pick := range picks {
		device, target, found := strings.Cut(pick, "|")
		if !found {
			continue
		}
		name := strings.TrimSpace(r.FormValue("name|" + pick))
		if name == "" {
			name = suggestName(target)
		}
		list = append(list, store.Metric{
			Name:    name,
			Topic:   strings.TrimSpace(r.FormValue("topic|" + pick)),
			Device:  device,
			Target:  target,
			Kind:    store.KindNumber,
			Enabled: true,
		})
	}

	added, err := s.db.AddMetrics(r.Context(), inv.ID, list)
	if err != nil {
		s.metricsRedirect(w, r, inv.ID, err.Error(), "")
		return
	}

	s.reload(r)
	s.metricsRedirect(w, r, inv.ID, "", plural(added, "Добавлена метрика", "Добавлено метрик", "Ничего не добавилось: имена уже заняты"))
}

// suggestName делает из пути API приличное имя метрики:
// minute_data.C_100_remain -> c_100_remain, _UNET -> unet.
func suggestName(path string) string {
	name := path
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(strings.TrimLeft(name, "_"))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	return strings.Trim(name, "_")
}

func (s *Server) inverterFromPath(w http.ResponseWriter, r *http.Request) (store.Inverter, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return store.Inverter{}, false
	}
	inv, err := s.db.Inverter(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return store.Inverter{}, false
	}
	return inv, true
}

func (s *Server) metricsRedirect(w http.ResponseWriter, r *http.Request, invID int64, errMsg, notice string) {
	s.redirect(w, r, "/inverters/"+strconv.FormatInt(invID, 10)+"/metrics", errMsg, notice)
}

func plural(n int, one, many, none string) string {
	if n == 0 {
		return none
	}
	if n == 1 {
		return one
	}
	return many + ": " + strconv.Itoa(n)
}
