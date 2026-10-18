package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

func TestBootstrapExposesDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))

	app := newApp(syslimit.Info{})
	if app.bootstrap.DefaultDurationSec != defaultGUIDuration {
		t.Fatalf("DefaultDurationSec = %d, want %d", app.bootstrap.DefaultDurationSec, defaultGUIDuration)
	}
	if app.bootstrap.DefaultFailStop != defaultGUIFailStop {
		t.Fatalf("DefaultFailStop = %d, want %d", app.bootstrap.DefaultFailStop, defaultGUIFailStop)
	}
	if app.bootstrap.DefaultRatePerSec != tester.DefaultRatePerSec {
		t.Fatalf("DefaultRatePerSec = %d, want %d", app.bootstrap.DefaultRatePerSec, tester.DefaultRatePerSec)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(app.bootstrapJSON()), &data); err != nil {
		t.Fatalf("bootstrap JSON: %v", err)
	}
	for _, key := range []string{"defaultDurationSec", "defaultFailStop", "defaultRatePerSec", "domains"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("bootstrap JSON missing %q", key)
		}
	}

	var duration int
	if err := json.Unmarshal(data["defaultDurationSec"], &duration); err != nil || duration != defaultGUIDuration {
		t.Fatalf("defaultDurationSec = %s (%v)", data["defaultDurationSec"], err)
	}
}
