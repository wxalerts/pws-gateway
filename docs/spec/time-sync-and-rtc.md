# Time sync and RTC

## Goal

Correct timestamps on every observation. A Pi 3B+ has no real-time clock, so
after an offline boot its clock is wrong until it syncs.

## Decision

- Wait for NTP or cellular network time before stamping and uplinking
  observations.

## Design

- Sources: NTP, LTE network time (ModemManager), GPS (gpsd), all disciplined by
  chrony.
- Until synced, the collector buffers observations with monotonic timestamps.
  After sync, it converts them using the measured offset.
- Optional DS3231 RTC (see
  [Direct-attached sensors](direct-attached-sensors-ds3231-bmp280.md)).
  Proposed: treat it as provisional time after a power loss, and check the
  oscillator-stop flag and battery state. Disable fake-hwclock when an RTC is
  present.
- The UI shows the current time source, offset, and last sync.
- Time state is reported over MQTT (`tel.time`). Unsynced observations are
  flagged, never silently uplinked.

## Open

- The RTC-as-provisional-time proposal is marked "proposed" in the source. It
  has not been decided whether RTC time alone is good enough to stamp and
  uplink, or only to order buffered samples until a network source arrives.

## Implementation notes (M7)

- `tel.time.src` is the class of the source chrony has selected: `ntp`, `gps`
  or `lte`. While unsynced it is `rtc` when a DS3231 is present and its
  oscillator-stop flag is clear, otherwise `none`. An RTC never marks the clock
  synced, so observations stay held until a network or GPS source answers.
- When a DS3231 answers, the daemon disables `fake-hwclock` (through the root
  helper) and re-checks every ten minutes.
- The oscillator-stop flag is shown on the time page. The `tel` schema has no
  key for it yet; a stopped oscillator only keeps `time.src` from being `rtc`.
- `deploy/chrony/pwsgw.conf` adds the gpsd shared-memory sources and a socket
  source that the daemon feeds with the modem's network time.
