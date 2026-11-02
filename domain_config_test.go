package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tcpconnbenchmark/internal/syslimit"
	"tcpconnbenchmark/internal/tester"
)

func TestDomainConfigSaveLoadAndRestore(t *testing.T) {
	dir := t.TempDir()
	a := newAppInDirectory(syslimit.Info{}, dir)
	t.Cleanup(func() { waitClosed(t, a.Shutdown()) })
	b := a.Bootstrap()
	if b.AppDir != dir || b.ConfigPath != filepath.Join(dir, "domains.json") || b.LogDir != filepath.Join(dir, "logs") || b.DefaultDurationSec != 360 || b.ConfigError != "" || !reflect.DeepEqual(b.Domains, b.BuiltinDomains) {
		t.Fatalf("bootstrap: %+v", b)
	}
	if _, err := os.Stat(b.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap created config: %v", err)
	}
	if _, err := os.Stat(b.LogDir); !os.IsNotExist(err) {
		t.Fatalf("bootstrap created logs: %v", err)
	}
	if r := getResult(t, a.SaveDomains(b.BuiltinDomains)); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(b.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("saving defaults created config: %v", err)
	}
	input := []string{"HTTPS://EXAMPLE.COM/path", "例子.中国", "example.com"}
	got := getResult(t, a.SaveDomains(input))
	want := []string{"example.com", "xn--fsqu00a.xn--fiqs8s"}
	if got.Err != nil || !reflect.DeepEqual(got.Value, want) {
		t.Fatalf("save = %+v", got)
	}
	data, err := os.ReadFile(b.ConfigPath)
	if err != nil || !strings.Contains(string(data), "\n  \"domains\"") {
		t.Fatalf("saved JSON: %q, %v", data, err)
	}
	stamp := time.Unix(1234567890, 0)
	if err := os.Chtimes(b.ConfigPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if r := getResult(t, a.SaveDomains(input)); r.Err != nil {
		t.Fatal(r.Err)
	}
	info, err := os.Stat(b.ConfigPath)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged config was rewritten: %v %v", info, err)
	}
	restarted := newAppInDirectory(syslimit.Info{}, dir)
	t.Cleanup(func() { waitClosed(t, restarted.Shutdown()) })
	if got := restarted.Bootstrap(); !reflect.DeepEqual(got.Domains, want) || !reflect.DeepEqual(got.BuiltinDomains, b.BuiltinDomains) {
		t.Fatalf("restart: %+v", got)
	}
	if r := getResult(t, restarted.SaveDomains(b.BuiltinDomains)); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(b.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("restore defaults did not remove config: %v", err)
	}
}

func TestBadDomainConfigIsNotRewritten(t *testing.T) {
	for _, data := range []string{"{broken", `{"domains":[]}`, `{"domains":["invalid_host"]}`, strings.Repeat("x", maxDomainConfigBytes+1)} {
		t.Run(data[:min(len(data), 20)], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "domains.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			a := newAppInDirectory(syslimit.Info{}, dir)
			t.Cleanup(func() { waitClosed(t, a.Shutdown()) })
			b := a.Bootstrap()
			if b.ConfigError == "" || !reflect.DeepEqual(b.Domains, tester.BuiltinDomains) {
				t.Fatalf("bad config bootstrap: %+v", b)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != data {
				t.Fatalf("bad config changed: %v", err)
			}
		})
	}
}

func TestSaveDomainsFailureKeepsBootstrap(t *testing.T) {
	dir := t.TempDir()
	a := newAppInDirectory(syslimit.Info{}, dir)
	t.Cleanup(func() { waitClosed(t, a.Shutdown()) })
	before := a.Bootstrap()
	if r := getResult(t, a.SaveDomains([]string{"example.com", "bad_host"})); r.Err == nil {
		t.Fatal("invalid domain accepted")
	}
	// A directory at the target makes atomic replacement fail on all platforms.
	if err := os.Mkdir(before.ConfigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := getResult(t, a.SaveDomains([]string{"example.net"})); r.Err == nil {
		t.Fatal("unwritable config target accepted")
	}
	after := a.Bootstrap()
	if !reflect.DeepEqual(before.Domains, after.Domains) {
		t.Fatalf("failed save changed bootstrap: %v -> %v", before.Domains, after.Domains)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "domains.json" {
		t.Fatalf("temporary file was left behind: %v %v", entries, err)
	}
}

func TestUnavailableAppDirectoryDoesNotFallback(t *testing.T) {
	for _, dir := range []string{"", "relative"} {
		a := newAppInDirectory(syslimit.Info{}, dir)
		b := a.Bootstrap()
		if b.AppDir != "" || b.LogDir != "" || b.ConfigPath != "" || b.ConfigError == "" {
			t.Fatalf("unsafe directory bootstrap: %+v", b)
		}
		if r := getResult(t, a.SaveDomains([]string{"example.com"})); r.Err == nil {
			t.Fatal("save accepted without app directory")
		}
		waitClosed(t, a.Shutdown())
	}
}
