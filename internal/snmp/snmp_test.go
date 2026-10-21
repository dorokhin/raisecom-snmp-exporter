package snmp

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestWalkGuardAcceptsUnorderedOIDs(t *testing.T) {
	// ROS returns e.g. ...2.742 before ...2.719 inside its process table.
	g := newWalkGuard("1.3.6", 10)
	for _, oid := range []string{"1.3.6.1.742", "1.3.6.1.719", "1.3.6.1.720"} {
		if err := g.add(PDU{OID: oid}); err != nil {
			t.Fatalf("add %s: %v", oid, err)
		}
	}
	if len(g.rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(g.rows))
	}
}

func TestWalkGuardStopsOnRepeat(t *testing.T) {
	g := newWalkGuard("1.3.6", 10)
	_ = g.add(PDU{OID: "1.3.6.1.1"})
	_ = g.add(PDU{OID: "1.3.6.1.2"})
	err := g.add(PDU{OID: "1.3.6.1.1"})
	if !errors.Is(err, errStop) {
		t.Fatalf("got %v, want errStop", err)
	}
	if g.err == nil || !strings.Contains(g.err.Error(), "twice") {
		t.Fatalf("unexpected guard error %v", g.err)
	}
}

func TestWalkGuardRowLimit(t *testing.T) {
	g := newWalkGuard("1.3.6", 2)
	_ = g.add(PDU{OID: "1.3.6.1"})
	_ = g.add(PDU{OID: "1.3.6.2"})
	if err := g.add(PDU{OID: "1.3.6.3"}); !errors.Is(err, errStop) {
		t.Fatalf("got %v, want errStop", err)
	}
}

func TestPDUConversions(t *testing.T) {
	cases := []struct {
		pdu  PDU
		want float64
	}{
		{PDU{Type: gosnmp.Integer, Value: -1992}, -1992},
		{PDU{Type: gosnmp.Gauge32, Value: uint(13345)}, 13345},
		{PDU{Type: gosnmp.Counter64, Value: uint64(163098876524)}, 163098876524},
		{PDU{Type: gosnmp.TimeTicks, Value: uint32(786125500)}, 786125500},
		{PDU{Type: gosnmp.Counter64, Value: big.NewInt(42)}, 42},
	}
	for _, c := range cases {
		got, ok := c.pdu.Float()
		if !ok || got != c.want {
			t.Errorf("Float(%v) = %v, %v; want %v", c.pdu.Value, got, ok, c.want)
		}
	}
}

func TestCleanString(t *testing.T) {
	cases := map[string]string{
		"TELCON          ":      "TELCON",
		"web\x00\x00\x00":       "web",
		"\xb0\xb0\xb0":          "",
		"tKscrn\x00garbage":     "tKscrn",
		"  ISCOM2624G-4GE-PWR ": "ISCOM2624G-4GE-PWR",
	}
	for in, want := range cases {
		if got := CleanString([]byte(in)); got != want {
			t.Errorf("CleanString(%q) = %q, want %q", in, got, want)
		}
	}
}

type mapClient map[string][]PDU

func (m mapClient) Get(context.Context, ...string) (map[string]PDU, error) { return nil, nil }
func (m mapClient) Walk(_ context.Context, root string) ([]PDU, error)     { return m[root], nil }

func TestWalkColumns(t *testing.T) {
	c := mapClient{
		"1.3.6.1.2.1.105.1.1.1.3": {
			{OID: "1.3.6.1.2.1.105.1.1.1.3.1.2082476042", Value: 1},
			{OID: "1.3.6.1.2.1.105.1.1.1.3.1.2082476033", Value: 2},
		},
		"1.3.6.1.2.1.105.1.1.1.6": {
			{OID: "1.3.6.1.2.1.105.1.1.1.6.1.2082476042", Value: 3},
		},
	}
	tbl, err := WalkColumns(context.Background(), c, ".1.3.6.1.2.1.105.1.1.1", 3, 6, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tbl.Indexes(), []string{"1.2082476033", "1.2082476042"}; !slices.Equal(got, want) {
		t.Fatalf("indexes %v, want %v", got, want)
	}
	if v, _ := tbl["1.2082476042"][6].Int64(); v != 3 {
		t.Fatalf("cell = %d, want 3", v)
	}
	if _, ok := tbl["1.2082476033"][6]; ok {
		t.Fatal("unexpected cell for missing column")
	}
}

func TestCompareOID(t *testing.T) {
	if CompareOID("1.10", "1.9") <= 0 {
		t.Error("1.10 should sort after 1.9")
	}
	if CompareOID("1.2", "1.2.1") >= 0 {
		t.Error("prefix should sort first")
	}
	if CompareOID("1.2.3", "1.2.3") != 0 {
		t.Error("equal OIDs")
	}
}
