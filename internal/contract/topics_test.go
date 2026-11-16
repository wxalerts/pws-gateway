package contract_test

import (
	"errors"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/contract"
)

func TestTopics(t *testing.T) {
	if got := contract.Topic("d_8f3a", contract.KindObs); got != "pws/d_8f3a/obs" {
		t.Errorf("Topic = %q", got)
	}
	if got := contract.CommandTopic("d_8f3a", contract.CmdConfig); got != "pws/d_8f3a/cmd/config" {
		t.Errorf("CommandTopic = %q", got)
	}
	if got := contract.CommandFilter("d_8f3a"); got != "pws/d_8f3a/cmd/#" {
		t.Errorf("CommandFilter = %q", got)
	}
	if got := contract.AckTopic("d_8f3a", "c_01"); got != "pws/d_8f3a/ack/c_01" {
		t.Errorf("AckTopic = %q", got)
	}
}

func TestParseTopic(t *testing.T) {
	good := map[string]contract.ParsedTopic{
		"pws/d_8f3a/status":     {DeviceID: "d_8f3a", Kind: contract.KindStatus},
		"pws/d_8f3a/obs":        {DeviceID: "d_8f3a", Kind: contract.KindObs},
		"pws/d_8f3a/cmd/reboot": {DeviceID: "d_8f3a", Kind: contract.KindCmd, Suffix: "reboot"},
		"pws/d_8f3a/ack/c_01":   {DeviceID: "d_8f3a", Kind: contract.KindAck, Suffix: "c_01"},
	}
	for topic, want := range good {
		got, err := contract.ParseTopic(topic)
		if err != nil {
			t.Errorf("%s: %v", topic, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", topic, got, want)
		}
	}
	for _, topic := range []string{
		"", "pws", "pws/d_8f3a", "pws//obs", "other/d_8f3a/obs", "pws/d_8f3a/obs/extra",
		"pws/d_8f3a/cmd", "pws/d_8f3a/cmd/", "pws/d_8f3a/cmd/a/b", "pws/d_8f3a/nope",
	} {
		if _, err := contract.ParseTopic(topic); !errors.Is(err, contract.ErrBadTopic) {
			t.Errorf("%q: want ErrBadTopic, got %v", topic, err)
		}
	}
}
