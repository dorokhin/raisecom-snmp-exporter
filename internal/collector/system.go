package collector

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"raisecom-snmp-exporter/internal/snmp"
)

// OIDs below were verified on ISCOM2624G-4GE-PWR-AC, software 3.60.311 (ROS 5.2.1). On this
// firmware the RAISECOM-SYSTEM-MIB scalars sit one level higher than the MIB
// file says (e.g. memory at raisecomMemory.N.0, not raisecomMemoryScalarGroup.N.0).
const (
	oidSysName = "1.3.6.1.2.1.1.5.0"

	// iscomSwitch system info, 1.3.6.1.4.1.8886.6.1.1.1.N.0
	oidRcSoftwareVersion = "1.3.6.1.4.1.8886.6.1.1.1.1.0"
	oidRcHardwareVersion = "1.3.6.1.4.1.8886.6.1.1.1.2.0"
	oidRcMAC             = "1.3.6.1.4.1.8886.6.1.1.1.9.0"
	oidRcBootromVersion  = "1.3.6.1.4.1.8886.6.1.1.1.13.0"
	oidRcSerial          = "1.3.6.1.4.1.8886.6.1.1.1.14.0"
	oidRcModel           = "1.3.6.1.4.1.8886.6.1.1.1.19.0"

	// raisecomMemory
	oidRcMemTotal     = "1.3.6.1.4.1.8886.1.1.3.1.0"
	oidRcMemAvailable = "1.3.6.1.4.1.8886.1.1.3.2.0"

	// raisecomTemperature
	oidRcTempValue    = "1.3.6.1.4.1.8886.1.1.4.2.1.0"
	oidRcTempMin      = "1.3.6.1.4.1.8886.1.1.4.2.2.0"
	oidRcTempMax      = "1.3.6.1.4.1.8886.1.1.4.2.3.0"
	oidRcTempThreshLo = "1.3.6.1.4.1.8886.1.1.4.2.5.0"
	oidRcTempThreshHi = "1.3.6.1.4.1.8886.1.1.4.2.6.0"

	// Total CPU utilization (%) indexed by raisecomTotalCPUPeriod.
	oidRcTotalCPU = "1.3.6.1.4.1.8886.1.1.1.5.1.4.1.2"

	// raisecomFanMonitorStateEntry: 2 speed (rpm), 3 work state, 4 speed level.
	oidRcFanEntry = "1.3.6.1.4.1.8886.1.1.5.2.2.1"
)

var cpuPeriods = map[int64]string{1: "1s", 2: "5s", 3: "1m", 4: "10m", 5: "2h"}

var (
	sysInfoDesc = desc("", "device_info", "Device identification; value is always 1.",
		"model", "serial", "hardware", "software", "bootrom", "mac", "sysname")
	sysUptimeDesc   = desc("", "uptime_seconds", "Time since the agent (re)started (sysUpTime).")
	sysCPUDesc      = desc("cpu", "utilization_ratio", "Total CPU utilization averaged over period.", "period")
	sysMemTotalDesc = desc("memory", "total_bytes", "Total RAM.")
	sysMemAvailDesc = desc("memory", "available_bytes", "Available RAM.")
	sysTempDesc     = desc("", "temperature_celsius", "Board temperature.")
	sysTempMinDesc  = desc("", "temperature_min_celsius", "Lowest board temperature since boot.")
	sysTempMaxDesc  = desc("", "temperature_max_celsius", "Highest board temperature since boot.")
	sysTempThrDesc  = desc("", "temperature_threshold_celsius", "Configured temperature alarm thresholds.", "type")
	fanSpeedDesc    = desc("fan", "speed_rpm", "Fan speed.", "fan")
	fanUpDesc       = desc("fan", "up", "1 when the fan reports normal operation.", "fan")
	fanLevelDesc    = desc("fan", "speed_level", "Current fan speed level selected by temperature.", "fan")
)

type systemCollector struct{}

func (systemCollector) Collect(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error {
	res, err := t.Client.Get(ctx,
		oidSysUpTime, oidSysName,
		oidRcSoftwareVersion, oidRcHardwareVersion, oidRcMAC, oidRcBootromVersion, oidRcSerial, oidRcModel,
		oidRcMemTotal, oidRcMemAvailable,
		oidRcTempValue, oidRcTempMin, oidRcTempMax, oidRcTempThreshLo, oidRcTempThreshHi,
	)
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return fmt.Errorf("agent returned none of the system objects")
	}
	str := func(oid string) string { return res[oid].String() }
	num := func(oid string, d *prometheus.Desc, labels ...string) {
		if p, ok := res[oid]; ok {
			if v, ok := p.Float(); ok {
				gauge(ch, d, v, labels...)
			}
		}
	}

	gauge(ch, sysInfoDesc, 1,
		str(oidRcModel), str(oidRcSerial), str(oidRcHardwareVersion), str(oidRcSoftwareVersion),
		str(oidRcBootromVersion), formatMAC(res[oidRcMAC].Bytes()), str(oidSysName))
	if p, ok := res[oidSysUpTime]; ok {
		v, _ := p.Float()
		v /= 100
		t.uptime = &v
		gauge(ch, sysUptimeDesc, v)
	}
	num(oidRcMemTotal, sysMemTotalDesc)
	num(oidRcMemAvailable, sysMemAvailDesc)
	num(oidRcTempValue, sysTempDesc)
	num(oidRcTempMin, sysTempMinDesc)
	num(oidRcTempMax, sysTempMaxDesc)
	num(oidRcTempThreshLo, sysTempThrDesc, "low")
	num(oidRcTempThreshHi, sysTempThrDesc, "high")

	cpu, err := t.Client.Walk(ctx, oidRcTotalCPU)
	if err != nil {
		return err
	}
	for _, p := range cpu {
		idx, _ := snmp.LastIndex(p.OID)
		period, ok := cpuPeriods[idx]
		if !ok {
			continue
		}
		v, _ := p.Float()
		gauge(ch, sysCPUDesc, v/100, period)
	}

	fans, err := snmp.WalkColumns(ctx, t.Client, oidRcFanEntry, 2, 3, 4)
	if err != nil {
		return err
	}
	for _, idx := range fans.Indexes() {
		row := fans[idx]
		if v, ok := cellFloat(row, 2, 1); ok {
			gauge(ch, fanSpeedDesc, v, idx)
		}
		if v, ok := cellInt(row, 3); ok {
			gauge(ch, fanUpDesc, boolFloat(v == 1), idx)
		}
		if v, ok := cellFloat(row, 4, 1); ok {
			gauge(ch, fanLevelDesc, v, idx)
		}
	}
	return nil
}

func formatMAC(b []byte) string {
	if len(b) != 6 {
		return ""
	}
	return strings.ToLower(net.HardwareAddr(b).String())
}
