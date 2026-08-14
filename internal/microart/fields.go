package microart

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Field — поле ответа API с его текущим значением.
type Field struct {
	Path    string // путь для target: minute_data.C_100_remain
	Value   string // что сейчас отдаёт «Малина»
	Numeric bool   // значение похоже на число — можно публиковать как number
}

// Fields разбирает уже полученный ответ API в плоский список полей.
//
// Нужен веб-интерфейсу: метрика добавляется выбором из живого списка с
// текущими значениями, а не вслепую по названию из документации. Пути
// собираются из json-имён — ровно тех, что понимает FieldFrom.
//
// Поля, которых «Малина» не прислала, в список не попадают: у моделей стоит
// omitempty, и это к лучшему — показываем только то, что железо реально
// отдаёт для этой конфигурации.
func Fields(resp any) ([]Field, error) {
	if resp == nil {
		return nil, fmt.Errorf("пустой ответ API")
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("разбор ответа API: %w", err)
	}

	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("разбор ответа API: %w", err)
	}

	var out []Field
	walk("", tree, &out)

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func walk(prefix string, v any, out *[]Field) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			walk(join(prefix, k), child, out)
		}
	case []any:
		for i, child := range t {
			walk(join(prefix, strconv.Itoa(i)), child, out)
		}
	case nil:
		// Ничего: пустое поле метрикой не сделаешь.
	default:
		s := scalar(t)
		if prefix == "" || s == "" {
			return
		}
		*out = append(*out, Field{Path: prefix, Value: s, Numeric: isNumeric(s)})
	}
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
