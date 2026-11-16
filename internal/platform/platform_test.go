package platform

import "testing"

func TestDecodeThrottledUnderVoltage(t *testing.T) {
	// 0x50005: under-voltage now and occurred, throttled now and occurred.
	th, err := ParseThrottled("0x50005\n")
	if err != nil {
		t.Fatal(err)
	}
	if !th.Known {
		t.Fatal("Known should be true")
	}
	if !th.UnderVoltageNow || !th.UnderVoltageOccurred {
		t.Errorf("under-voltage flags not set: %+v", th)
	}
	if !th.ThrottledNow || !th.ThrottledOccurred {
		t.Errorf("throttled flags not set: %+v", th)
	}
	if th.FreqCappedNow || th.FreqCappedOccurred || th.SoftTempLimitNow || th.SoftTempLimitOccurred {
		t.Errorf("unexpected flags set: %+v", th)
	}
}

func TestParseThrottledForms(t *testing.T) {
	for _, in := range []string{"throttled=0x80000", "0x80000", "524288"} {
		th, err := ParseThrottled(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !th.SoftTempLimitOccurred || th.Raw != 0x80000 {
			t.Errorf("%q decoded to %+v", in, th)
		}
	}
	if _, err := ParseThrottled(""); err == nil {
		t.Error("empty input should fail")
	}
	if _, err := ParseThrottled("zz"); err == nil {
		t.Error("garbage input should fail")
	}
}

func TestIsRTLSDR(t *testing.T) {
	cases := []struct {
		dev  USBDevice
		want bool
	}{
		{USBDevice{VendorID: 0x0bda, ProductID: 0x2838}, true},
		{USBDevice{VendorID: 0x0bda, ProductID: 0x2832}, true},
		{USBDevice{VendorID: 0x0ccd, ProductID: 0x00a9}, true},
		{USBDevice{VendorID: 0x1d6b, ProductID: 0x0002}, false}, // Linux root hub
		{USBDevice{VendorID: 0x0bda, ProductID: 0x8153}, false}, // Realtek NIC
	}
	for _, c := range cases {
		if got := c.dev.IsRTLSDR(); got != c.want {
			t.Errorf("%s IsRTLSDR = %v, want %v", c.dev.ID(), got, c.want)
		}
	}
	if (USBDevice{VendorID: 0x0bda, ProductID: 0x2838}).ID() != "0bda:2838" {
		t.Error("ID formatting")
	}
}

type fakePlatform struct{ Platform }

func TestDetectUsesPriorityOrder(t *testing.T) {
	registryMu.Lock()
	saved := registry
	registry = nil
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})

	if _, err := Detect(); err == nil {
		t.Fatal("Detect with no probes should fail")
	}
	low := &fakePlatform{}
	high := &fakePlatform{}
	Register("low", 0, func() (Platform, bool) { return low, true })
	Register("never", 50, func() (Platform, bool) { return nil, false })
	Register("high", 10, func() (Platform, bool) { return high, true })
	p, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if p != high {
		t.Fatal("Detect did not pick the highest priority claiming probe")
	}
}
