# MIBs for Raisecom ISCOM2600G (software 3.60.311, ROS 5.2.1)

**English** | [Русский](README.Ru.md)

Unofficial MIBs written from what an **ISCOM2624G-4GE-PWR-AC with software 3.60.311 (ROS 5.2.1)** actually returns.
They cover every private table that `raisecom-snmp-exporter` reads, with all of their columns.

| File | What it is |
|---|---|
| `RAISECOM-ISCOM2600G-MIB.mib` | **self-contained** module: on its own it is enough for every private object of the switch |
| `modules/*.mib` | the same objects split **into modules, the way vendors usually ship them** (linked by IMPORTS) |
| `gen_mibs.py` | generator of both variants from one specification (edit it, not the `.mib` files) |
| `check_mibs.sh` | check: net-snmp loading and resolution of every exporter OID (`make mibcheck`) |
| `upstream-librenms/` | copies of the vendor MIBs from LibreNMS, for reference only (incomplete and with different OIDs, see below) |

Both variants are generated from the same definitions. A given descriptor always names the same OID, so both
variants can be loaded at the same time.

## Self-contained file

It imports only `SNMPv2-SMI`, `SNMPv2-TC` and `SNMPv2-CONF`. These are the SMIv2 base modules; every SNMP tool
(net-snmp, pysnmp, Zabbix, LibreNMS) has them, and by the standard a MIB cannot exist without them.
Local index columns are used instead of `IF-MIB::ifIndex`, so IF-MIB is not needed either.

```bash
snmpwalk -v2c -c public -M +./mibs -m +RAISECOM-ISCOM2600G-MIB 192.0.2.10 raisecomPoePortTable
snmpget  -v2c -c public -M +./mibs -m +RAISECOM-ISCOM2600G-MIB 192.0.2.10 raisecomPoePsePower.1
```

## Modular set

| Module | Root | Contents |
|---|---|---|
| `RAISECOM-BASE-MIB` | `8886` | enterprise root, `raisecomAgent`, `iscomSwitch`, TC `EnableVar` |
| `RAISECOM-SYSTEM-MIB` | `8886.1.1` | CPU (total per period), memory, temperature |
| `RAISECOM-FANMONITOR-MIB` | `8886.1.1.5` | fans: mode, state, speed |
| `RAISECOM-OPTICAL-TRANSCEIVER-MIB` | `8886.1.18` | global DDM flags, module information, DDM with thresholds |
| `RAISECOM-DEVICE-MIB` | `8886.6.1.1` | model, serial number, software and hardware versions, MAC (*reconstructed*) |
| `RAISECOM-PORT-MIB` | `8886.6.1.20` | port description (*reconstructed*) |
| `RAISECOM-POE-MIB` | `8886.6.1.51` | per-port PoE (mW, mV, mA, peak, limit) and per-PSE PoE (*reconstructed*) |

The modular set also needs the standard `IF-MIB` (for the `ifIndex` index).

```bash
snmpwalk -v2c -c public -M +./mibs/modules -m ALL 192.0.2.10 raisecomOpticalTransceiverDDMTable
```

## Differences from the vendor MIBs

The vendor files (`upstream-librenms/`) do not work as-is with this firmware:

- the set is incomplete: `RAISECOM-OPTICAL-TRANSCEIVER-MIB` imports the missing `RAISECOM-COMMON-MANAGEMENT-MIB`;
- **CPU** lives at `raisecomCpu.5.1.4` on the device (the MIB says `raisecomCpu.3.1.2`), **memory** at
  `raisecomMemory.N.0` (the MIB says `raisecomMemory.2.N.0`);
- the device returns `raisecomMemoryUtil` and `raisecomOpticalTransceiverWaveTolerance` as INTEGER, not OCTET STRING;
- the **wavelength** is reported in nm, although the MIB says pm;
- **DDM parameter 5** is the 3.3 V supply voltage in mV (the vendor MIB calls it `laserTemperature`);
- hyphens were removed from enum labels, since SMIv2 forbids them (`high-alarm` → `highAlarm`, `xfp-e` → `xfpE`);
- there are no vendor MIBs for PoE, device information and port descriptions. These objects were reconstructed
  from device data and are marked as such in their DESCRIPTION.

DDM units (`OpticalParameterValue`, DISPLAY-HINT `d-3`): temperature in m°C, bias current in µA, Tx/Rx in
dBm×1000, voltage in mV. net-snmp already shows them in base units: `43.500`, `-4.271`, `3.284`.

## Not included

Objects whose meaning is not confirmed yet are left out of the MIBs (snmpwalk shows them numerically):

- `raisecomMemory.9`, `.10` (values 9600 and 55);
- `raisecomDeviceInfo` 3, 4, 6–8, 10–12, 15;
- DDM table columns 13–24 (OCTET STRING, not described in the vendor MIB);
- column 13 of the PoE port table (0 on every port, the test did not touch it);
- PSE table columns 4, 6, 7: these are the PSE operation status, the global administrator status and the power
  mode, but telling which is which requires disabling PoE on the whole switch.

## How the PoE settings were decoded

Columns 7–15 of the PoE port table were identified differentially: on the idle port 1/1/1 one setting at a time
was changed through the CLI, and SNMP was read before and after. All settings were then restored, and the final
state matched the initial one.

| Column | Object | CLI | Verified |
|---|---|---|---|
| 7 | `raisecomPoePortAdminStatus` | `poe enable/disable` | changes together with 15; identified as admin by the table structure |
| 8 | `raisecomPoePortForcePower` | `poe force-power` | by value (the only 2 = Disable) |
| 9 | `raisecomPoePortScheduleProfile` | `poe schedule profile` | by position only (profile 1 was not applied) |
| 10 | `raisecomPoePortAliveCheck` | `poe alive check enable/disable` | yes |
| 11 | `raisecomPoePortAliveAction` | `poe alive action …`: none 0, reboot 1, reboot-alarm 2, alarm 3 | yes |
| 12 | `raisecomPoePortRebootInterval` | `poe reboot interval` | yes (30 → 60) |
| 14 | `raisecomPoePortLinktraceTrack` | `poe linktrace track <1-128>` | yes |
| 15 | `raisecomPoePortOperStatus` | operation / actual running status | changes together with 7 |

The PSE table was matched against `show poe pse`: C1 is Average Power(mW), C2 Peak Power(mW), C8 Wattage Rating(W).
C3 (`raisecomPoePseLegacyDetection`, `poe legacy`) and C5 (`raisecomPoePseTemperatureProtection`,
`poe temperature-protection`) were confirmed by toggling them.

Column 3 (`raisecomPoePortPowerInstant`) is the "Output Power(mW)" of `show poe interface` (100 mW steps), and
column 2 (`raisecomPoePortPower`) is its smoothed average.

## Checking

```bash
make mibcheck
```

The script checks three things:
- the self-contained file loads from an empty directory with only the standard MIBs next to it;
- the modular set loads without net-snmp diagnostics;
- every private OID the exporter reads (from `testdata/iscom2624g.walk`) resolves to a name, the same in both
  variants.

In addition, both variants were checked with the strict pysmi compiler (`mibdump`).
