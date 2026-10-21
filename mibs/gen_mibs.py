#!/usr/bin/env python3
"""Generates the Raisecom ISCOM2600G MIB files from one specification.

    python3 mibs/gen_mibs.py            # writes both variants next to this script

Outputs:
  RAISECOM-ISCOM2600G-MIB.mib   single self-contained module (imports only SNMPv2-SMI/TC/CONF)
  modules/*.mib                 conventional set: one module per file, linked by IMPORTS

Both variants are rendered from the same objects below, so a descriptor always
names the same OID in either variant. OIDs and types follow what an
ISCOM2624G-4GE-PWR-AC running software 3.60.311 (ROS 5.2.1) actually returns;
where that differs from Raisecom's published MIBs the device wins and the
DESCRIPTION says so.
"""
import os
import re
import textwrap

HERE = os.path.dirname(os.path.abspath(__file__))
REVISION = "202610210000Z"
ORG = "raisecom-snmp-exporter project (unofficial, not a Raisecom publication)"
CONTACT = "See README.md in the raisecom-snmp-exporter repository."
RECON = ("Reconstructed from data returned by ISCOM2624G-4GE-PWR-AC, software 3.60.311 (ROS 5.2.1); "
         "Raisecom publishes no MIB for this object.")
DEVICE_NOTE = "Position verified on software 3.60.311 (ROS 5.2.1), where it differs from the published Raisecom MIB."

# --------------------------------------------------------------------------- model


class Node:
    def __init__(self, module, name, kind, parent, arc, **kw):
        self.module, self.name, self.kind, self.parent, self.arc = module, name, kind, parent, arc
        self.syntax = kw.get("syntax")
        self.access = kw.get("access", "read-only")
        self.units = kw.get("units")
        self.status = kw.get("status", "current")
        self.desc = kw.get("desc", "")
        self.index = kw.get("index", [])
        self.columns = []
        self.entry = None


class Module:
    def __init__(self, name, identity, parent, arc, desc):
        self.name, self.identity, self.desc = name, identity, desc
        self.nodes, self.tcs = [], []
        self.ident = Node(self, identity, "identity", parent, arc, desc=desc)
        self.conformance_arc = None


MODULES = []
TCS = {}          # name -> (module, syntax, desc, hint)
NODES = {}        # name -> Node


def module(name, identity, parent, arc, desc, conformance_arc=100):
    m = Module(name, identity, parent, arc, desc)
    m.conformance_arc = conformance_arc
    MODULES.append(m)
    NODES[identity] = m.ident
    return m


def tc(m, name, syntax, desc, hint=None):
    TCS[name] = (m, syntax, desc, hint)
    m.tcs.append(name)


def oid(m, name, parent, arc):
    n = Node(m, name, "oid", parent, arc)
    m.nodes.append(n)
    NODES[name] = n
    return n


def scalar(m, name, parent, arc, syntax, desc, **kw):
    n = Node(m, name, "scalar", parent, arc, syntax=syntax, desc=desc, **kw)
    m.nodes.append(n)
    NODES[name] = n
    return n


def table(m, name, parent, arc, desc, entry, index, entry_desc):
    t = Node(m, name, "table", parent, arc, desc=desc)
    e = Node(m, entry, "entry", name, 1, desc=entry_desc, index=index)
    t.entry = e
    m.nodes += [t, e]
    NODES[name] = t
    NODES[entry] = e
    return t


def column(t, name, arc, syntax, desc, **kw):
    n = Node(t.module, name, "column", t.entry.name, arc, syntax=syntax, desc=desc, **kw)
    t.entry.columns.append(n)
    t.module.nodes.append(n)
    NODES[name] = n
    return n


# --------------------------------------------------------------------------- specification

# RAISECOM-BASE-MIB ------------------------------------------------------------
base = module("RAISECOM-BASE-MIB", "raisecom", "enterprises", 8886,
              "Root of the Raisecom enterprise tree and textual conventions shared by the other modules.",
              conformance_arc=None)
oid(base, "raisecomAgent", "raisecom", 1)
oid(base, "iscomSeries", "raisecom", 6)
oid(base, "iscomSwitch", "iscomSeries", 1)
tc(base, "EnableVar", "INTEGER { enable(1), disable(2) }", "Enable or disable a function.")

# RAISECOM-SYSTEM-MIB ----------------------------------------------------------
sysm = module("RAISECOM-SYSTEM-MIB", "raisecomSystem", "raisecomAgent", 1,
              "CPU, memory and temperature of Raisecom ROS devices, laid out as software 3.60.311 (ROS 5.2.1) serves them.")
oid(sysm, "raisecomCpu", "raisecomSystem", 1)
oid(sysm, "raisecomCPUTableGroup", "raisecomCpu", 5)
oid(sysm, "raisecomCPUUtilizationGroup", "raisecomCPUTableGroup", 1)
t = table(sysm, "raisecomTotalCPUUtilizationTable", "raisecomCPUUtilizationGroup", 4,
          "Total CPU utilization per averaging period. " + DEVICE_NOTE,
          "raisecomTotalCPUUtilizationEntry", ["raisecomTotalCPUPeriod"], "CPU utilization for one period.")
column(t, "raisecomTotalCPUPeriod", 1,
       "INTEGER { oneSec(1), fiveSec(2), oneMin(3), tenMin(4), twoHour(5) }",
       "Period over which CPU utilization is averaged.", access="not-accessible")
column(t, "raisecomTotalCPUUtilization", 2, "Integer32 (0..100)",
       "CPU busy percentage over the period.", units="percent")

oid(sysm, "raisecomMemory", "raisecomSystem", 3)
for arc, name, syn, acc, units, desc in [
    (1, "raisecomMemoryTotal", "Integer32", "read-only", "bytes", "Total memory of the system."),
    (2, "raisecomMemoryAvailable", "Integer32", "read-only", "bytes", "Available memory of the system."),
    (3, "raisecomMemoryUtil", "Integer32 (0..100)", "read-only", "percent",
     "Current memory utilization. The published MIB types this OCTET STRING; software 3.60.311 (ROS 5.2.1) returns INTEGER."),
    (4, "raisecomMemoryTrapEnable", "EnableVar", "read-write", None, "Whether the memory trap is enabled."),
    (5, "raisecomMemoryThrshd", "Integer32 (0..100)", "read-write", "percent", "Memory utilization alarm threshold."),
    (6, "raisecomMemoryThrshdRecover", "Integer32 (0..100)", "read-write", "percent",
     "Memory utilization at which the alarm clears."),
    (7, "raisecomMemoryMonInterval", "Integer32", "read-write", "seconds", "Memory monitor interval."),
]:
    scalar(sysm, name, "raisecomMemory", arc, syn, desc + (" " + DEVICE_NOTE if arc == 1 else ""),
           access=acc, units=units)

oid(sysm, "raisecomInformation", "raisecomSystem", 4)
oid(sysm, "raisecomTemperature", "raisecomInformation", 2)
for arc, name, syn, acc, units, desc in [
    (1, "raisecomTemperatureValue", "Integer32", "read-only", "degrees Celsius", "Current board temperature."),
    (2, "raisecomTemperatureMin", "Integer32", "read-only", "degrees Celsius", "Lowest temperature since boot."),
    (3, "raisecomTemperatureMax", "Integer32", "read-only", "degrees Celsius", "Highest temperature since boot."),
    (4, "raisecomTemperatureTrapEnable", "EnableVar", "read-write", None, "Whether the temperature trap is enabled."),
    (5, "raisecomTemperatureThresholdLow", "Integer32", "read-write", "degrees Celsius", "Low temperature alarm threshold."),
    (6, "raisecomTemperatureThresholdHigh", "Integer32", "read-write", "degrees Celsius", "High temperature alarm threshold."),
    (7, "raisecomTemperatureTrapTimes", "Integer32", "read-only", None, "Number of abnormal temperature events."),
    (8, "raisecomTemperatureHighTimes", "Integer32", "read-only", None, "Number of high temperature events."),
    (9, "raisecomTemperatureLowTimes", "Integer32", "read-only", None, "Number of low temperature events."),
]:
    scalar(sysm, name, "raisecomTemperature", arc, syn, desc, access=acc, units=units)

# RAISECOM-FANMONITOR-MIB ------------------------------------------------------
fan = module("RAISECOM-FANMONITOR-MIB", "raisecomFanMonitor", "raisecomSystem", 5,
             "Fan monitoring of Raisecom ROS devices.")
oid(fan, "raisecomFanMonitorMibObjects", "raisecomFanMonitor", 2)
oid(fan, "raisecomFanMonitorGlobalGroup", "raisecomFanMonitorMibObjects", 1)
for arc, name, syn, acc, desc in [
    (1, "raisecomFanMonitorMode", "INTEGER { enforce(1), auto(2) }", "read-write",
     "Fan control mode: enforce(1) uses a fixed speed level, auto(2) follows the temperature."),
    (2, "raisecomFanMonitorSpdLevel", "Unsigned32 (1..4)", "read-write", "Speed level used in enforce mode."),
    (3, "raisecomFanMonitorNumber", "Unsigned32", "read-only", "Number of monitored fans."),
    (4, "raisecomFanMonitorLevlNumber", "Unsigned32", "read-only", "Number of fan speed levels."),
    (5, "raisecomFanCardState", "INTEGER { allDown(1), allUp(2), card1Up(3), card2Up(4) }", "read-only",
     "State of the fan cards."),
    (6, "raisecomFanCardSerialNumber", "OCTET STRING (SIZE (0..64))", "read-only", "Serial number of the fan card."),
    (7, "raisecomFanMonitorTrapSendEnable", "INTEGER { disable(0), enable(1) }", "read-write",
     "Whether fan traps are sent."),
]:
    scalar(fan, name, "raisecomFanMonitorGlobalGroup", arc, syn, desc, access=acc)
t = table(fan, "raisecomFanMonitorStateTable", "raisecomFanMonitorMibObjects", 2, "Fan state table.",
          "raisecomFanMonitorStateEntry", ["raisecomFanIndex"], "State of one fan.")
column(t, "raisecomFanIndex", 1, "Unsigned32 (1..64)", "Fan number.", access="not-accessible")
column(t, "raisecomFanSpeedValue", 2, "Unsigned32", "Current fan speed.", units="rpm")
column(t, "raisecomFanWorkState", 3, "INTEGER { normal(1), abnormal(2) }", "Current fan state.")
column(t, "raisecomFanSpeedCurrentLevel", 4, "Unsigned32 (1..4)", "Speed level currently selected by temperature.")

# RAISECOM-OPTICAL-TRANSCEIVER-MIB ---------------------------------------------
opt = module("RAISECOM-OPTICAL-TRANSCEIVER-MIB", "raisecomOpticalTransceiver", "raisecomAgent", 18,
             "Optical transceiver information and digital diagnostics (DDM) of Raisecom ROS devices.")
tc(opt, "OpticalParameterType",
   "INTEGER { transceiverTemperature(1), txBiasCurrent(2), txPower(3), rxPower(4), supplyVoltage(5) }",
   "Monitored DDM parameter. The published Raisecom MIB names value 5 laserTemperature; software 3.60.311 (ROS 5.2.1) "
   "returns the 3.3 V supply voltage there (about 3300 mV with 2900..3600 mV thresholds).")
tc(opt, "OpticalParameterValue", "Integer32 (-1000000 | -40000..6553600)",
   "DDM value in thousandths of the base unit: transceiverTemperature in millidegrees Celsius, "
   "txBiasCurrent in microamperes, txPower and rxPower in thousandths of dBm, supplyVoltage in millivolts. "
   "-1000000 means not available.", hint="d-3")
oid(opt, "raisecomOpticalTransceiverGlobalEnable", "raisecomOpticalTransceiver", 1)
for arc, name, syn, desc in [
    (1, "raisecomOpticalTransceiverNotifyEnable", "EnableVar", "Whether transceiver notifications are sent."),
    (2, "raisecomOpticalTransceiverDDMEnable", "EnableVar",
     "Whether digital diagnostics monitoring is enabled. DDM values are only populated when enabled."),
    (3, "raisecomOpticalTransceiverCheckPwdEnable", "EnableVar", "Whether transceiver password checking is enabled."),
    (4, "raisecomOpticalTransceiverPollInterval", "Integer32", "DDM poll interval."),
]:
    scalar(opt, name, "raisecomOpticalTransceiverGlobalEnable", arc, syn, desc, access="read-write",
           units="seconds" if arc == 4 else None)
oid(opt, "raisecomOpticalTransceiverObjects", "raisecomOpticalTransceiver", 2)
oid(opt, "raisecomOpticalTransceiverInfoGroup", "raisecomOpticalTransceiverObjects", 1)
t = table(opt, "raisecomOpticalTransceiverInfoTable", "raisecomOpticalTransceiverInfoGroup", 1,
          "Information about the transceiver in each pluggable port.",
          "raisecomOpticalTransceiverInfoEntry", ["ifIndex"], "Transceiver in one port.")
SUP = "INTEGER { support(1), notSupport(2) }"
for arc, name, syn, desc, units in [
    (1, "Type", "INTEGER { unknown(1), gbic(2), soldered(3), sfp(4), xbi(5), xenpak(6), xfp(7), xff(8), "
                "xfpE(9), xpak(10), x2(11), sfpj(12) }", "Physical type of the transceiver.", None),
    (2, "ConnectorType", "INTEGER { unknown(0), sc(1), db9(2), hssdc(3), bncTnc(4), fiberCoaxialHead(5), "
                         "fiberJack(6), lc(7), mtRj(8), mu(9), sg(10), fiberPigtail(11), mpoParallelOptic(12), "
                         "hssdcII(20), copper(21), rj45(22) }", "Connector type.", None),
    (3, "VendorName", "OCTET STRING (SIZE (0..32))", "Vendor name (space padded).", None),
    (4, "VendorPN", "OCTET STRING (SIZE (0..32))", "Vendor part number (space padded).", None),
    (5, "VendorSN", "OCTET STRING (SIZE (0..32))", "Vendor serial number (space padded).", None),
    (6, "MediaType", "INTEGER { singleMode(1), multiModeE50(2), multiMode50(3), multiMode625(4), copper(5), "
                     "singleModeKm(6), multiModeOM3(7) }", "Fiber or cable type.", None),
    (7, "TransmissionDistance", "Integer32", "Maximum transmission distance.", "meters"),
    (8, "Ability", "Unsigned32", "Bit mask of the DDM parameters the module supports.", None),
    (9, "DDM", SUP, "Whether the module supports digital diagnostics.", None),
    (10, "CalibrationType", "INTEGER { unknown(1), internal(2), external(3) }", "DDM calibration type.", None),
    (11, "RSSI", SUP, "Whether the module supports RSSI.", None),
    (12, "VendorRev", "OCTET STRING (SIZE (0..16))", "Vendor revision.", None),
    (13, "BRMax", "Integer32", "Maximum bit rate.", "Mbps"),
    (14, "BRMin", "Integer32", "Minimum bit rate.", "Mbps"),
    (15, "WavelengthContrl", SUP, "Whether wavelength control is supported.", None),
    (16, "Wavelength", "Integer32", "Nominal laser wavelength. The published MIB says picometres; "
                                    "software 3.60.311 (ROS 5.2.1) returns nanometres (e.g. 1310).", "nm"),
    (17, "WaveTolerance", "Integer32", "Wavelength tolerance. The published MIB types this OCTET STRING; "
                                       "software 3.60.311 (ROS 5.2.1) returns INTEGER.", None),
    (18, "Compatibility", "OCTET STRING (SIZE (0..32))", "Compatibility code, e.g. 10GBASE-SR.", None),
    (19, "PowerDissipation", "INTEGER { unknown(0), p1W5(1), p2W5(2), p3W5(3), exceed(4) }",
     "Power class. software 3.60.311 (ROS 5.2.1) returns unknown(0) for SFPs.", None),
    (20, "CDR", SUP, "Whether the module has clock and data recovery.", None),
    (21, "RefClock", "INTEGER { required(1), notRequired(2) }", "Whether a reference clock is required.", None),
    (22, "TransmitterType", "INTEGER { vcsel850nm(1), vcsel1310nm(2), vcsel1550nm(3), fp1310nm(4), dfb1310nm(5), "
                            "dfb1550nm(6), eml1310nm(7), eml1550nm(8), copperOthers(9) }", "Transmitter technology.", None),
    (24, "Cooled", "INTEGER { cooled(1), uncooled(2) }", "Whether the laser is cooled.", None),
    (25, "Tunalbe", "INTEGER { tunable(1), untunable(2) }", "Whether the laser is tunable.", None),
    (26, "DetectorType", "INTEGER { pin(1), apd(2) }", "Receiver detector type.", None),
    (27, "LineLoopBack", SUP, "Line loopback support.", None),
    (28, "XFILoopBack", SUP, "XFI loopback support.", None),
    (29, "Vps", SUP, "Variable power supply support.", None),
    (30, "TxDis", SUP, "Transmitter disable support.", None),
    (31, "Standby", SUP, "Standby mode support.", None),
    (32, "InVpsLowPower", SUP, "Low power mode support inside VPS.", None),
    (33, "OutVpsLowPower", SUP, "Low power mode support outside VPS.", None),
    (34, "FEC", SUP, "Forward error correction support.", None),
    (35, "CMU", SUP, "Clock multiplier unit support.", None),
]:
    column(t, "raisecomOpticalTransceiver" + name, arc, syn, desc, units=units)

oid(opt, "raisecomOpticalTransceiverDDMGroup", "raisecomOpticalTransceiverObjects", 2)
t = table(opt, "raisecomOpticalTransceiverDDMTable", "raisecomOpticalTransceiverDDMGroup", 1,
          "Digital diagnostics of each transceiver parameter. Rows of modules without DDM support contain zeros.",
          "raisecomOpticalTransceiverDDMEntry", ["ifIndex", "raisecomOpticalTransceiverParameterType"],
          "One monitored parameter of one transceiver.")
column(t, "raisecomOpticalTransceiverParameterType", 1, "OpticalParameterType", "Monitored parameter.",
       access="not-accessible")
for arc, name, syn, desc in [
    (2, "ParameterValue", "OpticalParameterValue", "Current value."),
    (3, "ParamHighAlarmThresh", "OpticalParameterValue", "High alarm threshold."),
    (4, "ParamHighWarningThresh", "OpticalParameterValue", "High warning threshold."),
    (5, "ParamLowAlarmThresh", "OpticalParameterValue", "Low alarm threshold."),
    (6, "ParamLowWarningThresh", "OpticalParameterValue", "Low warning threshold."),
    (7, "ParamAlarmStatus", "INTEGER { unknown(0), normal(1), highAlarm(2), highWarning(3), lowAlarm(4), lowWarning(5) }",
     "Current threshold state."),
    (8, "ParamAlarmLastValue", "OpticalParameterValue", "Value when the last alarm was raised."),
    (9, "ParamAlarmLastChange", "TimeTicks", "sysUpTime when the alarm status last changed."),
    (10, "DDM15MinValidIntervals", "Unsigned32 (0..96)", "Number of valid 15-minute history intervals."),
    (11, "DDM24HrValidIntervals", "Unsigned32 (0..7)", "Number of valid 24-hour history intervals."),
    (12, "DDMValidStatus", "INTEGER { unknown(0), valid(1), invalid(2) }", "Whether the current value is valid."),
]:
    column(t, "raisecomOpticalTransceiver" + name, arc, syn, desc)

# RAISECOM-DEVICE-MIB (reconstructed) ------------------------------------------
dev = module("RAISECOM-DEVICE-MIB", "raisecomDeviceMIB", "iscomSwitch", 1,
             "Identification of ISCOM switches. " + RECON)
oid(dev, "raisecomDeviceInfo", "raisecomDeviceMIB", 1)
for arc, name, syn, desc in [
    (1, "raisecomDeviceSoftwareVersion", "DisplayString", "Software version, e.g. 3.60.311(Compiled Mar 25 2020 18:40:44)."),
    (2, "raisecomDeviceHardwareVersion", "DisplayString", "Hardware version, e.g. A.01."),
    (5, "raisecomDevicePortNumber", "Integer32", "Number of interfaces (equals ifNumber on the reference device)."),
    (9, "raisecomDeviceMacAddress", "MacAddress", "System MAC address."),
    (13, "raisecomDeviceBootromVersion", "DisplayString", "Bootrom version."),
    (14, "raisecomDeviceSerialNumber", "DisplayString", "Serial number."),
    (16, "raisecomDeviceDescription", "DisplayString", "Model and software version, e.g. ISCOM2624G-4GE-PWR-AC 3.60.311."),
    (17, "raisecomDeviceVendor", "DisplayString", "Vendor short name."),
    (18, "raisecomDeviceManufacturer", "DisplayString", "Manufacturer full name."),
    (19, "raisecomDeviceModelName", "DisplayString", "Model name, e.g. ISCOM2624G-4GE-PWR-AC."),
]:
    scalar(dev, name, "raisecomDeviceInfo", arc, syn, desc + " " + RECON)

# RAISECOM-PORT-MIB (reconstructed) --------------------------------------------
port = module("RAISECOM-PORT-MIB", "raisecomPortMIB", "iscomSwitch", 20,
              "Port configuration of ISCOM switches. " + RECON)
oid(port, "raisecomPortObjects", "raisecomPortMIB", 1)
t = table(port, "raisecomPortTable", "raisecomPortObjects", 1, "Per-port settings.",
          "raisecomPortEntry", ["ifIndex"], "Settings of one port.")
column(t, "raisecomPortDescription", 2, "DisplayString",
       "Description configured on the port (ROS leaves IF-MIB ifAlias empty). " + RECON)

# RAISECOM-POE-MIB (reconstructed) ---------------------------------------------
poe = module("RAISECOM-POE-MIB", "raisecomPoeMIB", "iscomSwitch", 51,
             "Power over Ethernet measurements of ISCOM PoE switches, complementing POWER-ETHERNET-MIB "
             "(RFC 3621) with per-port power, voltage and current. " + RECON)
t = table(poe, "raisecomPoePortTable", "raisecomPoeMIB", 1, "Per-port PoE measurements.",
          "raisecomPoePortEntry", ["raisecomPoePortGroupIndex", "ifIndex"],
          "PoE measurements of one port, indexed like pethPsePortTable (PSE group, port ifIndex).")
column(t, "raisecomPoePortGroupIndex", 100, "Integer32 (1..2147483647)",
       "PSE group, always 1 on single-PSE switches. Matches pethPsePortGroupIndex.", access="not-accessible")
for arc, name, units, desc in [
    (1, "raisecomPoePortPeakPower", "milliwatts", "Highest power drawn on the port."),
    (2, "raisecomPoePortPower", "milliwatts", "Power drawn by the powered device, smoothed (moving average of "
                                              "raisecomPoePortPowerInstant)."),
    (3, "raisecomPoePortPowerInstant", "milliwatts", "Latest power sample, 100 mW resolution; this is the "
                                                     "'Output Power(mW)' column of CLI 'show poe interface'."),
    (4, "raisecomPoePortVoltage", "millivolts", "Output voltage ('Output Voltage(mV)' in CLI)."),
    (5, "raisecomPoePortCurrent", "milliamperes", "Output current ('Output Current(mA)' in CLI)."),
    (6, "raisecomPoePortPowerLimit", "milliwatts", "Maximum power the port may deliver ('Max Power(mW)' in "
                                                   "CLI, set with 'poe max-power')."),
]:
    column(t, name, arc, "Gauge32", desc + " " + RECON, units=units)
# Settings and status. Decoded by changing one setting at a time on an idle port and
# diffing SNMP (2026-10-21); the CLI command that sets each value is named.
VERIFIED = "Mapping verified by changing the setting via CLI and observing this column."
for arc, name, syn, units, desc in [
    (7, "raisecomPoePortAdminStatus", "EnableVar", None,
     "Administrative PoE state of the port (CLI 'poe enable|disable', shown as 'POE administrator status'). "
     "'poe disable' changes this column together with raisecomPoePortOperStatus and pethPsePortAdminEnable; "
     "it is taken as the administrative one because columns 7..14 all hold port settings."),
    (8, "raisecomPoePortForcePower", "EnableVar", None,
     "Force power without PD detection (CLI 'poe force-power', shown as 'POE force-power status')."),
    (9, "raisecomPoePortScheduleProfile", "Integer32 (0..2147483647)", None,
     "PoE schedule profile bound to the port, 0 for none (CLI 'poe schedule profile', shown as "
     "'POE schedule profile'). Inferred from the column position and the CLI field order; not verified."),
    (10, "raisecomPoePortAliveCheck", "EnableVar", None,
     "PD alive check (CLI 'poe alive check enable|disable', shown as 'POE PD check status'). " + VERIFIED),
    (11, "raisecomPoePortAliveAction", "INTEGER { none(0), reboot(1), rebootAlarm(2), alarm(3) }", None,
     "Action when the PD alive check fails (CLI 'poe alive action reboot|reboot-alarm|alarm', 'no poe alive "
     "action' gives none; shown as 'POE PD fail action', N/A for none). " + VERIFIED),
    (12, "raisecomPoePortRebootInterval", "Integer32 (0..2147483647)", "seconds",
     "Power-off time when rebooting a failed PD (CLI 'poe reboot interval', shown as "
     "'POE PD fail reboot interval'). " + VERIFIED),
    (14, "raisecomPoePortLinktraceTrack", "Integer32 (0..128)", None,
     "Track object the port follows, 0 for none (CLI 'poe linktrace track <1-128>', 'no poe linktrace' "
     "gives 0; shown as 'POE PD linktrace'). " + VERIFIED),
    (15, "raisecomPoePortOperStatus", "EnableVar", None,
     "Operational PoE state of the port (shown as 'POE operation status' / 'POE actual running status'; "
     "the two could not be told apart). Follows raisecomPoePortAdminStatus when PoE is disabled on the port."),
]:
    column(t, name, arc, syn, desc + " " + RECON, units=units)
t = table(poe, "raisecomPoePseTable", "raisecomPoeMIB", 2, "Per-PSE PoE totals.",
          "raisecomPoePseEntry", ["raisecomPoePseGroupIndex"], "Totals of one PSE.")
column(t, "raisecomPoePseGroupIndex", 100, "Integer32 (1..2147483647)",
       "PSE group, always 1 on single-PSE switches. Matches pethMainPseGroupIndex.", access="not-accessible")
for arc, name, units, desc in [
    (1, "raisecomPoePseAveragePower", "milliwatts",
     "Total power delivered, averaged by the switch ('Average Power(mW)' in CLI 'show poe pse'). "
     "The instantaneous total in whole watts is pethMainPseConsumptionPower ('Output Power(W)')."),
    (2, "raisecomPoePsePeakPower", "milliwatts", "Highest total power delivered ('Peak Power(mW)' in CLI)."),
    (8, "raisecomPoePsePower", "watts", "Power budget of the PSE ('Wattage Rating(W)' in CLI, pethMainPsePower)."),
]:
    column(t, name, arc, "Gauge32", desc + " " + RECON, units=units)
for arc, name, desc in [
    (3, "raisecomPoePseLegacyDetection", "Detection of legacy (pre-802.3af) powered devices (CLI 'poe legacy "
                                         "enable|disable', shown as 'PSE legacy detection'). " + VERIFIED),
    (5, "raisecomPoePseTemperatureProtection", "Over-temperature protection of the PSE (CLI 'poe "
                                               "temperature-protection enable|disable', shown as 'Switch "
                                               "temperature-protection'). " + VERIFIED),
]:
    column(t, name, arc, "EnableVar", desc + " " + RECON)

# --------------------------------------------------------------------------- rendering

SMI = {"MODULE-IDENTITY", "OBJECT-TYPE", "OBJECT-IDENTITY", "enterprises", "Integer32", "Unsigned32",
       "Gauge32", "Counter32", "Counter64", "TimeTicks", "IpAddress"}
TC = {"TEXTUAL-CONVENTION", "DisplayString", "MacAddress", "TruthValue"}
CONF = {"OBJECT-GROUP", "MODULE-COMPLIANCE"}
EXTERNAL = {"ifIndex": "IF-MIB"}


def q(text, indent):
    """Quoted, wrapped DESCRIPTION text."""
    text = text.replace('"', "'")
    lines = textwrap.wrap(text, 72 - len(indent))
    body = ("\n" + indent + " ").join(lines)
    return f'{indent}"{body}"'


def type_refs(syntax):
    return set(re.findall(r"\b[A-Z][A-Za-z0-9]+\b", syntax or "")) - {"INTEGER", "OCTET", "STRING", "SIZE",
                                                                       "SEQUENCE", "OF", "OBJECT", "IDENTIFIER"}


def seq_type(col):
    s = col.syntax
    if s.startswith("INTEGER"):
        return "INTEGER"
    if s.startswith("OCTET STRING"):
        return "OCTET STRING"
    return s.split()[0]


def entry_type(e):
    n = e.name
    return n[0].upper() + n[1:]


def render_node(n, single, extra_index):
    out = []
    if n.kind == "oid":
        out.append(f"{n.name} OBJECT IDENTIFIER ::= {{ {n.parent} {n.arc} }}\n")
    elif n.kind in ("scalar", "column"):
        out.append(f"{n.name} OBJECT-TYPE")
        out.append(f"    SYNTAX      {n.syntax}")
        if n.units:
            out.append(f'    UNITS       "{n.units}"')
        out.append(f"    MAX-ACCESS  {n.access}")
        out.append(f"    STATUS      {n.status}")
        out.append("    DESCRIPTION")
        out.append(q(n.desc, "        "))
        out.append(f"    ::= {{ {n.parent} {n.arc} }}\n")
    elif n.kind == "table":
        e = n.entry
        out.append(f"{n.name} OBJECT-TYPE")
        out.append(f"    SYNTAX      SEQUENCE OF {entry_type(e)}")
        out.append("    MAX-ACCESS  not-accessible")
        out.append("    STATUS      current")
        out.append("    DESCRIPTION")
        out.append(q(n.desc, "        "))
        out.append(f"    ::= {{ {n.parent} {n.arc} }}\n")
    elif n.kind == "entry":
        idx = [extra_index.get(i, i) for i in n.index] if single else n.index
        out.append(f"{n.name} OBJECT-TYPE")
        out.append(f"    SYNTAX      {entry_type(n)}")
        out.append("    MAX-ACCESS  not-accessible")
        out.append("    STATUS      current")
        out.append("    DESCRIPTION")
        out.append(q(n.desc, "        "))
        out.append(f"    INDEX       {{ {', '.join(idx)} }}")
        out.append(f"    ::= {{ {n.parent} {n.arc} }}\n")
        cols = list(n.columns)
        if single:
            cols += [c for c in extra_cols_for(n)]
        width = max(len(c.name) for c in cols)
        members = ",\n".join(f"    {c.name:<{width}}  {seq_type(c)}" for c in sorted(cols, key=lambda c: c.arc))
        out.append(f"{entry_type(n)} ::= SEQUENCE {{\n{members}\n}}\n")
    return "\n".join(out)


def extra_cols_for(entry):
    """Single-file variant: replace IF-MIB ifIndex with a local not-accessible index column."""
    if "ifIndex" not in entry.index:
        return []
    name = entry.name[:-len("Entry")] + "IfIndex"
    used = {c.arc for c in entry.columns}
    arc = next(a for a in range(100, 200) if a not in used)
    return [Node(entry.module, name, "column", entry.name, arc, syntax="Integer32 (1..2147483647)",
                 access="not-accessible",
                 desc="ifIndex of the port (IF-MIB ifIndex). Defined locally to keep this module self-contained.")]


def render_tc(name):
    _, syntax, desc, hint = TCS[name]
    out = [f"{name} ::= TEXTUAL-CONVENTION"]
    if hint:
        out.append(f'    DISPLAY-HINT "{hint}"')
    out += ["    STATUS      current", "    DESCRIPTION", q(desc, "        "), f"    SYNTAX      {syntax}\n"]
    return "\n".join(out)


def render_identity(name, parent, arc, desc, revision_desc):
    return "\n".join([
        f"{name} MODULE-IDENTITY",
        f'    LAST-UPDATED "{REVISION}"',
        f'    ORGANIZATION "{ORG}"',
        "    CONTACT-INFO",
        q(CONTACT, "        "),
        "    DESCRIPTION",
        q(desc, "        "),
        f'    REVISION     "{REVISION}"',
        "    DESCRIPTION",
        q(revision_desc, "        "),
        f"    ::= {{ {parent} {arc} }}\n",
    ])


def accessible(m):
    return [n for n in m.nodes if n.kind in ("scalar", "column") and n.access != "not-accessible"]


def render_conformance(m):
    root = m.identity
    conf, groups, compl = root + "Conformance", root + "Groups", root + "Compliances"
    g = root + "Group"
    objs = accessible(m)
    members = ",\n".join(f"        {o.name}" for o in objs)
    return "\n".join([
        f"{conf} OBJECT IDENTIFIER ::= {{ {root} {m.conformance_arc} }}",
        f"{groups} OBJECT IDENTIFIER ::= {{ {conf} 1 }}",
        f"{compl} OBJECT IDENTIFIER ::= {{ {conf} 2 }}\n",
        f"{g} OBJECT-GROUP",
        f"    OBJECTS {{\n{members}\n    }}",
        "    STATUS      current",
        "    DESCRIPTION",
        q(f"Objects of {m.name}.", "        "),
        f"    ::= {{ {groups} 1 }}\n",
        f"{root}Compliance MODULE-COMPLIANCE",
        "    STATUS      current",
        "    DESCRIPTION",
        q(f"Devices implementing {m.name}.", "        "),
        "    MODULE",
        f"        MANDATORY-GROUPS {{ {g} }}",
        f"    ::= {{ {compl} 1 }}\n",
    ])


def defined_here(m):
    s = {m.identity} | {n.name for n in m.nodes} | set(m.tcs)
    return s


def imports_block(needed, local, owner_of):
    groups = {}
    for sym in sorted(needed - local):
        if sym in SMI:
            mod = "SNMPv2-SMI"
        elif sym in TC:
            mod = "SNMPv2-TC"
        elif sym in CONF:
            mod = "SNMPv2-CONF"
        elif sym in EXTERNAL:
            mod = EXTERNAL[sym]
        else:
            mod = owner_of[sym]
        groups.setdefault(mod, []).append(sym)
    order = ["SNMPv2-SMI", "SNMPv2-TC", "SNMPv2-CONF", "IF-MIB"]
    mods = sorted(groups, key=lambda x: (order.index(x) if x in order else len(order), x))
    lines = []
    for i, mod in enumerate(mods):
        end = ";" if i == len(mods) - 1 else ""
        lines.append("    " + ",\n    ".join(groups[mod]) + f"\n        FROM {mod}{end}")
    return "IMPORTS\n" + "\n".join(lines) + "\n"


def needed_symbols(nodes, tcs, identity_parent, with_conf, single):
    need = {"MODULE-IDENTITY", identity_parent}
    if tcs:
        need.add("TEXTUAL-CONVENTION")
    for name in tcs:
        need |= type_refs(TCS[name][1])
    for n in nodes:
        if n.kind == "oid":
            need.add(n.parent)
        elif n.kind in ("scalar", "column", "table", "entry"):
            need |= {"OBJECT-TYPE", n.parent}
            need |= type_refs(n.syntax)
            if n.kind == "entry" and not single:
                need |= set(n.index)
    if with_conf:
        need |= {"OBJECT-GROUP", "MODULE-COMPLIANCE"}
    return need


HEADER = """-- {name}
--
-- Generated by mibs/gen_mibs.py; edit the generator, not this file.
-- Unofficial MIB for Raisecom ISCOM2600G switches, written from what
-- an ISCOM2624G-4GE-PWR-AC with software 3.60.311 (ROS 5.2.1) actually returns. Objects marked
-- "Reconstructed" have no published Raisecom MIB.
--
"""


def write(path, text):
    with open(path, "w", encoding="ascii") as f:
        f.write(text)


def render_modular():
    owner = {}
    for m in MODULES:
        for s in defined_here(m):
            owner[s] = m.name
    os.makedirs(os.path.join(HERE, "modules"), exist_ok=True)
    for m in MODULES:
        with_conf = m.conformance_arc is not None and accessible(m)
        need = needed_symbols(m.nodes, m.tcs, m.ident.parent, with_conf, single=False)
        body = [HEADER.format(name=m.name), f"{m.name} DEFINITIONS ::= BEGIN\n",
                imports_block(need, defined_here(m), owner),
                render_identity(m.identity, m.ident.parent, m.ident.arc, m.desc, "Initial version.")]
        body += [render_tc(t) for t in m.tcs]
        body += [render_node(n, False, {}) for n in m.nodes]
        if with_conf:
            body.append(render_conformance(m))
        body.append("END\n")
        write(os.path.join(HERE, "modules", m.name + ".mib"), "\n".join(body))


def render_single():
    name = "RAISECOM-ISCOM2600G-MIB"
    nodes, tcs = [], []
    for m in MODULES:
        if m is not base:
            nodes.append(Node(m, m.identity, "oid", m.ident.parent, m.ident.arc))
        nodes += m.nodes
        tcs += m.tcs
    need = needed_symbols(nodes, tcs, "enterprises", False, single=True)
    local = {n.name for n in nodes} | set(tcs) | {"raisecom"}
    body = [HEADER.format(name=name), f"{name} DEFINITIONS ::= BEGIN\n",
            imports_block(need, local, {}),
            render_identity("raisecom", "enterprises", 8886,
                            "Self-contained MIB with every private object the raisecom_exporter reads from "
                            "Raisecom ISCOM2600G switches: device identification, port descriptions, PoE "
                            "measurements, CPU, memory, temperature, fans and optical transceivers with DDM. "
                            "It imports only SNMPv2-SMI, SNMPv2-TC and SNMPv2-CONF. The same objects are "
                            "available split into conventional modules in mibs/modules.",
                            "Initial version.")]
    body += [render_tc(t) for t in tcs]
    for n in nodes:
        if n.kind == "oid" and n.name == n.module.identity:
            body.append(f"-- Objects of {n.module.name}\n")
        extra = {"ifIndex": extra_cols_for(n)[0].name} if n.kind == "entry" and "ifIndex" in n.index else {}
        body.append(render_node(n, True, extra))
        if n.kind == "entry":
            body += [render_node(c, True, {}) for c in extra_cols_for(n)]
    body.append("END\n")
    write(os.path.join(HERE, name + ".mib"), "\n".join(b for b in body if b))


if __name__ == "__main__":
    render_modular()
    render_single()
