package store

// DefaultMetrics — набор, который предлагается при добавлении инвертора.
//
// Это то, что обычно и нужно от МАП: заряд и ток батареи, сеть, нагрузка,
// солнце и режим работы. Набор накладывается поверх уже настроенного и не
// затирает метрики с теми же именами, так что применить его можно в любой
// момент — например, после обновления, когда появились новые поля.
func DefaultMetrics() []Metric {
	p := func(n int) *int { return &n }

	return []Metric{
		{Name: "soc", Topic: "battery/soc", Device: DeviceBat,
			Target: "minute_data.C_100_remain", Kind: KindNumber, Precision: p(1)},
		{Name: "battery_voltage", Topic: "battery/voltage", Device: DeviceBat,
			Target: "minute_data.Uacc_avg", Kind: KindNumber, Precision: p(2)},
		{Name: "battery_current", Topic: "battery/current", Device: DeviceBat,
			Target: "minute_data.Iavg", Kind: KindNumber, Precision: p(2)},
		{Name: "battery_remain_ah", Topic: "battery/remain_ah", Device: DeviceBat,
			Target: "minute_data.C_Ah_remain", Kind: KindNumber, Precision: p(1)},

		{Name: "grid_voltage", Topic: "grid/voltage", Device: DeviceMap,
			Target: "_UNET", Kind: KindNumber, Precision: p(1)},
		{Name: "grid_power", Topic: "grid/power", Device: DeviceMap,
			Target: "_PNET", Kind: KindNumber, Precision: p(1)},

		{Name: "load_power", Topic: "load/power", Device: DeviceMap,
			Target: "_PLoad", Kind: KindNumber, Precision: p(1)},

		{Name: "solar_power", Topic: "solar/power", Device: DeviceBat,
			Target: "second_data.Sun_P", Kind: KindNumber, Precision: p(1)},
		{Name: "solar_day_wh", Topic: "solar/day_wh", Device: DeviceBat,
			Target: "minute_data.mppt_day_E", Kind: KindNumber, Precision: p(0)},

		{Name: "status", Topic: "status", Device: DeviceMap,
			Target: "_Status_Char", Kind: KindNumber},
		{Name: "mode", Topic: "mode", Device: DeviceMap,
			Target: "_MODE", Kind: KindString},
		{Name: "temperature", Topic: "temperature", Device: DeviceBat,
			Target: "second_data.t_bat", Kind: KindNumber, Precision: p(1)},
	}
}

// CopyMetrics готовит метрики одного инвертора к переносу на другой:
// сбрасывает идентификаторы, чтобы они добавились как новые.
func CopyMetrics(src []Metric) []Metric {
	out := make([]Metric, 0, len(src))
	for _, m := range src {
		m.ID = 0
		m.InverterID = 0
		m.Position = 0
		out = append(out, m)
	}
	return out
}
