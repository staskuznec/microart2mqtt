<?php
// Обновление бинарника агента microart2mqtt из веба.
// Передаёт URL в agent-ctl.sh update; тот скачает, сверит сумму, проверит
// запуск и перезапустит агент. Пусто — последняя версия с GitHub.

$CTL = "/settings/microart-mqtt/agent-ctl.sh";
$DEFAULT = "https://github.com/staskuznec/microart2mqtt/releases/latest/download/malina-agent-linux-armv7";

if ($_SERVER["REQUEST_METHOD"] !== "POST") {
    header("Location: mqtt.php");
    exit;
}

$url = trim((string)($_POST["url"] ?? ""));
if ($url === "") $url = $DEFAULT;

// Разрешаем только http(s) — в update передаётся во внешнюю команду.
if (!preg_match('#^https?://[^\s"\'`;|&$()<>]+$#', $url)) {
    header("Location: mqtt.php?err=" . rawurlencode("Неверный URL"));
    exit;
}

$out = shell_exec("sudo " . escapeshellarg($CTL) . " update " . escapeshellarg($url) . " 2>&1");
$out = trim((string)$out);

// Последняя строка лога — самая содержательная (обновлён / ошибка).
$last = $out === "" ? "нет ответа" : trim(substr($out, strrpos($out, "\n") !== false ? strrpos($out, "\n") + 1 : 0));

if (stripos($out, "обновл") !== false || stripos($out, "updated") !== false) {
    header("Location: mqtt.php?ok=" . rawurlencode("Обновление: " . $last));
} else {
    header("Location: mqtt.php?err=" . rawurlencode("Обновление не удалось: " . $last));
}
