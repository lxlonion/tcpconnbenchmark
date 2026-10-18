package tester

import (
	"encoding/csv"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestEngine creates an Engine whose logs live under a temporary config
// directory instead of the current user's real application data.
func newTestEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))

	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func baseConfig() Config {
	return Config{
		Mode:        Mode80,
		Domains:     []string{"127.0.0.1"},
		DurationSec: 1,
		FailStop:    0,
		RatePerSec:  100,
	}
}

func closedTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func acceptingListener(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := l.(*net.TCPListener)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func readResultCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(records) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return records[1:]
}

func countResult(rows [][]string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		if len(row) >= 5 {
			counts[row[4]]++
		}
	}
	return counts
}

func TestAttemptAfterCancelIsNotCounted(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	defer e.logger.close()

	e.cancel()
	e.attempt(target{domain: "127.0.0.1", port: 80})

	snap := e.Snapshot()
	if snap.Attempts != 0 || snap.Aborted != 0 {
		t.Fatalf("canceled attempt counted: attempts=%d aborted=%d", snap.Attempts, snap.Aborted)
	}
	if logs, _ := e.LogsSince(0, 10); len(logs) != 0 {
		t.Fatalf("canceled attempt produced %d log entries", len(logs))
	}
}

func TestDurationEndCountsAndLogsEveryAttempt(t *testing.T) {
	port := closedTCPPort(t)
	for i := 0; i < 3; i++ {
		e := newTestEngine(t, Config{
			Mode:        Mode80,
			Domains:     []string{"127.0.0.1"},
			DurationSec: 1,
			FailStop:    0,
			RatePerSec:  1000,
		})
		e.targets = []target{{domain: "127.0.0.1", port: port}}
		e.Start()
		e.Wait()

		snap := e.Snapshot()
		if want := snap.Success + snap.Failed + snap.Aborted; snap.Attempts != want {
			t.Fatalf("run %d: attempts=%d, success+failed+aborted=%d", i, snap.Attempts, want)
		}

		rows := readResultCSV(t, e.logger.csvPath)
		if uint64(len(rows)) != snap.Attempts {
			t.Fatalf("run %d: csv rows=%d, attempts=%d", i, len(rows), snap.Attempts)
		}
		counts := countResult(rows)
		if uint64(counts["failed"]) != snap.Failed {
			t.Fatalf("run %d: csv failed=%d, snapshot failed=%d", i, counts["failed"], snap.Failed)
		}
		if uint64(counts["aborted"]) != snap.Aborted {
			t.Fatalf("run %d: csv aborted=%d, snapshot aborted=%d", i, counts["aborted"], snap.Aborted)
		}
	}
}

func TestSuccessfulRunClosesConnectionsAndLogsAllAttempts(t *testing.T) {
	ln := acceptingListener(t)
	e := newTestEngine(t, Config{
		Mode:        Mode80,
		Domains:     []string{"127.0.0.1"},
		DurationSec: 1,
		FailStop:    0,
		RatePerSec:  200,
	})
	e.targets = []target{{domain: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port}}
	e.Start()
	e.Wait()

	snap := e.Snapshot()
	if snap.Attempts < 100 {
		t.Fatalf("too few attempts: %d", snap.Attempts)
	}
	if snap.Failed != 0 {
		t.Fatalf("unexpected failures: failed=%d", snap.Failed)
	}
	if snap.Active != 0 || snap.Closed != snap.Success {
		t.Fatalf("connections not closed: active=%d closed=%d success=%d", snap.Active, snap.Closed, snap.Success)
	}

	rows := readResultCSV(t, e.logger.csvPath)
	if uint64(len(rows)) != snap.Attempts {
		t.Fatalf("csv rows=%d, attempts=%d", len(rows), snap.Attempts)
	}
	counts := countResult(rows)
	if uint64(counts["success"]) != snap.Success {
		t.Fatalf("csv success=%d, snapshot success=%d", counts["success"], snap.Success)
	}
	if counts["failed"] != 0 {
		t.Fatalf("unexpected CSV failures: %v", counts)
	}
}

func TestFirstAttemptStartsImmediately(t *testing.T) {
	e := newTestEngine(t, Config{
		Mode:        Mode80,
		Domains:     []string{"127.0.0.1"},
		DurationSec: 1,
		FailStop:    0,
		RatePerSec:  1,
	})
	e.targets = []target{{domain: "127.0.0.1", port: closedTCPPort(t)}}
	e.Start()
	e.Wait()

	snap := e.Snapshot()
	if snap.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (first attempt should start at t=0)", snap.Attempts)
	}
	if snap.Success+snap.Failed+snap.Aborted != 1 {
		t.Fatalf("completed attempts = %d, want 1", snap.Success+snap.Failed+snap.Aborted)
	}
	rows := readResultCSV(t, e.logger.csvPath)
	if len(rows) != 1 {
		t.Fatalf("csv rows: %v", rows)
	}
}

func TestFailStopStopsAtThreshold(t *testing.T) {
	e := newTestEngine(t, Config{
		Mode:        Mode80,
		Domains:     []string{"127.0.0.1"},
		DurationSec: 10,
		FailStop:    5,
		RatePerSec:  20,
	})
	e.targets = []target{{domain: "127.0.0.1", port: closedTCPPort(t)}}
	e.Start()
	e.Wait()

	snap := e.Snapshot()
	if snap.Failed != 5 {
		t.Fatalf("failed=%d, want 5 (attempts=%d)", snap.Failed, snap.Attempts)
	}
	if snap.StopReason != "失败次数达到上限" {
		t.Fatalf("stop reason = %q", snap.StopReason)
	}
	if snap.Attempts != snap.Success+snap.Failed+snap.Aborted {
		t.Fatalf("attempts=%d, success+failed+aborted=%d", snap.Attempts, snap.Success+snap.Failed+snap.Aborted)
	}
	rows := readResultCSV(t, e.logger.csvPath)
	if uint64(len(rows)) != snap.Attempts {
		t.Fatalf("csv rows=%d, attempts=%d", len(rows), snap.Attempts)
	}
}

func TestAbortedAttemptIsLogged(t *testing.T) {
	e := newTestEngine(t, baseConfig())

	started := time.Now().Add(-10 * time.Millisecond)
	e.recordAborted(target{domain: "127.0.0.1", port: 80}, "127.0.0.1", started)

	snap := e.Snapshot()
	if snap.Aborted != 1 || snap.Failed != 0 || snap.Success != 0 {
		t.Fatalf("counters after abort: aborted=%d failed=%d success=%d", snap.Aborted, snap.Failed, snap.Success)
	}
	logs, _ := e.LogsSince(0, 10)
	if len(logs) != 1 || !logs[0].Aborted || logs[0].OK {
		t.Fatalf("live log entry: %+v", logs)
	}
	if logs[0].Domain != "127.0.0.1" || logs[0].Port != 80 || logs[0].IP != "127.0.0.1" {
		t.Fatalf("live log target: %+v", logs[0])
	}

	if err := e.logger.close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}
	rows := readResultCSV(t, e.logger.csvPath)
	if len(rows) != 1 || rows[0][4] != "aborted" {
		t.Fatalf("csv rows: %v", rows)
	}
}

func TestSummaryOmitsUndefinedSuccessRate(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	e.finish()

	raw, err := os.ReadFile(e.logger.summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := string(raw)
	if !strings.Contains(summary, "成功率: —") {
		t.Fatalf("summary has no undefined success rate marker:\n%s", summary)
	}
	if strings.Contains(summary, "成功率: 0.00%") {
		t.Fatalf("summary reports 0.00%% with no completed attempts:\n%s", summary)
	}
}

func TestLiveLogEntriesStayInSequence(t *testing.T) {
	const n = 5000
	e := newTestEngine(t, baseConfig())
	defer e.logger.close()
	e.ring = newLogRing(n + 10)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.writeEntry(LogEntry{Domain: "example.com", Port: 80, OK: true, LatencyMS: float64(i)}, time.Now())
		}(i)
	}
	wg.Wait()

	logs, _ := e.LogsSince(0, n+10)
	if len(logs) != n {
		t.Fatalf("got %d log entries, want %d", len(logs), n)
	}
	for i := 1; i < len(logs); i++ {
		if logs[i].Seq != logs[i-1].Seq+1 {
			t.Fatalf("out-of-order sequence at %d: prev=%d current=%d", i, logs[i-1].Seq, logs[i].Seq)
		}
	}
}

func TestTargetSequence(t *testing.T) {
	e := newTestEngine(t, Config{
		Mode:        ModeBoth,
		Domains:     []string{"example.com", "example.net"},
		DurationSec: 1,
		FailStop:    0,
		RatePerSec:  1,
	})
	defer e.logger.close()

	want := []target{
		{domain: "example.com", port: 80},
		{domain: "example.com", port: 443},
		{domain: "example.net", port: 80},
		{domain: "example.net", port: 443},
	}
	if !reflect.DeepEqual(e.targets, want) {
		t.Fatalf("targets = %+v, want %+v", e.targets, want)
	}
}
