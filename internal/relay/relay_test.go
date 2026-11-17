package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnershipFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "relay", "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Cases []struct {
			Owner       string `json:"owner"`
			PiSends     bool   `json:"pi_sends"`
			ServerSends bool   `json:"server_sends"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range fx.Cases {
		o := Owner(c.Owner)
		if o.PiSends() != c.PiSends || o.ServerSends() != c.ServerSends {
			t.Errorf("owner %q: pi %v server %v, want %v %v", c.Owner, o.PiSends(), o.ServerSends(), c.PiSends, c.ServerSends)
		}
		if o.PiSends() && o.ServerSends() {
			t.Errorf("owner %q relays from both sides", c.Owner)
		}
	}
}
