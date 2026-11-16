#!/usr/bin/env python3
"""Build the wxlink OOK pulse train for a sample frame in rtl_433 .ook format.

Follows lib/wxlink/src/wxlink.cpp encode() in wx433-esp32: 12 preamble pulses
(400 us pulse, 400 us gap), a sync pulse with a 2400 us gap, 176 data bits MSB
first (pulse 400 us, gap 400 us for 0 or 1000 us for 1), and a closing pulse.
The train is sent twice. Usage: gen_ook.py [output.ook]
"""
import struct
import sys

PULSE, PREAMBLE_GAP, SYNC_GAP = 400, 400, 2400
BIT0_PERIOD, BIT1_PERIOD = 800, 1400
PREAMBLE_PULSES = 12
REPEAT_SILENCE = 20000


def crc8(data):
    crc = 0
    for byte in data:
        crc ^= byte
        for _ in range(8):
            crc = ((crc << 1) ^ 0x07) & 0xFF if crc & 0x80 else (crc << 1) & 0xFF
    return crc


def frame():
    # version, nodeId, seq, tempCx100, rhX100, pressurePa, iaqX10,
    # bvocPpmX100, co2Ppm, iaqAccuracy, vbatMv
    body = struct.pack("<BBHhHIHHHBH", 1, 1, 4242, 2137, 6120, 101325, 520, 55, 612, 3, 3800)
    assert len(body) == 21
    return body + bytes([crc8(body)])


def train(wire):
    pairs = [(PULSE, PREAMBLE_GAP)] * PREAMBLE_PULSES
    pairs.append((PULSE, SYNC_GAP))
    for i in range(len(wire) * 8):
        bit = wire[i // 8] & (0x80 >> (i % 8))
        pairs.append((PULSE, (BIT1_PERIOD if bit else BIT0_PERIOD) - PULSE))
    pairs.append((PULSE, 0))  # closing pulse
    return pairs


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "wxlink-sample.ook"
    one = train(frame())
    pairs = one[:-1] + [(PULSE, REPEAT_SILENCE)] + one[:-1] + [(PULSE, REPEAT_SILENCE * 5)]
    with open(out, "w") as f:
        f.write(";pulse data\n;version 1\n;timescale 1us\n")
        for p, g in pairs:
            f.write("%d %d\n" % (p, g))


if __name__ == "__main__":
    main()
