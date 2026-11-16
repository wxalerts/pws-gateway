# RF capture and station discovery

## Goal

Auto-detect weather stations within range of the Pi and show them in a list.

## Design

- `rtl_433` supervised as a service, JSON output, `-M level` for RSSI, SNR and
  noise.
- One dongle tunes one band at a time. 433.92 MHz and 915 MHz together need two
  dongles. Frequency hopping misses packets because sensors transmit every
  16 to 60 s.
- Enable all weather decoders, then filter. Noise causes false positives, so a
  station is listed only after 3 or more consistent sightings with plausible
  values.
- Discovery list shows model, ID, channel, last seen, RSSI, battery flag, and
  the fields it reports.

## Update (Nov 16)

- Launch with all 258 `rtl_433` decoders enabled. The discovery filter (3 or
  more consistent sightings) stays.
- Davis Instruments: wanted. Davis Vantage transmits with frequency hopping
  across 902 to 928 MHz in the US, which `rtl_433` does not decode. A separate
  decoder (rtldavis) exists. Pre-build task: read the repo and check its
  license, CPU load on a Pi 3B+, timing requirements, and dongle sharing.
  Assume one dongle per decoder, so Davis plus 433 MHz sensors needs two
  dongles.
- v2: listen mode for new protocols (see
  [Protocol capture assist (v2)](protocol-capture-assist-v2.md)).

## Davis research (Nov 16)

- Davis Vantage uses frequency hopping across 902 to 928 MHz. `rtl_433` has no
  Davis decoder. The working RTL-SDR route is rtldavis (Go).
- bemasher/rtldavis is GPL-3.0 and archived. The maintained fork used by
  weewx-rtldavis is lheijst/rtldavis, covering Vantage Pro and Vue with US and
  EU hop sets. Check its recent activity before depending on it.
- Reports say it needs an older Go toolchain to build. Pin the toolchain in CI.
- It needs transmitter IDs configured (the `-tr` option). Confirm whether
  discovery can find them automatically.
- Cheap dongles without a TCXO drift with temperature, which hurts hop tracking.
  Recommend a TCXO dongle for Davis.
- It owns the dongle while hopping, so Davis needs its own dongle. Hopping an
  `rtl_433` dongle drops 433 MHz packets.
- Alternatives for Davis owners: a WeatherLink Live local API over the LAN
  (verify), or a USB data logger.
- Recommendation: ship Davis as a separate optional module after launch. Each
  dongle is assigned a driver (`rtl_433` or rtldavis), and both feed the same
  canonical observation format. Test with a real Davis ISS and a TCXO dongle on
  a Pi 3B+ first.

Updated 2026-11-16 in repo: the daemon is written in Go, and rtldavis is also
Go. That does not change the rule in ADR 0002: rtldavis is GPL-3.0 and runs as
a separate process driven over stdout, never linked into the MIT binary. Davis
is out of v1 scope (see `docs/out-of-scope-v1.md`).

## Testing without hardware

The collector is driven in tests by replayed `rtl_433` JSON recordings under
`testdata/` and by a sensor simulator under `tools/`. Each decoder mapping added
to `internal/collector/mapping/` ships with a recording.

## Open

- Resolved: multiple SDRs are supported with per-radio roles (see
  [Radio roles and multi-SDR](radio-roles-and-multi-sdr.md)).
- Resolved: a powered USB hub is required for multi-device kits.
- The definition of "plausible values" for the discovery filter is per field
  and not yet written down; it comes out of the sensor test matrix.
