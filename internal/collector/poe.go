package collector

import (
	"context"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"raisecom-snmp-exporter/internal/snmp"
)

// Standard POWER-ETHERNET-MIB (RFC 3621). Port rows are indexed by
// pethPsePortGroupIndex.pethPsePortIndex; ROS uses group 1 and ifIndex as port index.
const (
	oidPethPsePortEntry = "1.3.6.1.2.1.105.1.1.1"
	oidPethMainPseEntry = "1.3.6.1.2.1.105.1.3.1.1"
)

const (
	pethAdminEnable         = 3
	pethDetectionStatus     = 6
	pethPowerPriority       = 7
	pethMPSAbsentCounter    = 8
	pethPowerClassification = 10
	pethInvalidSignature    = 11
	pethPowerDenied         = 12
	pethOverLoad            = 13
	pethShort               = 14

	pethMainPower       = 2
	pethMainOperStatus  = 3
	pethMainConsumption = 4
	pethMainThreshold   = 5
)

// Raisecom private PoE tables (no public MIB). Column meanings were derived by
// polling ISCOM2624G-4GE-PWR-AC (ROS 3.60.311) with live PDs and comparing with
// the standard MIB. Port rows are indexed by 1.<ifIndex>.
const (
	oidRcPoePortEntry = "1.3.6.1.4.1.8886.6.1.51.1.1"
	oidRcPoePseEntry  = "1.3.6.1.4.1.8886.6.1.51.2.1"
)

const (
	rcPoePortPeakPower    = 1 // mW, highest power seen on the port
	rcPoePortPower        = 2 // mW, consumption (smoothed)
	rcPoePortPowerInstant = 3 // mW, latest sample
	rcPoePortVoltage      = 4 // mV
	rcPoePortCurrent      = 5 // mA
	rcPoePortPowerLimit   = 6 // mW, maximum power allowed on the port
	// Settings, decoded by toggling them via CLI and diffing SNMP (see mibs/README.md).
	rcPoePortForcePower  = 8  // enable(1)/disable(2), CLI "poe force-power"
	rcPoePortAliveCheck  = 10 // enable(1)/disable(2), CLI "poe alive check"
	rcPoePortAliveAction = 11 // none(0), reboot(1), reboot-alarm(2), alarm(3), CLI "poe alive action"

	rcPoePseConsumption           = 1 // mW, averaged by the switch ("Average Power" in show poe pse)
	rcPoePsePeakPower             = 2 // mW
	rcPoePseLegacyDetection       = 3 // enable(1)/disable(2), CLI "poe legacy"
	rcPoePseTemperatureProtection = 5 // enable(1)/disable(2), CLI "poe temperature-protection"
	rcPoePseBudget                = 8 // W
)

// rcEnabled is the ROS EnableVar value for "enable"; "disable" is 2.
const rcEnabled = 1

var poeDetectionStates = map[int64]string{
	1: "disabled", 2: "searching", 3: "delivering", 4: "fault", 5: "test", 6: "other_fault",
}

var poePriorities = map[int64]string{1: "critical", 2: "high", 3: "low"}

var poePseOperStates = map[int64]string{1: "on", 2: "off", 3: "faulty"}

var poeAliveActions = map[int64]string{0: "none", 1: "reboot", 2: "reboot_alarm", 3: "alarm"}

var (
	poeAdminDesc     = desc("poe_port", "admin_enabled", "1 when PoE is administratively enabled on the port.", portLabels...)
	poeDetectDesc    = desc("poe_port", "detection_status", "PSE detection state (pethPsePortDetectionStatus) as a state set.", append(portLabels, "status")...)
	poeDeliverDesc   = desc("poe_port", "delivering", "1 when the port is delivering power.", portLabels...)
	poeClassDesc     = desc("poe_port", "class", "Classification of the powered device (0-4); only present while a PD is classified.", portLabels...)
	poePriorityDesc  = desc("poe_port", "priority", "Power priority as a state set.", append(portLabels, "priority")...)
	poePowerDesc     = desc("poe_port", "power_watts", "Power drawn by the powered device.", portLabels...)
	poeInstantDesc   = desc("poe_port", "power_instant_watts", "Latest instantaneous power sample.", portLabels...)
	poePeakDesc      = desc("poe_port", "power_peak_watts", "Highest power drawn on the port.", portLabels...)
	poeLimitDesc     = desc("poe_port", "power_limit_watts", "Maximum power the port may deliver.", portLabels...)
	poeVoltageDesc   = desc("poe_port", "voltage_volts", "Output voltage.", portLabels...)
	poeCurrentDesc   = desc("poe_port", "current_amperes", "Output current.", portLabels...)
	poeMPSAbsentDesc = desc("poe_port", "mps_absent_total", "Times power was removed because the Maintain Power Signature was lost.", portLabels...)
	poeInvalidDesc   = desc("poe_port", "invalid_signature_total", "Times an invalid PD signature was detected.", portLabels...)
	poeDeniedDesc    = desc("poe_port", "power_denied_total", "Times power was denied (budget exhausted).", portLabels...)
	poeOverloadDesc  = desc("poe_port", "overload_total", "Times the PD drew more than allowed (overload).", portLabels...)
	poeShortDesc     = desc("poe_port", "short_total", "Times a short circuit was detected.", portLabels...)
	poeForceDesc     = desc("poe_port", "force_power", "1 when force-power is enabled: power is applied without PD detection.", portLabels...)
	poeAliveDesc     = desc("poe_port", "alive_check", "1 when the PD alive check is enabled.", portLabels...)
	poeAliveActDesc  = desc("poe_port", "alive_action", "Action taken when the PD alive check fails, as a state set.", append(portLabels, "action")...)

	poeBudgetDesc      = desc("poe", "power_budget_watts", "Total PoE power budget of the PSE.")
	poeConsumptionDesc = desc("poe", "power_consumption_watts", "Total PoE power delivered, averaged by the switch.")
	poePsePeakDesc     = desc("poe", "power_peak_watts", "Highest total PoE power delivered.")
	poeUsageDesc       = desc("poe", "power_usage_ratio", "Delivered power divided by the budget.")
	poeThresholdDesc   = desc("poe", "usage_threshold_ratio", "Usage ratio at which the PSE raises an alarm.")
	poePseOperDesc     = desc("poe", "pse_oper_status", "PSE operational state as a state set.", "status")
	poeLegacyDesc      = desc("poe", "pse_legacy_detection", "1 when detection of legacy (pre-802.3af) powered devices is enabled.")
	poeTempProtDesc    = desc("poe", "pse_temperature_protection", "1 when PSE over-temperature protection is enabled.")
)

type poeCollector struct{}

func (poeCollector) Collect(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error {
	ports, err := t.Ports(ctx)
	if err != nil {
		return err
	}

	std, err := snmp.WalkColumns(ctx, t.Client, oidPethPsePortEntry,
		pethAdminEnable, pethDetectionStatus, pethPowerPriority, pethMPSAbsentCounter,
		pethInvalidSignature, pethPowerDenied, pethOverLoad, pethShort)
	if err != nil {
		return err
	}
	if t.Module.WantPoEClass() {
		if err := fetchPoEClass(ctx, t.Client, std); err != nil {
			return err
		}
	}
	rc, err := snmp.WalkColumns(ctx, t.Client, oidRcPoePortEntry,
		rcPoePortPeakPower, rcPoePortPower, rcPoePortPowerInstant, rcPoePortVoltage, rcPoePortCurrent, rcPoePortPowerLimit,
		rcPoePortForcePower, rcPoePortAliveCheck, rcPoePortAliveAction)
	if err != nil {
		return err
	}
	if len(std) == 0 && len(rc) == 0 {
		return fmt.Errorf("device exposes no PoE tables")
	}

	// Both tables are indexed 1.<ifIndex>; walk the union so a port present in
	// only one of them is still reported.
	seen := map[string]bool{}
	for _, idx := range append(std.Indexes(), rc.Indexes()...) {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		ifIndex, ok := snmp.LastIndex(idx)
		if !ok {
			continue
		}
		labels := ports.Get(ifIndex).Labels()

		if row := std[idx]; row != nil {
			if v, ok := cellInt(row, pethAdminEnable); ok {
				gauge(ch, poeAdminDesc, boolFloat(v == 1), labels...)
			}
			if v, ok := cellInt(row, pethDetectionStatus); ok {
				stateSet(ch, poeDetectDesc, poeDetectionStates, v, labels...)
				gauge(ch, poeDeliverDesc, boolFloat(v == 3), labels...)
			}
			// pethPsePortPowerClassifications: class0(1) .. class4(5); ROS reports 0 when no PD.
			if v, ok := cellInt(row, pethPowerClassification); ok && v >= 1 && v <= 5 {
				gauge(ch, poeClassDesc, float64(v-1), labels...)
			}
			if v, ok := cellInt(row, pethPowerPriority); ok {
				stateSet(ch, poePriorityDesc, poePriorities, v, labels...)
			}
			for col, d := range map[int]*prometheus.Desc{
				pethMPSAbsentCounter: poeMPSAbsentDesc,
				pethInvalidSignature: poeInvalidDesc,
				pethPowerDenied:      poeDeniedDesc,
				pethOverLoad:         poeOverloadDesc,
				pethShort:            poeShortDesc,
			} {
				if v, ok := cellFloat(row, col, 1); ok {
					counter(ch, d, v, labels...)
				}
			}
		}

		if row := rc[idx]; row != nil {
			for col, d := range map[int]*prometheus.Desc{
				rcPoePortPower:        poePowerDesc,
				rcPoePortPowerInstant: poeInstantDesc,
				rcPoePortPeakPower:    poePeakDesc,
				rcPoePortPowerLimit:   poeLimitDesc,
				rcPoePortVoltage:      poeVoltageDesc,
				rcPoePortCurrent:      poeCurrentDesc,
			} {
				if v, ok := cellFloat(row, col, 0.001); ok {
					gauge(ch, d, v, labels...)
				}
			}
			if v, ok := cellInt(row, rcPoePortForcePower); ok {
				gauge(ch, poeForceDesc, boolFloat(v == rcEnabled), labels...)
			}
			if v, ok := cellInt(row, rcPoePortAliveCheck); ok {
				gauge(ch, poeAliveDesc, boolFloat(v == rcEnabled), labels...)
			}
			if v, ok := cellInt(row, rcPoePortAliveAction); ok {
				stateSet(ch, poeAliveActDesc, poeAliveActions, v, labels...)
			}
		}
	}

	return collectPSE(ctx, t, ch)
}

// fetchPoEClass adds the class column for ports that deliver power. ROS answers
// pethPsePortPowerClassifications by querying the PoE controller (~80 ms per
// port, ~2 s for a full walk) and reports 0 for ports without a PD, so a single
// GET for the active ports is much cheaper.
func fetchPoEClass(ctx context.Context, c snmp.Client, std snmp.Table) error {
	var oids []string
	for idx, row := range std {
		if v, ok := cellInt(row, pethDetectionStatus); ok && v == 3 {
			oids = append(oids, fmt.Sprintf("%s.%d.%s", oidPethPsePortEntry, pethPowerClassification, idx))
		}
	}
	if len(oids) == 0 {
		return nil
	}
	res, err := c.Get(ctx, oids...)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("%s.%d.", oidPethPsePortEntry, pethPowerClassification)
	for oid, p := range res {
		if idx, ok := strings.CutPrefix(oid, prefix); ok && std[idx] != nil {
			std[idx][pethPowerClassification] = p
		}
	}
	return nil
}

// collectPSE reports the whole-switch budget. ROS has a single PSE (group 1).
func collectPSE(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error {
	std, err := snmp.WalkColumns(ctx, t.Client, oidPethMainPseEntry,
		pethMainPower, pethMainOperStatus, pethMainConsumption, pethMainThreshold)
	if err != nil {
		return err
	}
	rc, err := snmp.WalkColumns(ctx, t.Client, oidRcPoePseEntry,
		rcPoePseConsumption, rcPoePsePeakPower, rcPoePseLegacyDetection, rcPoePseTemperatureProtection, rcPoePseBudget)
	if err != nil {
		return err
	}
	srow, rrow := std["1"], rc["1"]

	budget, hasBudget := cellFloat(srow, pethMainPower, 1)
	if !hasBudget {
		budget, hasBudget = cellFloat(rrow, rcPoePseBudget, 1)
	}
	// The private table reports a mW average; the standard one an instantaneous value in whole watts.
	used, hasUsed := cellFloat(rrow, rcPoePseConsumption, 0.001)
	if !hasUsed {
		used, hasUsed = cellFloat(srow, pethMainConsumption, 1)
	}

	if hasBudget {
		gauge(ch, poeBudgetDesc, budget)
	}
	if hasUsed {
		gauge(ch, poeConsumptionDesc, used)
	}
	if hasBudget && hasUsed && budget > 0 {
		gauge(ch, poeUsageDesc, used/budget)
	}
	if v, ok := cellFloat(rrow, rcPoePsePeakPower, 0.001); ok {
		gauge(ch, poePsePeakDesc, v)
	}
	if v, ok := cellFloat(srow, pethMainThreshold, 0.01); ok {
		gauge(ch, poeThresholdDesc, v)
	}
	if v, ok := cellInt(srow, pethMainOperStatus); ok {
		stateSet(ch, poePseOperDesc, poePseOperStates, v)
	}
	if v, ok := cellInt(rrow, rcPoePseLegacyDetection); ok {
		gauge(ch, poeLegacyDesc, boolFloat(v == rcEnabled))
	}
	if v, ok := cellInt(rrow, rcPoePseTemperatureProtection); ok {
		gauge(ch, poeTempProtDesc, boolFloat(v == rcEnabled))
	}
	return nil
}
