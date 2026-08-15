package microart

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ClientOption - конфигурация клиента MicroArt
type ClientOption struct {
	BaseURL       string        // Базовый URL, например "http://192.168.20.250"
	Timeout       time.Duration // Общий таймаут HTTP клиента
	DialTimeout   time.Duration // Таймаут установки соединения
	MaxRetries    int           // Количество попыток
	RetryInterval time.Duration // Начальный интервал между попытками
}

// DefaultClientOption возвращает значения по умолчанию
func DefaultClientOption(baseURL string) ClientOption {
	return ClientOption{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		Timeout:       10 * time.Second,
		DialTimeout:   3 * time.Second,
		MaxRetries:    3,
		RetryInterval: 500 * time.Millisecond,
	}
}

// Client — HTTP-клиент для MicroArt API
type Client struct {
	opt        ClientOption
	httpClient *http.Client
}

// NewClient создает новый клиент MicroArt с явными опциями
func NewClient(opt ClientOption) (*Client, error) {
	if opt.BaseURL == "" {
		return nil, fmt.Errorf("BaseURL не может быть пустым")
	}
	if opt.Timeout == 0 {
		opt.Timeout = 10 * time.Second
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 3 * time.Second
	}
	if opt.MaxRetries <= 0 {
		opt.MaxRetries = 3
	}
	if opt.RetryInterval == 0 {
		opt.RetryInterval = 500 * time.Millisecond
	}

	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   opt.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 3 * time.Second,
		Proxy:               nil, // отключаем прокси — прямое соединение
	}

	client := &http.Client{
		Timeout:   opt.Timeout,
		Transport: tr,
	}

	return &Client{opt: opt, httpClient: client}, nil
}

// doRequestWithRetry выполняет GET-запрос с retry и возвращает тело ответа
func (c *Client) doRequestWithRetry(path string) ([]byte, error) {
	var lastErr error
	url := fmt.Sprintf("%s/%s", strings.TrimRight(c.opt.BaseURL, "/"), strings.TrimLeft(path, "/"))

	for attempt := 0; attempt < c.opt.MaxRetries; attempt++ {
		if attempt > 0 {
			// экспоненциальная задержка (умножаем на 2^(attempt-1))
			delay := c.opt.RetryInterval * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		resp, err := c.httpClient.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("попытка %d: %w", attempt+1, err)
			continue
		}

		// убедимся, что тело закроется
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("попытка %d: ошибка чтения ответа: %w", attempt+1, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("попытка %d: неверный статус: %d, тело: %s", attempt+1, resp.StatusCode, string(body))
			continue
		}

		return body, nil
	}

	return nil, fmt.Errorf("исчерпаны все попытки: %w", lastErr)
}

// GetDeviceInfo получает данные /read_json.php?device=map
// API возвращает несколько JSON-блоков разделенных переводом строки: MAP, контроллеры I2C, BMS
func (c *Client) GetDeviceInfo() (MapResponse, error) {
	var mapResp MapResponse

	data, err := c.doRequestWithRetry("read_json.php?device=map")
	if err != nil {
		return mapResp, err
	}

	clean := strings.TrimSpace(string(data))
	if clean == "" {
		return mapResp, fmt.Errorf("пустой ответ от сервера")
	}

	// Разделяем по строкам — каждая строка это отдельный JSON
	lines := strings.Split(clean, "\n")
	if len(lines) == 0 {
		return mapResp, fmt.Errorf("неверный формат ответа MAP")
	}

	// 1) Первый JSON — основной объект MAP
	if err := json.Unmarshal([]byte(lines[0]), &mapResp); err != nil {
		return mapResp, fmt.Errorf("ошибка разбора MAP: %w", err)
	}

	// 2) Второй JSON — контроллеры MPPT (массив объектов) — опционально
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		var mppts []MPPTController
		if err := json.Unmarshal([]byte(lines[1]), &mppts); err == nil {
			mapResp.MPPTControllers = mppts
		}
	}

	// 3) Третий JSON — BMS (массив объектов) — опционально
	if len(lines) > 2 && strings.TrimSpace(lines[2]) != "" {
		var bms []BMSCell
		if err := json.Unmarshal([]byte(lines[2]), &bms); err == nil {
			mapResp.BMSCells = bms
		}
	}

	return mapResp, nil
}

// GetBatteryInfo получает данные /read_json.php?device=bat
// API возвращает массив из двух объектов: [minuteData, secondData]
func (c *Client) GetBatteryInfo() (BatteryResponse, error) {
	var batResp BatteryResponse

	data, err := c.doRequestWithRetry("read_json.php?device=bat")
	if err != nil {
		return batResp, err
	}

	clean := strings.TrimSpace(string(data))
	if clean == "" {
		return batResp, fmt.Errorf("пустой ответ от сервера")
	}

	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(clean), &arr); err != nil {
		return batResp, fmt.Errorf("ошибка разбора массива BAT: %w", err)
	}

	if len(arr) > 0 {
		if err := json.Unmarshal(arr[0], &batResp.MinuteData); err != nil {
			return batResp, fmt.Errorf("ошибка разбора MinuteData: %w", err)
		}
	}
	if len(arr) > 1 {
		if err := json.Unmarshal(arr[1], &batResp.SecondData); err != nil {
			return batResp, fmt.Errorf("ошибка разбора SecondData: %w", err)
		}
	}

	return batResp, nil
}
