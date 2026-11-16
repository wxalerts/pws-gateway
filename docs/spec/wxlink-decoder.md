# WxLink decoder and the rtl_433 fork

Status: implemented. Decoder protocol number 291 in the wxalerts fork.

## Why

The Stevenson screen node (STM32F411 with a BME680, firmware in wx433-esp32,
`lib/wxlink`) sends its own OOK frame on 433.92 MHz. No upstream rtl_433 decoder
knows it, so the gateway runs a fork of rtl_433 that adds one.

## Air protocol

OOK at 433.92 MHz, pulse position coding. Every bit is a 400 us pulse followed
by a gap. Pulse plus gap is 800 us for a 0 and 1400 us for a 1, so the gaps are
nominally 400 us and 1000 us.

Train, sent twice per reading, one reading every 300 s:

1. 12 preamble pulses, 400 us pulse and 400 us gap.
2. One sync pulse followed by a 2400 us gap.
3. 22 bytes, 176 bits, MSB first.
4. One closing pulse, so the last bit has a measurable gap.

Payload, packed little-endian, 21 bytes then CRC-8 (polynomial 0x07, init 0x00,
over the 21 bytes):

| Field | Type | Scale |
|---|---|---|
| version | u8 | 1 |
| nodeId | u8 | |
| seq | u16 | |
| tempCx100 | i16 | 0.01 C |
| rhX100 | u16 | 0.01 % |
| pressurePa | u32 | Pa |
| iaqX10 | u16 | 0.1 |
| bvocPpmX100 | u16 | 0.01 ppm |
| co2Ppm | u16 | ppm |
| iaqAccuracy | u8 | 0..3 |
| vbatMv | u16 | mV |

The source of truth is `lib/wxlink/src/wxlink.h` and `wxlink.cpp` in wx433-esp32.

## Decoder

`src/devices/wxlink.c` in the fork. It uses `OOK_PULSE_PPM`, which measures the
gap only: short_width 400 and long_width 1000 split at 700 us, gap_limit 1800
puts the 2400 us sync gap outside a bit so it starts a new row, and reset_limit
5000 ends a message. The preamble becomes a short row that the decoder skips.
A row of 176 bits (one extra trailing bit tolerated) is accepted if version is 1
and the CRC matches.

Output fields: `model` (`WxAlerts-WxLink`), `id`, `seq`, `temperature_C`,
`humidity`, `pressure_hPa`, `iaq`, `iaq_accuracy`, `bvoc_ppm`, `co2_ppm`,
`battery_mV`, `mic`.

## Gateway mapping

The generic rules already map `temperature_C`, `humidity` and `pressure_hPa` to
the canonical fields. `iaq`, `iaq_accuracy`, `bvoc_ppm`, `co2_ppm`,
`battery_mV` and `seq` stay in Reading.Raw. The station key is
`WxAlerts-WxLink:<id>:`. `TestWxLinkNode` covers this.

## Test vector

`testdata/wxlink/gen_ook.py` builds `wxlink-sample.ook` from the encoder rules.
Check the decoder without hardware:

    rtl_433 -r testdata/wxlink/wxlink-sample.ook -R 291 -F json

## Fork policy

- Fork: https://github.com/wxalerts/rtl_433, branch `wxalerts`, of
  https://github.com/merbanan/rtl_433.
- The branch is rebased onto each new upstream release tag. It is based on 25.12.
- The decoder is kept upstreamable: one device file plus the usual registration
  lines, written to upstream conventions, with no gateway specific changes. It
  can be offered upstream if the protocol is ever published.
- `deploy/rtl433/Dockerfile` pins the exact fork commit and the tarball
  checksum. The release workflow attaches that source to each GitHub Release,
  as the GPL requires.
- Upstream has no `src/Makefile.am` in 25.12, so only `src/CMakeLists.txt`
  registers the file.
