package main

import (
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webServer поднимает веб-страницу агента. Вся вёрстка — здесь, в самом
// демоне: обновили бинарник — обновился и интерфейс, без отдельных PHP-файлов.
type webServer struct {
	cfgPath string
	status  func() (connected bool, base string)
	reload  func()       // применить настройки заново (перезапуск опроса)
	update  func() error // скачать и поставить новую версию (без перезапуска)
	restart func()       // перезапустить процесс новым бинарником
}

func (a *agent) startWeb(cfgPath string, cfg Config, reload func()) {
	ws := &webServer{
		cfgPath: cfgPath,
		status:  a.webStatus,
		reload:  reload,
		update:  a.selfUpdate,
		restart: a.restartSelf,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.index)
	mux.HandleFunc("/save", ws.save)
	mux.HandleFunc("/toggle", ws.toggle)
	mux.HandleFunc("/update", ws.doUpdate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	addr := cfg.WebAddr()
	srv := &http.Server{Handler: mux}

	// Порт занимаем СРАЗУ и синхронно — чтобы в логе было однозначно: либо
	// «веб слушает …», либо конкретная ошибка (порт занят, нет прав и т.п.),
	// а не молчание.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("НЕ удалось занять порт веб-страницы — открой не получится",
			"addr", addr, "err", err)
		return
	}
	slog.Info("веб-страница агента СЛУШАЕТ, открывай в браузере", "addr", addr)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("веб-страница остановилась", "err", err)
		}
	}()
}

func (a *agent) webStatus() (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	connected := a.client != nil && a.client.IsConnected()
	return connected, a.base
}

func (ws *webServer) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	cfg, _ := LoadConfig(ws.cfgPath)
	connected, base := ws.status()

	data := pageData{
		Config:     cfg,
		Connected:  connected,
		Base:       base,
		Version:    version,
		MalinaBase: malinaBase(r),
		Notice:     r.URL.Query().Get("ok"),
		Error:      r.URL.Query().Get("err"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		slog.Error("отрисовка страницы", "err", err)
	}
}

func (ws *webServer) save(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "./", http.StatusSeeOther)
		return
	}
	cfg, _ := LoadConfig(ws.cfgPath) // сохраняем поля, которых нет в форме
	cfg.Broker = strings.TrimSpace(r.FormValue("broker"))
	cfg.BaseTopic = strings.TrimSpace(r.FormValue("base_topic"))
	cfg.Username = r.FormValue("username")
	cfg.Password = r.FormValue("password")
	cfg.QoS = atoiClamp(r.FormValue("qos"), 0, 2, 0)
	cfg.Interval = atoiClamp(r.FormValue("interval_sec"), 1, 3600, 5)
	cfg.Republish = atoiClamp(r.FormValue("republish_sec"), 0, 86400, 600)
	cfg.Retain = r.FormValue("retain") != ""
	cfg.Enabled = r.FormValue("enabled") != ""

	if err := cfg.Save(ws.cfgPath); err != nil {
		redirect(w, r, "", "не удалось сохранить: "+err.Error())
		return
	}
	ws.reload()
	redirect(w, r, "Настройки сохранены и применены", "")
}

func (ws *webServer) toggle(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig(ws.cfgPath)
	cfg.Enabled = !cfg.Enabled
	if err := cfg.Save(ws.cfgPath); err != nil {
		redirect(w, r, "", err.Error())
		return
	}
	ws.reload()
	state := "выключен"
	if cfg.Enabled {
		state = "включён"
	}
	redirect(w, r, "Агент "+state, "")
}

func (ws *webServer) doUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "./", http.StatusSeeOther)
		return
	}
	if err := ws.update(); err != nil {
		redirect(w, r, "", "обновление не удалось: "+err.Error())
		return
	}

	// Бинарник заменён. Перезапуск откладываем: если сделать его прямо здесь,
	// текущий запрос оборвётся без ответа, а nginx «Малины» покажет на такой
	// обрыв свою страницу 404 (у них error_page 502 -> /404.html) — человек
	// решит, что обновление провалилось, хотя оно прошло.
	redirect(w, r, "Обновлено. Агент перезапускается — обновите страницу через несколько секунд", "")
	go func() {
		time.Sleep(1500 * time.Millisecond)
		ws.restart()
	}()
}

func redirect(w http.ResponseWriter, r *http.Request, ok, errMsg string) {
	// Относительный редирект: работает и напрямую (:8091 -> ./ = /), и за
	// прокси (/mqtt/save -> ./ = /mqtt/). Абсолютный "/" за прокси увёл бы на
	// корень малины.
	u := "./"
	switch {
	case errMsg != "":
		u += "?err=" + urlEscape(errMsg)
	case ok != "":
		u += "?ok=" + urlEscape(ok)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func urlEscape(s string) string {
	return strings.NewReplacer(" ", "%20", "\n", " ", "\"", "%22", "&", "%26",
		"#", "%23", "?", "%3F", "+", "%2B").Replace(s)
}

func atoiClamp(s string, lo, hi, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

type pageData struct {
	Config     Config
	Connected  bool
	Base       string
	Version    string
	MalinaBase string // http://<хост> без порта — на веб-морду малины (:80)
	Notice     string
	Error      string
}

// malinaBase собирает адрес веб-морды малины из хоста запроса: тот же хост, но
// без порта агента (веб малины на :80). Оттуда же берём их CSS и туда ведёт
// ссылка «Домой».
func malinaBase(r *http.Request) string {
	host := r.Host
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return ""
	}
	return "http://" + host
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"checked": func(b bool) template.HTMLAttr {
		if b {
			return template.HTMLAttr("checked")
		}
		return ""
	},
	"sel": func(a, b int) template.HTMLAttr {
		if a == b {
			return template.HTMLAttr("selected")
		}
		return ""
	},
}).Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="ru"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MQTT — Малина</title>
{{if .MalinaBase}}
<link href="{{.MalinaBase}}/css/bootstrap.min.css" rel="stylesheet">
<link href="{{.MalinaBase}}/css/glyphicons.css" rel="stylesheet">
<link href="{{.MalinaBase}}/css/glyphicons-bootstrap.css" rel="stylesheet">
<link href="{{.MalinaBase}}/css/mystyles.css" rel="stylesheet">
{{end}}
<style>
/* Мелкие правки поверх их темы: отступ под фиксированный навбар и ширина. */
body{padding-top:64px}
.mq-wrap{max-width:900px;margin:0 auto;padding:0 15px 40px}
.mq-mono{font-family:ui-monospace,Menlo,monospace;font-size:13px}
.mq-row{display:flex;gap:16px;flex-wrap:wrap}
.mq-row>div{flex:1;min-width:200px}
.mq-status{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
</style></head>
<body>
<nav class="navbar navbar-default navbar-fixed-top">
  <div class="container-fluid">
    <ul class="nav nav-pills">
      <li><a class="navbar-brand" href="{{.MalinaBase}}/index.php">МикроАрт</a></li>
      <li><a href="{{.MalinaBase}}/index.php"><span class="glyphicon glyphicon-home"></span>&nbsp;Домой</a></li>
      <li class="active"><a href="./"><span class="glyphicon glyphicon-cloud-upload"></span>&nbsp;MQTT</a></li>
    </ul>
  </div>
</nav>

<div class="mq-wrap">

{{if .Notice}}<div class="alert alert-success" role="alert">{{.Notice}}</div>{{end}}
{{if .Error}}<div class="alert alert-danger" role="alert">{{.Error}}</div>{{end}}

<div class="panel panel-primary">
  <div class="panel-heading">Состояние</div>
  <div class="panel-body mq-status">
    {{if .Config.Enabled}}
      {{if .Connected}}<span class="label label-success">подключён к брокеру</span>
      {{else}}<span class="label label-warning">нет связи с брокером</span>{{end}}
    {{else}}<span class="label label-default">выключен</span>{{end}}
    <span class="text-muted mq-mono">v{{.Version}}</span>
    <span style="flex:1"></span>
    <form method="post" action="toggle" style="margin:0">
      <button class="btn btn-default btn-sm">{{if .Config.Enabled}}Выключить{{else}}Включить{{end}}</button>
    </form>
  </div>
</div>

<form method="post" action="save">
<div class="panel panel-default">
  <div class="panel-heading">Брокер</div>
  <div class="panel-body">
    <div class="mq-row">
      <div class="form-group"><label>Адрес брокера (host:port)</label>
        <input class="form-control" type="text" name="broker" value="{{.Config.Broker}}" placeholder="192.168.20.10:1883"></div>
      <div class="form-group"><label>Корень топиков</label>
        <input class="form-control" type="text" name="base_topic" value="{{.Config.BaseTopic}}" placeholder="microart/inv1"></div>
    </div>
    <div class="mq-row">
      <div class="form-group"><label>Пользователь</label>
        <input class="form-control" type="text" name="username" value="{{.Config.Username}}" autocomplete="off"></div>
      <div class="form-group"><label>Пароль</label>
        <input class="form-control" type="password" name="password" value="{{.Config.Password}}" autocomplete="new-password"></div>
    </div>
    <div class="mq-row">
      <div class="form-group"><label>QoS</label>
        <select class="form-control" name="qos">
          <option value="0" {{sel .Config.QoS 0}}>0</option>
          <option value="1" {{sel .Config.QoS 1}}>1</option>
          <option value="2" {{sel .Config.QoS 2}}>2</option>
        </select></div>
      <div class="form-group"><label>Интервал опроса, сек</label>
        <input class="form-control" type="number" name="interval_sec" min="1" max="3600" value="{{.Config.Interval}}"></div>
      <div class="form-group"><label>Переотправка всего, сек</label>
        <input class="form-control" type="number" name="republish_sec" min="0" max="86400" value="{{.Config.Republish}}"></div>
    </div>
    <div class="checkbox"><label><input type="checkbox" name="retain" {{checked .Config.Retain}}> Ретейн (подписчик сразу получает последнее значение)</label></div>
    <div class="checkbox"><label><input type="checkbox" name="enabled" {{checked .Config.Enabled}}> Агент включён</label></div>
  </div>
  <div class="panel-footer">
    <button class="btn btn-primary" type="submit">Сохранить и применить</button>
    <span class="text-muted">&nbsp;Применяется сразу.</span>
  </div>
</div>
</form>

<div class="panel panel-default">
  <div class="panel-heading">Что публикуется</div>
  <div class="panel-body">
    <p class="text-muted">Все значения, что отдаёт малина — топики создаются сами, описывать ничего не нужно:</p>
    <p class="mq-mono">
      {{.Config.BaseTopic}}/map/&lt;поле&gt;<br>
      {{.Config.BaseTopic}}/bat/0/&lt;поле&gt;<br>
      {{.Config.BaseTopic}}/mppt/&lt;n&gt;/&lt;поле&gt;<br>
      {{.Config.BaseTopic}}/availability &nbsp;online|offline
    </p>
    <p class="text-muted">Полное описание: <a href="https://github.com/staskuznec/microart2mqtt/blob/main/TOPICS.md" target="_blank">docs/topics.md</a></p>
  </div>
</div>

<div class="panel panel-default">
  <div class="panel-heading">Обновление агента</div>
  <div class="panel-body">
    <form method="post" action="update" style="margin:0">
      <button class="btn btn-default" type="submit">Обновить до последней версии</button>
      <span class="text-muted">&nbsp;Скачает с GitHub, проверит и перезапустится. Веб-страница входит в бинарник — обновится вместе с ним.</span>
    </form>
  </div>
</div>

</div>
</body></html>`
