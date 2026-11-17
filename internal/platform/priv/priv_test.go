package priv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const sample = `# For more options see the documentation
dtparam=audio=on
#dtparam=i2c_arm=on
dtoverlay=vc4-kms-v3d
[cm4]
otg_mode=1
[all]
dtoverlay=i2c-rtc,ds3231
`

func TestEditI2CEnableUncommentsOnly(t *testing.T) {
	out := string(EditI2C([]byte(sample), true))
	if !I2CEnabled([]byte(out)) || strings.Count(out, "i2c_arm") != 1 {
		t.Fatalf("not enabled once:\n%s", out)
	}
	// Every other line is untouched.
	want := strings.Replace(sample, "#dtparam=i2c_arm=on", "dtparam=i2c_arm=on", 1)
	if out != want {
		t.Errorf("unexpected edit:\n%s", out)
	}
}

func TestEditI2CAppendsWhenAbsentAndDisables(t *testing.T) {
	in := "dtparam=audio=on\n[all]\n"
	on := string(EditI2C([]byte(in), true))
	if !I2CEnabled([]byte(on)) {
		t.Fatal("append did not enable")
	}
	off := string(EditI2C([]byte(on), false))
	if I2CEnabled([]byte(off)) {
		t.Fatalf("still enabled:\n%s", off)
	}
	if !strings.Contains(off, "#dtparam=i2c_arm=on") {
		t.Error("the disabled line should be kept as a comment")
	}
	if I2CEnabled([]byte("dtparam=i2c_arm=off\n")) {
		t.Error("explicit off")
	}
}

func executor(t *testing.T) (Executor, string, *[]string) {
	t.Helper()
	d := t.TempDir()
	cfg := filepath.Join(d, "config.txt")
	if err := os.WriteFile(cfg, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	ex := Executor{
		BootConfig: cfg, ModulesFile: filepath.Join(d, "pwsgw-i2c.conf"),
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil, nil
		},
	}
	return ex, cfg, &calls
}

func TestSubmitAndApplyRoundTrip(t *testing.T) {
	ex, cfg, _ := executor(t)
	dir := t.TempDir()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the root helper
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(RequestPath(dir)); err == nil {
				_ = Apply(context.Background(), dir, ex)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if err := Submit(context.Background(), dir, ActI2COn, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	b, _ := os.ReadFile(cfg)
	if !I2CEnabled(b) {
		t.Fatalf("config not edited:\n%s", b)
	}
	if _, err := os.Stat(cfg + ".pwsgw-orig"); err != nil {
		t.Error("original not backed up")
	}
	if m, _ := os.ReadFile(ex.ModulesFile); !strings.Contains(string(m), "i2c-dev") {
		t.Errorf("modules file = %q", m)
	}
}

func TestApplyRejectsUnlistedAction(t *testing.T) {
	ex, cfg, calls := executor(t)
	dir := t.TempDir()
	_ = os.WriteFile(RequestPath(dir), []byte(`{"id":"1","action":"rm_rf"}`), 0o640)
	if err := Apply(context.Background(), dir, ex); err == nil {
		t.Fatal("want an error")
	}
	if b, _ := os.ReadFile(cfg); string(b) != sample || len(*calls) != 0 {
		t.Error("an unlisted action must change nothing")
	}
	if rb, _ := os.ReadFile(filepath.Join(dir, "result.json")); !strings.Contains(string(rb), `"ok":false`) {
		t.Errorf("result = %s", rb)
	}
}

func TestSubmitTimesOutWithoutHelper(t *testing.T) {
	err := Submit(context.Background(), t.TempDir(), ActI2COn, 300*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestModemSignalPollRunsMmcli(t *testing.T) {
	ex, _, calls := executor(t)
	if err := ex.do(context.Background(), ActModemSignalPoll); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "mmcli -m any --signal-setup=60" {
		t.Errorf("calls = %v", *calls)
	}
}

func TestFakeHwclockDisableRunsSystemctl(t *testing.T) {
	ex, _, calls := executor(t)
	if err := ex.do(context.Background(), ActFakeHwclockDisable); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "disable --now fake-hwclock.service") {
		t.Errorf("calls = %v", *calls)
	}
}
