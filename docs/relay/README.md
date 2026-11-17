# Relay specification

This directory is the shared specification for relaying observations to third
parties, CWOP first. The Pi daemon (Go, `internal/relay/`) and the WxAlerts
ingest worker (Rust, private repo) are written independently against this text
and against the golden vectors in `testdata/relay/`. Both run the same vector
files in CI. If code and this page disagree, the vectors decide and this page
is wrong: fix it in the same pull request.

Background: [Relay plugin topology](../spec/relay-plugin-topology.md),
[Units, altitude and sensor config](../spec/units-altitude-and-sensor-config.md),
[MQTT contract](../contract/mqtt-v1-draft.md). The live CWOP and APRS-IS rules
(issue #2) are in [CWOP ingest status and APRS-IS login](cwop-aprs-is.md).

## Relay ownership

Every relayed station has one relay owner per destination: `device`, `server`
or none.

| Owner | Pi sends to CWOP | Server sends to CWOP |
| --- | --- | --- |
| `device` | yes | no |
| `server` | no | yes |
| none (absent, empty) | no | no |

- Never both for one station. Any value other than `device` or `server`
  (including a conflicting `both`) is treated as none and sends nothing.
- The Pi reads its own owner setting from its local config. It sends only when
  the owner is `device` and the plugin is enabled and valid.
- The server reads the owner from the station registry. It never relays a
  `device` station, so an owner can run an isolated Pi with no WxAlerts account.
- The table is a fixture, `testdata/relay/ownership.json`, asserted by both
  implementations.
- Switching owner is an explicit owner action. The new owner starts at the next
  bucket. There is no handover that sends a bucket twice.

## Plugin interface

A plugin is one destination. The Go shape is `relay.Plugin`; the Rust shape is
a trait with the same five operations.

| Operation | Contract |
| --- | --- |
| `configure(config)` | Load destination settings. No network access. |
| `validate(config)` | Return every problem found, with no side effects. Called before saving and before each send. |
| `map(observation)` | Pure function from one station in one bucket (plus the relay inputs below) to a destination message, or a typed error. No clock reads, no I/O. |
| `send(message)` | Deliver one message. The only operation that touches the network. Behind an interface so tests use a fake. |
| `health()` | Last success time, last error (time, stable code, redacted text), consecutive failures. |

Each plugin declares its required fields and its minimum send interval. CWOP
requires none of the weather fields (missing ones are sent as `...` or
omitted), requires a position, and a minimum interval of 5 minutes.

Delivery rules: at most one message per station per bucket, retry with
backoff (1 min, 2 min, 4 min, capped at 15 min) within the same bucket window,
then drop the bucket and record the error. CWOP is a current-conditions feed, so
late data is not replayed. Errors have a stable code, so the UI can translate
them: `no_data`, `no_position`, `bad_id`, `time_unsynced`, `connect`, `login`,
`send`.

## Inputs to `map` for CWOP

From the `obs` bucket (see the contract): `start`, `interval_s`, and for the
selected station `n`, `meta.alt_m`, `meta.wind_height_m` and `f`. Plus:

- `config.id`: the CWOP identifier.
- `config.lat`, `config.lon`: decimal degrees, WGS84. Only present when the
  owner opted in to sending a position (below).
- `rain`: the plugin's own rolling totals in mm: `last_1h_mm`, `last_24h_mm`,
  `since_midnight_mm` (local midnight). The `obs` bucket carries only the
  bucket's own rain, so the plugin keeps the sums. Absent when the plugin has
  not been running long enough to know them; the rain groups are then omitted.

The altitude and mounting height may be overridden by the owner (the registry
value on the server, the local station settings on the Pi) before `map`.

## Sea-level pressure

```
P0 = P * (1 - g / (T + g*h + 273.15)) ^ -5.257     g = 0.0065 K/m
```

`P` station pressure in hPa (`pressure_station_hpa.avg`), `T` air temperature in
C from the same bucket (`temp_c.avg`), `h` altitude in m (`meta.alt_m`). Absent
if any input is missing, if `T + g*h + 273.15 <= 0`, or if the base of the power
is not positive. Rounded to 0.1 hPa. This is exactly what the WxAlerts API
serves as `pressure_slp_hpa` (`services/pws_readings.py`, `docs/pws.md`), so the
dashboard, the server relay and the Pi relay agree. Vectors: `testdata/relay/slp.json`
(the acceptance case, 1008.3 hPa at 31 m and 18.2 C, gives 1012.0 hPa; the
implementation tolerance is 0.1 hPa).

## 10 m wind

```
v10 = v * ln(10 / z0) / ln(z / z0)        z0 = 0.03 m
```

`v` the speed in m/s, `z` the anemometer height in m (`meta.wind_height_m`).
Absent without a height or when `z <= z0`. Rounded to 0.01 m/s. This matches the
API's `wind_speed_10m_ms`. The relay applies the same factor to the gust (the
API does not serve a 10 m gust; the factor is the same profile). Direction is
not changed. Vectors: `testdata/relay/wind10m.json` (2.1 m/s at 4.5 m gives
2.43 m/s).

## CWOP APRS packet

One APRS-IS line per station per bucket, terminated by CR LF on the wire (the
golden strings omit the terminator).

```
<ID>>APRS,TCPIP*:@<DDHHMM>z<lat>/<lon>_<dir>/<spd>g<gst>t<tmp>r<r1h>p<p24h>P<pmid>h<hh>b<bbbbb>eWxPG
```

| Part | Rule |
| --- | --- |
| `<ID>` | The CWOP identifier, upper case. |
| `APRS,TCPIP*` | Fixed header, upper case. |
| `@DDHHMMz` | UTC day, hour, minute of the bucket end (`start + interval_s`). The server sets the receive time authoritatively, but a synced clock is still required. |
| `<lat>` | `ddmm.hhN` or `S`: degrees, minutes and hundredths of minutes, leading zeros. Computed as `n = round(abs(lat) * 6000)`; `deg = n / 6000`, `min.hh = (n % 6000) / 100`. |
| `<lon>` | `dddmm.hhE` or `W`, same method. |
| `_` | Weather symbol; `/` before it is the symbol table. |
| `<dir>` | Wind direction, 3 digits, degrees from true north, `vec` rounded to an integer. 0 is sent as `360`. `...` if missing. |
| `<spd>` | 10 m average wind in whole mph, 3 digits. The 10 m value in m/s is rounded to 0.01 first, then multiplied by 2.2369362920544 and rounded. `...` if missing or no mounting height. Capped at 999. |
| `g<gst>` | 10 m gust in mph, same method. |
| `t<tmp>` | Temperature in whole degrees F, 3 characters, `-` included for negatives (`-05`). `...` if missing. |
| `r`, `p`, `P` | Rain in hundredths of an inch (`mm / 0.254`, rounded), 3 digits: last hour, last 24 hours, since local midnight. All three are omitted when the rolling totals are unknown. MADIS only accepts `r` and `p`. |
| `h<hh>` | Relative humidity, whole percent, 2 digits; 100 is `00`. Omitted if missing. |
| `b<bbbbb>` | Sea-level pressure in tenths of hPa, 5 digits (the 0.1 hPa value times 10). Omitted if sea-level pressure is absent. |
| `eWxPG` | Software tag: `e` for other, then the unit name. |

All rounding is half away from zero. The order of the first four fields (dir,
speed, gust, temperature) is fixed and required by CWOP; the rest follow in the
order shown. Vectors: `testdata/relay/packet.json`, including the byte for byte
packet for the contract sample bucket.

Position privacy: the packet publishes the station position publicly on CWOP
and findU. Nothing is sent without `position_opt_in` set by the owner. The
position is the owner's entered coordinates, never the GPS fix by default, and
the local UI says it is public and exact (not fuzzed like the WxAlerts
dashboard).

Login line, sent after the server banner: `user <ID> pass <passcode> vers
pws-gateway <version>`. Non-ham IDs (`CW`, `DW`, `EW`, `FW`, `GW` plus four
digits) use passcode `-1`. A ham callsign needs its validation code (see
[cwop-aprs-is.md](cwop-aprs-is.md)). Send the packet, then close. Not more often
than every 5 minutes per station.

## Time guard

The Pi plugin sends nothing while time is unsynced. It asks the same
time-sync check the uplink uses; `time_unsynced` is the recorded reason. A
packet that cannot carry a trustworthy timestamp is worse than a missing one.

## Credentials: what must never leave the Pi

The CWOP passcode (ham callsigns only; `-1` is a public constant, not a secret)
lives in a file referenced by the config, mode 0600, exactly like the MQTT
credential. It must never appear in:

- `config.json`, MQTT payloads of any topic, the `tel` and `event` details.
- Log lines (the ring buffer served by `/api/logs`, stderr, journald), error
  strings, HTTP API responses (the relay API reports whether a passcode is set,
  never the value).
- Support bundles. The bundle builder passes every text through the plugin's
  redactor, and the credential file is never included.
- Repository files, fixtures and vectors.

The CWOP ID itself is not secret, but it is only sent to CWOP and shown in the
local UI. The server never receives the device-owned CWOP ID or passcode. For a
server-owned station the passcode (if a ham) is the server's concern and is
specified in the server design, Phase E.

## Local relay page

The device-owned case shows: owner, CWOP ID, position opt-in state, last
success time, last error (code, time, redacted text), consecutive failures,
next send time. For a server-owned station the page shows only that the server
relays it.
