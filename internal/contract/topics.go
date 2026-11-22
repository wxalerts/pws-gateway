package contract

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// TopicPrefix is the first segment of every v1 topic in the contract.
const TopicPrefix = "pws"

// Topic tree of contract v2: V2/{user_id}/PWS/{device_id}/{leaf}. The leaves
// under the device are the same as in v1. {user_id} is the owner's numeric
// account id, returned by the claim flow.
const (
	TopicPrefixV2 = "V2"
	topicPWSV2    = "PWS"
)

// ErrBadTopic is returned by ParseTopic for a topic outside the contract.
var ErrBadTopic = errors.New("contract: topic is not part of the PWS contract")

// Scope says which topic tree a device uses. A device that holds a user id
// (UserID > 0) uses the V2 tree; a device without one keeps the v1 tree, so a
// gateway on old firmware, or one claimed before the server returned a user
// id, is unchanged.
type Scope struct {
	DeviceID string
	// UserID is the owner's numeric account id. 0 means "none": v1 topics.
	UserID int64
}

// V2 reports whether the scope uses the V2 topic tree.
func (s Scope) V2() bool { return s.UserID > 0 }

// base is pws/{device} or V2/{user}/PWS/{device}.
func (s Scope) base() string {
	if s.V2() {
		return TopicPrefixV2 + "/" + strconv.FormatInt(s.UserID, 10) + "/" + topicPWSV2 + "/" + s.DeviceID
	}
	return TopicPrefix + "/" + s.DeviceID
}

// Topic returns the topic of a single segment kind (status, obs, tel, event,
// update). KindObs2 maps to the obs leaf.
func (s Scope) Topic(kind Kind) string {
	if kind == KindObs2 {
		kind = KindObs
	}
	return s.base() + "/" + string(kind)
}

// CommandTopic returns .../cmd/{type}.
func (s Scope) CommandTopic(cmdType string) string { return s.Topic(KindCmd) + "/" + cmdType }

// CommandFilter returns the subscription filter .../cmd/# that a device
// subscribes to. The broker ACL allows nothing else.
func (s Scope) CommandFilter() string { return s.Topic(KindCmd) + "/#" }

// AckTopic returns .../ack/{cmdID}.
func (s Scope) AckTopic(cmdID string) string { return s.Topic(KindAck) + "/" + cmdID }

// Topic returns pws/{deviceID}/{kind} for the single segment kinds (status,
// obs, tel, event, update). Use CommandTopic and AckTopic for cmd and ack.
// It builds the v1 tree; use Scope for the V2 tree.
func Topic(deviceID string, kind Kind) string { return Scope{DeviceID: deviceID}.Topic(kind) }

// CommandTopic returns pws/{deviceID}/cmd/{type}.
func CommandTopic(deviceID, cmdType string) string {
	return Scope{DeviceID: deviceID}.CommandTopic(cmdType)
}

// CommandFilter returns the subscription filter pws/{deviceID}/cmd/#.
func CommandFilter(deviceID string) string { return Scope{DeviceID: deviceID}.CommandFilter() }

// AckTopic returns pws/{deviceID}/ack/{cmdID}.
func AckTopic(deviceID, cmdID string) string { return Scope{DeviceID: deviceID}.AckTopic(cmdID) }

// ParsedTopic is the result of ParseTopic.
type ParsedTopic struct {
	DeviceID string
	// UserID is the account id of a V2 topic, 0 for a v1 topic.
	UserID int64
	Kind   Kind
	// Suffix is the command type for cmd topics and the command id for ack
	// topics. Empty for other kinds.
	Suffix string
}

// V2 reports whether the topic is in the V2 tree.
func (p ParsedTopic) V2() bool { return p.UserID > 0 }

// ParseTopic splits a topic from the contract into its parts. It accepts
// exactly pws/{device_id}/{kind} for the single segment kinds and
// pws/{device_id}/{cmd|ack}/{suffix} for the two segment kinds, and the same
// under V2/{user_id}/PWS/{device_id}/.
func ParseTopic(topic string) (ParsedTopic, error) {
	parts := strings.Split(topic, "/")
	var p ParsedTopic
	switch {
	case len(parts) >= 3 && parts[0] == TopicPrefix && parts[1] != "":
		p.DeviceID = parts[1]
		parts = parts[2:]
	case len(parts) >= 5 && parts[0] == TopicPrefixV2 && parts[2] == topicPWSV2 && parts[3] != "":
		uid, err := strconv.ParseInt(parts[1], 10, 64)
		// Canonical decimal only: "007" and "+7" are different topics.
		if err != nil || uid <= 0 || strconv.FormatInt(uid, 10) != parts[1] {
			return ParsedTopic{}, fmt.Errorf("%w: bad user id in %q", ErrBadTopic, topic)
		}
		p.UserID, p.DeviceID = uid, parts[3]
		parts = parts[4:]
	default:
		return ParsedTopic{}, fmt.Errorf("%w: %q", ErrBadTopic, topic)
	}
	p.Kind = Kind(parts[0])
	switch p.Kind {
	case KindStatus, KindObs, KindTel, KindEvent, KindUpdate:
		if len(parts) != 1 {
			return ParsedTopic{}, fmt.Errorf("%w: %q has extra segments", ErrBadTopic, topic)
		}
	case KindCmd, KindAck:
		if len(parts) != 2 || parts[1] == "" {
			return ParsedTopic{}, fmt.Errorf("%w: %q needs exactly one segment after %s", ErrBadTopic, topic, p.Kind)
		}
		p.Suffix = parts[1]
	default:
		return ParsedTopic{}, fmt.Errorf("%w: unknown kind %q in %q", ErrBadTopic, parts[0], topic)
	}
	return p, nil
}
