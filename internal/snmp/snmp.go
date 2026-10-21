// Package snmp wraps gosnmp with the walk semantics Raisecom ROS agents need.
//
// ROS agents return OIDs out of lexicographic order in some tables, so walks run
// with gosnmp's "c" option (no increasing-order check). Without that check a
// buggy agent could loop forever, so every walk is guarded by a row limit and a
// repeated-OID detector.
package snmp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// PDU is a single variable binding with a normalized OID (no leading dot).
type PDU struct {
	OID   string
	Type  gosnmp.Asn1BER
	Value any
}

// Client is the subset of SNMP operations collectors use.
type Client interface {
	// Get fetches scalar OIDs. Missing objects are omitted from the result.
	Get(ctx context.Context, oids ...string) (map[string]PDU, error)
	// Walk returns every PDU below root.
	Walk(ctx context.Context, root string) ([]PDU, error)
}

// MaxWalkRows bounds a single walk; the largest table we read has a few hundred rows.
const MaxWalkRows = 10000

// errStop is returned from the walk callback to abort a runaway walk.
var errStop = errors.New("walk aborted")

// Params configures a connection to one agent.
type Params struct {
	Target         string
	Port           uint16
	Version        gosnmp.SnmpVersion
	Community      string
	Timeout        time.Duration
	Retries        int
	MaxRepetitions uint32

	// SNMPv3
	Username      string
	SecurityLevel gosnmp.SnmpV3MsgFlags
	AuthProtocol  gosnmp.SnmpV3AuthProtocol
	AuthPassword  string
	PrivProtocol  gosnmp.SnmpV3PrivProtocol
	PrivPassword  string
	ContextName   string
}

// GoSNMP is the real Client backed by gosnmp.
type GoSNMP struct {
	g *gosnmp.GoSNMP
}

// Dial opens a UDP "connection" to the agent.
func Dial(ctx context.Context, p Params) (*GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Target:             p.Target,
		Port:               p.Port,
		Version:            p.Version,
		Community:          p.Community,
		Timeout:            p.Timeout,
		Retries:            p.Retries,
		MaxRepetitions:     p.MaxRepetitions,
		MaxOids:            gosnmp.MaxOids,
		Context:            ctx,
		AppOpts:            map[string]any{"c": true},
		ExponentialTimeout: false,
	}
	if g.Port == 0 {
		g.Port = 161
	}
	if p.Version == gosnmp.Version3 {
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = p.SecurityLevel
		g.ContextName = p.ContextName
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 p.Username,
			AuthenticationProtocol:   p.AuthProtocol,
			AuthenticationPassphrase: p.AuthPassword,
			PrivacyProtocol:          p.PrivProtocol,
			PrivacyPassphrase:        p.PrivPassword,
		}
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("connect %s: %w", p.Target, err)
	}
	return &GoSNMP{g: g}, nil
}

// Close releases the socket.
func (c *GoSNMP) Close() error {
	if c.g.Conn == nil {
		return nil
	}
	return c.g.Conn.Close()
}

// Get implements Client.
func (c *GoSNMP) Get(ctx context.Context, oids ...string) (map[string]PDU, error) {
	c.g.Context = ctx
	out := make(map[string]PDU, len(oids))
	for start := 0; start < len(oids); start += c.g.MaxOids {
		end := min(start+c.g.MaxOids, len(oids))
		res, err := c.g.Get(oids[start:end])
		if err != nil {
			return nil, err
		}
		for _, v := range res.Variables {
			switch v.Type {
			case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
				continue
			}
			p := convert(v)
			out[p.OID] = p
		}
	}
	return out, nil
}

// Walk implements Client.
func (c *GoSNMP) Walk(ctx context.Context, root string) ([]PDU, error) {
	c.g.Context = ctx
	g := newWalkGuard(Normalize(root), MaxWalkRows)
	fn := func(v gosnmp.SnmpPDU) error { return g.add(convert(v)) }
	var err error
	if c.g.Version == gosnmp.Version1 {
		err = c.g.Walk(g.root, fn)
	} else {
		err = c.g.BulkWalk(g.root, fn)
	}
	if errors.Is(err, errStop) {
		return nil, g.err
	}
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", g.root, err)
	}
	return g.rows, nil
}

// walkGuard collects walk results and stops agents that loop. It is needed
// because walks run without gosnmp's increasing-OID check.
type walkGuard struct {
	root  string
	limit int
	rows  []PDU
	seen  map[string]struct{}
	err   error
}

func newWalkGuard(root string, limit int) *walkGuard {
	return &walkGuard{root: root, limit: limit, seen: map[string]struct{}{}}
}

func (g *walkGuard) add(p PDU) error {
	if _, dup := g.seen[p.OID]; dup {
		g.err = fmt.Errorf("walk %s: agent returned %s twice", g.root, p.OID)
		return errStop
	}
	if len(g.rows) >= g.limit {
		g.err = fmt.Errorf("walk %s: more than %d rows", g.root, g.limit)
		return errStop
	}
	g.seen[p.OID] = struct{}{}
	g.rows = append(g.rows, p)
	return nil
}

func convert(v gosnmp.SnmpPDU) PDU {
	return PDU{OID: Normalize(v.Name), Type: v.Type, Value: v.Value}
}

// Normalize strips the leading dot gosnmp and net-snmp put on OIDs.
func Normalize(oid string) string {
	return strings.TrimPrefix(oid, ".")
}

// Int64 returns the PDU as a signed integer.
func (p PDU) Int64() (int64, bool) {
	switch v := p.Value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	case *big.Int:
		return v.Int64(), true
	}
	return 0, false
}

// Float returns the PDU as float64. Counter64 values keep full precision up to 2^53.
func (p PDU) Float() (float64, bool) {
	switch v := p.Value.(type) {
	case uint64:
		return float64(v), true
	case *big.Int:
		f, _ := new(big.Float).SetInt(v).Float64()
		return f, true
	}
	i, ok := p.Int64()
	return float64(i), ok
}

// String returns OctetString values as text with NULs and padding removed.
func (p PDU) String() string {
	switch v := p.Value.(type) {
	case []byte:
		return CleanString(v)
	case string:
		return CleanString([]byte(v))
	}
	if i, ok := p.Int64(); ok {
		return fmt.Sprint(i)
	}
	return ""
}

// Bytes returns the raw octets of an OctetString.
func (p PDU) Bytes() []byte {
	switch v := p.Value.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	}
	return nil
}

// CleanString drops everything from the first NUL, invalid bytes and surrounding spaces.
// Raisecom pads fixed-width fields with spaces and terminates C strings with NUL.
func CleanString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	s := strings.ToValidUTF8(string(b), "")
	return strings.TrimSpace(s)
}
