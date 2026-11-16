package platform

import (
	"fmt"
	"strconv"
	"strings"
)

// Bit positions of the firmware get_throttled mask. See Throttled for the
// meaning of each bit.
const (
	BitUnderVoltageNow       = 1 << 0
	BitFreqCappedNow         = 1 << 1
	BitThrottledNow          = 1 << 2
	BitSoftTempLimitNow      = 1 << 3
	BitUnderVoltageOccurred  = 1 << 16
	BitFreqCappedOccurred    = 1 << 17
	BitThrottledOccurred     = 1 << 18
	BitSoftTempLimitOccurred = 1 << 19
)

// DecodeThrottled expands the raw firmware mask into named flags.
func DecodeThrottled(raw uint32) Throttled {
	return Throttled{
		Raw:                   raw,
		Known:                 true,
		UnderVoltageNow:       raw&BitUnderVoltageNow != 0,
		FreqCappedNow:         raw&BitFreqCappedNow != 0,
		ThrottledNow:          raw&BitThrottledNow != 0,
		SoftTempLimitNow:      raw&BitSoftTempLimitNow != 0,
		UnderVoltageOccurred:  raw&BitUnderVoltageOccurred != 0,
		FreqCappedOccurred:    raw&BitFreqCappedOccurred != 0,
		ThrottledOccurred:     raw&BitThrottledOccurred != 0,
		SoftTempLimitOccurred: raw&BitSoftTempLimitOccurred != 0,
	}
}

// ParseThrottled parses the text form of the register. The sysfs file prints
// "0x50005\n"; vcgencmd prints "throttled=0x50005". Both are accepted, as is a
// bare decimal number.
func ParseThrottled(s string) (Throttled, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "throttled=")
	if s == "" {
		return Throttled{}, fmt.Errorf("platform: empty get_throttled value")
	}
	v, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return Throttled{}, fmt.Errorf("platform: parse get_throttled %q: %w", s, err)
	}
	return DecodeThrottled(uint32(v)), nil
}
