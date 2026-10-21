package snmp

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Table holds rows of an SNMP table keyed by index suffix (e.g. "1.2082476033"),
// each row keyed by column number.
type Table map[string]map[int]PDU

// WalkColumns walks entry.<col> for every col and groups the results by row index.
// Columns are walked one by one: on ROS a whole-table walk is as slow as its
// slowest column, and columns that are absent simply produce no cells.
func WalkColumns(ctx context.Context, c Client, entry string, cols ...int) (Table, error) {
	entry = Normalize(entry)
	t := Table{}
	for _, col := range cols {
		prefix := entry + "." + strconv.Itoa(col)
		pdus, err := c.Walk(ctx, prefix)
		if err != nil {
			return nil, err
		}
		for _, p := range pdus {
			idx, ok := strings.CutPrefix(p.OID, prefix+".")
			if !ok {
				continue
			}
			row := t[idx]
			if row == nil {
				row = map[int]PDU{}
				t[idx] = row
			}
			row[col] = p
		}
	}
	return t, nil
}

// Indexes returns row indexes in numeric OID order.
func (t Table) Indexes() []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return CompareOID(out[i], out[j]) < 0 })
	return out
}

// CompareOID compares dotted OIDs numerically, component by component.
func CompareOID(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.ParseUint(as[i], 10, 64)
		y, _ := strconv.ParseUint(bs[i], 10, 64)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return len(as) - len(bs)
}

// LastIndex returns the last component of an index as an integer.
func LastIndex(idx string) (int64, bool) {
	if i := strings.LastIndexByte(idx, '.'); i >= 0 {
		idx = idx[i+1:]
	}
	v, err := strconv.ParseInt(idx, 10, 64)
	return v, err == nil
}
