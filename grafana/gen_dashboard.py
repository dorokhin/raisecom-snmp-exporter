#!/usr/bin/env python3
"""Generates grafana/raisecom.json:  python3 grafana/gen_dashboard.py > grafana/raisecom.json"""
import json, sys

DS = {"type": "prometheus", "uid": "${datasource}"}
I = 'instance="$instance"'
panels = []
_id = [0]


def nid():
    _id[0] += 1
    return _id[0]


def tgt(expr, legend="", ref="A", instant=False, fmt=None):
    t = {"datasource": DS, "expr": expr, "legendFormat": legend, "refId": ref}
    if instant:
        t["instant"] = True
        t["range"] = False
    if fmt:
        t["format"] = fmt
    return t


def thresholds(*steps):
    out = [{"color": steps[0], "value": None}]
    for i in range(1, len(steps), 2):
        out.append({"color": steps[i + 1], "value": steps[i]})
    return {"mode": "absolute", "steps": out}


def row(title, y, collapsed=False):
    panels.append({"type": "row", "title": title, "id": nid(), "collapsed": collapsed,
                   "gridPos": {"h": 1, "w": 24, "x": 0, "y": y}, "panels": []})


def panel(ptype, title, x, y, w, h, targets, unit=None, desc=None, defaults=None, options=None, overrides=None,
          transformations=None):
    fd = {"unit": unit} if unit else {}
    fd.update(defaults or {})
    p = {"type": ptype, "title": title, "id": nid(), "datasource": DS,
         "gridPos": {"h": h, "w": w, "x": x, "y": y}, "targets": targets,
         "fieldConfig": {"defaults": fd, "overrides": overrides or []}, "options": options or {}}
    if desc:
        p["description"] = desc
    if transformations:
        p["transformations"] = transformations
    panels.append(p)
    return p


def stat(title, x, y, w, h, targets, unit=None, th=None, mappings=None, desc=None, color_mode="value"):
    d = {"color": {"mode": "fixed", "fixedColor": "text"}}
    if th:
        d["thresholds"] = th
        d["color"] = {"mode": "thresholds"}
    if mappings:
        d["mappings"] = mappings
    return panel("stat", title, x, y, w, h, targets, unit, desc, d,
                 {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                  "colorMode": color_mode, "graphMode": "none", "textMode": "auto", "justifyMode": "auto"})


def ts(title, x, y, w, h, targets, unit=None, stack=False, desc=None, overrides=None, fill=10, minv=None):
    custom = {"drawStyle": "line", "lineWidth": 1, "fillOpacity": fill, "showPoints": "never",
              "spanNulls": True, "stacking": {"mode": "normal" if stack else "none", "group": "A"}}
    d = {"custom": custom}
    if minv is not None:
        d["min"] = minv
    return panel("timeseries", title, x, y, w, h, targets, unit, desc, d,
                 {"legend": {"displayMode": "table", "placement": "right", "calcs": ["lastNotNull", "max"]},
                  "tooltip": {"mode": "multi", "sort": "desc"}}, overrides)


def timeline(title, x, y, w, h, targets, mappings, desc=None):
    return panel("state-timeline", title, x, y, w, h, targets, None, desc,
                 {"mappings": mappings, "color": {"mode": "thresholds"},
                  "thresholds": thresholds("red", 1, "green"), "custom": {"fillOpacity": 80, "lineWidth": 0}},
                 {"showValue": "never", "mergeValues": True, "rowHeight": 0.9, "alignValue": "left",
                  "legend": {"showLegend": False}})


ONOFF = [{"type": "value", "options": {"0": {"text": "off", "color": "transparent", "index": 0},
                                       "1": {"text": "ON", "color": "green", "index": 1}}}]
UPDOWN = [{"type": "value", "options": {"0": {"text": "down", "color": "red", "index": 0},
                                        "1": {"text": "up", "color": "green", "index": 1}}}]

# Table panels join several instant queries with the "merge" transformation,
# which needs identical label sets: aggregate to the port labels only.
PORT_BY = "ifindex, port, ifname, description"
SFP_INFO_BY = PORT_BY + ", vendor, part_number, serial, wavelength_nm, distance_meters, media, type, ddm_supported"

y = 0
# ---------------------------------------------------------------- overview
row("Overview", y); y += 1
panel("table", "Device", 0, y, 9, 4,
      [tgt(f"raisecom_device_info{{{I}}}", ref="A", instant=True, fmt="table")],
      options={"showHeader": True, "cellHeight": "sm"},
      transformations=[{"id": "organize", "options": {
          "excludeByName": {"Time": True, "Value": True, "__name__": True, "job": True, "instance": True, "site": True},
          "indexByName": {"sysname": 0, "model": 1, "software": 2, "serial": 3, "mac": 4, "hardware": 5, "bootrom": 6}}}])
stat("Probe", 9, y, 3, 4, [tgt(f"raisecom_probe_success{{{I}}}")],
     th=thresholds("red", 1, "green"),
     mappings=[{"type": "value", "options": {"0": {"text": "FAIL"}, "1": {"text": "OK"}}}], color_mode="background")
stat("Uptime", 12, y, 3, 4, [tgt(f"raisecom_uptime_seconds{{{I}}}")], unit="s")
stat("CPU (1m)", 15, y, 3, 4, [tgt(f'raisecom_cpu_utilization_ratio{{{I},period="1m"}}')], unit="percentunit",
     th=thresholds("green", 0.7, "orange", 0.9, "red"))
stat("Memory used", 18, y, 3, 4,
     [tgt(f"1 - raisecom_memory_available_bytes{{{I}}} / raisecom_memory_total_bytes{{{I}}}")],
     unit="percentunit", th=thresholds("green", 0.85, "orange", 0.95, "red"))
stat("Temperature", 21, y, 3, 4, [tgt(f"raisecom_temperature_celsius{{{I}}}")], unit="celsius",
     th=thresholds("green", 60, "orange", 75, "red"))
y += 4

# ---------------------------------------------------------------- poe
row("PoE", y); y += 1
panel("gauge", "PoE budget used", 0, y, 4, 7, [tgt(f"raisecom_poe_power_usage_ratio{{{I}}}")], "percentunit",
      defaults={"min": 0, "max": 1, "thresholds": thresholds("green", 0.7, "orange", 0.9, "red"),
                "color": {"mode": "thresholds"}},
      options={"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
               "showThresholdMarkers": True})
stat("Delivered / budget", 4, y, 4, 7,
     [tgt(f"raisecom_poe_power_consumption_watts{{{I}}}", "Delivered", "A"),
      tgt(f"raisecom_poe_power_budget_watts{{{I}}}", "Budget", "B"),
      tgt(f"raisecom_poe_power_peak_watts{{{I}}}", "Peak", "C")], unit="watt")
stat("Powered devices", 8, y, 3, 7, [tgt(f"sum(raisecom_poe_port_delivering{{{I}}})")],
     desc="Ports currently delivering power.")
stat("Ports in fault", 11, y, 3, 7,
     [tgt(f'sum(raisecom_poe_port_detection_status{{{I},status=~"fault|other_fault"}}) or vector(0)')],
     th=thresholds("green", 1, "red"), color_mode="background")
stat("Force-power ports", 14, y, 3, 7,
     [tgt(f"sum(raisecom_poe_port_force_power{{{I}}}) or vector(0)")],
     th=thresholds("green", 1, "red"), color_mode="background",
     desc="Ports that apply power without PD detection. A non-PoE device plugged into such a port can be damaged.")
stat("PSE settings", 17, y, 3, 7,
     [tgt(f"raisecom_poe_pse_legacy_detection{{{I}}}", "legacy detection", "A"),
      tgt(f"raisecom_poe_pse_temperature_protection{{{I}}}", "temp. protection", "B")],
     mappings=[{"type": "value", "options": {"0": {"text": "off", "color": "orange"}, "1": {"text": "on", "color": "green"}}}],
     desc="Legacy PD detection and over-temperature protection of the PSE.")
stat("PoE events (24h)", 20, y, 4, 7,
     [tgt(f"sum(increase(raisecom_poe_port_overload_total{{{I}}}[24h]))", "overload", "A"),
      tgt(f"sum(increase(raisecom_poe_port_short_total{{{I}}}[24h]))", "short", "B"),
      tgt(f"sum(increase(raisecom_poe_port_power_denied_total{{{I}}}[24h]))", "denied", "C"),
      tgt(f"sum(increase(raisecom_poe_port_mps_absent_total{{{I}}}[24h]))", "PD lost (MPS)", "D")],
     th=thresholds("green", 1, "orange"), desc="Overload, short circuit, power denied and MPS-absent events.")
y += 7

tbl_queries = [
    ("A", "raisecom_poe_port_admin_enabled", "Enabled", None),
    ("B", "raisecom_poe_port_delivering", "Delivering", None),
    ("C", "raisecom_poe_port_class", "Class", None),
    ("D", "raisecom_poe_port_power_watts", "Power", "watt"),
    ("E", "raisecom_poe_port_power_instant_watts", "Instant", "watt"),
    ("F", "raisecom_poe_port_power_peak_watts", "Peak", "watt"),
    ("G", "raisecom_poe_port_power_limit_watts", "Limit", "watt"),
    ("H", "raisecom_poe_port_voltage_volts", "Voltage", "volt"),
    ("I", "raisecom_poe_port_current_amperes", "Current", "amp"),
    ("J", "raisecom_poe_port_overload_total", "Overloads", None),
    ("K", "raisecom_poe_port_short_total", "Shorts", None),
    ("L", "raisecom_poe_port_power_denied_total", "Denied", None),
    ("M", "raisecom_poe_port_force_power", "Force power", None),
    ("N", "raisecom_poe_port_alive_check", "Alive check", None),
]
overrides = [
    {"matcher": {"id": "byName", "options": "Enabled"},
     "properties": [{"id": "mappings", "value": ONOFF}, {"id": "custom.cellOptions", "value": {"type": "color-text"}}]},
    {"matcher": {"id": "byName", "options": "Delivering"},
     "properties": [{"id": "mappings", "value": ONOFF}, {"id": "custom.cellOptions", "value": {"type": "color-background"}}]},
    {"matcher": {"id": "byName", "options": "Power"},
     "properties": [{"id": "custom.cellOptions", "value": {"type": "gauge", "mode": "lcd"}}, {"id": "min", "value": 0},
                    {"id": "max", "value": 30}, {"id": "color", "value": {"mode": "continuous-GrYlRd"}},
                    {"id": "custom.width", "value": 220}]},
]
for _, _, name, unit in tbl_queries:
    if unit:
        overrides.append({"matcher": {"id": "byName", "options": name},
                          "properties": [{"id": "unit", "value": unit}, {"id": "decimals", "value": 2}]})
overrides += [
    {"matcher": {"id": "byName", "options": "Force power"},
     "properties": [{"id": "mappings", "value": [{"type": "value", "options": {
         "0": {"text": "off", "color": "transparent", "index": 0}, "1": {"text": "FORCED", "color": "red", "index": 1}}}]},
                    {"id": "custom.cellOptions", "value": {"type": "color-background"}}]},
    {"matcher": {"id": "byName", "options": "Alive check"},
     "properties": [{"id": "mappings", "value": ONOFF}, {"id": "custom.cellOptions", "value": {"type": "color-text"}}]},
    {"matcher": {"id": "byName", "options": "Alive action"},
     "properties": [{"id": "mappings", "value": [{"type": "value", "options": {
         "0": {"text": "none", "index": 0}, "1": {"text": "reboot", "index": 1},
         "2": {"text": "reboot+alarm", "index": 2}, "3": {"text": "alarm", "index": 3}}}]}]},
]
rename = {f"Value #{ref}": name for ref, _, name, _ in tbl_queries}
rename["Value #O"] = "Alive action"
panel("table", "PoE ports", 0, y, 24, 12,
      [tgt(f"max by ({PORT_BY}) ({m}{{{I}}})", ref=ref, instant=True, fmt="table") for ref, m, _, _ in tbl_queries]
      # alive_action is a state set; fold it into one numeric code per port for the table.
      + [tgt(" + ".join(f'max by ({PORT_BY}) (raisecom_poe_port_alive_action{{{I},action="{a}"}}) * {code}'
                        for code, a in [(1, "reboot"), (2, "reboot_alarm"), (3, "alarm")]),
             ref="O", instant=True, fmt="table")],
      defaults={"custom": {"align": "auto", "filterable": True}},
      options={"showHeader": True, "cellHeight": "sm", "sortBy": [{"displayName": "Port", "desc": False}]},
      overrides=overrides,
      transformations=[
          {"id": "merge", "options": {}},
          {"id": "convertFieldType", "options": {"conversions": [{"targetField": "port", "destinationType": "number"}]}},
          {"id": "sortBy", "options": {"sort": [{"field": "port"}]}},
          {"id": "organize", "options": {
              "excludeByName": {"Time": True, "instance": True, "job": True, "ifindex": True, "site": True},
              "indexByName": {"port": 0, "ifname": 1, "description": 2},
              "renameByName": {"port": "Port", "ifname": "Interface", "description": "Description", **rename}}}])
y += 12

ts("Power per port", 0, y, 16, 9,
   [tgt(f"raisecom_poe_port_power_watts{{{I}}} > 0", "{{port}} {{description}}")], "watt", stack=True, minv=0,
   desc="Stacked power drawn by each powered device.")
ts("Total vs budget", 16, y, 8, 9,
   [tgt(f"raisecom_poe_power_consumption_watts{{{I}}}", "delivered", "A"),
    tgt(f"raisecom_poe_power_budget_watts{{{I}}}", "budget", "B")], "watt", minv=0,
   overrides=[{"matcher": {"id": "byName", "options": "budget"},
               "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 10]}},
                              {"id": "custom.fillOpacity", "value": 0}, {"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]}])
y += 9
ts("Voltage", 0, y, 8, 8, [tgt(f"raisecom_poe_port_voltage_volts{{{I}}} > 0", "{{port}}")], "volt")
ts("Current", 8, y, 8, 8, [tgt(f"raisecom_poe_port_current_amperes{{{I}}} > 0", "{{port}}")], "amp", minv=0)
ts("PoE events", 16, y, 8, 8,
   [tgt(f"increase(raisecom_poe_port_overload_total{{{I}}}[$__rate_interval]) > 0", "{{port}} overload", "A"),
    tgt(f"increase(raisecom_poe_port_short_total{{{I}}}[$__rate_interval]) > 0", "{{port}} short", "B"),
    tgt(f"increase(raisecom_poe_port_power_denied_total{{{I}}}[$__rate_interval]) > 0", "{{port}} denied", "C"),
    tgt(f"increase(raisecom_poe_port_mps_absent_total{{{I}}}[$__rate_interval]) > 0", "{{port}} PD lost", "D")],
   None, minv=0)
y += 8
timeline("PoE delivering", 0, y, 24, 8, [tgt(f"raisecom_poe_port_delivering{{{I}}}", "{{port}} {{description}}")],
         ONOFF)
y += 8

# ---------------------------------------------------------------- interfaces
row("Interfaces", y); y += 1
up = f"and on(instance, ifindex) raisecom_interface_oper_up{{{I}}} == 1"
ts("Traffic (bits/s)", 0, y, 24, 9,
   [tgt(f"rate(raisecom_interface_receive_bytes_total{{{I}}}[$__rate_interval]) * 8 {up}", "{{port}} {{description}} in", "A"),
    tgt(f"rate(raisecom_interface_transmit_bytes_total{{{I}}}[$__rate_interval]) * 8 {up}", "{{port}} {{description}} out", "B")],
   "bps", desc="Ports that are up. Outbound traffic is drawn below zero.",
   overrides=[{"matcher": {"id": "byFrameRefID", "options": "B"},
               "properties": [{"id": "custom.transform", "value": "negative-Y"}]}])
y += 9
ts("Errors & discards (packets/s)", 0, y, 12, 8,
   [tgt(f"rate(raisecom_interface_receive_errors_total{{{I}}}[$__rate_interval]) > 0", "{{port}} rx errors", "A"),
    tgt(f"rate(raisecom_interface_transmit_errors_total{{{I}}}[$__rate_interval]) > 0", "{{port}} tx errors", "B"),
    tgt(f"rate(raisecom_interface_receive_discards_total{{{I}}}[$__rate_interval]) > 0", "{{port}} rx discards", "C"),
    tgt(f"rate(raisecom_interface_transmit_discards_total{{{I}}}[$__rate_interval]) > 0", "{{port}} tx discards", "D")],
   "pps", minv=0)
timeline("Link state", 12, y, 12, 8, [tgt(f"raisecom_interface_oper_up{{{I}}}", "{{port}} {{description}}")], UPDOWN)
y += 8

# ---------------------------------------------------------------- sfp
row("SFP / DDM", y); y += 1
ts("Rx power", 0, y, 12, 8,
   [tgt(f"raisecom_sfp_rx_power_dbm{{{I}}}", "{{port}} {{description}}", "A"),
    tgt(f'raisecom_sfp_threshold{{{I},param="rx_power_dbm",level="low_alarm"}}', "{{port}} low alarm", "B")],
   "dBm", desc="Dashed lines are the module's low-alarm thresholds.",
   overrides=[{"matcher": {"id": "byFrameRefID", "options": "B"},
               "properties": [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [10, 10]}},
                              {"id": "custom.fillOpacity", "value": 0}]}], fill=0)
ts("Tx power", 12, y, 12, 8, [tgt(f"raisecom_sfp_tx_power_dbm{{{I}}}", "{{port}} {{description}}")], "dBm", fill=0)
y += 8
sfp_q = [("A", f"max by ({SFP_INFO_BY}) (raisecom_sfp_info{{{I}}})", None),
         ("B", f"max by ({PORT_BY}) (raisecom_sfp_temperature_celsius{{{I}}})", "celsius"),
         ("C", f"max by ({PORT_BY}) (raisecom_sfp_rx_power_dbm{{{I}}})", "dBm"),
         ("D", f"max by ({PORT_BY}) (raisecom_sfp_tx_power_dbm{{{I}}})", "dBm"),
         ("E", f"max by ({PORT_BY}) (raisecom_sfp_bias_current_amperes{{{I}}})", "amp"),
         ("F", f"max by ({PORT_BY}) (raisecom_sfp_voltage_volts{{{I}}})", "volt"),
         ("G", f"max by ({PORT_BY}) (raisecom_sfp_alarm_status{{{I}}})", None)]
sfp_names = {"B": "Temperature", "C": "Rx", "D": "Tx", "E": "Bias", "F": "Voltage", "G": "Alarm"}
sfp_over = [{"matcher": {"id": "byName", "options": sfp_names[r]},
             "properties": [{"id": "unit", "value": u}, {"id": "decimals", "value": 2}]}
            for r, _, u in sfp_q if u]
sfp_over.append({"matcher": {"id": "byName", "options": "Alarm"}, "properties": [
    {"id": "mappings", "value": [{"type": "value", "options": {
        "0": {"text": "unknown"}, "1": {"text": "normal", "color": "green"}, "2": {"text": "high alarm", "color": "red"},
        "3": {"text": "high warning", "color": "orange"}, "4": {"text": "low alarm", "color": "red"},
        "5": {"text": "low warning", "color": "orange"}}}]},
    {"id": "custom.cellOptions", "value": {"type": "color-background"}}]})
panel("table", "Transceivers", 0, y, 24, 6,
      [tgt(m, ref=r, instant=True, fmt="table") for r, m, _ in sfp_q],
      options={"showHeader": True, "cellHeight": "sm"}, overrides=sfp_over,
      transformations=[
          {"id": "merge", "options": {}},
          {"id": "filterByValue", "options": {"filters": [{"fieldName": "vendor", "config": {"id": "isNotNull"}}],
                                              "type": "include", "match": "any"}},
          {"id": "convertFieldType", "options": {"conversions": [{"targetField": "port", "destinationType": "number"}]}},
          {"id": "sortBy", "options": {"sort": [{"field": "port"}]}},
          {"id": "organize", "options": {
              "excludeByName": {"Time": True, "instance": True, "job": True, "ifindex": True, "Value #A": True,
                                "site": True, "__name__": True},
              "indexByName": {"port": 0, "ifname": 1, "description": 2, "vendor": 3, "part_number": 4},
              "renameByName": {"port": "Port", "ifname": "Interface", "description": "Description",
                               "vendor": "Vendor", "part_number": "Part number", "serial": "Serial",
                               "wavelength_nm": "λ, nm", "distance_meters": "Distance, m", "media": "Media",
                               "type": "Type", "ddm_supported": "DDM",
                               **{f"Value #{r}": n for r, n in sfp_names.items()}}}}])
y += 6

# ---------------------------------------------------------------- system
row("System", y); y += 1
ts("CPU", 0, y, 6, 7, [tgt(f'raisecom_cpu_utilization_ratio{{{I},period=~"1m|10m"}}', "{{period}}")],
   "percentunit", minv=0)
ts("Memory", 6, y, 6, 7,
   [tgt(f"raisecom_memory_total_bytes{{{I}}} - raisecom_memory_available_bytes{{{I}}}", "used", "A"),
    tgt(f"raisecom_memory_total_bytes{{{I}}}", "total", "B")], "bytes", minv=0)
ts("Temperature", 12, y, 6, 7,
   [tgt(f"raisecom_temperature_celsius{{{I}}}", "board", "A"),
    tgt(f'raisecom_temperature_threshold_celsius{{{I},type="high"}}', "high threshold", "B")], "celsius")
ts("Fans", 18, y, 6, 7, [tgt(f"raisecom_fan_speed_rpm{{{I}}}", "fan {{fan}}")], "rotrpm", minv=0)
y += 7
ts("Exporter: collector duration", 0, y, 12, 7,
   [tgt(f"raisecom_collector_duration_seconds{{{I}}}", "{{collector}}", "A"),
    tgt(f"raisecom_probe_duration_seconds{{{I}}}", "probe total", "B")], "s", minv=0)
timeline("Exporter: collector success", 12, y, 12, 7,
         [tgt(f"raisecom_collector_success{{{I}}}", "{{collector}}")],
         [{"type": "value", "options": {"0": {"text": "fail", "color": "red"}, "1": {"text": "ok", "color": "green"}}}])

dash = {
    "title": "Raisecom switch (PoE)",
    "uid": "raisecom-poe",
    "tags": ["raisecom", "poe", "network"],
    "timezone": "browser",
    "schemaVersion": 39,
    "version": 1,
    "editable": True,
    "graphTooltip": 1,
    "refresh": "30s",
    "time": {"from": "now-6h", "to": "now"},
    "templating": {"list": [
        {"name": "datasource", "type": "datasource", "query": "prometheus", "label": "Data source",
         "current": {}, "hide": 0, "refresh": 1},
        {"name": "instance", "type": "query", "label": "Switch", "datasource": DS,
         "query": {"query": "label_values(raisecom_device_info, instance)", "refId": "q"},
         "definition": "label_values(raisecom_device_info, instance)", "refresh": 2, "sort": 1,
         "current": {}, "hide": 0, "includeAll": False, "multi": False},
    ]},
    "annotations": {"list": []},
    "panels": panels,
}
json.dump(dash, sys.stdout, indent=2, ensure_ascii=False)
sys.stdout.write("\n")
