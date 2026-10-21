# MIB для Raisecom ISCOM2600G (ROS 5.x)

Неофициальные MIB, написанные по тому, что реально отдаёт **ISCOM2624G-4GE-PWR-AC с ROS 3.60.311**.
Покрывают все приватные таблицы, которые читает `raisecom_exporter`, со всеми их колонками.

| Файл | Что это |
|---|---|
| `RAISECOM-ISCOM2600G-MIB.mib` | **самодостаточный** модуль: его одного хватает для всех приватных объектов коммутатора |
| `modules/*.mib` | тот же набор объектов, разложенный **по модулям, как обычно поставляют вендоры** (связаны через IMPORTS) |
| `gen_mibs.py` | генератор обоих вариантов из одной спецификации (правьте его, а не `.mib`) |
| `check_mibs.sh` | проверка: загрузка net-snmp и резолв всех OID экспортера (`make mibcheck`) |
| `upstream-librenms/` | копии вендорских MIB из LibreNMS, только для справки (неполные и с другими OID, см. ниже) |

Оба варианта генерируются из одних и тех же определений. Один и тот же дескриптор указывает на один и тот же OID,
поэтому оба варианта можно загружать одновременно.

## Самодостаточный файл

Импортирует только `SNMPv2-SMI`, `SNMPv2-TC` и `SNMPv2-CONF`. Это базовые модули SMIv2, они есть в любом
SNMP-инструменте (net-snmp, pysnmp, Zabbix, LibreNMS), и без них MIB по стандарту существовать не может.
Вместо `IF-MIB::ifIndex` используются локальные индексные колонки, поэтому IF-MIB тоже не нужен.

```bash
snmpwalk -v2c -c public -M +./mibs -m +RAISECOM-ISCOM2600G-MIB 192.0.2.10 raisecomPoePortTable
snmpget  -v2c -c public -M +./mibs -m +RAISECOM-ISCOM2600G-MIB 192.0.2.10 raisecomPoePsePower.1
```

## Модульный набор

| Модуль | Корень | Содержимое |
|---|---|---|
| `RAISECOM-BASE-MIB` | `8886` | корень enterprise, `raisecomAgent`, `iscomSwitch`, TC `EnableVar` |
| `RAISECOM-SYSTEM-MIB` | `8886.1.1` | CPU (total по периодам), память, температура |
| `RAISECOM-FANMONITOR-MIB` | `8886.1.1.5` | вентиляторы: режим, состояние, обороты |
| `RAISECOM-OPTICAL-TRANSCEIVER-MIB` | `8886.1.18` | глобальные флаги DDM, информация о модуле, DDM с порогами |
| `RAISECOM-DEVICE-MIB` | `8886.6.1.1` | модель, серийный номер, версии ПО и железа, MAC (*реконструкция*) |
| `RAISECOM-PORT-MIB` | `8886.6.1.20` | описание порта (*реконструкция*) |
| `RAISECOM-POE-MIB` | `8886.6.1.51` | PoE по порту (мВт, мВ, мА, пик, лимит) и по PSE (*реконструкция*) |

Для модульного набора нужен ещё стандартный `IF-MIB` (индекс `ifIndex`).

```bash
snmpwalk -v2c -c public -M +./mibs/modules -m ALL 192.0.2.10 raisecomOpticalTransceiverDDMTable
```

## Отличия от вендорских MIB

Вендорские файлы (`upstream-librenms/`) на этой прошивке не работают как есть:

- набор неполный: `RAISECOM-OPTICAL-TRANSCEIVER-MIB` импортирует отсутствующий `RAISECOM-COMMON-MANAGEMENT-MIB`;
- **CPU** на устройстве лежит в `raisecomCpu.5.1.4` (по MIB должен быть в `raisecomCpu.3.1.2`), **память** в
  `raisecomMemory.N.0` (по MIB в `raisecomMemory.2.N.0`);
- `raisecomMemoryUtil` и `raisecomOpticalTransceiverWaveTolerance` устройство отдаёт как INTEGER, а не OCTET STRING;
- **длина волны** отдаётся в нм, хотя по MIB должна быть в пм;
- **DDM-параметр 5** — это напряжение питания 3.3 В в мВ (вендорский MIB называет его `laserTemperature`);
- в метках enum убраны дефисы: SMIv2 их запрещает (`high-alarm` → `highAlarm`, `xfp-e` → `xfpE`);
- для PoE, информации об устройстве и описаний портов вендорских MIB нет. Эти объекты реконструированы по данным
  устройства и помечены в DESCRIPTION.

Единицы DDM (`OpticalParameterValue`, DISPLAY-HINT `d-3`): температура в м°C, ток смещения в мкА, Tx/Rx в dBm×1000,
напряжение в мВ. net-snmp показывает их уже в базовых единицах: `43.500`, `-4.271`, `3.284`.

## Не включено

Объекты, смысл которых пока не подтверждён, в MIB не попадают (snmpwalk покажет их числовыми):

- `raisecomMemory.9`, `.10` (значения 9600 и 55);
- `raisecomDeviceInfo` 3, 4, 6–8, 10–12, 15;
- колонки DDM-таблицы 13–24 (OCTET STRING, в вендорском MIB не описаны);
- колонка 13 PoE-таблицы порта (на всех портах 0, тест её не затронул);
- колонки 4, 6, 7 PSE-таблицы: это PSE operation status, global administrator status и power mode, но какая из них
  какая, без отключения PoE на всём коммутаторе не определить.

## Как расшифрованы настройки PoE

Колонки 7–15 PoE-таблицы порта определены дифференциальным методом: на свободном порту 1/1/1 по одной
менялась настройка через CLI, а SNMP снимался до и после. Все настройки затем возвращены, финальное состояние
совпало с исходным.

| Колонка | Объект | CLI | Проверено |
|---|---|---|---|
| 7 | `raisecomPoePortAdminStatus` | `poe enable/disable` | меняется вместе с 15; admin определён по структуре таблицы |
| 8 | `raisecomPoePortForcePower` | `poe force-power` | по значению (единственная 2 = Disable) |
| 9 | `raisecomPoePortScheduleProfile` | `poe schedule profile` | только по положению (профиль 1 не применился) |
| 10 | `raisecomPoePortAliveCheck` | `poe alive check enable/disable` | да |
| 11 | `raisecomPoePortAliveAction` | `poe alive action …`: none 0, reboot 1, reboot-alarm 2, alarm 3 | да |
| 12 | `raisecomPoePortRebootInterval` | `poe reboot interval` | да (30 → 60) |
| 14 | `raisecomPoePortLinktraceTrack` | `poe linktrace track <1-128>` | да |
| 15 | `raisecomPoePortOperStatus` | operation / actual running status | меняется вместе с 7 |

PSE-таблица сопоставлена с `show poe pse`: C1 — Average Power(mW), C2 — Peak Power(mW), C8 — Wattage Rating(W).
C3 (`raisecomPoePseLegacyDetection`, `poe legacy`) и C5 (`raisecomPoePseTemperatureProtection`,
`poe temperature-protection`) подтверждены переключением.

Колонка 3 (`raisecomPoePortPowerInstant`) — это «Output Power(mW)» из `show poe interface` (шаг 100 мВт),
а колонка 2 (`raisecomPoePortPower`) — её сглаженное среднее.

## Проверка

```bash
make mibcheck
```

Скрипт проверяет три вещи:
- самодостаточный файл загружается из пустого каталога рядом только со стандартными MIB;
- модульный набор загружается без диагностик net-snmp;
- каждый приватный OID, который читает экспортер (из `testdata/iscom2624g.walk`), резолвится в имя, одинаковое в
  обоих вариантах.

Дополнительно оба варианта проверены строгим компилятором pysmi (`mibdump`).
