package web

import (
	"html/template"
	"strings"
	"time"
)

// funcs — помощники для шаблонов.
var funcs = template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "никогда"
		}
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "только что"
		case d < time.Hour:
			return itoa(int(d.Minutes())) + " мин назад"
		case d < 24*time.Hour:
			return itoa(int(d.Hours())) + " ч назад"
		default:
			return t.Format("02.01.2006 15:04")
		}
	},
	"hhmmss": func(t time.Time) string { return t.Format("15:04:05") },
	"dur":    func(d time.Duration) string { return d.Truncate(time.Second).String() },
	"join":   strings.Join,
	"default": func(def, v string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return v
	},
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// allPages — содержимое страниц по именам. Обвязка у всех общая.
var allPages = map[string]string{
	"overview":      pageOverviewTmpl,
	"inverters":     pageInvertersTmpl,
	"inverter-form": pageInverterFormTmpl,
	"metrics":       pageMetricsTmpl,
	"metric-form":   pageMetricFormTmpl,
	"discover":      pageDiscoverTmpl,
	"settings":      pageSettingsTmpl,
	"log":           pageLogTmpl,
}

// layout — общая обвязка страниц. Стили inline: демон должен работать без
// внешних файлов и без интернета, страница открывается внутри панели.
const layout = `{{define "layout"}}<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} — microart2mqtt</title>
<style>
:root{
  --bg:#f6f7f9; --panel:#fff; --line:#e3e6ea; --text:#1c2430; --muted:#6b7684;
  --accent:#2f6fed; --ok:#1a9e5c; --warn:#c47b0a; --err:#d13b3b; --code:#f1f3f6;
}
@media (prefers-color-scheme:dark){
  :root{--bg:#161a1f;--panel:#1e242b;--line:#2c343d;--text:#e6eaef;--muted:#96a1ae;
        --accent:#5b91ff;--ok:#3fbd7d;--warn:#e0a33c;--err:#f06a6a;--code:#232a32;}
}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);
     font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
header{background:var(--panel);border-bottom:1px solid var(--line);padding:0 20px;
       display:flex;align-items:center;gap:22px;flex-wrap:wrap;position:sticky;top:0;z-index:5}
header .brand{font-weight:600;padding:14px 0;margin-right:6px}
header nav{display:flex;gap:18px;flex-wrap:wrap}
header nav a{padding:14px 0;color:var(--muted);border-bottom:2px solid transparent}
header nav a.on{color:var(--text);border-bottom-color:var(--accent)}
header .right{margin-left:auto;color:var(--muted);font-size:13px;display:flex;gap:14px;align-items:center}
main{max-width:1100px;margin:0 auto;padding:22px 20px 60px}
h1{font-size:21px;margin:0 0 18px}
h2{font-size:16px;margin:26px 0 12px}
.card{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:18px;margin-bottom:16px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:14px}
table{width:100%;border-collapse:collapse}
th,td{text-align:left;padding:9px 10px;border-bottom:1px solid var(--line);vertical-align:middle}
th{color:var(--muted);font-weight:500;font-size:13px}
tr:last-child td{border-bottom:0}
.wrap{overflow-x:auto}
code,.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px}
code{background:var(--code);padding:2px 6px;border-radius:5px}
.pill{display:inline-block;padding:2px 9px;border-radius:20px;font-size:12px;font-weight:500}
.pill.ok{background:rgba(26,158,92,.14);color:var(--ok)}
.pill.err{background:rgba(209,59,59,.14);color:var(--err)}
.pill.off{background:rgba(107,118,132,.16);color:var(--muted)}
.muted{color:var(--muted)}
.btn{display:inline-block;border:1px solid var(--line);background:var(--panel);color:var(--text);
     padding:7px 14px;border-radius:7px;cursor:pointer;font-size:14px;font-family:inherit}
.btn:hover{border-color:var(--accent);text-decoration:none}
.btn.primary{background:var(--accent);border-color:var(--accent);color:#fff}
.btn.danger:hover{border-color:var(--err);color:var(--err)}
.btn.sm{padding:4px 10px;font-size:13px}
form.inline{display:inline}
label{display:block;margin:14px 0 5px;font-size:13px;color:var(--muted)}
input[type=text],input[type=password],input[type=number],select{
  width:100%;padding:8px 10px;border:1px solid var(--line);border-radius:7px;
  background:var(--bg);color:var(--text);font:inherit;font-size:14px}
input:focus,select:focus{outline:2px solid var(--accent);outline-offset:-1px}
.row{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:0 16px}
.check{display:flex;align-items:center;gap:8px;margin-top:14px}
.check input{width:auto}
.check label{margin:0}
.actions{margin-top:20px;display:flex;gap:10px;align-items:center}
.msg{padding:11px 14px;border-radius:8px;margin-bottom:16px}
.msg.err{background:rgba(209,59,59,.12);color:var(--err)}
.msg.ok{background:rgba(26,158,92,.12);color:var(--ok)}
.hint{font-size:13px;color:var(--muted);margin-top:5px}
.kv{display:flex;justify-content:space-between;gap:12px;padding:5px 0}
.kv .k{color:var(--muted)}
.lvl-error{color:var(--err)}.lvl-warn{color:var(--warn)}.lvl-debug{color:var(--muted)}
.empty{text-align:center;padding:40px 20px;color:var(--muted)}
</style>
</head>
<body>
<header>
  <span class="brand">microart2mqtt</span>
  <nav>
    <a href="{{.Base}}/" {{if eq .Nav "overview"}}class="on"{{end}}>Обзор</a>
    <a href="{{.Base}}/inverters" {{if eq .Nav "inverters"}}class="on"{{end}}>Инверторы</a>
    <a href="{{.Base}}/settings" {{if eq .Nav "settings"}}class="on"{{end}}>Настройки</a>
    <a href="{{.Base}}/log" {{if eq .Nav "log"}}class="on"{{end}}>Журнал</a>
  </nav>
  <span class="right">
    {{if .Update.HasUpdate}}<a href="{{.Base}}/">вышла версия {{.Update.Latest}}</a>{{end}}
    <span>v{{.Version}}</span>
  </span>
</header>
<main>
{{if .Error}}<div class="msg err">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="msg ok">{{.Notice}}</div>{{end}}
{{template "content" .}}
</main>
</body>
</html>{{end}}`

// pageOverview — состояние моста и инверторов.
const pageOverviewTmpl = `{{define "content"}}
<h1>Обзор</h1>

<div class="grid">
  <div class="card">
    <h2 style="margin-top:0">Брокер</h2>
    {{with .MQTT}}
      {{if .Connected}}<span class="pill ok">подключён</span>
      {{else if .Configured}}<span class="pill err">нет связи</span>
      {{else}}<span class="pill off">не настроен</span>{{end}}
      <div style="margin-top:12px">
        <div class="kv"><span class="k">Адрес</span><span class="mono">{{default "—" .Broker}}</span></div>
        <div class="kv"><span class="k">Клиент</span><span class="mono">{{default "—" .ClientID}}</span></div>
        {{if not .Since.IsZero}}<div class="kv"><span class="k">Соединение с</span><span>{{ago .Since}}</span></div>{{end}}
        {{if .LastError}}<div class="kv"><span class="k">Ошибка</span><span class="lvl-error">{{.LastError}}</span></div>{{end}}
      </div>
    {{end}}
    {{if not .MQTT.Configured}}<p class="hint">Задайте адрес брокера в разделе «Настройки».</p>{{end}}
  </div>

  <div class="card">
    <h2 style="margin-top:0">Демон</h2>
    <div class="kv"><span class="k">Версия</span><span>{{.Version}}</span></div>
    <div class="kv"><span class="k">Работает</span><span>{{dur .Uptime}}</span></div>
    <div class="kv"><span class="k">Корень топиков</span><span class="mono">{{.BaseTopic}}/</span></div>
    <div class="kv"><span class="k">Инверторов</span><span>{{.Online}} из {{.Total}} на связи</span></div>
    <div style="margin-top:14px">
      {{if .Update.HasUpdate}}
        <div class="msg ok" style="margin:0 0 10px">Вышла версия {{.Update.Latest}}</div>
        <code>{{.InstallCommand}}</code>
      {{else if .Update.Error}}
        <span class="muted">Проверка обновлений: {{.Update.Error}}</span>
      {{else if .Update.Latest}}
        <span class="muted">Установлена последняя версия</span>
      {{end}}
      <form method="post" action="{{.Base}}/update/check" style="margin-top:12px">
        <button class="btn sm">Проверить обновления</button>
      </form>
    </div>
  </div>
</div>

<h2>Инверторы</h2>
{{if not .Inverters}}
  <div class="card empty">
    Ни одного инвертора ещё нет.<br>
    <a class="btn primary" style="margin-top:14px" href="{{.Base}}/inverters/new">Добавить инвертор</a>
  </div>
{{else}}
  <div class="grid">
  {{range .Inverters}}
    <div class="card">
      <div style="display:flex;align-items:center;gap:10px">
        <strong><a href="{{$.Base}}/inverters/{{.ID}}/metrics">{{.Name}}</a></strong>
        {{if not .Enabled}}<span class="pill off">выключен</span>
        {{else if .Available}}<span class="pill ok">online</span>
        {{else}}<span class="pill err">offline</span>{{end}}
      </div>
      <div class="hint mono">{{.URL}}</div>
      {{if .LastError}}<div class="hint lvl-error">{{.LastError}}</div>{{end}}
      {{if .Values}}
        <div style="margin-top:12px">
        {{range .Values}}
          <div class="kv"><span class="k">{{.Name}}</span><span class="mono">{{.Value}}</span></div>
        {{end}}
        </div>
      {{else if .Enabled}}
        <p class="hint">Данных пока нет{{if .LastOK.IsZero}}, первый опрос ещё не прошёл{{end}}.</p>
      {{end}}
      {{if not .LastOK.IsZero}}<div class="hint">Последний успешный опрос: {{ago .LastOK}}</div>{{end}}
    </div>
  {{end}}
  </div>
{{end}}
{{end}}`

// pageInverters — список инверторов.
const pageInvertersTmpl = `{{define "content"}}
<h1>Инверторы</h1>

{{if not .Inverters}}
  <div class="card empty">
    Ни одного инвертора ещё нет.<br>
    <a class="btn primary" style="margin-top:14px" href="{{.Base}}/inverters/new">Добавить инвертор</a>
  </div>
{{else}}
<div class="card wrap">
<table>
<tr><th>Имя</th><th>Адрес</th><th>Опрос</th><th>Метрик</th><th>Состояние</th><th></th></tr>
{{range .Inverters}}
<tr>
  <td><strong>{{.Name}}</strong></td>
  <td class="mono">{{.URL}}</td>
  <td>{{if .PollIntervalSec}}{{.PollIntervalSec}} с{{else}}<span class="muted">общий</span>{{end}}</td>
  <td><a href="{{$.Base}}/inverters/{{.ID}}/metrics">{{len .Metrics}}</a></td>
  <td>
    {{if not .Enabled}}<span class="pill off">выключен</span>
    {{else if .Available}}<span class="pill ok">online</span>
    {{else}}<span class="pill err">offline</span>{{end}}
  </td>
  <td style="text-align:right;white-space:nowrap">
    <a class="btn sm" href="{{$.Base}}/inverters/{{.ID}}/metrics">Метрики</a>
    <a class="btn sm" href="{{$.Base}}/inverters/{{.ID}}">Правка</a>
    <form class="inline" method="post" action="{{$.Base}}/inverters/{{.ID}}/toggle">
      <button class="btn sm">{{if .Enabled}}Выключить{{else}}Включить{{end}}</button>
    </form>
    <form class="inline" method="post" action="{{$.Base}}/inverters/{{.ID}}/delete"
          onsubmit="return confirm('Удалить инвертор {{.Name}} вместе с его метриками?')">
      <button class="btn sm danger">Удалить</button>
    </form>
  </td>
</tr>
{{end}}
</table>
</div>
<a class="btn primary" href="{{.Base}}/inverters/new">Добавить инвертор</a>
{{end}}
{{end}}`

// pageInverterForm — добавление и правка инвертора.
const pageInverterFormTmpl = `{{define "content"}}
<h1>{{if .Inverter.ID}}Инвертор {{.Inverter.Name}}{{else}}Новый инвертор{{end}}</h1>

<form method="post" action="{{.Base}}/inverters/save">
<input type="hidden" name="id" value="{{.Inverter.ID}}">
<div class="card">
  <div class="row">
    <div>
      <label for="name">Имя</label>
      <input id="name" name="name" type="text" value="{{.Inverter.Name}}" required
             placeholder="inv1" pattern="[^/+# ]+">
      <div class="hint">Попадает в топик: <code class="mono">{{.BaseTopic}}/имя/...</code></div>
    </div>
    <div>
      <label for="url">Адрес «Малины»</label>
      <input id="url" name="url" type="text" value="{{.Inverter.URL}}" required
             placeholder="http://192.168.20.250">
      <div class="hint">Тот же адрес, что открывается в браузере</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label for="poll">Интервал опроса, секунд</label>
      <input id="poll" name="poll_interval_sec" type="number" min="0" max="86400"
             value="{{.Inverter.PollIntervalSec}}">
      <div class="hint">0 — брать общий ({{.DefaultPoll}})</div>
    </div>
    <div>
      <label for="timeout">Таймаут запроса, секунд</label>
      <input id="timeout" name="http_timeout_sec" type="number" min="0" max="120"
             value="{{.Inverter.HTTPTimeoutSec}}">
      <div class="hint">0 — брать общий ({{.DefaultTimeout}})</div>
    </div>
  </div>
  <div class="check">
    <input id="enabled" name="enabled" type="checkbox" {{if .Inverter.Enabled}}checked{{end}}>
    <label for="enabled">Опрашивать</label>
  </div>
  {{if not .Inverter.ID}}
  <div class="check">
    <input id="preset" name="preset" type="checkbox" checked>
    <label for="preset">Сразу добавить стандартный набор метрик</label>
  </div>
  <div class="hint">Заряд и ток батареи, сеть, нагрузка, солнце, режим. Потом можно поправить.</div>
  {{end}}
  <div class="actions">
    <button class="btn primary">Сохранить</button>
    <a class="btn" href="{{.Base}}/inverters">Отмена</a>
  </div>
</div>
</form>
{{end}}`

// pageMetrics — метрики инвертора и добавление новых.
const pageMetricsTmpl = `{{define "content"}}
<h1>{{.Inverter.Name}}: метрики</h1>
<p class="muted mono">{{.Inverter.URL}}</p>

{{if .Metrics}}
<div class="card wrap">
<table>
<tr><th>Имя</th><th>Топик</th><th>Источник</th><th>Поле API</th><th>Вид</th><th>Сейчас</th><th></th></tr>
{{range .Metrics}}
<tr{{if not .Enabled}} class="muted"{{end}}>
  <td><strong>{{.Name}}</strong></td>
  <td class="mono">{{$.BaseTopic}}/{{$.Inverter.Name}}/{{.FlatTopic}}</td>
  <td>{{.Device}}</td>
  <td class="mono">{{.Target}}</td>
  <td>{{if .IsNumber}}число{{if .Precision}}, {{.Precision}} зн.{{end}}{{else}}строка{{end}}</td>
  <td class="mono">{{index $.Values .Name}}</td>
  <td style="text-align:right;white-space:nowrap">
    <a class="btn sm" href="{{$.Base}}/inverters/{{$.Inverter.ID}}/metrics/{{.ID}}">Правка</a>
    <form class="inline" method="post" action="{{$.Base}}/metrics/{{.ID}}/delete"
          onsubmit="return confirm('Удалить метрику {{.Name}}?')">
      <button class="btn sm danger">Удалить</button>
    </form>
  </td>
</tr>
{{end}}
</table>
</div>
{{else}}
<div class="card empty">Метрик пока нет — ничего не публикуется.</div>
{{end}}

<div class="actions">
  <a class="btn primary" href="{{.Base}}/inverters/{{.Inverter.ID}}/discover">Добавить из API</a>
  <a class="btn" href="{{.Base}}/inverters/{{.Inverter.ID}}/metrics/new">Добавить вручную</a>
  <form class="inline" method="post" action="{{.Base}}/inverters/{{.Inverter.ID}}/metrics/preset">
    <button class="btn">Добавить стандартный набор</button>
  </form>
  {{if .Others}}
  <form class="inline" method="post" action="{{.Base}}/inverters/{{.Inverter.ID}}/metrics/copy">
    <select name="from" style="width:auto;display:inline-block">
      {{range .Others}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
    </select>
    <button class="btn">Скопировать метрики</button>
  </form>
  {{end}}
  <a class="btn" href="{{.Base}}/inverters">К списку</a>
</div>
{{end}}`

// pageMetricForm — правка одной метрики.
const pageMetricFormTmpl = `{{define "content"}}
<h1>{{if .Metric.ID}}Метрика {{.Metric.Name}}{{else}}Новая метрика{{end}}</h1>
<p class="muted">{{.Inverter.Name}} — {{.Inverter.URL}}</p>

<form method="post" action="{{.Base}}/inverters/{{.Inverter.ID}}/metrics/save">
<input type="hidden" name="id" value="{{.Metric.ID}}">
<div class="card">
  <div class="row">
    <div>
      <label for="name">Имя</label>
      <input id="name" name="name" type="text" value="{{.Metric.Name}}" required pattern="[^/+# ]+">
      <div class="hint">Ключ в JSON-стейте</div>
    </div>
    <div>
      <label for="topic">Топик</label>
      <input id="topic" name="topic" type="text" value="{{.Metric.Topic}}" placeholder="battery/soc">
      <div class="hint">Пусто — совпадает с именем</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label for="device">Источник</label>
      <select id="device" name="device">
        <option value="map" {{if eq .Metric.Device "map"}}selected{{end}}>map — состояние МАП</option>
        <option value="bat" {{if eq .Metric.Device "bat"}}selected{{end}}>bat — батарейный монитор</option>
        <option value="mppt" {{if eq .Metric.Device "mppt"}}selected{{end}}>mppt — контроллеры КЭС</option>
      </select>
    </div>
    <div>
      <label for="target">Поле API</label>
      <input id="target" name="target" type="text" value="{{.Metric.Target}}" required
             placeholder="minute_data.C_100_remain">
      <div class="hint">Вложенность через точку, индекс массива числом</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label for="kind">Вид значения</label>
      <select id="kind" name="kind">
        <option value="number" {{if .Metric.IsNumber}}selected{{end}}>Число</option>
        <option value="string" {{if not .Metric.IsNumber}}selected{{end}}>Строка</option>
      </select>
    </div>
    <div>
      <label for="precision">Округление, знаков</label>
      <input id="precision" name="precision" type="number" min="0" max="6"
             value="{{if .Metric.Precision}}{{.Metric.Precision}}{{end}}" placeholder="без округления">
    </div>
  </div>
  <div class="check">
    <input id="menabled" name="enabled" type="checkbox" {{if .Metric.Enabled}}checked{{end}}>
    <label for="menabled">Публиковать</label>
  </div>
  <div class="actions">
    <button class="btn primary">Сохранить</button>
    <a class="btn" href="{{.Base}}/inverters/{{.Inverter.ID}}/metrics">Отмена</a>
  </div>
</div>
</form>
{{end}}`

// pageDiscover — живой список полей API.
const pageDiscoverTmpl = `{{define "content"}}
<h1>{{.Inverter.Name}}: поля API</h1>
<p class="muted">Значения прямо сейчас с {{.Inverter.URL}}. Отметьте нужные — они станут метриками.</p>

{{if .Failed}}
  <div class="msg err">{{.Failed}}</div>
{{end}}

{{if .Groups}}
<form method="post" action="{{.Base}}/inverters/{{.Inverter.ID}}/metrics/add">
{{range .Groups}}
  <h2>{{.Title}} <span class="muted">({{len .Fields}} полей)</span></h2>
  <div class="card wrap">
  <table>
  <tr><th style="width:34px"></th><th>Поле</th><th>Значение</th><th>Имя метрики</th><th>Топик</th></tr>
  {{$device := .Device}}
  {{range .Fields}}
  <tr>
    <td><input type="checkbox" name="pick" value="{{$device}}|{{.Path}}" {{if .Used}}disabled{{end}}></td>
    <td class="mono">{{.Path}}</td>
    <td class="mono">{{.Value}}</td>
    <td>{{if .Used}}<span class="muted">уже добавлено: {{.UsedAs}}</span>
        {{else}}<input type="text" name="name|{{$device}}|{{.Path}}" value="{{.Suggest}}" pattern="[^/+# ]+">{{end}}</td>
    <td>{{if not .Used}}<input type="text" name="topic|{{$device}}|{{.Path}}" placeholder="как имя">{{end}}</td>
  </tr>
  {{end}}
  </table>
  </div>
{{end}}
<div class="actions">
  <button class="btn primary">Добавить отмеченные</button>
  <a class="btn" href="{{.Base}}/inverters/{{.Inverter.ID}}/metrics">Назад к метрикам</a>
</div>
</form>
{{else if not .Failed}}
<div class="card empty">«Малина» не вернула ни одного поля.</div>
{{end}}
{{end}}`

// pageSettings — брокер и общие параметры опроса.
const pageSettingsTmpl = `{{define "content"}}
<h1>Настройки</h1>

<form method="post" action="{{.Base}}/settings">
<div class="card">
  <h2 style="margin-top:0">Брокер MQTT</h2>
  <div class="row">
    <div>
      <label for="addr">Адрес</label>
      <input id="addr" name="mqtt_addr" type="text" value="{{.Config.MQTTAddr}}" required
             placeholder="192.168.20.218:1883">
      <div class="hint">host:port. Порт можно не указывать</div>
    </div>
    <div>
      <label for="client_id">Идентификатор клиента</label>
      <input id="client_id" name="mqtt_client_id" type="text" value="{{.Config.MQTTClientID}}"
             placeholder="microart2mqtt-{{.Hostname}}">
      <div class="hint">Пусто — соберётся из имени хоста</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label for="user">Пользователь</label>
      <input id="user" name="mqtt_user" type="text" value="{{.Config.MQTTUser}}" autocomplete="off">
    </div>
    <div>
      <label for="password">Пароль</label>
      <input id="password" name="mqtt_password" type="password" value="{{.Config.MQTTPassword}}" autocomplete="new-password">
    </div>
  </div>
  <div class="row">
    <div>
      <label for="base_topic">Корень топиков</label>
      <input id="base_topic" name="mqtt_base_topic" type="text" value="{{.Config.MQTTBaseTopic}}">
      <div class="hint">Топики будут вида <code class="mono">{{.Config.MQTTBaseTopic}}/inv1/battery/soc</code></div>
    </div>
    <div>
      <label for="qos">QoS</label>
      <select id="qos" name="mqtt_qos">
        <option value="0" {{if eq .Config.MQTTQoS 0}}selected{{end}}>0 — без подтверждения</option>
        <option value="1" {{if eq .Config.MQTTQoS 1}}selected{{end}}>1 — хотя бы раз</option>
        <option value="2" {{if eq .Config.MQTTQoS 2}}selected{{end}}>2 — ровно раз</option>
      </select>
    </div>
  </div>
  <div class="check">
    <input id="retain" name="mqtt_retain" type="checkbox" {{if .Config.MQTTRetain}}checked{{end}}>
    <label for="retain">Ретейн — подписчик сразу получает последнее значение</label>
  </div>
  <div class="check">
    <input id="tls" name="mqtt_tls" type="checkbox" {{if .Config.MQTTTLS}}checked{{end}}>
    <label for="tls">Подключаться по TLS</label>
  </div>
</div>

<div class="card">
  <h2 style="margin-top:0">Опрос</h2>
  <div class="row">
    <div>
      <label for="interval">Интервал опроса, секунд</label>
      <input id="interval" name="poll_interval_sec" type="number" min="1" max="86400" required
             value="{{.PollSec}}">
      <div class="hint">Чаще 5 с смысла нет: «Малина» усредняет с этим шагом</div>
    </div>
    <div>
      <label for="http_timeout">Таймаут запроса, секунд</label>
      <input id="http_timeout" name="http_timeout_sec" type="number" min="1" max="120" required
             value="{{.TimeoutSec}}">
    </div>
  </div>
  <div class="row">
    <div>
      <label for="republish">Переотправка всего, секунд</label>
      <input id="republish" name="republish_sec" type="number" min="0" max="86400" required
             value="{{.RepublishSec}}">
      <div class="hint">Между этим шлём только изменения. 0 — слать всегда всё</div>
    </div>
    <div>
      <label for="offline">Неудач до offline</label>
      <input id="offline" name="offline_after" type="number" min="1" max="100" required
             value="{{.Config.OfflineAfter}}">
    </div>
  </div>
  <div class="actions">
    <button class="btn primary">Сохранить и применить</button>
  </div>
  <div class="hint">Опрос перезапустится сразу, перезагружать демон не нужно.</div>
</div>
</form>
{{end}}`

// pageLog — последние записи журнала.
const pageLogTmpl = `{{define "content"}}
<h1>Журнал</h1>
<div class="actions" style="margin:0 0 16px">
  {{range .Levels}}
    <a class="btn sm{{if eq . $.Level}} primary{{end}}" href="{{$.Base}}/log?level={{.}}">{{.}}</a>
  {{end}}
</div>

{{if .Entries}}
<div class="card wrap">
<table>
<tr><th style="width:80px">Время</th><th style="width:60px">Уровень</th><th>Сообщение</th></tr>
{{range .Entries}}
<tr>
  <td class="mono muted">{{hhmmss .Time}}</td>
  <td class="lvl-{{.LevelName}}">{{.LevelName}}</td>
  <td>
    {{.Message}}
    {{range .Attrs}}<span class="muted mono"> {{.Key}}={{.Value}}</span>{{end}}
  </td>
</tr>
{{end}}
</table>
</div>
{{else}}
<div class="card empty">Записей этого уровня пока нет.</div>
{{end}}
{{end}}`
