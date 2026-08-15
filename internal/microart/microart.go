package microart

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// MicroArtOption представляет опцию конфигурации для MicroArt
type MicroArtOption struct {
	BaseURL       string
	Timeout       time.Duration
	MaxRetries    int
	RetryInterval time.Duration
}

// MicroArt - структура для работы с API MicroArt
type MicroArt struct {
	config     MicroArtOption
	httpClient *http.Client
}

func NewMicroArt(option MicroArtOption) (*MicroArt, error) {
	if option.BaseURL == "" {
		return nil, fmt.Errorf("baseURL не может быть пустым")
	}
	if option.Timeout == 0 {
		option.Timeout = 5 * time.Second
	}
	if option.MaxRetries < 1 {
		option.MaxRetries = 3
	}
	client := &http.Client{
		Timeout: option.Timeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: option.Timeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 3 * time.Second,
		},
	}
	return &MicroArt{config: option, httpClient: client}, nil
}

// RawJSON возвращает сырой ответ read_json.php?device=… без разбора в структуры.
// Нужен агенту на «Малине»: он публикует в MQTT всё, что отдаёт устройство, а
// не только поля известных моделей. device — map, bat или mppt.
func (m *MicroArt) RawJSON(device string) ([]byte, error) {
	url := fmt.Sprintf("%s/read_json.php?device=%s", m.config.BaseURL, device)
	return m.doRequestWithRetry(url)
}

func (m *MicroArt) doRequestWithRetry(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < m.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := m.config.RetryInterval * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}
		resp, err := m.httpClient.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		// явно игнорируем ошибку закрытия тела, чтобы не оставлять неучтённый err
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			continue
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}
		return b, nil
	}
	return nil, fmt.Errorf("all attempts failed: %w", lastErr)
}

func (m *MicroArt) GetDeviceInfo() (*MapResponse, error) {
	var mapResp *MapResponse
	data, err := m.doRequestWithRetry(fmt.Sprintf("%s/read_json.php?device=map", m.config.BaseURL))
	if err != nil {
		return mapResp, err
	}
	clean := strings.TrimSpace(string(data))
	if clean == "" {
		return mapResp, fmt.Errorf("empty response")
	}
	// try parsing as single JSON
	if err := json.Unmarshal([]byte(clean), &mapResp); err == nil {
		return mapResp, nil
	}
	// try splitting by lines
	lines := strings.Split(clean, "\n")
	if len(lines) > 0 {
		if err := json.Unmarshal([]byte(lines[0]), &mapResp); err == nil {
			// optional extra arrays
			if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
				var mppts []MPPTController
				if err := json.Unmarshal([]byte(lines[1]), &mppts); err == nil {
					mapResp.MPPTControllers = mppts
				}
			}
			if len(lines) > 2 && strings.TrimSpace(lines[2]) != "" {
				var bms []BMSCell
				if err := json.Unmarshal([]byte(lines[2]), &bms); err == nil {
					mapResp.BMSCells = bms
				}
			}
			return mapResp, nil
		}
	}
	return mapResp, fmt.Errorf("cannot parse device map response")
}

func (m *MicroArt) GetBatteryInfo() (BatteryResponse, error) {
	var batResp BatteryResponse
	data, err := m.doRequestWithRetry(fmt.Sprintf("%s/read_json.php?device=bat", m.config.BaseURL))
	if err != nil {
		return batResp, err
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return batResp, err
	}
	if len(arr) > 0 {
		if err := json.Unmarshal(arr[0], &batResp.MinuteData); err != nil {
			// ignore parse error for minute data
		}
	}
	if len(arr) > 1 {
		if err := json.Unmarshal(arr[1], &batResp.SecondData); err != nil {
			// ignore parse error for second data
		}
	}
	return batResp, nil
}

// GetMpptInfo получает данные от MPPT контроллера
func (m *MicroArt) GetMpptInfo() (*MPPTResponse, error) {
	var mpptResp MPPTResponse
	data, err := m.doRequestWithRetry(fmt.Sprintf("%s/read_json.php?device=mppt", m.config.BaseURL))
	if err != nil {
		return nil, err
	}
	clean := strings.TrimSpace(string(data))
	if clean == "" {
		return nil, fmt.Errorf("empty response")
	}
	// Парсим как единичный JSON-объект
	if err := json.Unmarshal([]byte(clean), &mpptResp); err != nil {
		return nil, fmt.Errorf("cannot parse mppt response: %w", err)
	}
	return &mpptResp, nil
}

// GetBatteryField возвращает строковое представление значения поля батареи по пути.
// Путь задаётся через точку, например:
//   - "minute_data.C_100_remain" (используя json-ключи)
//   - "MinuteData.C100Remain" (используя имена полей struct)
//
// Функция поддерживает вложенные структуры и массивы (возвращает первый элемент при обходе).
func (m *MicroArt) GetBatteryField(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path пуст")
	}
	bat, err := m.GetBatteryInfo()
	if err != nil {
		return "", fmt.Errorf("получение BatteryInfo: %w", err)
	}
	// представим структуру как map[string]interface{} с ключами и json-ключами
	mp := structToDualKeyMap(bat)
	// обход по части через точку
	parts := strings.Split(path, ".")
	var cur interface{} = mp
	for _, p := range parts {
		switch c := cur.(type) {
		case map[string]interface{}:
			// попробуем точное совпадение
			if v, ok := c[p]; ok {
				cur = v
				continue
			}
			// если нет, попробуем привести ключ к json-стилю (lower_snake) — но мы уже заполнили обе версии при конвертации
			// попробуем также без учёта регистра
			found := false
			for k, v := range c {
				if strings.EqualFold(k, p) {
					cur = v
					found = true
					break
				}
			}
			if found {
				continue
			}
			// если не нашли, и текущий элемент представляет массив — попробуем взять первый элемент и повторить
			return "", fmt.Errorf("ключ %s не найден в текущем объекте", p)
		case []interface{}:
			if len(c) == 0 {
				return "", fmt.Errorf("массив пуст при попытке доступа по %s", p)
			}
			cur = c[0]
			// повторим текущую часть (не продолжаем на next), чтобы попытаться найти p в первом элементе
			// уменьшаем итератор: на практике мы просто повторим обработку текущ p с новым cur
			// реализовано путём цикла while-like: снизим индекс на 1
			// но проще — выполнить поиск в новом cur, поэтому повторим текущ p by reassigning loop
			// обозначим это хитрым способом: повторим текущ p by using goto-like loop
			// На Go это неудобно; вместо этого мы просто continue, но это приведёт к пропуску части — поэтому лучше обработать заранее
			// Для упрощения: если встретили слайс, берем первый элемент и продолжаем (без шага i)
			// Чтобы сделать это корректно, используем recursion: вызовем вспомогательную функцию
			return resolvePathFrom(cur, append([]string{p}, parts[1:]...))
		default:
			// конечное значение — вернуть его строковое представление
			return fmt.Sprintf("%v", cur), nil
		}
	}
	// после цикла cur содержит найденное значение
	return fmt.Sprintf("%v", cur), nil
}

// resolvePathFrom — рекурсивная/итеративная помощь для обхода пути, поддерживает индексы массивов
func resolvePathFrom(root interface{}, parts []string) (string, error) {
	cur := root
	for i := 0; i < len(parts); {
		p := parts[i]
		switch c := cur.(type) {
		case map[string]interface{}:
			// прямое совпадение
			if v, ok := c[p]; ok {
				cur = v
				i++
				continue
			}
			// case-insensitive
			found := false
			for k, v := range c {
				if strings.EqualFold(k, p) {
					cur = v
					found = true
					break
				}
			}
			if found {
				i++
				continue
			}
			return "", fmt.Errorf("ключ %s не найден (часть %d)", p, i)
		case []interface{}:
			// поддержка записи индекса как части пути: .0 .1 и т.д.
			if idx, err := strconv.Atoi(p); err == nil {
				if idx < 0 || idx >= len(c) {
					return "", fmt.Errorf("индекс %d вне диапазона (часть %d)", idx, i)
				}
				cur = c[idx]
				i++
				continue
			}
			// если часть не числовая — берем первый элемент и повторяем ту же часть
			if len(c) == 0 {
				return "", fmt.Errorf("массив пуст при попытке доступа по %s", p)
			}
			cur = c[0]
			// не увеличиваем i, чтобы повторно обработать текущую часть с новым cur
			continue
		default:
			// если дошли до скалярного значения раньше времени или нет вложенности — вернём значение
			return fmt.Sprintf("%v", cur), nil
		}
	}
	return fmt.Sprintf("%v", cur), nil
}

// structToDualKeyMap конвертирует произвольную структуру в map[string]interface{}
// и добавляет для каждого поля два ключа: имя поля struct и json-ключ (если задан).
func structToDualKeyMap(in interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	rv := reflect.ValueOf(in)
	if !rv.IsValid() {
		return out
	}
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return out
		}
		rv = rv.Elem()
	}
	rt := rv.Type()
	if rv.Kind() != reflect.Struct {
		// если не структура — попытаемся раскодировать через json
		b, err := json.Marshal(in)
		if err != nil {
			return out
		}
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		return m
	}

	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		fv := rv.Field(i)
		// пропускаем неэкспортируемые поля
		if f.PkgPath != "" {
			continue
		}
		fieldName := f.Name
		jsonTag := f.Tag.Get("json")
		jsonKey := strings.Split(jsonTag, ",")[0]
		if jsonKey == "" {
			// default: lowercased first letter
			jsonKey = strings.ToLower(fieldName[:1]) + fieldName[1:]
		}
		var val interface{}
		if fv.Kind() == reflect.Struct || (fv.Kind() == reflect.Ptr && fv.Elem().Kind() == reflect.Struct) {
			val = structToDualKeyMap(fv.Interface())
		} else if fv.Kind() == reflect.Slice {
			// слайсы — попытка преобразовать элементов
			arr := make([]interface{}, fv.Len())
			for j := 0; j < fv.Len(); j++ {
				e := fv.Index(j).Interface()
				if reflect.ValueOf(e).Kind() == reflect.Struct || (reflect.ValueOf(e).Kind() == reflect.Ptr && reflect.ValueOf(e).Elem().Kind() == reflect.Struct) {
					arr[j] = structToDualKeyMap(e)
				} else {
					arr[j] = e
				}
			}
			val = arr
		} else {
			val = fv.Interface()
		}
		out[fieldName] = val
		out[jsonKey] = val
	}
	return out
}

// GetDeviceField возвращает строковое представление значения из MapResponse по пути.
// Путь задаётся через точку, например "_Uacc" или "MPPTControllers.0.I" (для массивов берётся первый элемент).
func (m *MicroArt) GetDeviceField(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path пуст")
	}
	mp, err := m.GetDeviceInfo()
	if err != nil {
		return "", fmt.Errorf("получение DeviceInfo: %w", err)
	}
	// конвертируем MapResponse в двойную map (имена полей + json-ключи)
	mm := structToDualKeyMap(mp)
	parts := strings.Split(path, ".")
	return resolvePathFrom(mm, parts)
}

// GetMpptField возвращает строковое представление значения поля MPPT по пути.
// Путь задаётся через точку, например "VcPV" или "Vc_PV" или "P_PV".
func (m *MicroArt) GetMpptField(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path пуст")
	}
	mppt, err := m.GetMpptInfo()
	if err != nil {
		return "", fmt.Errorf("получение MpptInfo: %w", err)
	}
	// конвертируем MPPTResponse в двойную map (имена полей + json-ключи)
	mm := structToDualKeyMap(mppt)
	parts := strings.Split(path, ".")
	return resolvePathFrom(mm, parts)
}

// FieldFrom возвращает значение по пути из УЖЕ полученного ответа API —
// MapResponse, BatteryResponse или MPPTResponse. В отличие от GetDeviceField
// и соседей, ничего не запрашивает по сети: демон забирает ответ один раз
// за цикл опроса и достаёт из него все нужные поля.
func FieldFrom(resp interface{}, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path пуст")
	}
	if resp == nil {
		return "", fmt.Errorf("пустой ответ API")
	}
	return resolvePathFrom(structToDualKeyMap(resp), strings.Split(path, "."))
}

func (m *MicroArt) Close() error {
	m.httpClient.CloseIdleConnections()
	return nil
}
