package collector

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"raisecom-snmp-exporter/internal/snmp"
)

const (
	oidIfEntry  = "1.3.6.1.2.1.2.2.1"
	oidIfXEntry = "1.3.6.1.2.1.31.1.1.1"
)

// ifTable columns
const (
	ifSpeed       = 5
	ifAdminStatus = 7
	ifOperStatus  = 8
	ifLastChange  = 9
	ifInDiscards  = 13
	ifInErrors    = 14
	ifOutDiscards = 19
	ifOutErrors   = 20
)

// ifXTable columns
const (
	ifHCInOctets        = 6
	ifHCInUcastPkts     = 7
	ifHCInMulticastPkts = 8
	ifHCInBroadcastPkts = 9
	ifHCOutOctets       = 10
	ifHCOutUcastPkts    = 11
	ifHCOutMulticastPkt = 12
	ifHCOutBroadcastPkt = 13
	ifHighSpeed         = 15
)

var (
	ifOperUpDesc     = desc("interface", "oper_up", "1 when ifOperStatus is up.", portLabels...)
	ifAdminUpDesc    = desc("interface", "admin_up", "1 when ifAdminStatus is up.", portLabels...)
	ifSpeedDesc      = desc("interface", "speed_bytes", "Negotiated speed in bytes per second (0 when down).", portLabels...)
	ifLastChangeDesc = desc("interface", "last_change_timestamp_seconds",
		"Unix time of the last oper status change (boot time if unchanged since boot).", portLabels...)
)

type ifCounter struct {
	table, col int // table 0 = ifTable, 1 = ifXTable
	desc       *prometheus.Desc
}

var ifCounters = []ifCounter{
	{1, ifHCInOctets, desc("interface", "receive_bytes_total", "Received octets (ifHCInOctets).", portLabels...)},
	{1, ifHCOutOctets, desc("interface", "transmit_bytes_total", "Transmitted octets (ifHCOutOctets).", portLabels...)},
	{1, ifHCInUcastPkts, desc("interface", "receive_unicast_packets_total", "Received unicast packets.", portLabels...)},
	{1, ifHCOutUcastPkts, desc("interface", "transmit_unicast_packets_total", "Transmitted unicast packets.", portLabels...)},
	{1, ifHCInMulticastPkts, desc("interface", "receive_multicast_packets_total", "Received multicast packets.", portLabels...)},
	{1, ifHCOutMulticastPkt, desc("interface", "transmit_multicast_packets_total", "Transmitted multicast packets.", portLabels...)},
	{1, ifHCInBroadcastPkts, desc("interface", "receive_broadcast_packets_total", "Received broadcast packets.", portLabels...)},
	{1, ifHCOutBroadcastPkt, desc("interface", "transmit_broadcast_packets_total", "Transmitted broadcast packets.", portLabels...)},
	{0, ifInErrors, desc("interface", "receive_errors_total", "Inbound packets with errors.", portLabels...)},
	{0, ifOutErrors, desc("interface", "transmit_errors_total", "Outbound packets that could not be sent due to errors.", portLabels...)},
	{0, ifInDiscards, desc("interface", "receive_discards_total", "Inbound packets discarded without error.", portLabels...)},
	{0, ifOutDiscards, desc("interface", "transmit_discards_total", "Outbound packets discarded without error.", portLabels...)},
}

type interfacesCollector struct{}

func (interfacesCollector) Collect(ctx context.Context, t *Target, ch chan<- prometheus.Metric) error {
	ports, err := t.Ports(ctx)
	if err != nil {
		return err
	}
	uptime, err := t.Uptime(ctx)
	if err != nil {
		return err
	}
	now := t.now()

	ift, err := snmp.WalkColumns(ctx, t.Client, oidIfEntry,
		ifSpeed, ifAdminStatus, ifOperStatus, ifLastChange, ifInDiscards, ifInErrors, ifOutDiscards, ifOutErrors)
	if err != nil {
		return err
	}
	ifx, err := snmp.WalkColumns(ctx, t.Client, oidIfXEntry,
		ifHCInOctets, ifHCInUcastPkts, ifHCInMulticastPkts, ifHCInBroadcastPkts,
		ifHCOutOctets, ifHCOutUcastPkts, ifHCOutMulticastPkt, ifHCOutBroadcastPkt, ifHighSpeed)
	if err != nil {
		return err
	}
	tables := [2]snmp.Table{ift, ifx}

	for _, idx := range ift.Indexes() {
		ifIndex, err := strconv.ParseInt(idx, 10, 64)
		if err != nil {
			continue
		}
		row, xrow := ift[idx], ifx[idx]
		labels := ports.Get(ifIndex).Labels()

		if v, ok := cellInt(row, ifOperStatus); ok {
			gauge(ch, ifOperUpDesc, boolFloat(v == 1), labels...)
		}
		if v, ok := cellInt(row, ifAdminStatus); ok {
			gauge(ch, ifAdminUpDesc, boolFloat(v == 1), labels...)
		}
		if v, ok := cellFloat(xrow, ifHighSpeed, 1e6/8); ok && v > 0 {
			gauge(ch, ifSpeedDesc, v, labels...)
		} else if v, ok := cellFloat(row, ifSpeed, 1.0/8); ok {
			gauge(ch, ifSpeedDesc, v, labels...)
		}
		if v, ok := cellFloat(row, ifLastChange, 0.01); ok && v <= uptime {
			ts := now.Add(-time.Duration((uptime - v) * float64(time.Second)))
			gauge(ch, ifLastChangeDesc, float64(ts.Unix()), labels...)
		}
		for _, c := range ifCounters {
			if v, ok := cellFloat(tables[c.table][idx], c.col, 1); ok {
				counter(ch, c.desc, v, labels...)
			}
		}
	}
	return nil
}
