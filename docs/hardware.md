# Hardware

Requirements for a WxAlerts PWS Gateway, from the "Before development handoff"
list in the spec. These are requirements and reasons, not brand endorsements.
Where the spec names a known-good example it is mentioned as such.

## Summary

| Item | Required | Requirement |
| --- | --- | --- |
| Raspberry Pi | yes | 3B+, 4 or 5, 64-bit OS, 1 GB RAM minimum |
| RTL-SDR dongle | yes | RTL2832U with R820T2 or R860 tuner and a TCXO (1 ppm or better) |
| Antenna and USB extension | yes | Antenna tuned for the band in use, away from the Pi |
| SD card | yes | A2 or at least A1 rated, 16 GB or more, from a reputable maker |
| Power supply | yes | The official supply for the Pi model, or equivalent current rating |
| Powered USB hub | for multi-device kits | Externally powered, 2 A or more total |
| DS3231 RTC | optional | I2C module with battery, at address `0x68` |
| BMP280 | optional | I2C module at `0x76` or `0x77` |
| LTE modem | optional | USB modem supported by ModemManager |
| GPS | optional | USB GPS supported by gpsd |

## Raspberry Pi

- Models: Pi 3B+ (the floor), Pi 4, Pi 5. 64-bit OS only. Pi Zero W, Pi 2 and
  Pi 1 are not supported.
- Why the floor is the 3B+: it is the oldest board with 1 GB RAM and a 64-bit
  capable SoC that is still widely owned. Everything below it is 32-bit or too
  constrained.
- Pi 3B+ limits to know about: all USB ports share one USB 2 hub with the
  Ethernet port, and the ports can supply about 1.2 A in total. Two dongles,
  or a dongle plus a modem, exceed that; hence the powered hub requirement.
- Pi 3B+ has no real-time clock. After an offline boot its clock is wrong
  until network, GPS or an attached RTC provides time; the daemon does not
  stamp observations until then.
- Pi 4 and 5 support tryboot in the bootloader, which the A/B update scheme
  uses. The equivalent for the 3B+ is still an open item.
- A heatsink or case with airflow is recommended; `rtl_433` keeps one core
  busy and the daemon reports CPU temperature and throttling flags.

## RTL-SDR dongle

Requirement: RTL2832U based dongle with an R820T2 or R860 tuner and a
temperature-compensated crystal oscillator (TCXO), 1 ppm or better.

Why a TCXO: a plain crystal drifts with temperature by tens of ppm. At 433 or
915 MHz that is tens of kHz of frequency error, and it changes as the dongle
warms up. `rtl_433` tolerates some of it, but weak sensors at the edge of
range get lost, and the spec notes that drift hurts frequency-hop tracking for
Davis. A TCXO keeps the error around 1 kHz across the day, so the gateway
hears the same stations at midnight that it heard at noon. The spec recommends
a TCXO dongle in general and requires it for the Davis module.

Other requirements:

- A metal case, for heat and shielding, is preferred.
- Each dongle covers one band at a time. 433.92 MHz and 915 MHz together need
  two dongles.
- Generic dongles often ship with identical USB serials. The Radios page can
  write a unique serial to the EEPROM (with a warning) and otherwise falls back
  to the USB port path, so this is not a purchasing requirement, only a thing
  to expect.
- Known-good examples from the SDR community are the "RTL-SDR Blog V3" and
  "V4" style dongles and the Nooelec NESDR SMArt line, all of which have a
  TCXO. Any dongle meeting the requirement above works.

## Antenna and USB extension

- An antenna tuned for the band you receive: a quarter-wave for 433 MHz is
  about 17 cm; for 915 MHz about 8 cm. The stock telescopic antennas that come
  with many dongles are acceptable to start.
- Mount the antenna away from the Pi, the hub and the power supply. A USB
  extension cable of 1 to 2 m, or an SMA extension to the antenna, moves the
  receiver away from the Pi's own noise and lets the antenna see out a window.
- Outdoor antennas need a lightning-safe installation and are outside what this
  project advises on.

## SD card

- Application class A2 (or at least A1), 16 GB or more, from a reputable
  maker. Capacity matters less than endurance: the whole history fits in well
  under 100 MB.
- Why: the daemon runs a read-only overlay root, volatile logs and batched
  15-minute commits precisely to limit writes, but the card still takes every
  rollup flush and every update. Cheap cards fail silently.
- High-endurance or industrial cards are a reasonable choice for stations in
  hot enclosures.

## Power supply

- The official Raspberry Pi supply for the model in use, or an equivalent with
  the same current rating: 2.5 A at 5.1 V for the 3B+, 3 A USB-C for the Pi 4,
  5 A USB-C PD for the Pi 5.
- Why: the Pi reports under-voltage flags, and the UI warns on them. Most
  "random" SDR dropouts and USB resets trace back to a phone charger.

## Powered USB hub

Required for multi-device kits: two or more SDRs, or an SDR plus an LTE modem
or GPS.

- Externally powered, with its own supply of 2 A or more, and USB 2.0 or 3.0.
- Why: the Pi's USB ports share a limited power budget (about 1.2 A total on
  the 3B+) and brown out under two dongles or a modem. The decision in the
  spec is to require a hub rather than build a "USB spike" board.
- The UI warns when it sees under-voltage flags or USB resets and points the
  user here.

## DS3231 real-time clock (optional)

- I2C RTC module based on the DS3231, with a coin cell, at I2C address `0x68`.
- Why: it gives the Pi a plausible time after a power loss before the network
  is up. The daemon treats it as provisional and still waits for a network
  source before uplinking, but it can order buffered samples and shortens the
  unsynced window.
- Wiring (Pi header): 3.3 V (pin 1), SDA (GPIO2, pin 3), SCL (GPIO3, pin 5),
  GND (pin 6). The UI has an I2C enable action and a scan page.

## BMP280 pressure sensor (optional)

- I2C module with a BMP280 at `0x76` or `0x77`. Cheap boards are often
  mislabeled; the daemon reads the chip ID (`0x58` BMP280, `0x60` BME280) and
  handles either.
- Why: many 433 MHz stations have no barometer. A BMP280 on the Pi gives
  station pressure, which the server converts to sea-level pressure using the
  altitude the user sets.
- Only pressure is used. The temperature reading is biased by Pi heat and is
  ignored.
- Same wiring as the DS3231; both can share the bus.

## LTE modem (optional)

- A USB LTE modem that ModemManager supports and that exposes network time.
  The spec names Quectel EC25 and Sierra Wireless modules as the likely
  starting points; the supported list is an open item.
- Why: stations without Ethernet or Wi-Fi. The daemon keeps data use low with
  15-minute batches, restricts OS image updates to Ethernet or Wi-Fi, and
  reports carrier, registration and RSSI in telemetry.
- Counts as a USB device for the powered hub requirement.
- The test Pi 4 on the owner's LAN has an LTE modem attached for M7
  integration tests.

## GPS (optional)

- A USB GPS receiver that gpsd supports (most u-blox based units do).
- Why: location without typing coordinates, and a time source for chrony. The
  exact location stays on the server; the public dashboard shows a position
  fuzzed within 1 km.
- Counts as a USB device for the powered hub requirement.
