package main

import (
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tcpconnbenchmark/internal/syslimit"
	"tcpconnbenchmark/internal/tester"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := newAppInDirectory(syslimit.Info{}, t.TempDir())
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
	// Make nested address ownership deterministic even on hosts with no interfaces.
	a.bootstrap.Interfaces = []tester.InterfaceInfo{{Name: "test", IPv4: []string{"192.0.2.1"}, IPv6: []string{"2001:db8::1"}}}
	b := a.Bootstrap()
	if b.DefaultDurationSec != tester.DefaultDurationSec || b.DefaultFailStop != defaultGUIFailStop || b.DefaultRatePerSec != tester.DefaultRatePerSec {
		t.Fatalf("unexpected defaults: %+v", b)
	}
	if len(b.Domains) == 0 {
		t.Fatal("missing builtin domains")
	}
	wantDomain := b.Domains[0]
	wantBuiltin := b.BuiltinDomains[0]
	b.Domains[0] = "changed"
	b.BuiltinDomains[0] = "changed"
	b.Interfaces[0].Name = "changed"
	b.Interfaces[0].IPv4[0] = "changed"
	b.Interfaces[0].IPv6[0] = "changed"
	again := a.Bootstrap()
	if again.Domains[0] != wantDomain || again.BuiltinDomains[0] != wantBuiltin || again.Interfaces[0].Name != "test" || again.Interfaces[0].IPv4[0] != "192.0.2.1" || again.Interfaces[0].IPv6[0] != "2001:db8::1" {
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

func TestStartPassesSelectedSourceAddress(t *testing.T) {
	a := testApp(t)
	var received tester.Config
	a.newEngine = func(cfg tester.Config) (engine, error) {
		received = cfg
		return newFakeEngine("ipv6"), nil
	}
	req := validRequest()
	req.Interface, req.SourceIP = "en0", "2001:db8::10"
	if r := getResult(t, a.Start(req)); r.Err != nil {
		t.Fatal(r.Err)
	}
	if received.Interface != req.Interface || received.SourceIP != req.SourceIP {
		t.Fatalf("selected source not passed to engine: %+v", received)
	}
}

type fakeEngine struct {
	mu           sync.Mutex
	session      string
	started      bool
	attempts     int
	stops        int
	discarded    bool
	discardErr   error
	discardCalls int
	waits        int
	logs         []tester.LogEntry
	done         chan struct{}
	once         sync.Once
}

func newFakeEngine(id string) *fakeEngine { return &fakeEngine{session: id, done: make(chan struct{})} }
func (e *fakeEngine) Start() {
	e.mu.Lock()
	if e.stops == 0 {
		e.attempts++
	}
	e.started = true
	e.mu.Unlock()
}
func (e *fakeEngine) Stop(string) {
	e.mu.Lock()
	e.stops++
	e.mu.Unlock()
	e.once.Do(func() { close(e.done) })
}
func (e *fakeEngine) Wait() {
	e.mu.Lock()
	e.waits++
	e.mu.Unlock()
	<-e.done
}
func (e *fakeEngine) Finished() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
func (e *fakeEngine) Snapshot() tester.Snapshot {
	return tester.Snapshot{SessionID: e.session, Finished: e.Finished()}
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

func (e *fakeEngine) Export(dir string) (tester.ExportResult, error) {
	return tester.ExportResult{LogPath: filepath.Join(dir, "fake.csv"), SummaryPath: filepath.Join(dir, "fake.txt")}, nil
}
func (e *fakeEngine) Discard() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.discardCalls++
	if e.discardErr != nil {
		return e.discardErr
	}
	e.discarded = true
	return nil
}

func TestSampleInitialWindowAndIncrementalLimit(t *testing.T) {
	a := testApp(t)
	e := newFakeEngine("session")
	for i := 1; i <= 1000; i++ {
		e.logs = append(e.logs, tester.LogEntry{Seq: uint64(i)})
	}
	a.newEngine = func(tester.Config) (engine, error) { return e, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	initial := getResult(t, a.Sample("session", 0))
	if initial.Err != nil {
		t.Fatal(initial.Err)
	}
	if len(initial.Value.Logs) != 600 || initial.Value.Logs[0].Seq != 401 || initial.Value.Logs[599].Seq != 1000 || initial.Value.NextSeq != 1000 {
		t.Fatalf("initial sample: %+v", initial.Value)
	}
	end := getResult(t, a.Sample("session", 1000))
	if end.Err != nil || len(end.Value.Logs) != 0 || end.Value.NextSeq != 1000 {
		t.Fatalf("end sample: %+v", end)
	}
	incremental := getResult(t, a.Sample("session", 1))
	if incremental.Err != nil {
		t.Fatal(incremental.Err)
	}
	if len(incremental.Value.Logs) != 400 || incremental.Value.Logs[0].Seq != 2 || incremental.Value.Logs[399].Seq != 401 || incremental.Value.NextSeq != 401 {
		t.Fatalf("incremental sample: %+v", incremental.Value)
	}
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
	if !fake.started || fake.stops == 0 || !fake.Finished() || fake.attempts != 0 {
		t.Fatalf("shutdown missed pre-canceling engine: started=%v stops=%d attempts=%d", fake.started, fake.stops, fake.attempts)
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

func TestShutdownStopsWithoutExport(t *testing.T) {
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
	if snap.LogPath != "" || snap.SummaryPath != "" {
		t.Fatalf("shutdown unexpectedly exported: %+v", snap)
	}
	if _, err := os.Stat(a.bootstrap.LogDir); !os.IsNotExist(err) {
		t.Fatalf("shutdown created logs directory: %v", err)
	}
}

func TestStopShutdownAndFinalLogsPersist(t *testing.T) {
	a := testApp(t)
	started := getResult(t, a.Start(validRequest()))
	if started.Err != nil {
		t.Fatal(started.Err)
	}
	sid := started.Value.SessionID
	if started.Value.LogPath != "" || started.Value.SummaryPath != "" {
		t.Fatalf("start unexpectedly exported: %+v", started.Value)
	}
	if r := getResult(t, a.Export(sid)); r.Err == nil {
		t.Fatal("export before completion accepted")
	}
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
	if snap.LogPath != "" || snap.SummaryPath != "" {
		t.Fatalf("test automatically exported: %+v", snap)
	}
	if _, err := os.Stat(a.bootstrap.LogDir); !os.IsNotExist(err) {
		t.Fatalf("test created logs directory: %v", err)
	}
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
	exported := getResult(t, a.Export(sid))
	if exported.Err != nil {
		t.Fatal(exported.Err)
	}
	f, err := os.Open(exported.Value.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || uint64(len(rows)-1) != snap.Attempts {
		t.Fatalf("CSV rows=%d attempts=%d err=%v", len(rows), snap.Attempts, err)
	}
	summary, err := os.ReadFile(exported.Value.SummaryPath)
	if err != nil || !strings.Contains(string(summary), "手动停止") {
		t.Fatalf("summary err=%v contents=%s", err, summary)
	}
	waitClosed(t, a.Shutdown())
}

type blockedExportEngine struct {
	*fakeEngine
	entered chan struct{}
	release chan struct{}
}

func (e *blockedExportEngine) Export(dir string) (tester.ExportResult, error) {
	close(e.entered)
	<-e.release
	return e.fakeEngine.Export(dir)
}

func TestExportCoordinatesStartAndShutdown(t *testing.T) {
	a := testApp(t)
	e := &blockedExportEngine{fakeEngine: newFakeEngine("first"), entered: make(chan struct{}), release: make(chan struct{})}
	a.newEngine = func(tester.Config) (engine, error) { return e, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Stop("first")); r.Err != nil {
		t.Fatal(r.Err)
	}
	exported := a.Export("first")
	<-e.entered
	if r := getResult(t, a.Start(validRequest())); r.Err == nil {
		t.Fatal("start accepted during export")
	}
	if r := getResult(t, a.Export("first")); r.Err == nil {
		t.Fatal("overlapping export accepted")
	}
	if r := getResult(t, a.SaveDomains([]string{"example.com"})); r.Err == nil {
		t.Fatal("save accepted during export")
	}
	done := a.Shutdown()
	select {
	case <-done:
		t.Fatal("shutdown completed before export")
	default:
	}
	if r := getResult(t, a.Export("first")); r.Err == nil {
		t.Fatal("export accepted during shutdown")
	}
	close(e.release)
	if r := getResult(t, exported); r.Err != nil {
		t.Fatal(r.Err)
	}
	waitClosed(t, done)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.discarded {
		t.Fatal("shutdown did not discard engine")
	}
}

func TestFailedStartRetainsOldEngineUntilReplacement(t *testing.T) {
	a := testApp(t)
	old, next := newFakeEngine("old"), newFakeEngine("next")
	a.newEngine = func(tester.Config) (engine, error) { return old, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Stop("old")); r.Err != nil {
		t.Fatal(r.Err)
	}
	a.newEngine = func(tester.Config) (engine, error) { return nil, errors.New("factory failed") }
	if r := getResult(t, a.Start(validRequest())); r.Err == nil {
		t.Fatal("factory failure not reported")
	}
	if r := getResult(t, a.Export("old")); r.Err != nil {
		t.Fatalf("failed start lost old export: %v", r.Err)
	}
	old.mu.Lock()
	discarded := old.discarded
	old.mu.Unlock()
	if discarded {
		t.Fatal("failed start discarded old engine")
	}
	a.newEngine = func(tester.Config) (engine, error) { return next, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	old.mu.Lock()
	discarded = old.discarded
	old.mu.Unlock()
	if !discarded {
		t.Fatal("successful replacement did not discard old engine")
	}
	if r := getResult(t, a.Export("old")); r.Err == nil {
		t.Fatal("stale session export accepted")
	}
}

func TestDiscardFailureRetainsSessionAndRetriesCandidateOnShutdown(t *testing.T) {
	a := testApp(t)
	old, candidate := newFakeEngine("old"), newFakeEngine("candidate")
	a.newEngine = func(tester.Config) (engine, error) { return old, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Stop("old")); r.Err != nil {
		t.Fatal(r.Err)
	}
	old.mu.Lock()
	old.discardErr = errors.New("/tmp/old-journal: permission denied")
	old.mu.Unlock()
	candidate.mu.Lock()
	candidate.discardErr = errors.New("/tmp/candidate-journal: permission denied")
	candidate.mu.Unlock()
	a.newEngine = func(tester.Config) (engine, error) { return candidate, nil }
	r := getResult(t, a.Start(validRequest()))
	if r.Err == nil || !strings.Contains(r.Err.Error(), "/tmp/old-journal") || !strings.Contains(r.Err.Error(), "/tmp/candidate-journal") {
		t.Fatalf("cleanup errors not reported: %v", r.Err)
	}
	if a.sessionID != "old" || a.engine != old || len(a.pendingCleanup) != 1 || a.pendingCleanup[0] != candidate {
		t.Fatal("failed cleanup lost old session or candidate")
	}
	if r := getResult(t, a.Export("old")); r.Err != nil {
		t.Fatalf("old session unavailable: %v", r.Err)
	}
	if r := getResult(t, a.Export("candidate")); r.Err == nil {
		t.Fatal("candidate was published")
	}
	candidate.mu.Lock()
	started, stops, waits, discards, attempts := candidate.started, candidate.stops, candidate.waits, candidate.discardCalls, candidate.attempts
	candidate.discardErr = nil
	candidate.mu.Unlock()
	if !started || stops != 1 || waits != 1 || discards != 1 || attempts != 0 || !candidate.Finished() {
		t.Fatalf("candidate cleanup: started=%v stops=%d waits=%d discards=%d attempts=%d", started, stops, waits, discards, attempts)
	}
	old.mu.Lock()
	old.discardErr = nil
	old.mu.Unlock()
	waitClosed(t, a.Shutdown())
	for _, e := range []*fakeEngine{old, candidate} {
		e.mu.Lock()
		if !e.discarded || e.discardCalls != 2 {
			t.Errorf("%s not retried: discarded=%v calls=%d", e.session, e.discarded, e.discardCalls)
		}
		e.mu.Unlock()
	}
}

func TestDiscardFailureCleansCandidateAndAllowsStartRetry(t *testing.T) {
	a := testApp(t)
	old, candidate, next := newFakeEngine("old"), newFakeEngine("candidate"), newFakeEngine("next")
	a.newEngine = func(tester.Config) (engine, error) { return old, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := getResult(t, a.Stop("old")); r.Err != nil {
		t.Fatal(r.Err)
	}
	old.mu.Lock()
	old.discardErr = errors.New("old cleanup failed")
	old.mu.Unlock()
	a.newEngine = func(tester.Config) (engine, error) { return candidate, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err == nil || !strings.Contains(r.Err.Error(), "old cleanup failed") {
		t.Fatalf("old cleanup failure not reported: %v", r.Err)
	}
	if r := getResult(t, a.Sample("old", 0)); r.Err != nil || r.Value.State.SessionID != "old" {
		t.Fatalf("old session lost: %+v", r)
	}
	candidate.mu.Lock()
	if !candidate.started || candidate.attempts != 0 || candidate.stops != 1 || candidate.waits != 1 || !candidate.discarded || candidate.discardCalls != 1 {
		t.Errorf("candidate cleanup: started=%v attempts=%d stops=%d waits=%d discarded=%v calls=%d", candidate.started, candidate.attempts, candidate.stops, candidate.waits, candidate.discarded, candidate.discardCalls)
	}
	candidate.mu.Unlock()
	if len(a.pendingCleanup) != 0 {
		t.Fatal("successfully discarded candidate retained for retry")
	}
	old.mu.Lock()
	old.discardErr = nil
	old.mu.Unlock()
	a.newEngine = func(tester.Config) (engine, error) { return next, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil || r.Value.SessionID != "next" {
		t.Fatalf("start retry failed: %+v", r)
	}
	if r := getResult(t, a.Sample("old", 0)); r.Err == nil {
		t.Fatal("old session still published after successful retry")
	}
}

func TestShutdownReportsDiscardFailure(t *testing.T) {
	a := testApp(t)
	e := newFakeEngine("old")
	e.discardErr = errors.New("/tmp/unremoved-journal: permission denied")
	a.newEngine = func(tester.Config) (engine, error) { return e, nil }
	if r := getResult(t, a.Start(validRequest())); r.Err != nil {
		t.Fatal(r.Err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous }()
	waitClosed(t, a.Shutdown())
	os.Stderr = previous
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !strings.Contains(string(output), "/tmp/unremoved-journal") {
		t.Fatalf("shutdown did not report cleanup path: %q (%v)", output, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.discardCalls != 1 || e.discarded {
		t.Fatalf("unexpected cleanup state: calls=%d discarded=%v", e.discardCalls, e.discarded)
	}
}
