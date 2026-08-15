<?php
// Сохранение настроек агента microart2mqtt и применение на лету.
// Пишет /settings/web-data/mqtt.json и шлёт агенту SIGUSR1 через agent-ctl.sh.

$CFG = "/settings/web-data/mqtt.json";
$CTL = "/settings/microart-mqtt/agent-ctl.sh";

function back($ok = "", $err = "") {
    $q = $ok !== "" ? "ok=" . rawurlencode($ok) : ($err !== "" ? "err=" . rawurlencode($err) : "");
    header("Location: mqtt.php" . ($q ? "?$q" : ""));
    exit;
}

if ($_SERVER["REQUEST_METHOD"] !== "POST") back("", "ожидался POST");

// Прежний конфиг — чтобы не терять поля, которых нет в форме.
$cfg = @json_decode(@file_get_contents($CFG), true);
if (!is_array($cfg)) $cfg = array();

$cfg["broker"]        = trim((string)($_POST["broker"] ?? ""));
$cfg["base_topic"]    = trim((string)($_POST["base_topic"] ?? "microart/inv1"));
$cfg["username"]      = (string)($_POST["username"] ?? "");
$cfg["password"]      = (string)($_POST["password"] ?? "");
$cfg["qos"]           = max(0, min(2, (int)($_POST["qos"] ?? 0)));
$cfg["interval_sec"]  = max(1, (int)($_POST["interval_sec"] ?? 5));
$cfg["republish_sec"] = max(0, (int)($_POST["republish_sec"] ?? 600));
$cfg["retain"]        = isset($_POST["retain"]);
$cfg["enabled"]       = isset($_POST["enabled"]);

// Метрики приходят JSON-текстом. Пусто — оставляем как было (агент подставит
// набор по умолчанию, если список пуст).
$mtext = trim((string)($_POST["metrics"] ?? ""));
if ($mtext !== "") {
    $m = json_decode($mtext, true);
    if (!is_array($m)) back("", "метрики: неверный JSON");
    $cfg["metrics"] = $m;
}

$json = json_encode($cfg, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
if ($json === false) back("", "не удалось собрать JSON");

@mkdir(dirname($CFG), 0775, true);
if (@file_put_contents($CFG, $json) === false) back("", "не удалось записать настройки");
@chmod($CFG, 0664);

// Применяем: агент перечитает конфиг (или поднимется, если был остановлен).
@shell_exec("sudo " . escapeshellarg($CTL) . " reload 2>/dev/null");

back("Настройки сохранены и применены");
