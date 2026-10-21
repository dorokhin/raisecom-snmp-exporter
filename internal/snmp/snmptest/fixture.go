// Package snmptest provides an snmp.Client that serves data recorded with
// `snmpbulkwalk -On -Oe -Ot`, so collectors can be tested against real devices' output.
package snmptest

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"

	"raisecom-snmp-exporter/internal/snmp"
)

// Fixture is an in-memory agent.
type Fixture struct {
	order []string
	pdus  map[string]snmp.PDU
	// Walked records every root passed to Walk, for assertions.
	Walked []string
}

var _ snmp.Client = (*Fixture)(nil)

// Load parses one or more walk files.
func Load(paths ...string) (*Fixture, error) {
	f := &Fixture{pdus: map[string]snmp.PDU{}}
	for _, p := range paths {
		if err := f.load(p); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (f *Fixture) load(path string) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var last string
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if !strings.HasPrefix(line, ".") {
			// net-snmp wraps long Hex-STRINGs onto continuation lines.
			if p, ok := f.pdus[last]; ok && p.Type == gosnmp.OctetString && strings.TrimSpace(line) != "" {
				b, err := parseHex(line)
				if err != nil {
					return fmt.Errorf("%s:%d: %w", path, n, err)
				}
				p.Value = append(p.Bytes(), b...)
				f.pdus[last] = p
			}
			continue
		}
		oid, val, ok := strings.Cut(line, " = ")
		if !ok {
			continue // "No Such Object" and similar diagnostics
		}
		p, err := parseValue(val)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, n, err)
		}
		p.OID = snmp.Normalize(oid)
		if _, dup := f.pdus[p.OID]; !dup {
			f.order = append(f.order, p.OID)
		}
		f.pdus[p.OID] = p
		last = p.OID
	}
	return sc.Err()
}

func parseValue(v string) (snmp.PDU, error) {
	typ, raw, typed := strings.Cut(v, ": ")
	if !typed || strings.HasPrefix(v, "\"") {
		raw = v
		typ = ""
	}
	switch typ {
	case "INTEGER":
		i, err := strconv.ParseInt(raw, 10, 64)
		return snmp.PDU{Type: gosnmp.Integer, Value: int(i)}, err
	case "Gauge32", "Counter32", "Unsigned32":
		u, err := strconv.ParseUint(raw, 10, 32)
		t := gosnmp.Gauge32
		if typ == "Counter32" {
			t = gosnmp.Counter32
		}
		return snmp.PDU{Type: t, Value: uint(u)}, err
	case "Counter64":
		u, err := strconv.ParseUint(raw, 10, 64)
		return snmp.PDU{Type: gosnmp.Counter64, Value: u}, err
	case "Timeticks":
		u, err := strconv.ParseUint(strings.Trim(strings.SplitN(raw, ")", 2)[0], "( "), 10, 32)
		return snmp.PDU{Type: gosnmp.TimeTicks, Value: uint32(u)}, err
	case "STRING":
		return snmp.PDU{Type: gosnmp.OctetString, Value: []byte(unquote(raw))}, nil
	case "Hex-STRING":
		b, err := parseHex(raw)
		return snmp.PDU{Type: gosnmp.OctetString, Value: b}, err
	case "OID":
		return snmp.PDU{Type: gosnmp.ObjectIdentifier, Value: raw}, nil
	case "IpAddress":
		return snmp.PDU{Type: gosnmp.IPAddress, Value: raw}, nil
	case "":
		if strings.HasPrefix(raw, "\"") {
			return snmp.PDU{Type: gosnmp.OctetString, Value: []byte(unquote(raw))}, nil
		}
		// -Ot prints TimeTicks as a bare number.
		u, err := strconv.ParseUint(raw, 10, 32)
		return snmp.PDU{Type: gosnmp.TimeTicks, Value: uint32(u)}, err
	}
	return snmp.PDU{}, fmt.Errorf("unsupported type %q", typ)
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return strings.ReplaceAll(s, `\"`, `"`)
}

func parseHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.Join(strings.Fields(s), ""))
}

// Get implements snmp.Client.
func (f *Fixture) Get(_ context.Context, oids ...string) (map[string]snmp.PDU, error) {
	out := map[string]snmp.PDU{}
	for _, o := range oids {
		if p, ok := f.pdus[snmp.Normalize(o)]; ok {
			out[p.OID] = p
		}
	}
	return out, nil
}

// Walk implements snmp.Client.
func (f *Fixture) Walk(_ context.Context, root string) ([]snmp.PDU, error) {
	root = snmp.Normalize(root)
	f.Walked = append(f.Walked, root)
	var out []snmp.PDU
	for _, o := range f.order {
		if strings.HasPrefix(o, root+".") {
			out = append(out, f.pdus[o])
		}
	}
	return out, nil
}
