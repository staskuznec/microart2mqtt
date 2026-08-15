<?php
// Включение/выключение агента microart2mqtt. Меняет флаг enabled в mqtt.json
// и просит агент перечитать настройки (он сам замолчит/заговорит).

$CFG = "/settings/web-data/mqtt.json";
$CTL = "/settings/microart-mqtt/agent-ctl.sh";

$cfg = @json_decode(@file_get_contents($CFG), true);
if (!is_array($cfg)) $cfg = array();

$cfg["enabled"] = empty($cfg["enabled"]);   // переключаем

$json = json_encode($cfg, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
@mkdir(dirname($CFG), 0775, true);
@file_put_contents($CFG, $json);
@chmod($CFG, 0664);

@shell_exec("sudo " . escapeshellarg($CTL) . " reload 2>/dev/null");

$state = $cfg["enabled"] ? "включён" : "выключен";
header("Location: mqtt.php?ok=" . rawurlencode("Агент $state"));
