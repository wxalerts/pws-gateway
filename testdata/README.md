# Test recordings

Recordings of `rtl_433` JSON output, used to drive the collector without a
radio (AGENTS.md rule 3). Replay one with `rtl433.Replay` or through
`collector.NewReplayFile`; the end-to-end tests in `internal/collector`
show how.

## Real captures

| File | What it is |
| --- | --- |
| `real/acurite-433-20261116.jsonl` | 25 minutes (16:52 to 17:16 UTC on 2026-11-16, 416 lines) from a Raspberry Pi 4 with an RTL-SDR, rtl_433 22.11. |

What is in it:

- `Acurite-5n1` id 4026 channel A: the maintainer's own station, 246 lines.
  Message types 49 (wind, wind direction, rain) and 56 (wind, temperature,
  humidity) alternate every 18 s, and every transmission appears as three
  identical lines with the same timestamp and `sequence_num` 0, 1, 2.
- `Acurite-609TXC` id 224: an outdoor temperature sensor, 157 lines. One
  transmission every 57 s, each repeated five or six times within half a
  second. Humidity reads 1, which is what this sensor decodes to when it has
  no humidity element.
- `Acurite-5n1` id 2710 channel A: a neighbour's station, weak, 11 lines
  (five transmissions).
- `Hyundai-VDO` TPMS: two lines from a car passing the house. Its pressure is
  tyre pressure in kPa, not air pressure.

Capture command, exactly:

```
rtl_433 -d 0 -f 433.92M -M level -M time:iso:usec:utc -M protocol -F json
```

Timestamps carry no zone suffix (no `:tz`), so readers must treat them as UTC.
The collector does by default (`Config.Location`).

### The one edit

The two TPMS lines had their `id` value replaced with the synthetic
`0badc0de`, because a tyre sensor id can identify a vehicle. Nothing else in
the file differs from the original capture, byte for byte. The recording
contains no location fields.

## Synthetic recordings

Written by a script to match the field names rtl_433 documents for each
decoder (the same names `internal/collector/mapping` checked against
upstream), at realistic cadence, for cases the real capture does not cover.
Timestamps are zone-less UTC like the real capture.

| File | Cadence | Covers |
| --- | --- | --- |
| `synthetic/fineoffset-wh65b.jsonl` | 14 min, 16 s | A native SI station (m/s wind, mm rain, battery flag) with two rain tips. |
| `synthetic/lacrosse-tx141thbv2.jsonl` | 15 min, 31 s | A temperature and humidity sensor with a channel and a raw `test` string. |
| `synthetic/counter-reset.jsonl` | 12 min, 16 s | `rain_mm` counting up to 19659.9 then wrapping to 0.3, which must be one 0.3 mm delta, not a huge negative or positive jump. |
| `synthetic/noise-false-positives.jsonl` | 12 min, sparse | One-off implausible sightings (temperature -187 C, humidity 233, wind 241 m/s), ids heard twice, and an id that glitches before its third sighting. Must yield zero stations. |

## Contributing a capture

New decoders and models need a recording (AGENTS.md, "Where things go").

1. Capture at least 20 minutes, near the sensor if you can:

   ```
   rtl_433 -f 433.92M -M level -M time:iso:usec:utc -M protocol -F json:out.jsonl
   ```

   Do not use `-R` to filter decoders: the point is to see what else is on
   the air, including the noise.
2. Check for location data before submitting. Search the file for `lat`,
   `lon`, `latitude`, `longitude` and `gps`; some decoders and some
   configurations add them. Remove those keys or do not submit.
3. Check for identifiers of other people. Tyre pressure sensors, key fobs,
   doorbells and alarm sensors have ids that can identify a vehicle or a
   home. Replace such ids with a synthetic value of the same shape, change
   nothing else, and list the edit in this file.
4. Name it `real/<brand>-<band>-<yyyymmdd>.jsonl` and add a row to the table
   above saying what the capture contains.
