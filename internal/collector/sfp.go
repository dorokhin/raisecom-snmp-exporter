package collector

import (
	"context"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"raisecom-snmp-exporter/internal/snmp"
)

// RAISECOM-OPTICAL-TRANSCEIVER-MIB (raisecomAgent 18).
const (
	oidRcOptDDMEnable = "1.3.6.1.4.1.8886.1.18.1.2.0"
	oidRcOptInfoEntry = "1.3.6.1.4.1.8886.1.18.2.1.1.1"
	// DDM rows are indexed ifIndex.parameterType.
	oidRcOptDDMEntry = "1.3.6.1.4.1.8886.1.18.2.2.1.1"
)

// raisecomOpticalTransceiverInfoEntry columns
const (
	optType       = 1
	optVendor     = 3
	optPartNumber = 4
	optSerial     = 5
	optMedia      = 6
	optDistance   = 7 // metres
	optDDM        = 9 // support(1) / notsupport(2)
	optWavelength = 16
)

// raisecomOpticalTransceiverDDMEntry columns
const (
	ddmValue       = 2
	ddmHighAlarm   = 3
	ddmHighWarning = 4
	ddmLowAlarm    = 5
	ddmLowWarning  = 6
	ddmAlarmStatus = 7
)

var optTypeNames = map[int64]string{
	1: "unknown", 2: "gbic", 3: "soldered", 4: "sfp", 5: "xbi", 6: "xenpak", 7: "xfp",
	8: "xff", 9: "xfp-e", 10: "xpak", 11: "x2", 12: "sfpj",
}

var optMediaNames = map[int64]string{
	1: "single-mode", 2: "multi-mode-e50", 3: "multi-mode-50", 4: "multi-mode-62.5",
	5: "copper", 6: "single-mode-km", 7: "multi-mode-om3",
}

// ddmParam maps OpticalParameterType to a metric and the factor that converts
// the raw value to base units. ROS reports thousandths: m°C, mV, µA, and dBm*1000.
type ddmParam struct {
	name  string
	scale float64
	desc  *prometheus.Desc
}

var ddmParams = map[int64]ddmParam{
	1: {"temperature_celsius", 0.001, desc("sfp", "temperature_celsius", "Transceiver temperature.", portLabels...)},
	2: {"bias_current_amperes", 0.000001, desc("sfp", "bias_current_amperes", "Laser bias current.", portLabels...)},
	3: {"tx_power_dbm", 0.001, desc("sfp", "tx_power_dbm", "Transmit optical power.", portLabels...)},
	4: {"rx_power_dbm", 0.001, desc("sfp", "rx_power_dbm", "Receive optical power.", portLabels...)},
	5: {"voltage_volts", 0.001, desc("sfp", "voltage_volts", "Supply voltage.", portLabels...)},
}

var ddmLevels = []struct {
	col  int
	name string
}{
	{ddmHighAlarm, "high_alarm"},
	{ddmHighWarning, "high_warning"},
	{ddmLowWarning, "low_warning"},
	{ddmLowAlarm, "low_alarm"},
}

var (
	sfpDDMEnabledDesc = desc("sfp", "ddm_enabled", "1 when digital diagnostics monitoring is enabled globally on the switch.")
	sfpInfoDesc       = desc("sfp", "info", "Installed transceiver; value is always 1.",
		append(portLabels, "type", "media", "vendor", "part_number", "serial", "wavelength_nm", "distance_meters", "ddm_supported")...)
	sfpThresholdDesc = desc("sfp", "threshold", "DDM alarm/warning threshold, in the unit of the metric named by param.",
		append(portLabels, "param", "level")...)
	sfpAlarmDesc = desc("sfp", "alarm_status",
		"DDM alarm status: 0 unknown, 1 normal, 2 high alarm, 3 high warning, 4 low alarm, 5 low warning.",
		append(portLabels, "param")...)
)

type sfpCollector struct{}

func (sfpCollector) Collect(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error {
	ports, err := t.Ports(ctx)
	if err != nil {
		return err
	}
	res, err := t.Client.Get(ctx, oidRcOptDDMEnable)
	if err != nil {
		return err
	}
	ddmEnabled := false
	if p, ok := res[oidRcOptDDMEnable]; ok {
		v, _ := p.Int64()
		ddmEnabled = v == 1
		gauge(ch, sfpDDMEnabledDesc, boolFloat(ddmEnabled))
	}

	info, err := snmp.WalkColumns(ctx, t.Client, oidRcOptInfoEntry,
		optType, optVendor, optPartNumber, optSerial, optMedia, optDistance, optDDM, optWavelength)
	if err != nil {
		return err
	}
	ddmCapable := map[int64]bool{}
	for _, idx := range info.Indexes() {
		ifIndex, ok := snmp.LastIndex(idx)
		if !ok {
			continue
		}
		row := info[idx]
		typ, _ := cellInt(row, optType)
		media, _ := cellInt(row, optMedia)
		ddm, _ := cellInt(row, optDDM)
		dist, _ := cellInt(row, optDistance)
		// The MIB documents picometres, but ROS 5.x returns whole nanometres.
		wl, _ := cellInt(row, optWavelength)
		ddmCapable[ifIndex] = ddm == 1
		gauge(ch, sfpInfoDesc, 1, append(ports.Get(ifIndex).Labels(),
			optTypeNames[typ], optMediaNames[media],
			row[optVendor].String(), row[optPartNumber].String(), row[optSerial].String(),
			itoaNonZero(wl), itoaNonZero(dist), strconv.FormatBool(ddm == 1))...)
	}

	if !ddmEnabled {
		return nil
	}
	ddm, err := snmp.WalkColumns(ctx, t.Client, oidRcOptDDMEntry,
		ddmValue, ddmHighAlarm, ddmHighWarning, ddmLowAlarm, ddmLowWarning, ddmAlarmStatus)
	if err != nil {
		return err
	}
	for _, idx := range ddm.Indexes() {
		ifIndex, param, ok := splitDDMIndex(idx)
		if !ok || !ddmCapable[ifIndex] {
			// Without DDM support the agent fills every cell with zeros.
			continue
		}
		pd, ok := ddmParams[param]
		if !ok {
			continue
		}
		row := ddm[idx]
		labels := ports.Get(ifIndex).Labels()
		if v, ok := cellFloat(row, ddmValue, pd.scale); ok {
			gauge(ch, pd.desc, v, labels...)
		}
		for _, l := range ddmLevels {
			if v, ok := cellFloat(row, l.col, pd.scale); ok {
				gauge(ch, sfpThresholdDesc, v, append(labels, pd.name, l.name)...)
			}
		}
		if v, ok := cellFloat(row, ddmAlarmStatus, 1); ok {
			gauge(ch, sfpAlarmDesc, v, append(labels, pd.name)...)
		}
	}
	return nil
}

// splitDDMIndex parses "<ifIndex>.<parameterType>".
func splitDDMIndex(idx string) (ifIndex, param int64, ok bool) {
	i := strings.LastIndexByte(idx, '.')
	if i < 0 {
		return 0, 0, false
	}
	a, err1 := strconv.ParseInt(idx[:i], 10, 64)
	b, err2 := strconv.ParseInt(idx[i+1:], 10, 64)
	return a, b, err1 == nil && err2 == nil
}

func itoaNonZero(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}
