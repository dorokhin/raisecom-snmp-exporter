#!/usr/bin/env bash
# Validates the generated MIBs with net-snmp:
#   1. the single-file MIB loads on its own (only the system's standard MIBs next to it);
#   2. the modular set loads without diagnostics;
#   3. every private OID the exporter reads (taken from the recorded walk) resolves
#      to a symbolic name, and to the same name in both variants.
set -euo pipefail

cd "$(dirname "$0")"
STD=${SNMP_STD_MIBS:-/usr/share/snmp/mibs}
MODULES=RAISECOM-BASE-MIB:RAISECOM-SYSTEM-MIB:RAISECOM-FANMONITOR-MIB:RAISECOM-OPTICAL-TRANSCEIVER-MIB:RAISECOM-DEVICE-MIB:RAISECOM-PORT-MIB:RAISECOM-POE-MIB
DIAG='At line [0-9]|^Cannot|^Did not find|^Unlinked'
WALK=../testdata/iscom2624g.walk

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp RAISECOM-ISCOM2600G-MIB.mib "$tmp/"

# mibpath/mibs print the -M/-m values of a variant (bash 3.2 compatible, no namerefs).
mibpath() { if [ "$1" = single ]; then echo "$STD:$tmp"; else echo "$STD:$PWD/modules"; fi; }
mibs()    { if [ "$1" = single ]; then echo RAISECOM-ISCOM2600G-MIB; else echo "$MODULES"; fi; }
fail=0

for v in single modular; do
  n=$(snmptranslate -M "$(mibpath $v)" -m "$(mibs $v)" -Tp 2>&1 | grep -cE "$DIAG" || true)
  echo "load $v: $n diagnostics"
  [ "$n" -eq 0 ] || { snmptranslate -M "$(mibpath $v)" -m "$(mibs $v)" -Tp 2>&1 | grep -E "$DIAG"; fail=1; }
done

# Branches read by internal/collector (keep in sync with the OID constants there).
LC_ALL=C grep -aoE '^\.1\.3\.6\.1\.4\.1\.8886\.[0-9.]+' "$WALK" |
  grep -E '8886\.(6\.1\.51\.1\.1\.([1-6]|8|10|11)\.|6\.1\.51\.2\.1\.[12358]\.|6\.1\.1\.1\.(1|2|9|13|14|19)\.0|6\.1\.20\.1\.1\.1\.2\.|1\.1\.1\.5\.1\.4\.|1\.1\.3\.[12]\.0|1\.1\.4\.2\.|1\.1\.5\.2\.2\.1\.|1\.18\.1\.2\.0|1\.18\.2\.1\.1\.1\.|1\.18\.2\.2\.1\.1\.[2-7]\.)' \
  > "$tmp/oids"

for v in single modular; do
  xargs -n 200 snmptranslate -M "$(mibpath $v)" -m "$(mibs $v)" -OS < "$tmp/oids" 2>&1 | grep . | sed -E 's/^[A-Z0-9-]+:://' > "$tmp/$v"
  bad=$(grep -cE 'enterprises\.8886|^\.?1\.3\.6|Unknown|Cannot|Entry\.[0-9]' "$tmp/$v" || true)
  echo "resolve $v: $(wc -l < "$tmp/$v" | tr -d ' ') OIDs, $bad unresolved"
  [ "$bad" -eq 0 ] || { grep -E 'enterprises\.8886|^\.?1\.3\.6|Unknown|Cannot|Entry\.[0-9]' "$tmp/$v" | head; fail=1; }
done

if diff -q "$tmp/single" "$tmp/modular" >/dev/null; then
  echo "names: identical in both variants"
else
  echo "names differ between variants:"; diff "$tmp/single" "$tmp/modular" | head; fail=1
fi
exit $fail
