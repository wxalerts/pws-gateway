# Direct-attached sensors (DS3231, BMP280)

## Goal

Users who plug a DS3231 RTC or a BMP280 pressure sensor into the Pi header get
help from the web UI to enable and read them.

## Design

- UI action to enable I2C. It requires a reboot, applied through the same
  rollback-safe path as other boot config changes.
- I2C scan page. Expected addresses: DS3231 at `0x68`, BMP280/BME280 at `0x76`
  or `0x77`. Read the chip ID to tell them apart: `0x58` is BMP280, `0x60` is
  BME280. Cheap boards are often mislabeled.
- Built-in wiring help: 3.3 V (pin 1), SDA (GPIO2, pin 3), SCL (GPIO3, pin 5),
  GND (pin 6).
- A BMP280 appears as a station in the same pipeline, with its own station key.
  Use its pressure only: temperature is biased by Pi heat. User sets altitude,
  same as any barometer.
- DS3231 feeds the time sync stack (see [Time sync and RTC](time-sync-and-rtc.md)).

## Repo notes (2026-11-16)

- I2C bus access, the boot config edit that enables I2C, and the chip ID reads
  go through `internal/platform/`. Tests use a fake bus that returns the
  addresses and chip IDs above.
- The BMP280 station key format is a repo choice to be made when it is built;
  the Notion page only says "its own station key".

## Implementation notes (M7)

- The BMP280 station key is `BMP280:i2c1-0x76:` (model, bus and address, empty
  channel), so a second sensor on 0x77 is its own station. It reports
  `pressure_station_hpa` only and never a `temp_c`.
- A BME280 (chip ID `0x60`) and a BME680 (`0x61`) are identified by the scan
  but are not used as stations yet.
- A DS3231 bound by the `i2c-rtc` overlay shows as busy to `i2cdetect`. The
  daemon reads it with forced addressing and only ever reads, so the kernel
  driver is not disturbed.
- Enabling I2C edits `config.txt` through the root helper
  (`deploy/systemd/pwsgw-priv.path`) under the commit-confirm machine in
  `internal/bootchange`. The pending change is stored in the data directory,
  survives the reboot, and is rolled back if it is not confirmed in 15 minutes.
