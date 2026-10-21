package collector

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"raisecom-snmp-exporter/internal/config"
	"raisecom-snmp-exporter/internal/snmp"
	"raisecom-snmp-exporter/internal/snmp/snmptest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixture = "../../testdata/iscom2624g.walk"

// fixedNow pins timestamps derived from sysUpTime.
var fixedNow = func() time.Time { return time.Unix(1_790_000_000, 0) }

func newTarget(t *testing.T, mod *config.Module) *Target {
	t.Helper()
	fx, err := snmptest.Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if mod == nil {
		mod = &config.Module{Collectors: config.AllCollectors}
	}
	return &Target{Client: fx, Module: mod, Logger: slog.New(slog.DiscardHandler), Now: fixedNow}
}

// single exposes one collector as a prometheus.Collector.
type single struct {
	c   Collector
	t   *Target
	err error
}

func (s *single) Describe(chan<- *prometheus.Desc) {}
func (s *single) Collect(ch chan<- prometheus.Metric) {
	s.err = s.c.Collect(context.Background(), s.t, ch)
}

func gather(t *testing.T, name string, tgt *Target) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	s := &single{c: registry[name], t: tgt}
	reg.MustRegister(s)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather %s: %v", name, err)
	}
	if s.err != nil {
		t.Fatalf("collect %s: %v", name, s.err)
	}
	return mfs
}

func TestGolden(t *testing.T) {
	for _, name := range config.AllCollectors {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
			for _, mf := range gather(t, name, newTarget(t, nil)) {
				if err := enc.Encode(mf); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join("testdata", name+".prom")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("output differs from %s; run `go test ./internal/collector -update` and review the diff", path)
			}
		})
	}
}

// value finds a sample by metric name and label subset.
func value(t *testing.T, mfs []*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	metrics:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue metrics
				}
			}
			switch {
			case m.Gauge != nil:
				return m.Gauge.GetValue()
			case m.Counter != nil:
				return m.Counter.GetValue()
			}
		}
	}
	t.Fatalf("no sample %s%v", name, labels)
	return 0
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func hasSample(mfs []*dto.MetricFamily, name string, labels map[string]string) bool {
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for _, l := range m.GetLabel() {
				if v, ok := labels[l.GetName()]; ok && v != l.GetValue() {
					match = false
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

func TestPoEValues(t *testing.T) {
	mfs := gather(t, config.CollectorPoE, newTarget(t, nil))
	p10 := map[string]string{"port": "10", "ifname": "gigaethernet1/1/10", "ifindex": "2082476042"}

	near(t, "delivering", value(t, mfs, "raisecom_poe_port_delivering", p10), 1)
	near(t, "detection delivering", value(t, mfs, "raisecom_poe_port_detection_status", map[string]string{"port": "10", "status": "delivering"}), 1)
	near(t, "detection searching", value(t, mfs, "raisecom_poe_port_detection_status", map[string]string{"port": "10", "status": "searching"}), 0)
	near(t, "class", value(t, mfs, "raisecom_poe_port_class", p10), 4)
	near(t, "limit", value(t, mfs, "raisecom_poe_port_power_limit_watts", p10), 30)
	near(t, "priority low", value(t, mfs, "raisecom_poe_port_priority", map[string]string{"port": "10", "priority": "low"}), 1)

	// Port 1 has no PD: not delivering, no class sample.
	near(t, "p1 delivering", value(t, mfs, "raisecom_poe_port_delivering", map[string]string{"port": "1"}), 0)
	if hasSample(mfs, "raisecom_poe_port_class", map[string]string{"port": "1"}) {
		t.Error("port 1 must not report a class")
	}

	// Settings decoded via CLI: force-power disabled, alive check enabled, no fail action.
	near(t, "force power", value(t, mfs, "raisecom_poe_port_force_power", p10), 0)
	near(t, "alive check", value(t, mfs, "raisecom_poe_port_alive_check", p10), 1)
	near(t, "alive action none", value(t, mfs, "raisecom_poe_port_alive_action", map[string]string{"port": "10", "action": "none"}), 1)
	near(t, "alive action reboot", value(t, mfs, "raisecom_poe_port_alive_action", map[string]string{"port": "10", "action": "reboot"}), 0)
	near(t, "legacy detection", value(t, mfs, "raisecom_poe_pse_legacy_detection", nil), 1)
	near(t, "temperature protection", value(t, mfs, "raisecom_poe_pse_temperature_protection", nil), 1)

	near(t, "budget", value(t, mfs, "raisecom_poe_power_budget_watts", nil), 370)
	near(t, "threshold", value(t, mfs, "raisecom_poe_usage_threshold_ratio", nil), 1)
	near(t, "pse on", value(t, mfs, "raisecom_poe_pse_oper_status", map[string]string{"status": "on"}), 1)

	// Electrical sanity: P ≈ U·I within 25% (power is smoothed, U·I is a sample).
	for _, port := range []string{"10", "13", "15"} {
		l := map[string]string{"port": port}
		u := value(t, mfs, "raisecom_poe_port_voltage_volts", l)
		i := value(t, mfs, "raisecom_poe_port_current_amperes", l)
		p := value(t, mfs, "raisecom_poe_port_power_instant_watts", l)
		if u < 44 || u > 57 {
			t.Errorf("port %s voltage %v outside 802.3af/at range", port, u)
		}
		if p > 0 && math.Abs(u*i-p)/p > 0.5 {
			t.Logf("port %s: U·I=%.2fW vs instant %.2fW (samples taken at different moments)", port, u*i, p)
		}
	}

	// The consumption reported by the PSE should be close to the sum of the ports.
	var sum float64
	for _, port := range []string{"10", "13", "15"} {
		sum += value(t, mfs, "raisecom_poe_port_power_watts", map[string]string{"port": port})
	}
	total := value(t, mfs, "raisecom_poe_power_consumption_watts", nil)
	if math.Abs(sum-total)/total > 0.3 {
		t.Errorf("sum of ports %.2fW far from PSE consumption %.2fW", sum, total)
	}
	near(t, "usage ratio", value(t, mfs, "raisecom_poe_power_usage_ratio", nil), total/370)
}

func TestPoEClassOptional(t *testing.T) {
	off := false
	tgt := newTarget(t, &config.Module{Collectors: []string{config.CollectorPoE}, PoEClass: &off})
	mfs := gather(t, config.CollectorPoE, tgt)
	if hasSample(mfs, "raisecom_poe_port_class", nil) {
		t.Error("class reported although poe_class is false")
	}
}

func TestPoEClassNotWalked(t *testing.T) {
	tgt := newTarget(t, nil)
	gather(t, config.CollectorPoE, tgt)
	for _, w := range tgt.Client.(*snmptest.Fixture).Walked {
		if w == oidPethPsePortEntry+".10" {
			t.Error("class column must be fetched with GET for delivering ports, not walked")
		}
	}
}

func TestSFPValues(t *testing.T) {
	mfs := gather(t, config.CollectorSFP, newTarget(t, nil))
	near(t, "ddm enabled", value(t, mfs, "raisecom_sfp_ddm_enabled", nil), 1)

	p28 := map[string]string{"port": "28", "description": "uplink-28"}
	near(t, "temperature", value(t, mfs, "raisecom_sfp_temperature_celsius", p28), 44.5)
	near(t, "bias", value(t, mfs, "raisecom_sfp_bias_current_amperes", p28), 0.0312)
	near(t, "tx", value(t, mfs, "raisecom_sfp_tx_power_dbm", p28), -4.271)
	near(t, "rx", value(t, mfs, "raisecom_sfp_rx_power_dbm", p28), -40)
	near(t, "voltage", value(t, mfs, "raisecom_sfp_voltage_volts", p28), 3.282)
	near(t, "rx low alarm threshold", value(t, mfs, "raisecom_sfp_threshold",
		map[string]string{"port": "28", "param": "rx_power_dbm", "level": "low_alarm"}), -21.938)
	near(t, "rx alarm status", value(t, mfs, "raisecom_sfp_alarm_status",
		map[string]string{"port": "28", "param": "rx_power_dbm"}), 4)

	// Port 27 holds a copper module without DDM: info only.
	near(t, "p27 info", value(t, mfs, "raisecom_sfp_info", map[string]string{"port": "27", "media": "copper", "ddm_supported": "false"}), 1)
	if hasSample(mfs, "raisecom_sfp_temperature_celsius", map[string]string{"port": "27"}) {
		t.Error("port 27 has no DDM but reports temperature")
	}
	near(t, "p26 info", value(t, mfs, "raisecom_sfp_info", map[string]string{
		"port": "26", "vendor": "TELCON", "part_number": "TC-NW-10-27-10-D", "wavelength_nm": "1270", "description": "uplink-26",
	}), 1)
}

func TestSystemValues(t *testing.T) {
	mfs := gather(t, config.CollectorSystem, newTarget(t, nil))
	near(t, "info", value(t, mfs, "raisecom_device_info", map[string]string{
		"model": "ISCOM2624G-4GE-PWR-AC", "serial": "SN0000000000000000000001", "mac": "02:00:00:00:00:01",
		"software": "3.60.311(Compiled Mar 25 2020 18:40:44)", "hardware": "A.01", "bootrom": "2.0.24",
	}), 1)
	near(t, "mem total", value(t, mfs, "raisecom_memory_total_bytes", nil), 258486272)
	near(t, "temperature", value(t, mfs, "raisecom_temperature_celsius", nil), 38)
	near(t, "fan 1", value(t, mfs, "raisecom_fan_speed_rpm", map[string]string{"fan": "1"}), 4620)
	near(t, "fan up", value(t, mfs, "raisecom_fan_up", map[string]string{"fan": "3"}), 1)
	near(t, "cpu 1m", value(t, mfs, "raisecom_cpu_utilization_ratio", map[string]string{"period": "1m"}), 0.04)
}

func TestInterfaceValues(t *testing.T) {
	tgt := newTarget(t, nil)
	mfs := gather(t, config.CollectorInterfaces, tgt)
	ports, _ := tgt.Ports(context.Background())
	if ports.Len() != 28 {
		t.Fatalf("got %d ports, want 28", ports.Len())
	}
	near(t, "p24 up", value(t, mfs, "raisecom_interface_oper_up", map[string]string{"port": "24", "description": "access-24"}), 0)
	near(t, "p10 speed", value(t, mfs, "raisecom_interface_speed_bytes", map[string]string{"port": "10"}), 125e6)
	near(t, "p16 speed", value(t, mfs, "raisecom_interface_speed_bytes", map[string]string{"port": "16"}), 12.5e6)
	if v := value(t, mfs, "raisecom_interface_receive_bytes_total", map[string]string{"port": "2"}); v < 1e11 {
		t.Errorf("port 2 rx bytes %v: Counter64 lost precision", v)
	}
}

// timeoutClient fails every request like an unreachable agent.
type timeoutClient struct{ calls int }

func (c *timeoutClient) Get(context.Context, ...string) (map[string]snmp.PDU, error) {
	c.calls++
	return nil, errors.New("request timeout (after 1 retries)")
}

func (c *timeoutClient) Walk(context.Context, string) ([]snmp.PDU, error) {
	c.calls++
	return nil, errors.New("request timeout (after 1 retries)")
}

func TestProbeAbortsAfterTimeout(t *testing.T) {
	cl := &timeoutClient{}
	p := &Probe{Ctx: context.Background(), Target: &Target{
		Client: cl, Module: &config.Module{Collectors: config.AllCollectors}, Logger: slog.New(slog.DiscardHandler),
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(p)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if p.Success {
		t.Error("probe reported success")
	}
	if cl.calls != 1 {
		t.Errorf("agent contacted %d times, want 1 (remaining collectors must be skipped)", cl.calls)
	}
	for _, name := range config.AllCollectors {
		near(t, name+" success", value(t, mfs, "raisecom_collector_success", map[string]string{"collector": name}), 0)
	}
}
