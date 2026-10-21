# raisecom-snmp-exporter

[English](README.md) | **Русский**

Prometheus-экспортер для коммутаторов Raisecom серии ISCOM2600G под управлением ROS (Raisecom Operating System)
с упором на детальные PoE-метрики.
Собирает данные по SNMP (v2c/v3), работает в multi-target режиме как `snmp_exporter`: один экспортер опрашивает
любое число коммутаторов, Prometheus передаёт адрес в `/probe?target=`.

Проверено на **ISCOM2624G-4GE-PWR-AC**, прошивка 3.60.311 на ROS 5.2.1. Другие модели ISCOM2600G и версии ROS 5.x,
скорее всего, тоже работают, но не проверялись.

![Дашборд Grafana: обзор устройства, бюджет PoE и таблица PoE по портам](images/screenshot-02.png)

## Что собирается

| Коллектор    | Источник                                              | Что даёт                                                              |
|--------------|-------------------------------------------------------|-----------------------------------------------------------------------|
| `poe`        | POWER-ETHERNET-MIB + приватная ветка `8886.6.1.51`    | по порту: статус, класс, приоритет, Вт (средняя/мгновенная/пик/лимит), В, А, счётчики overload/short/denied/MPS, настройки; по PSE: бюджет, потребление, пик, % |
| `interfaces` | IF-MIB / ifXTable                                     | статус, скорость, 64-битные счётчики байт/пакетов, ошибки, дискарды    |
| `system`     | RAISECOM-SYSTEM-MIB, FANMONITOR, iscomSwitch          | модель/серийник/прошивка, uptime, CPU, память, температура, вентиляторы |
| `sfp`        | RAISECOM-OPTICAL-TRANSCEIVER-MIB                      | инфо о модуле, DDM: температура, Tx/Rx dBm, bias, напряжение, пороги и статус алармов |

### Почему SNMP, а не CLI

Все нужные данные, включая ватты/вольты/амперы по каждому PoE-порту, есть в SNMP (приватная ветка Raisecom).
SNMP быстрее (≈3 с на полный опрос против десятков секунд логина и разбора `--More--`), не занимает VTY-сессии,
не засоряет журнал входами и не ломается при смене формата вывода CLI.

## Подготовка коммутатора

Включите на коммутаторе цифровую диагностику SFP (DDM). Без неё таблицы трансиверов пустые и экспортируется только
`raisecom_sfp_ddm_enabled 0`:

```
config
transceiver ddm enable
exit
write
```

`exit` выходит из режима конфигурации, `write` сохраняет конфигурацию, чтобы DDM остался включённым после перезагрузки.

Проверка по SNMP: `raisecomOpticalTransceiverDDMEnable` (`1.3.6.1.4.1.8886.1.18.1.2.0`) должен быть равен `1`.

```bash
snmpget -v2c -c public <switch> 1.3.6.1.4.1.8886.1.18.1.2.0
```

Всё остальное, что читает экспортер (PoE, интерфейсы, система), работает на заводской конфигурации.

## Быстрый старт

```bash
make build
./bin/raisecom_exporter --config.file=config.example.yml
curl 'http://localhost:9840/probe?target=192.0.2.10&auth=public_v2'
```

### Docker Compose: экспортер + Prometheus + Grafana

В `deploy/` лежит полный рабочий стек: опубликованный образ, Prometheus и Grafana с уже подключённым дашбордом
(он открывается как домашняя страница Grafana).

```bash
cd deploy
cp .env.example .env                                                        # по желанию
cp prometheus/targets/raisecom.yml.example prometheus/targets/raisecom.yml  # здесь список коммутаторов
docker compose up -d
```

Grafana http://localhost:3000, Prometheus http://localhost:9090, экспортер http://localhost:9840.
Изменения в `prometheus/targets/raisecom.yml` Prometheus подхватывает сам. Конфиг экспортера —
`deploy/raisecom/config.yml`.

Все переменные необязательны, значения по умолчанию заданы в `docker-compose.yml`:

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `RAISECOM_EXPORTER_TAG` | `latest` | тег образа `andrewpinkerton/raisecom-snmp-exporter`, например `1.0.0` |
| `EXPORTER_LOG_LEVEL` | `info` | уровень логов экспортера |
| `SNMP_COMMUNITY` | `public` | SNMPv2c community, подставляется в `raisecom/config.yml` |
| `SNMP_AUTH_PASSWORD`, `SNMP_PRIV_PASSWORD` | пусто | пароли SNMPv3 (блок v3 в `raisecom/config.yml` закомментирован) |
| `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD` | `admin` / `admin` | администратор Grafana; смените пароль, прежде чем открывать Grafana наружу |
| `EXPORTER_PORT`, `PROMETHEUS_PORT`, `GRAFANA_PORT` | `9840`, `9090`, `3000` | публикуемые порты |
| `PROMETHEUS_RETENTION` | `30d` | сколько Prometheus хранит данные |
| `TZ` | `UTC` | часовой пояс Grafana |

Собрать экспортер из исходников вместо скачивания образа:
`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

## Конфигурация

```yaml
auths:
  public_v2:
    version: 2
    community: public
  secure_v3:
    version: 3
    username: monitor
    security_level: authPriv        # noAuthNoPriv | authNoPriv | authPriv
    auth_protocol: SHA              # MD5 | SHA | SHA224 | SHA256 | SHA384 | SHA512
    auth_password: ${SNMP_AUTH_PASSWORD}
    priv_protocol: AES              # DES | AES | AES192 | AES256 | AES192C | AES256C
    priv_password: ${SNMP_PRIV_PASSWORD}

modules:
  default:                          # все коллекторы
    timeout: 5s                     # на один SNMP-запрос
    retries: 1
    max_repetitions: 25
  poe_fast:
    collectors: [poe]
    poe_class: false                # не спрашивать класс PoE (≈80 мс на каждый активный порт)
```

- `auth` в запросе можно не указывать, если в конфиге один auth; `module` по умолчанию `default`.
- Секреты подставляются из окружения только в форме `${VAR}` (одиночный `$` в пароле не трогается).
  Если переменная не задана, конфиг не загрузится.
- Перечитать конфиг: `SIGHUP` или `POST /-/reload`. Проверить без запуска: `--config.check`.

### Флаги

| Флаг                        | По умолчанию | Описание                                                                |
|-----------------------------|--------------|-------------------------------------------------------------------------|
| `--config.file`             | `config.yml` | файл конфигурации                                                       |
| `--web.listen-address`      | `:9840`      | адрес HTTP                                                              |
| `--web.config.file`         |              | TLS/basic-auth ([exporter-toolkit](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md)) |
| `--scrape.timeout-offset`   | `500ms`      | вычитается из `X-Prometheus-Scrape-Timeout-Seconds`                     |
| `--log.level`, `--log.format` | `info`, `logfmt` |                                                                   |

### Prometheus

```yaml
scrape_configs:
  - job_name: raisecom
    scrape_interval: 30s
    scrape_timeout: 15s
    metrics_path: /probe
    params:
      module: [default]
      auth: [public_v2]
    static_configs:
      - targets: [192.0.2.10]
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: raisecom-snmp-exporter:9840
```

Полный пример с file_sd: `deploy/prometheus/prometheus.yml`.

## Метрики

Все метрики порта несут метки `ifindex`, `port` (номер, `10`), `ifname` (`gigaethernet1/1/10`) и
`description` (описание порта из ROS).

### PoE

| Метрика | Тип | Описание |
|---|---|---|
| `raisecom_poe_port_admin_enabled` | gauge | PoE включён на порту |
| `raisecom_poe_port_delivering` | gauge | порт отдаёт питание (0/1) |
| `raisecom_poe_port_detection_status{status}` | gauge | state set: disabled, searching, delivering, fault, test, other_fault |
| `raisecom_poe_port_class` | gauge | класс PD 0–4 (только у порта с подключённым PD) |
| `raisecom_poe_port_priority{priority}` | gauge | state set: critical, high, low |
| `raisecom_poe_port_power_watts` | gauge | потребляемая мощность (сглаженная) |
| `raisecom_poe_port_power_instant_watts` | gauge | мгновенный замер мощности |
| `raisecom_poe_port_power_peak_watts` | gauge | пиковая мощность на порту |
| `raisecom_poe_port_power_limit_watts` | gauge | лимит мощности порта |
| `raisecom_poe_port_voltage_volts` | gauge | напряжение |
| `raisecom_poe_port_current_amperes` | gauge | ток |
| `raisecom_poe_port_overload_total` | counter | срабатывания защиты от перегрузки |
| `raisecom_poe_port_short_total` | counter | короткие замыкания |
| `raisecom_poe_port_power_denied_total` | counter | отказы в питании (не хватило бюджета) |
| `raisecom_poe_port_mps_absent_total` | counter | снятие питания из-за пропажи MPS (PD отключился) |
| `raisecom_poe_port_invalid_signature_total` | counter | невалидная сигнатура PD |
| `raisecom_poe_port_force_power` | gauge | force-power включён: питание подаётся без детекта PD (опасно для не-PoE устройств) |
| `raisecom_poe_port_alive_check` | gauge | включена проверка, что PD жив (`poe alive check`) |
| `raisecom_poe_port_alive_action{action}` | gauge | state set, что делать при сбое PD: none, reboot, reboot_alarm, alarm |
| `raisecom_poe_power_budget_watts` | gauge | бюджет PSE |
| `raisecom_poe_power_consumption_watts` | gauge | суммарное потребление, усреднённое коммутатором (точность мВт) |
| `raisecom_poe_power_peak_watts` | gauge | пиковое суммарное потребление |
| `raisecom_poe_power_usage_ratio` | gauge | потребление / бюджет |
| `raisecom_poe_usage_threshold_ratio` | gauge | порог аларма по загрузке PSE |
| `raisecom_poe_pse_oper_status{status}` | gauge | state set: on, off, faulty |
| `raisecom_poe_pse_legacy_detection` | gauge | включён детект legacy-PD (`poe legacy`) |
| `raisecom_poe_pse_temperature_protection` | gauge | включена защита PSE от перегрева (`poe temperature-protection`) |

### Интерфейсы

`raisecom_interface_oper_up`, `raisecom_interface_admin_up`, `raisecom_interface_speed_bytes`,
`raisecom_interface_last_change_timestamp_seconds`,
`raisecom_interface_{receive,transmit}_{bytes,unicast_packets,multicast_packets,broadcast_packets,errors,discards}_total`.

### Система

`raisecom_device_info{model,serial,hardware,software,bootrom,mac,sysname}`, `raisecom_uptime_seconds`,
`raisecom_cpu_utilization_ratio{period="1s|5s|1m|10m|2h"}`, `raisecom_memory_{total,available}_bytes`,
`raisecom_temperature_celsius`, `raisecom_temperature_{min,max}_celsius`,
`raisecom_temperature_threshold_celsius{type="low|high"}`, `raisecom_fan_speed_rpm{fan}`, `raisecom_fan_up{fan}`,
`raisecom_fan_speed_level{fan}`.

### SFP / DDM

| Метрика | Описание |
|---|---|
| `raisecom_sfp_ddm_enabled` | DDM включён глобально на коммутаторе |
| `raisecom_sfp_info{type,media,vendor,part_number,serial,wavelength_nm,distance_meters,ddm_supported}` | установленный модуль |
| `raisecom_sfp_temperature_celsius`, `raisecom_sfp_voltage_volts`, `raisecom_sfp_bias_current_amperes`, `raisecom_sfp_tx_power_dbm`, `raisecom_sfp_rx_power_dbm` | значения DDM (только для модулей с DDM) |
| `raisecom_sfp_threshold{param,level}` | пороги модуля: `level` = high_alarm, high_warning, low_warning, low_alarm; `param` — имя метрики выше |
| `raisecom_sfp_alarm_status{param}` | 0 unknown, 1 normal, 2 high alarm, 3 high warning, 4 low alarm, 5 low warning |

DDM включается на коммутаторе глобально (см. [Подготовка коммутатора](#подготовка-коммутатора)). Пока он выключен,
ROS отдаёт пустые таблицы трансиверов, поэтому экспортируется только `raisecom_sfp_ddm_enabled 0`.

### Служебные

`raisecom_probe_success`, `raisecom_probe_duration_seconds`, `raisecom_collector_success{collector}`,
`raisecom_collector_duration_seconds{collector}`. На `/metrics` экспортера:
`raisecom_exporter_probes_total{module,result}`, `raisecom_exporter_config_last_reload_successful`, `raisecom_exporter_build_info`.

## Карта OID и особенности ROS

| Данные | OID | Примечание |
|---|---|---|
| PoE порт (стандарт) | `1.3.6.1.2.1.105.1.1.1.<col>.1.<ifIndex>` | группа PSE = 1, порт = ifIndex |
| PoE PSE (стандарт) | `1.3.6.1.2.1.105.1.3.1.1.<col>.1` | потребление в целых Вт |
| PoE порт (Raisecom) | `1.3.6.1.4.1.8886.6.1.51.1.1.<col>.1.<ifIndex>` | 1 пик мВт, 2 мощность мВт, 3 мгновенная мВт, 4 мВ, 5 мА, 6 лимит мВт, 8 force-power, 10 alive check, 11 alive action |
| PoE PSE (Raisecom) | `1.3.6.1.4.1.8886.6.1.51.2.1.<col>.1` | 1 среднее потребление мВт, 2 пик мВт, 3 legacy detection, 5 temperature-protection, 8 бюджет Вт |
| Описание порта | `1.3.6.1.4.1.8886.6.1.20.1.1.1.2.<ifIndex>` | `ifAlias` на ROS пустой |
| Инфо об устройстве | `1.3.6.1.4.1.8886.6.1.1.1.{1,2,9,13,14,19}.0` | ПО, железо, MAC, bootrom, серийник, модель |
| CPU | `1.3.6.1.4.1.8886.1.1.1.5.1.4.1.2.<period>` | %, периоды 1s/5s/1m/10m/2h |
| Память | `1.3.6.1.4.1.8886.1.1.3.{1,2}.0` | total / available, байты |
| Температура | `1.3.6.1.4.1.8886.1.1.4.2.{1,2,3,5,6}.0` | °C |
| Вентиляторы | `1.3.6.1.4.1.8886.1.1.5.2.2.1.{2,3,4}.<fan>` | об/мин, состояние, уровень |
| SFP info | `1.3.6.1.4.1.8886.1.18.2.1.1.1.<col>.<ifIndex>` | |
| SFP DDM | `1.3.6.1.4.1.8886.1.18.2.2.1.1.<col>.<ifIndex>.<param>` | param: 1 темп. (м°C), 2 bias (мкА), 3 Tx, 4 Rx (dBm×1000), 5 напряжение (мВ) |

Особенности агента ROS, учтённые в коде:

- **OID приходят не по порядку** (например, в таблице процессов `8886.1.1.1.5.2`). Walk выполняется без
  проверки возрастания OID, а вместо неё стоит защита от зацикливания: лимит строк и детект повторов.
- **Скаляры RAISECOM-SYSTEM-MIB сдвинуты** относительно MIB-файла (память на `raisecomMemory.N.0`, CPU на
  `raisecomCpu.5`), используются фактические OID.
- **Длина волны SFP** отдаётся в нм, хотя по MIB должна быть в пм.
- **`pethPsePortPowerClassifications` отвечает медленно**: агент опрашивает PoE-контроллер, около 80 мс на порт,
  полный walk занимает около 2 с. Поэтому класс запрашивается одним GET только для портов в состоянии `delivering`.
- **Скорость опроса ограничена самим агентом**: около 0.1 с на запрос независимо от размера ответа. Полный probe
  занимает около 3.5 с (system 0.5, interfaces 1.0, poe 1.6, sfp 0.3), модуль `poe_fast` около 1 с.
  Рекомендуемый `scrape_timeout` не меньше 10 с.
- **Недоступный коммутатор:** после первого тайм-аута остальные коллекторы пропускаются, и probe укладывается
  в тайм-аут скрейпа.

Смысл колонок приватной PoE-таблицы Raisecom выведен из данных: публичного MIB нет. Измерения сверены с
`show poe interface` / `show poe pse` и стандартным MIB, настройки определены переключением через CLI
(подробно в [mibs/README.Ru.md](mibs/README.Ru.md)).

## Безопасность

- Используйте отдельный read-only community с ACL на адрес экспортера, а лучше SNMPv3 `authPriv`.
- Проверьте, что на коммутаторе не включён community с правом записи (например, `private`), и при
  необходимости отключите его: экспортеру нужно только чтение.
- Веб-интерфейс экспортера можно закрыть TLS и basic-auth через `--web.config.file`.

## Деплой

- **Docker:** образ [`andrewpinkerton/raisecom-snmp-exporter`](https://hub.docker.com/r/andrewpinkerton/raisecom-snmp-exporter):
  distroless около 15 МБ, работает от non-root, собирается для linux/amd64 и linux/arm64. Конфиг монтируется в
  `/etc/raisecom_exporter/config.yml`:

  ```bash
  docker run -d -p 9840:9840 -v $PWD/config.yml:/etc/raisecom_exporter/config.yml:ro \
    andrewpinkerton/raisecom-snmp-exporter:1.0.0
  ```

  Версии по SemVer (номер в файле `VERSION`). Каждый релиз публикуется одним тегом с точной версией, например `1.0.0`.
  - `make docker` собирает локальный образ;
  - `make docker-push` пушит его (только платформа хоста);
  - `make docker-release` собирает amd64+arm64 и сразу пушит.

  Для пуша нужен `docker login`.
- **systemd:** `deploy/systemd/raisecom-snmp-exporter.service` (DynamicUser, hardening). Инструкция по установке в
  комментарии внутри файла. Секреты класть в `/etc/default/raisecom_exporter` с правами 0600.
- **Grafana:** дашборд `grafana/raisecom.json` (переменные datasource и switch). Он генерируется скриптом
  `python3 grafana/gen_dashboard.py > grafana/raisecom.json`.

## Разработка

```bash
make test     # unit + golden-тесты на реальном дампе testdata/iscom2624g.walk
make lint     # gofmt + go vet
make golden   # перегенерировать internal/collector/testdata/*.prom после осознанного изменения вывода
```

Фикстуры снимаются так (только нужные ветки):

```bash
snmpbulkwalk -v2c -c public -On -Oe -Ot -Cc <switch> 1.3.6.1.4.1.8886.6.1.51 >> testdata/<model>.walk
```

## MIB

В `mibs/` лежат MIB для всех приватных объектов, которые читает экспортер, в двух вариантах:
- `RAISECOM-ISCOM2600G-MIB.mib`: один самодостаточный файл, импортирует только SNMPv2-SMI/TC/CONF;
- `mibs/modules/`: тот же набор объектов, разложенный по модулям, как обычно поставляют вендоры.

Оба варианта генерируются `mibs/gen_mibs.py` и проверяются `make mibcheck`. Подробности и отличия от вендорских
MIB описаны в [mibs/README.Ru.md](mibs/README.Ru.md). Сам экспортер MIB не загружает, OID зашиты в код.

## Лицензия

[MIT](LICENSE)
