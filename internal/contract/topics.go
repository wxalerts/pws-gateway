package contract

import (
	"errors"
	"fmt"
	"strings"
)

// TopicPrefix is the first segment of every topic in the contract.
const TopicPrefix = "pws"

// ErrBadTopic is returned by ParseTopic for a topic outside the contract.
var ErrBadTopic = errors.New("contract: topic is not part of the PWS contract")

// Topic returns pws/{deviceID}/{kind} for the single segment kinds (status,
// obs, tel, event, update). Use CommandTopic and AckTopic for cmd and ack.
func Topic(deviceID string, kind Kind) string {
	return TopicPrefix + "/" + deviceID + "/" + string(kind)
}

// CommandTopic returns pws/{deviceID}/cmd/{type}.
func CommandTopic(deviceID, cmdType string) string {
	return Topic(deviceID, KindCmd) + "/" + cmdType
}

// CommandFilter returns the subscription filter pws/{deviceID}/cmd/# that a
// device subscribes to. The broker ACL allows nothing else.
func CommandFilter(deviceID string) string {
	return Topic(deviceID, KindCmd) + "/#"
}

// AckTopic returns pws/{deviceID}/ack/{cmdID}.
func AckTopic(deviceID, cmdID string) string {
	return Topic(deviceID, KindAck) + "/" + cmdID
}

// ParsedTopic is the result of ParseTopic.
type ParsedTopic struct {
	DeviceID string
	Kind     Kind
	// Suffix is the command type for cmd topics and the command id for ack
	// topics. Empty for other kinds.
	Suffix string
}

// ParseTopic splits a topic from the contract into its parts. It accepts
// exactly pws/{device_id}/{kind} for the single segment kinds and
// pws/{device_id}/{cmd|ack}/{suffix} for the two segment kinds.
func ParseTopic(topic string) (ParsedTopic, error) {
	parts := strings.Split(topic, "/")
	if len(parts) < 3 || parts[0] != TopicPrefix || parts[1] == "" {
		return ParsedTopic{}, fmt.Errorf("%w: %q", ErrBadTopic, topic)
	}
	p := ParsedTopic{DeviceID: parts[1], Kind: Kind(parts[2])}
	switch p.Kind {
	case KindStatus, KindObs, KindTel, KindEvent, KindUpdate:
		if len(parts) != 3 {
			return ParsedTopic{}, fmt.Errorf("%w: %q has extra segments", ErrBadTopic, topic)
		}
	case KindCmd, KindAck:
		if len(parts) != 4 || parts[3] == "" {
			return ParsedTopic{}, fmt.Errorf("%w: %q needs exactly one segment after %s", ErrBadTopic, topic, p.Kind)
		}
		p.Suffix = parts[3]
	default:
		return ParsedTopic{}, fmt.Errorf("%w: unknown kind %q in %q", ErrBadTopic, parts[2], topic)
	}
	return p, nil
}
