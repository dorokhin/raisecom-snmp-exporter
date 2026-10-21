// Package collector turns SNMP data from Raisecom ROS switches into Prometheus metrics.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"raisecom-snmp-exporter/internal/config"
	"raisecom-snmp-exporter/internal/snmp"
)

const namespace = "raisecom"

// Collector gathers one area of metrics from a target.
type Collector interface {
	Collect(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error
}

// registry maps config collector names to implementations.
var registry = map[string]Collector{
	config.CollectorSystem:     systemCollector{},
	config.CollectorInterfaces: interfacesCollector{},
	config.CollectorPoE:        poeCollector{},
	config.CollectorSFP:        sfpCollector{},
}

// Target is one device being probed. It caches data shared between collectors.
type Target struct {
	Client snmp.Client
	Module *config.Module
	Logger *slog.Logger
	// Now is the clock used for timestamps; nil means time.Now.
	Now func() time.Time

	ports    *PortMap
	portsErr error
	uptime   *float64
}

// Ports returns the ifIndex → port mapping, loading it on first use.
func (t *Target) Ports(ctx context.Context) (*PortMap, error) {
	if t.ports == nil && t.portsErr == nil {
		t.ports, t.portsErr = loadPortMap(ctx, t.Client)
	}
	return t.ports, t.portsErr
}

func (t *Target) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Uptime returns sysUpTime in seconds, loading it on first use.
func (t *Target) Uptime(ctx context.Context) (float64, error) {
	if t.uptime != nil {
		return *t.uptime, nil
	}
	res, err := t.Client.Get(ctx, oidSysUpTime)
	if err != nil {
		return 0, err
	}
	p, ok := res[oidSysUpTime]
	if !ok {
		return 0, fmt.Errorf("sysUpTime not available")
	}
	v, _ := p.Float()
	v /= 100
	t.uptime = &v
	return v, nil
}

var (
	collectorSuccessDesc = prometheus.NewDesc(namespace+"_collector_success",
		"Whether the collector finished without errors.", []string{"collector"}, nil)
	collectorDurationDesc = prometheus.NewDesc(namespace+"_collector_duration_seconds",
		"Time the collector took.", []string{"collector"}, nil)
)

// Probe runs the module's collectors against one target. It is an unchecked
// prometheus.Collector meant to be registered in a per-request registry.
type Probe struct {
	Ctx    context.Context
	Target *Target

	// Success is set after Collect: true when every collector succeeded.
	Success bool
}

// Describe implements prometheus.Collector (unchecked).
func (p *Probe) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (p *Probe) Collect(ch chan<- prometheus.Metric) {
	p.Success = true
	var abort error
	for _, name := range p.Target.Module.Collectors {
		start := time.Now()
		err := abort
		if err == nil {
			err = registry[name].Collect(p.Ctx, p.Target, ch)
		}
		ok := 1.0
		if err != nil {
			ok = 0
			p.Success = false
			if err != abort {
				p.Target.Logger.Warn("collector failed", "collector", name, "err", err)
			}
			// An unreachable agent would make every remaining collector wait for
			// its own timeout; fail them immediately instead.
			if abort == nil && isTimeout(err) {
				abort = fmt.Errorf("skipped after timeout: %w", err)
			}
		}
		ch <- prometheus.MustNewConstMetric(collectorSuccessDesc, prometheus.GaugeValue, ok, name)
		ch <- prometheus.MustNewConstMetric(collectorDurationDesc, prometheus.GaugeValue, time.Since(start).Seconds(), name)
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	// gosnmp reports exhausted retries as a plain error: "request timeout (after N retries)".
	return strings.Contains(err.Error(), "request timeout")
}

// metric helpers

func desc(subsystem, name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, name), help, labels, nil)
}

func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func counter(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// cellFloat returns column col of a row scaled by mul.
func cellFloat(row map[int]snmp.PDU, col int, mul float64) (float64, bool) {
	p, ok := row[col]
	if !ok {
		return 0, false
	}
	v, ok := p.Float()
	return scale(v, mul), ok
}

// scale multiplies v by mul. Fractional factors are applied as a division
// (v*0.001 gives 45.800000000000004, v/1000 gives 45.8).
func scale(v, mul float64) float64 {
	if mul > 0 && mul < 1 {
		return v / math.Round(1/mul)
	}
	return v * mul
}

func cellInt(row map[int]snmp.PDU, col int) (int64, bool) {
	p, ok := row[col]
	if !ok {
		return 0, false
	}
	return p.Int64()
}

// stateSet emits one series per state with value 1 for the current one.
func stateSet(ch chan<- prometheus.Metric, d *prometheus.Desc, states map[int64]string, current int64, labels ...string) {
	for code, name := range states {
		gauge(ch, d, boolFloat(code == current), append(slices.Clone(labels), name)...)
	}
}
