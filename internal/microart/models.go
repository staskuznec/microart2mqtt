package microart

// Здесь находятся модельные структуры MicroArt (перенесены из transport)
// Эти структуры используются клиентом API (internal/api/microart) и другими пакетами.

// BatteryResponse - основные данные о батарее (первый объект в массиве)
type BatteryResponse struct {
	MinuteData BatteryMinuteData `json:"minute_data,omitempty"` // Данные за минуту
	SecondData BatterySecondData `json:"second_data,omitempty"` // Данные за секунду
}

// BatteryMinuteData — данные, обновляемые раз в минуту (первый объект в массиве)
type BatteryMinuteData struct {
	Timestamp   string `json:"timestamp,omitempty"`     // Unix-время (сек)
	Time        string `json:"time,omitempty"`          // Время (HH:MM:SS)
	C20         string `json:"C20,omitempty"`           // Пересчитанная емкость АКБ в С20, Ач
	C20T        string `json:"C20_t,omitempty"`         // То же, с коррекцией по температуре, Ач
	UaccAvg     string `json:"Uacc_avg,omitempty"`      // Усредненное напряжение АКБ за минуту, В
	Iavg        string `json:"Iavg,omitempty"`          // Усредненный ток, А
	CAhRemain   string `json:"C_Ah_remain,omitempty"`   // Остаточный заряд АКБ, Ач
	C100Remain  string `json:"C_100_remain,omitempty"`  // Остаточный уровень заряда, %
	CI          string `json:"C(I),omitempty"`          // Емкость в Ач как функция разрядного тока
	TTG         string `json:"TTG,omitempty"`           // Время до полного разряда, сек (0 — если нет разрядного тока)
	MpptDayE    string `json:"mppt_day_E,omitempty"`    // Энергия за день от MPPT (Вт·ч)
	LowV        string `json:"low_V,omitempty"`         // Минимальное напряжение за сутки, В
	HiV         string `json:"hi_V,omitempty"`          // Максимальное напряжение за сутки, В
	Cycles      string `json:"cycles,omitempty"`        // Количество циклов АКБ за сутки (ниже 90%)
	LD          string `json:"LD,omitempty"`            // Последний разряд (latest discharge), %
	DD          string `json:"DD,omitempty"`            // Наинизший разряд (deepest discharge), %
	AD          string `json:"AD,omitempty"`            // Средний разряд (average discharge), %
	EsumFromBat string `json:"Esum_from_bat,omitempty"` // Суммарная энергия на батарею, Вт·ч
	EsumToBat   string `json:"Esum_to_bat,omitempty"`   // Суммарная энергия от батареи, Вт·ч
	NoAsync     string `json:"No_async,omitempty"`      // Количество автосинхронизаций на 100% по заряду
	EstSoc      string `json:"est_soc,omitempty"`       // Оценочный уровень заряда (при покое АКБ), %
	ConsAh      string `json:"cons_Ah,omitempty"`       // Потреблено электричества, Ач
	EWindDay    string `json:"E_wind_day,omitempty"`    // Энергия за сутки по ветру, Вт·ч
}

// BatterySecondData — данные, обновляемые каждую секунду (второй объект в массиве)
type BatterySecondData struct {
	Timestamp   string `json:"timestamp,omitempty"`      // Unix-время (сек)
	Time        string `json:"time,omitempty"`           // Время (HH:MM:SS)
	Dcdt        string `json:"dCdt,omitempty"`           // Дельта уровня заряда, Ач/сек
	Dedt        string `json:"dEdt,omitempty"`           // Дельта по энергии, Вт·ч/сек
	Isumm       string `json:"Isumm,omitempty"`          // Мгновенная сумма токов по АКБ, А
	IntegralC0T string `json:"IntegralC_0_t,omitempty"`  // Интегральное приращение заряда, Ач
	IntegralE0T string `json:"Integral_E_0_t,omitempty"` // Интегральное приращение энергии, Вт·ч
	TBat        string `json:"t_bat,omitempty"`          // Температура батареи, °C
	AhUser      string `json:"Ah_user,omitempty"`        // Счетчик пользователя, Ач
	WhUser      string `json:"Wh_user,omitempty"`        // Счетчик пользователя, Вт·ч
	SunI        string `json:"Sun_I,omitempty"`          // Мгновенный ток от контроллеров, А
	SunP        string `json:"Sun_P,omitempty"`          // Мгновенная мощность от контроллеров, Вт
	WindI       string `json:"wind_I,omitempty"`         // Мгновенный ток по ветру, А
	WindP       string `json:"wind_P,omitempty"`         // Мгновенная мощность по ветру, Вт
}

// MapResponse — основной объект ответа read_json.php?device=map
type MapResponse struct {
	Timestamp      string `json:"timestamp,omitempty"`         // Временная метка (Unix time)
	Time           string `json:"time,omitempty"`              // Текущее время (HH:MM:SS)
	UID            string `json:"_UID,omitempty"`              // Уникальный ID устройства (EEPROM)
	Mode           string `json:"_MODE,omitempty"`             // Режим работы МАП (см. документацию)
	StatusChar     string `json:"_Status_Char,omitempty"`      // Статус заряда (по протоколу МикроАрт)
	Uacc           string `json:"_Uacc,omitempty"`             // Напряжение АКБ, В
	UchT           string `json:"_Uch_T,omitempty"`            // Напряжение окончания заряда АКБ с коррекцией по температуре
	UbufT          string `json:"_Ubuf_T,omitempty"`           // Буферное напряжение АКБ с коррекцией по температуре
	Iacc           string `json:"_Iacc,omitempty"`             // Ток АКБ, А
	PLoad          string `json:"_PLoad,omitempty"`            // Мощность по АКБ, Вт
	PLoadCalc      string `json:"_PLoad_calc,omitempty"`       // Расчетная мощность по АКБ, Вт
	FAccOver       string `json:"_F_Acc_Over,omitempty"`       // Флаг перегрузки по АКБ
	FNetOver       string `json:"_F_Net_Over,omitempty"`       // Флаг перегрузки по сети 220В
	UNET           string `json:"_UNET,omitempty"`             // Напряжение сети (вход МАП), В
	INET           string `json:"_INET,omitempty"`             // Ток по входу МАП, А (грубое значение)
	PNET           string `json:"_PNET,omitempty"`             // Мощность по входу МАП, ВА
	PNETCalc       string `json:"_PNET_calc,omitempty"`        // Расчетная мощность по входу МАП, ВА
	TFNET          string `json:"_TFNET,omitempty"`            // Частота сети по входу МАП, Гц
	ThFMAP         string `json:"_ThFMAP,omitempty"`           // Частота по выходу МАП, Гц
	UOUTmed        string `json:"_UOUTmed,omitempty"`          // Усредненное напряжение на выходе МАП, В
	TFNETLimit     string `json:"_TFNET_Limit,omitempty"`      // Лимит частоты сети
	UNETLimit      string `json:"_UNET_Limit,omitempty"`       // Лимит напряжения сети
	RSErrSis       string `json:"_RSErrSis,omitempty"`         // Ошибки системы
	RSErrJobM      string `json:"_RSErrJobM,omitempty"`        // Ошибки задания режима
	RSErrJob       string `json:"_RSErrJob,omitempty"`         // Ошибки работы
	RSWarning      string `json:"_RSWarning,omitempty"`        // Предупреждения
	RSErrDop       string `json:"_RSErrDop,omitempty"`         // Дополнительные ошибки
	TempGrad0      string `json:"_Temp_Grad0,omitempty"`       // Температура внешнего датчика (на АКБ)
	TempGrad2      string `json:"_Temp_Grad2,omitempty"`       // Температура транзисторов
	InetFlag       string `json:"_Inet_flag,omitempty"`        // Флаг сети (см. протокол)
	IAccAvg        string `json:"_I_acc_avg,omitempty"`        // Средний ток по АКБ
	IMpptAvg       string `json:"_I_mppt_avg,omitempty"`       // Средний ток от всех MPPT-контроллеров
	I2CErr         string `json:"_I2C_Err,omitempty"`          // Ошибки по шине I2C
	TempGrad1      string `json:"_Temp_Grad1,omitempty"`       // Температура датчика тора (DOMINATOR)
	Relay1         string `json:"_Relay1,omitempty"`           // Состояние реле 1
	Relay2         string `json:"_Relay2,omitempty"`           // Состояние реле 2
	FlagECO        string `json:"_Flag_ECO,omitempty"`         // Флаг ECO режима
	FlagUnet2      string `json:"_flagUnet2,omitempty"`        // Флаг напряжения сети 2
	CoolerSpeed    string `json:"_CoolerSpeed,omitempty"`      // Скорость вентилятора
	MPPTsMode      string `json:"_MPPTs_mode,omitempty"`       // Режим работы MPPT-контроллеров
	IAcc3ph        string `json:"_I_acc_3ph,omitempty"`        // Общий ток потребления/заряда по АКБ для 3-ф системы, А
	IPh1           string `json:"_I_ph1,omitempty"`            // Ток по фазе 1 потребления/заряда по АКБ
	IPh2           string `json:"_I_ph2,omitempty"`            // Ток по фазе 2 потребления/заряда по АКБ
	IPh3           string `json:"_I_ph3,omitempty"`            // Ток по фазе 3 потребления/заряда по АКБ
	Fw             string `json:"fw,omitempty"`                // Версия прошивки МАП
	PMpptAvg       string `json:"_P_mppt_avg,omitempty"`       // Средняя мощность от MPPT-контроллеров, Вт
	PAcc3ph        string `json:"_P_acc_3ph,omitempty"`        // Мощность по АКБ для 3-ф системы, Вт
	PPh1           string `json:"_P_ph1,omitempty"`            // Мощность по фазе 1, Вт
	PPh2           string `json:"_P_ph2,omitempty"`            // Мощность по фазе 2, Вт
	PPh3           string `json:"_P_ph3,omitempty"`            // Мощность по фазе 3, Вт
	ENETB          string `json:"_E_NET_B,omitempty"`          // Энергия сети (резерв)
	EACCB          string `json:"_E_ACC_B,omitempty"`          // Энергия АКБ (резерв)
	EACCCHARGEB    string `json:"_E_ACC_CHARGE_B,omitempty"`   // Энергия заряда АКБ (резерв)
	ENETSIGNB      string `json:"_E_NET_SIGN_B,omitempty"`     // Счетчик направления тока энергии по входу МАП (резерв)
	IMPPTWIND      string `json:"_I_MPPT_WIND,omitempty"`      // Ток MPPT ветрогенератора, А
	PMPPTWIND      string `json:"_P_MPPT_WIND,omitempty"`      // Мощность MPPT ветрогенератора, Вт
	FlUAccChBUF24h string `json:"_fl_UAccChBUF_24h,omitempty"` // Флаг напряжения АКБ за 24ч

	// Массивы для BMS и MPPT-контроллеров (если есть)
	BMSCells        []BMSCell        `json:"bms_cells,omitempty"`        // Массив ячеек BMS
	MPPTControllers []MPPTController `json:"mppt_controllers,omitempty"` // Массив MPPT-контроллеров
}

// BMSCell — объект ячейки BMS
type BMSCell struct {
	CID string `json:"CID,omitempty"` // ID ячейки
	V   string `json:"V,omitempty"`   // Напряжение ячейки, В
	I   string `json:"I,omitempty"`   // Ток балансировки, А
	T   string `json:"t,omitempty"`   // Температура ячейки, °C (127 — нет датчика)
}

// MPPTController — объект MPPT-контроллера на шине I2C
type MPPTController struct {
	MID string `json:"MID,omitempty"` // ID контроллера (адрес на шине)
	I   string `json:"I,omitempty"`   // Ток от контроллера, А
	T   string `json:"T,omitempty"`   // Тип (0 — солнце, 1 — ветер)
}

// MPPTResponse — структура данных ответа read_json.php?device=mppt
type MPPTResponse struct {
	Timestamp string `json:"timestamp,omitempty"` // Временная метка (Unix time)
	Time      string `json:"time,omitempty"`      // Текущее время (HH:MM:SS)
	UID       string `json:"UID,omitempty"`       // Уникальный ID контроллера (может быть установлен пользователем)
	VcPV      string `json:"Vc_PV,omitempty"`     // Напряжение панелей, В
	IcPV      string `json:"Ic_PV,omitempty"`     // Ток панелей, А
	VBat      string `json:"V_Bat,omitempty"`     // Напряжение батареи, В
	PPV       string `json:"P_PV,omitempty"`      // Мощность с панелей, Вт
	POut      string `json:"P_Out,omitempty"`     // Мощность по выходу контроллера, Вт
	PLoad     string `json:"P_Load,omitempty"`    // Не используется
	PCurr     string `json:"P_curr,omitempty"`    // Мощность на АКБ, Вт
	ICh       string `json:"I_Ch,omitempty"`      // Ток на АКБ, А
	IOut      string `json:"IOut,omitempty"`      // Не используется
	TempInt   string `json:"Temp_Int,omitempty"`  // Температура транзисторов, °C
	TempBat   string `json:"Temp_Bat,omitempty"`  // Температура батареи, °C
	PwrKW     string `json:"Pwr_kW,omitempty"`    // Выработка за день от панелей, кВт·ч
	SignC0    string `json:"Sign_C0,omitempty"`   // Знак по внешнему датчику Холла 0
	SignC1    string `json:"Sign_C1,omitempty"`   // Знак по внешнему датчику Холла 1
	IEXTS0    string `json:"I_EXTS0,omitempty"`   // Ток по внешнему датчику Холла 0, А
	IEXTS1    string `json:"I_EXTS1,omitempty"`   // Ток по внешнему датчику Холла 1, А
	PEXTS0    string `json:"P_EXTS0,omitempty"`   // Мощность по внешнему датчику Холла 0, Вт
	PEXTS1    string `json:"P_EXTS1,omitempty"`   // Мощность по внешнему датчику Холла 1, Вт
	RelayC    string `json:"Relay_C,omitempty"`   // Композитное значение состояний реле
	RSErrSis  string `json:"RSErrSis,omitempty"`  // Ошибки системы
	Mode      string `json:"Mode,omitempty"`      // Режим работы контроллера
	Sign      string `json:"Sign,omitempty"`      // Знак режима работы
	MPP       string `json:"MPP,omitempty"`       // Тип MPPT (L — линейный, др. значения см. инструкцию)
	Windspeed string `json:"windspeed,omitempty"` // Частота вращения ротора ВГ (0xFFFF — нет данных)
	FW        string `json:"FW,omitempty"`        // Версия прошивки контроллера
	R1        string `json:"R1,omitempty"`        // Состояние реле 1
	R2        string `json:"R2,omitempty"`        // Состояние реле 2
	R3        string `json:"R3,omitempty"`        // Состояние реле 3
}
