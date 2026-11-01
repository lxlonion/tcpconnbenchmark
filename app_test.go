package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))
	a := newApp(syslimit.Info{})
	t.Cleanup(func() { waitClosed(t, a.Shutdown()) })
	return a
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

func getResult[T any](t *testing.T, ch <-chan result[T]) result[T] {
	t.Helper()
	if cap(ch) != 1 {
		t.Fatalf("result channel capacity = %d, want 1", cap(ch))
	}
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatal("result channel closed without a result")
		}
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("result channel returned more than one value")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("result channel did not close")
		}
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for result")
		return result[T]{}
	}
}

func validRequest() startRequest {
	return startRequest{Mode: tester.Mode80, Domains: []string{"127.0.0.1"}, DurationSec: 30, RatePerSec: 50}
}

func TestBootstrapDefaultsAndDeepCopy(t *testing.T) {
	a := testApp(t)
	// Make nested IPv4 ownership deterministic even on hosts with no interfaces.
	a.bootstrap.Interfaces = []tester.InterfaceInfo{{Name: "test", IPv4: []string{"192.0.2.1"}}}
	b := a.Bootstrap()
	if b.DefaultDurationSec != defaultGUIDuration || b.DefaultFailStop != defaultGUIFailStop || b.DefaultRatePerSec != tester.DefaultRatePerSec {
		t.Fatalf("unexpected defaults: %+v", b)
	}
	if len(b.Domains) == 0 {
		t.Fatal("missing builtin domains")
	}
	wantDomain := b.Domains[0]
	b.Domains[0] = "changed"
	b.Interfaces[0].Name = "changed"
	b.Interfaces[0].IPv4[0] = "changed"
	again := a.Bootstrap()
	if again.Domains[0] != wantDomain || again.Interfaces[0].Name != "test" || again.Interfaces[0].IPv4[0] != "192.0.2.1" {
		t.Fatalf("Bootstrap exposes internal slices: %+v", again)
	}
}

func TestStartRejectsInvalidConfig(t *testing.T) {
	a := testApp(t)
	cases := []startRequest{
		{Mode: "invalid", Domains: []string{"127.0.0.1"}, DurationSec: 1, RatePerSec: 1},
		{Mode: tester.Mode80, Domains: []string{"127.0.0.1"}, DurationSec: 0, RatePerSec: 1},
		{Mode: tester.Mode80, Domains: []string{"127.0.0.1"}, DurationSec: 1, RatePerSec: 1001},
		{Mode: tester.Mode80, Domains: []string{"127.0.0.1"}, DurationSec: 1, RatePerSec: 1, FailStop: 10000001},
		{Mode: tester.Mode80, DurationSec: 1, RatePerSec: 1},
	}
	for _, req := range cases {
		if r := getResult(t, a.Start(req)); r.Err == nil {
			t.Fatalf("accepted invalid request: %+v", req)
		}
	}
}

type fakeEngine struct {
	mu      sync.Mutex
	session string
	started bool
	stops   int
	logs    []tester.LogEntry
	done    chan struct{}
	once    sync.Once
}

func newFakeEngine(id string) *fakeEngine { return &fakeEngine{session: id, done: make(chan struct{})} }
func (e *fakeEngine) Start() {
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
}
func (e *fakeEngine) Stop(string) {
	e.mu.Lock()
	e.stops++
	e.mu.Unlock()
	e.once.Do(func() { close(e.done) })
}
func (e *fakeEngine) Wait() { <-e.done }
func (e *fakeEngine) Finished() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
func (e *fakeEngine) Snapshot() tester.Snapshot {
	return tester.Snapshot{SessionID: e.session, HasEngine: true, Finished: e.Finished()}
}
func (e *fakeEngine) LogsSince(since uint64, limit int) ([]tester.LogEntry, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if since == 0 && len(e.logs) > limit {
		return append([]tester.LogEntry(nil), e.logs[len(e.logs)-limit:]...), false
	}
	var logs []tester.LogEntry
	for _, entry := range e.logs {
		if entry.Seq > since && len(logs) < limit {
			logs = append(logs, entry)
		}
	}
	return logs, false
}

func TestStartReservationAndShutdownDuringConstruction(t *testing.T) {
	a := testApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	fake := newFakeEngine("first")
	var gotDomains []string
	a.newEngine = func(cfg tester.Config) (engine, error) {
		close(entered)
		<-release
		gotDomains = cfg.Domains
		return fake, nil
	}
	req := validRequest()
	start := a.Start(req)
	<-entered
	req.Domains[0] = "mutated"
	if r := getResult(t, a.Start(validRequest())); r.Err == nil {
		t.Fatal("second start was not rejected while factory blocked")
	}
	done := a.Shutdown()
	if done != a.Shutdown() {
		t.Fatal("Shutdown did not return same channel")
	}
	if r := getResult(t, a.Start(validRequest())); r.Err == nil {
		t.Fatal("start accepted after shutdown")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned while factory was blocked")
	default:
	}
	close(release)
	if r := getResult(t, start); r.Err != nil {
		t.Fatal(r.Err)
	}
	waitClosed(t, done)
	if len(gotDomains) != 1 || gotDomains[0] != "127.0.0.1" {
		t.Fatalf("start input not copied: %v", gotDomains)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.started || fake.stops == 0 || !fake.Finished() {
		t.Fatalf("shutdown missed starting engine: started=%v stops=%d", fake.started, fake.stops)
	}
}

func TestSessionIsolationAndTail(t *testing.T) {
	a := testApp(t)
	first, second := newFakeEngine("first"), newFakeEngine("second")
	first.logs = []tester.LogEntry{{Seq: 1}, {Seq: 2}, {Seq: 3}}
	second.logs = []tester.LogEntry{{Seq: 1}}
	engines := []engine{first, second}
	a.newEngine = func(tester.Config) (engine, error) {
		e := engines[0]
		engines = engines[1:]
		return e, nil
	}
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if tail, err := a.Tail("first"); err != nil || tail != 3 {
		t.Fatalf("tail=%d err=%v", tail, err)
	}
	r := getResult(t, a.Sample("first", 1))
	if r.Err != nil || r.Value.NextSeq != 3 || len(r.Value.Logs) != 2 || r.Value.State.SessionID != "first" {
		t.Fatalf("sample: %+v", r)
	}
	if r := getResult(t, a.Stop("first")); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Stop("first")); r.Err == nil {
		t.Fatal("stale stop accepted")
	}
	if r := getResult(t, a.Sample("first", 0)); r.Err == nil {
		t.Fatal("stale sample accepted")
	}
	if _, err := a.Tail("first"); err == nil {
		t.Fatal("stale tail accepted")
	}
	r = getResult(t, a.Sample("second", 0))
	if r.Err != nil || r.Value.State.SessionID != "second" || len(r.Value.Logs) != 1 || r.Value.NextSeq != 1 {
		t.Fatalf("new session sample: %+v", r)
	}
	if second.Finished() {
		t.Fatal("stale stop affected new session")
	}
}

type blockedSnapshotEngine struct {
	*fakeEngine
	entered chan struct{}
	release chan struct{}
	muGate  sync.Mutex
	armed   bool
}

func (e *blockedSnapshotEngine) Snapshot() tester.Snapshot {
	e.muGate.Lock()
	armed := e.armed
	e.muGate.Unlock()
	if armed {
		close(e.entered)
		<-e.release
	}
	return e.fakeEngine.Snapshot()
}

func TestInFlightSampleUsesCapturedSession(t *testing.T) {
	a := testApp(t)
	first := &blockedSnapshotEngine{fakeEngine: newFakeEngine("first"), entered: make(chan struct{}), release: make(chan struct{})}
	first.logs = []tester.LogEntry{{Seq: 1, Domain: "first"}}
	second := newFakeEngine("second")
	second.logs = []tester.LogEntry{{Seq: 1, Domain: "second"}}
	engines := []engine{first, second}
	a.newEngine = func(tester.Config) (engine, error) {
		e := engines[0]
		engines = engines[1:]
		return e, nil
	}
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	first.muGate.Lock()
	first.armed = true
	first.muGate.Unlock()
	oldSample := a.Sample("first", 0)
	<-first.entered
	if r := getResult(t, a.Stop("first")); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	close(first.release)
	r := getResult(t, oldSample)
	if r.Err != nil || r.Value.State.SessionID != "first" || len(r.Value.Logs) != 1 || r.Value.Logs[0].Domain != "first" {
		t.Fatalf("old sample crossed sessions: %+v", r)
	}
	r = getResult(t, a.Sample("second", 0))
	if r.Err != nil || r.Value.State.SessionID != "second" || len(r.Value.Logs) != 1 || r.Value.Logs[0].Domain != "second" {
		t.Fatalf("new sample crossed sessions: %+v", r)
	}
}

func TestShutdownStopsRunningEngineAndPersists(t *testing.T) {
	a := testApp(t)
	started := getResult(t, a.Start(validRequest()))
	if started.Err != nil {
		t.Fatal(started.Err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		r := getResult(t, a.Sample(started.Value.SessionID, 0))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if r.Value.State.Attempts > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no local connection attempt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitClosed(t, a.Shutdown())
	snap := a.engine.Snapshot()
	if !snap.Finished || snap.LogError != "" || snap.StopReason != "程序关闭" {
		t.Fatalf("shutdown snapshot: %+v", snap)
	}
	f, err := os.Open(snap.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || uint64(len(rows)-1) != snap.Attempts {
		t.Fatalf("CSV rows=%d attempts=%d err=%v", len(rows), snap.Attempts, err)
	}
	summary, err := os.ReadFile(snap.SummaryPath)
	if err != nil || !strings.Contains(string(summary), "程序关闭") {
		t.Fatalf("summary err=%v contents=%s", err, summary)
	}
}

func TestStopShutdownAndFinalLogsPersist(t *testing.T) {
	a := testApp(t)
	started := getResult(t, a.Start(validRequest()))
	if started.Err != nil {
		t.Fatal(started.Err)
	}
	sid := started.Value.SessionID
	deadline := time.Now().Add(3 * time.Second)
	for {
		r := getResult(t, a.Sample(sid, 0))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if r.Value.State.Attempts > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no local connection attempt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r := getResult(t, a.Stop(sid)); r.Err != nil {
		t.Fatal(r.Err)
	}
	// Wait for completion while the session is still available for final sampling.
	for !a.engine.Finished() {
		if time.Now().After(deadline) {
			t.Fatal("engine did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := getResult(t, a.Sample(sid, 0))
	if r.Err != nil || !r.Value.State.Finished || r.Value.State.LogError != "" {
		t.Fatalf("final sample: %+v", r)
	}
	snap := r.Value.State
	if uint64(len(r.Value.Logs)) != snap.Attempts {
		t.Fatalf("logs=%d attempts=%d", len(r.Value.Logs), snap.Attempts)
	}
	tail, err := a.Tail(sid)
	if err != nil || tail != r.Value.NextSeq {
		t.Fatalf("tail=%d next=%d err=%v", tail, r.Value.NextSeq, err)
	}
	last := getResult(t, a.Sample(sid, tail))
	if last.Err != nil || len(last.Value.Logs) != 0 {
		t.Fatalf("tail sample: %+v", last)
	}
	f, err := os.Open(snap.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || uint64(len(rows)-1) != snap.Attempts {
		t.Fatalf("CSV rows=%d attempts=%d err=%v", len(rows), snap.Attempts, err)
	}
	summary, err := os.ReadFile(snap.SummaryPath)
	if err != nil || !strings.Contains(string(summary), "手动停止") {
		t.Fatalf("summary err=%v contents=%s", err, summary)
	}
	waitClosed(t, a.Shutdown())
}
