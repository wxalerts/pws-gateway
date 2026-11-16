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
