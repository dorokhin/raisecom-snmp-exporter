package collector

import (
	"context"
	"strconv"
	"strings"

	"raisecom-snmp-exporter/internal/snmp"
)

const (
	oidSysUpTime = "1.3.6.1.2.1.1.3.0"
	oidIfDescr   = "1.3.6.1.2.1.2.2.1.2"
	oidIfName    = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias   = "1.3.6.1.2.1.31.1.1.1.18"
	// ROS keeps the `description` set on a port here; ifAlias stays empty.
	oidRcPortDescription = "1.3.6.1.4.1.8886.6.1.20.1.1.1.2"
)

// portLabels are attached to every per-port metric.
var portLabels = []string{"ifindex", "port", "ifname", "description"}

// Port describes one interface.
type Port struct {
	IfIndex     int64
	Name        string // gigaethernet1/1/10
	Number      string // 10
	Description string
}

// Labels returns values for portLabels.
func (p *Port) Labels() []string {
	return []string{strconv.FormatInt(p.IfIndex, 10), p.Number, p.Name, p.Description}
}

// PortMap resolves ifIndex values to ports.
type PortMap struct {
	byIndex map[int64]*Port
}

// Get returns the port for ifIndex; unknown indexes get a placeholder so
// metrics are still emitted.
func (m *PortMap) Get(ifIndex int64) *Port {
	if p, ok := m.byIndex[ifIndex]; ok {
		return p
	}
	return &Port{IfIndex: ifIndex}
}

// Len is the number of known ports.
func (m *PortMap) Len() int { return len(m.byIndex) }

func loadPortMap(ctx context.Context, c snmp.Client) (*PortMap, error) {
	m := &PortMap{byIndex: map[int64]*Port{}}
	names, err := c.Walk(ctx, oidIfName)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		if names, err = c.Walk(ctx, oidIfDescr); err != nil {
			return nil, err
		}
	}
	for _, p := range names {
		idx, ok := snmp.LastIndex(p.OID)
		if !ok {
			continue
		}
		name := p.String()
		m.byIndex[idx] = &Port{IfIndex: idx, Name: name, Number: portNumber(name)}
	}

	// Prefer the ROS description table, fall back to ifAlias.
	for _, root := range []string{oidIfAlias, oidRcPortDescription} {
		pdus, err := c.Walk(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, p := range pdus {
			idx, ok := snmp.LastIndex(p.OID)
			if !ok {
				continue
			}
			if port, ok := m.byIndex[idx]; ok {
				if d := p.String(); d != "" {
					port.Description = d
				}
			}
		}
	}
	return m, nil
}

// portNumber extracts the port number from names like "gigaethernet1/1/10".
func portNumber(name string) string {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ""
	}
	return name[i+1:]
}
