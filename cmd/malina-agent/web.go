package main

import (
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// webServer поднимает веб-страницу агента. Вся вёрстка — здесь, в самом
// демоне: обновили бинарник — обновился и интерфейс, без отдельных PHP-файлов.
type webServer struct {
	cfgPath string
	status  func() (connected bool, base string)
	reload  func()       // применить настройки заново (перезапуск опроса)
	update  func() error // самообновление; при успехе процесс перезапускается
}

func (a *agent) startWeb(cfgPath string, cfg Config, reload func()) {
	ws := &webServer{
		cfgPath: cfgPath,
		status:  a.webStatus,
		reload:  reload,
		update:  a.selfUpdate,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.index)
	mux.HandleFunc("/save", ws.save)
	mux.HandleFunc("/toggle", ws.toggle)
	mux.HandleFunc("/update", ws.doUpdate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	addr := cfg.WebAddr()
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		slog.Info("веб-страница агента", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("веб-страница не поднялась", "err", err)
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
		Config:    cfg,
		Connected: connected,
		Base:      base,
		Version:   version,
		Notice:    r.URL.Query().Get("ok"),
		Error:     r.URL.Query().Get("err"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		slog.Error("отрисовка страницы", "err", err)
	}
}

func (ws *webServer) save(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
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
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// selfUpdate при успехе перезапускает процесс и не возвращается.
	// Показываем страницу заранее, чтобы браузер не завис в ожидании.
	if err := ws.update(); err != nil {
		redirect(w, r, "", "обновление не удалось: "+err.Error())
		return
	}
	redirect(w, r, "Обновление применяется, агент перезапускается…", "")
}

func redirect(w http.ResponseWriter, r *http.Request, ok, errMsg string) {
	u := "/"
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
	Config    Config
	Connected bool
	Base      string
	Version   string
	Notice    string
	Error     string
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
<style>
:root{--bg:#f4f6f8;--card:#fff;--line:#dfe3e8;--tx:#1c2430;--mut:#6b7684;--acc:#2f6fed;--ok:#1a9e5c;--err:#d13b3b}
@media(prefers-color-scheme:dark){:root{--bg:#161a1f;--card:#1e242b;--line:#2c343d;--tx:#e6eaef;--mut:#96a1ae;--acc:#5b91ff;--ok:#3fbd7d;--err:#f06a6a}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--tx);font:15px/1.5 -apple-system,Segoe UI,Roboto,Arial,sans-serif}
.wrap{max-width:820px;margin:0 auto;padding:20px}
h1{font-size:20px;margin:0 0 4px}.sub{color:var(--mut);margin:0 0 18px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:18px;margin-bottom:16px}
h3{margin:0 0 6px;font-size:16px}
label{display:block;margin:12px 0 4px;font-size:13px;color:var(--mut)}
input[type=text],input[type=password],input[type=number],select{width:100%;padding:8px 10px;border:1px solid var(--line);border-radius:7px;background:var(--bg);color:var(--tx);font:inherit;font-size:14px}
.row{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:0 16px}
.chk{display:flex;align-items:center;gap:8px;margin-top:12px}.chk input{width:auto}.chk label{margin:0}
.btn{display:inline-block;border:1px solid var(--line);background:var(--card);color:var(--tx);padding:8px 16px;border-radius:7px;cursor:pointer;font:inherit}
.btn.primary{background:var(--acc);border-color:var(--acc);color:#fff}.btn:hover{border-color:var(--acc)}
.pill{display:inline-block;padding:2px 10px;border-radius:20px;font-size:12px}
.pill.on{background:rgba(26,158,92,.15);color:var(--ok)}.pill.off{background:rgba(107,118,132,.18);color:var(--mut)}
.msg{padding:10px 14px;border-radius:8px;margin-bottom:14px}
.msg.ok{background:rgba(26,158,92,.13);color:var(--ok)}.msg.err{background:rgba(209,59,59,.13);color:var(--err)}
.mut{color:var(--mut);font-size:13px}.mono{font-family:ui-monospace,Menlo,monospace}
.bar{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
</style></head><body><div class="wrap">
<h1>MQTT</h1><p class="sub">Агент публикует показания инвертора в MQTT-брокер.</p>

{{if .Notice}}<div class="msg ok">{{.Notice}}</div>{{end}}
{{if .Error}}<div class="msg err">{{.Error}}</div>{{end}}

<div class="card"><div class="bar">
  <strong>Состояние:</strong>
  {{if .Config.Enabled}}
    {{if .Connected}}<span class="pill on">подключён к брокеру</span>
    {{else}}<span class="pill off">нет связи с брокером</span>{{end}}
  {{else}}<span class="pill off">выключен</span>{{end}}
  <span class="mut mono">v{{.Version}}</span>
  <span style="flex:1"></span>
  <form method="post" action="toggle" style="display:inline">
    <button class="btn">{{if .Config.Enabled}}Выключить{{else}}Включить{{end}}</button>
  </form>
</div></div>

<form method="post" action="save">
<div class="card">
  <h3>Брокер</h3>
  <div class="row">
    <div><label>Адрес брокера (host:port)</label><input type="text" name="broker" value="{{.Config.Broker}}" placeholder="192.168.20.10:1883"></div>
    <div><label>Корень топиков</label><input type="text" name="base_topic" value="{{.Config.BaseTopic}}" placeholder="microart/inv1"></div>
  </div>
  <div class="row">
    <div><label>Пользователь</label><input type="text" name="username" value="{{.Config.Username}}" autocomplete="off"></div>
    <div><label>Пароль</label><input type="password" name="password" value="{{.Config.Password}}" autocomplete="new-password"></div>
  </div>
  <div class="row">
    <div><label>QoS</label><select name="qos">
      <option value="0" {{sel .Config.QoS 0}}>0</option>
      <option value="1" {{sel .Config.QoS 1}}>1</option>
      <option value="2" {{sel .Config.QoS 2}}>2</option>
    </select></div>
    <div><label>Интервал опроса, сек</label><input type="number" name="interval_sec" min="1" max="3600" value="{{.Config.Interval}}"></div>
    <div><label>Переотправка всего, сек</label><input type="number" name="republish_sec" min="0" max="86400" value="{{.Config.Republish}}"></div>
  </div>
  <div class="chk"><input type="checkbox" id="retain" name="retain" {{checked .Config.Retain}}><label for="retain">Ретейн</label></div>
  <div class="chk"><input type="checkbox" id="enabled" name="enabled" {{checked .Config.Enabled}}><label for="enabled">Агент включён</label></div>
</div>
<div class="bar"><button class="btn primary" type="submit">Сохранить и применить</button><span class="mut">Применяется сразу.</span></div>
</form>

<div class="card" style="margin-top:16px">
  <h3>Что публикуется</h3>
  <p class="mut">Все значения, что отдаёт малина — топики создаются сами, описывать ничего не нужно:</p>
  <p class="mono" style="font-size:13px">
    {{.Config.BaseTopic}}/map/&lt;поле&gt;<br>
    {{.Config.BaseTopic}}/bat/0/&lt;поле&gt;<br>
    {{.Config.BaseTopic}}/mppt/&lt;n&gt;/&lt;поле&gt;<br>
    {{.Config.BaseTopic}}/availability &nbsp;online|offline
  </p>
</div>

<div class="card">
  <h3>Обновление агента</h3>
  <form method="post" action="update">
    <div class="bar"><button class="btn" type="submit">Обновить до последней версии</button>
    <span class="mut">Скачает с GitHub, проверит и перезапустится. Веб-страница входит в бинарник — обновится вместе с ним.</span></div>
  </form>
</div>
</div></body></html>`
