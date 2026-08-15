<?php
// Раздел «MQTT» в вебморде «Малины». Показывает настройки агента microart2mqtt,
// его состояние и кнопку обновления. Форма шлёт данные в mqtt_save.php.
//
// Агент публикует показания инвертора в MQTT-брокер. Настройки лежат в
// /settings/web-data/mqtt.json; их же читает агент (перечитывает по SIGUSR1,
// который шлёт mqtt_save.php после сохранения).

$CFG = "/settings/web-data/mqtt.json";
$CTL = "/settings/microart-mqtt/agent-ctl.sh";

$cfg = @json_decode(@file_get_contents($CFG), true);
if (!is_array($cfg)) $cfg = array();
function v($cfg, $k, $d = "") { return isset($cfg[$k]) ? $cfg[$k] : $d; }
function h($s) { return htmlspecialchars((string)$s, ENT_QUOTES, "UTF-8"); }

$status  = @shell_exec("sudo " . escapeshellarg($CTL) . " status 2>/dev/null");
$status  = trim((string)$status);
$running = (strpos($status, "running") === 0);
$version = trim(str_replace(array("running", "stopped"), "", $status));

$metrics_json = json_encode(
    isset($cfg["metrics"]) ? $cfg["metrics"] : array(),
    JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE
);

$msg = isset($_GET["ok"]) ? $_GET["ok"] : "";
$err = isset($_GET["err"]) ? $_GET["err"] : "";
?>
<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>MQTT — Малина</title>
<style>
:root{--bg:#f4f6f8;--card:#fff;--line:#dfe3e8;--tx:#1c2430;--mut:#6b7684;--acc:#2f6fed;--ok:#1a9e5c;--err:#d13b3b}
@media(prefers-color-scheme:dark){:root{--bg:#161a1f;--card:#1e242b;--line:#2c343d;--tx:#e6eaef;--mut:#96a1ae;--acc:#5b91ff;--ok:#3fbd7d;--err:#f06a6a}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--tx);font:15px/1.5 -apple-system,Segoe UI,Roboto,Arial,sans-serif}
.wrap{max-width:820px;margin:0 auto;padding:20px}
h1{font-size:20px;margin:0 0 4px}.sub{color:var(--mut);margin:0 0 18px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:18px;margin-bottom:16px}
label{display:block;margin:12px 0 4px;font-size:13px;color:var(--mut)}
input[type=text],input[type=password],input[type=number],select,textarea{width:100%;padding:8px 10px;border:1px solid var(--line);border-radius:7px;background:var(--bg);color:var(--tx);font:inherit;font-size:14px}
textarea{font-family:ui-monospace,Menlo,monospace;min-height:180px}
.row{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:0 16px}
.chk{display:flex;align-items:center;gap:8px;margin-top:12px}.chk input{width:auto}.chk label{margin:0}
.btn{display:inline-block;border:1px solid var(--line);background:var(--card);color:var(--tx);padding:8px 16px;border-radius:7px;cursor:pointer;font:inherit}
.btn.primary{background:var(--acc);border-color:var(--acc);color:#fff}
.btn:hover{border-color:var(--acc)}
.pill{display:inline-block;padding:2px 10px;border-radius:20px;font-size:12px}
.pill.on{background:rgba(26,158,92,.15);color:var(--ok)}.pill.off{background:rgba(107,118,132,.18);color:var(--mut)}
.msg{padding:10px 14px;border-radius:8px;margin-bottom:14px}
.msg.ok{background:rgba(26,158,92,.13);color:var(--ok)}.msg.err{background:rgba(209,59,59,.13);color:var(--err)}
.mut{color:var(--mut);font-size:13px}.mono{font-family:ui-monospace,Menlo,monospace}
.bar{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
</style>
</head>
<body>
<div class="wrap">
  <h1>MQTT</h1>
  <p class="sub">Агент публикует показания инвертора в MQTT-брокер.</p>

  <?php if ($msg): ?><div class="msg ok"><?= h($msg) ?></div><?php endif; ?>
  <?php if ($err): ?><div class="msg err"><?= h($err) ?></div><?php endif; ?>

  <div class="card">
    <div class="bar">
      <strong>Состояние агента:</strong>
      <?php if ($running): ?><span class="pill on">работает</span>
      <?php else: ?><span class="pill off">остановлен</span><?php endif; ?>
      <?php if ($version): ?><span class="mut mono"><?= h($version) ?></span><?php endif; ?>
      <span style="flex:1"></span>
      <form method="post" action="mqtt_switch.php" style="display:inline">
        <button class="btn"><?= v($cfg,"enabled") ? "Выключить" : "Включить" ?></button>
      </form>
    </div>
  </div>

  <form method="post" action="mqtt_save.php">
  <div class="card">
    <h3 style="margin-top:0">Брокер</h3>
    <div class="row">
      <div>
        <label>Адрес брокера (host:port)</label>
        <input type="text" name="broker" value="<?= h(v($cfg,"broker")) ?>" placeholder="192.168.20.10:1883">
      </div>
      <div>
        <label>Корень топиков</label>
        <input type="text" name="base_topic" value="<?= h(v($cfg,"base_topic","microart/inv1")) ?>" placeholder="microart/inv1">
      </div>
    </div>
    <div class="row">
      <div><label>Пользователь</label><input type="text" name="username" value="<?= h(v($cfg,"username")) ?>" autocomplete="off"></div>
      <div><label>Пароль</label><input type="password" name="password" value="<?= h(v($cfg,"password")) ?>" autocomplete="new-password"></div>
    </div>
    <div class="row">
      <div>
        <label>QoS</label>
        <select name="qos">
          <?php $q = (int)v($cfg,"qos",0); foreach (array(0,1,2) as $i): ?>
            <option value="<?= $i ?>" <?= $q===$i?"selected":"" ?>><?= $i ?></option>
          <?php endforeach; ?>
        </select>
      </div>
      <div>
        <label>Интервал опроса, сек</label>
        <input type="number" name="interval_sec" min="1" max="3600" value="<?= h(v($cfg,"interval_sec",5)) ?>">
      </div>
      <div>
        <label>Переотправка всего, сек</label>
        <input type="number" name="republish_sec" min="0" max="86400" value="<?= h(v($cfg,"republish_sec",600)) ?>">
      </div>
    </div>
    <div class="chk"><input type="checkbox" id="retain" name="retain" <?= v($cfg,"retain",true)?"checked":"" ?>><label for="retain">Ретейн (подписчик сразу получает последнее значение)</label></div>
    <div class="chk"><input type="checkbox" id="enabled" name="enabled" <?= v($cfg,"enabled")?"checked":"" ?>><label for="enabled">Агент включён</label></div>
  </div>

  <div class="card">
    <h3 style="margin-top:0">Метрики</h3>
    <p class="mut">Список значений в формате JSON. Поля: <span class="mono">name, topic, device (map|bat|mppt), target, kind (number|string), precision</span>. Пусто — набор по умолчанию.</p>
    <textarea name="metrics"><?= h($metrics_json) ?></textarea>
  </div>

  <div class="bar">
    <button class="btn primary" type="submit">Сохранить и применить</button>
    <span class="mut">Опрос перезапустится сразу.</span>
  </div>
  </form>

  <div class="card" style="margin-top:16px">
    <h3 style="margin-top:0">Обновление агента</h3>
    <form method="post" action="mqtt_update.php">
      <label>URL бинарника (ARM). Пусто — последняя версия с GitHub.</label>
      <div class="bar">
        <input type="text" name="url" placeholder="https://github.com/staskuznec/microart2mqtt/releases/latest/download/malina-agent-linux-armv7">
        <button class="btn" type="submit">Обновить</button>
      </div>
    </form>
    <p class="mut">Скачает бинарник, сверит контрольную сумму, проверит запуск и перезапустит агент. Прежняя версия сохраняется для отката.</p>
  </div>
</div>
</body>
</html>
