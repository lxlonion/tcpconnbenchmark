package tester

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJournalManualExportFullOrderedAndIdempotent(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	dir := t.TempDir()
	if _, err := e.Export(dir); err == nil {
		t.Fatal("export before finish succeeded")
	}
	if err := e.Discard(); err == nil {
		t.Fatal("discard before finish succeeded")
	}
	if s := e.Snapshot(); s.LogPath != "" || s.SummaryPath != "" {
		t.Fatalf("premature paths: %+v", s)
	}
	const n = 6000
	for i := 0; i < n; i++ {
		// Keep this synthetic producer below the writer speed; production never
		// waits on the queue and instead reports overload.
		for len(e.logger.queue) > journalQueueSize/2 {
			time.Sleep(time.Millisecond)
		}
		e.writeEntry(LogEntry{Domain: fmt.Sprintf("entry-%06d", i), Port: 80, OK: true}, time.Now())
	}
	e.finish()
	if s := e.Snapshot(); s.LogPath != "" || s.SummaryPath != "" || s.LogError != "" {
		t.Fatalf("unexpected finished state: %+v", s)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("unrequested export: %v %v", files, err)
	}
	result, err := e.Export(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := readResultCSV(t, result.LogPath)
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	for i, row := range rows {
		if row[1] != fmt.Sprintf("entry-%06d", i) {
			t.Fatalf("row %d: %v", i, row)
		}
	}
	s := e.Snapshot()
	if s.LogPath != result.LogPath || s.SummaryPath != result.SummaryPath {
		t.Fatalf("paths not published: %+v", s)
	}
	if err := os.WriteFile(result.LogPath, []byte("user changes"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := e.Export(dir)
	if err != nil || again != result {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	bytes, err := os.ReadFile(result.LogPath)
	if err != nil || string(bytes) != "user changes" {
		t.Fatalf("repeat overwrote user file: %q %v", bytes, err)
	}
	if _, err := e.Export(t.TempDir()); err == nil {
		t.Fatal("export to different directory accepted")
	}
	if err := os.Remove(result.SummaryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Export(dir); err == nil {
		t.Fatal("missing saved file was silently recreated")
	}
	if err := e.Discard(); err != nil {
		t.Fatal(err)
	}
	if err := e.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Export(dir); err == nil {
		t.Fatal("export after discard succeeded")
	}
	if _, err := os.Stat(result.LogPath); err != nil {
		t.Fatalf("discard removed exported file: %v", err)
	}
}

func TestExportFailureRetryAndTruncatedJournal(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		e := newTestEngine(t, baseConfig())
		e.writeEntry(LogEntry{Domain: "local", Port: 80}, time.Now())
		e.finish()
		dir := t.TempDir()
		bad := filepath.Join(dir, "not-directory")
		if err := os.WriteFile(bad, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Export(bad); err == nil {
			t.Fatal("invalid destination accepted")
		}
		if e.Snapshot().LogError != "" {
			t.Fatal("destination error poisoned journal")
		}
		if data, _ := os.ReadFile(bad); string(data) != "keep" {
			t.Fatal("existing file changed")
		}
		if _, err := e.Export(filepath.Join(dir, "logs")); err != nil {
			t.Fatalf("retry: %v", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		e := newTestEngine(t, baseConfig())
		e.writeEntry(LogEntry{Domain: "local"}, time.Now())
		e.finish()
		info, err := os.Stat(e.logger.journalPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(e.logger.journalPath, info.Size()-1); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := e.Export(dir); err == nil {
			t.Fatal("truncated journal exported")
		}
		if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
			t.Fatalf("partial export not removed: %v %v", files, err)
		}
		if s := e.Snapshot(); s.LogPath != "" || s.SummaryPath != "" {
			t.Fatalf("partial paths published: %+v", s)
		}
	})
}

func TestExportPartialFilesAreCleanedAndRetryable(t *testing.T) {
	for _, failing := range []string{"results.csv", "summary.txt"} {
		t.Run(failing, func(t *testing.T) {
			e := newTestEngine(t, baseConfig())
			e.writeEntry(LogEntry{Domain: "local"}, time.Now())
			e.finish()
			dir := t.TempDir()
			e.logger.createFile = func(path string) (*os.File, error) {
				if filepath.Base(path) == failing {
					return nil, fmt.Errorf("injected create failure")
				}
				return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			}
			if _, err := e.Export(dir); err == nil {
				t.Fatal("injected export failure succeeded")
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("partial export remains: %v %v", files, err)
			}
			if s := e.Snapshot(); s.LogError != "" || s.LogPath != "" || s.SummaryPath != "" {
				t.Fatalf("failure contaminated state: %+v", s)
			}
			e.logger.createFile = nil
			if _, err := e.Export(dir); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}

func TestJournalQueueFullStopsEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{ctx: ctx, cancel: cancel, ring: newLogRing(8)}
	e.reason.Store("")
	e.logger = &resultLogger{queue: make(chan journalRecord, 1), onError: func() { e.stopWithReason("日志暂存失败") }}
	e.writeEntry(LogEntry{}, time.Now())
	e.writeEntry(LogEntry{}, time.Now())
	if e.logger.err() == nil || ctx.Err() == nil {
		t.Fatal("queue overflow did not report error and stop")
	}
}

func TestJournalWriteFailureStopsAndRefusesExport(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	if err := e.logger.file.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000 && e.ctx.Err() == nil; i++ {
		for len(e.logger.queue) > journalQueueSize/2 {
			time.Sleep(time.Millisecond)
		}
		e.writeEntry(LogEntry{Error: strings.Repeat("x", 240)}, time.Now())
	}
	deadline := time.Now().Add(2 * time.Second)
	for e.ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.ctx.Err() == nil {
		t.Fatal("writer failure did not stop test")
	}
	e.finish()
	if e.Snapshot().LogError == "" {
		t.Fatal("writer failure not visible")
	}
	if _, err := e.Export(t.TempDir()); err == nil {
		t.Fatal("incomplete journal exported")
	}
}

func TestExportDiscardConcurrent(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	e.writeEntry(LogEntry{Domain: "local"}, time.Now())
	e.finish()
	dir := t.TempDir()
	var wg sync.WaitGroup
	wg.Add(2)
	var result ExportResult
	var exportErr, discardErr error
	go func() { defer wg.Done(); result, exportErr = e.Export(dir) }()
	go func() { defer wg.Done(); discardErr = e.Discard() }()
	wg.Wait()
	if discardErr != nil {
		t.Fatal(discardErr)
	}
	if exportErr == nil {
		if _, err := os.Stat(result.LogPath); err != nil {
			t.Fatalf("CSV removed: %v", err)
		}
		if _, err := os.Stat(result.SummaryPath); err != nil {
			t.Fatalf("summary removed: %v", err)
		}
	}
	if _, err := os.Stat(e.logger.tempDir); !os.IsNotExist(err) {
		t.Fatalf("temporary journal remains: %v", err)
	}
}

func TestHoldUntilCancellationAndNoTimer(t *testing.T) {
	e := newTestEngine(t, baseConfig())
	conn, peer := net.Pipe()
	defer peer.Close()
	e.incrementActive()
	done := make(chan struct{})
	go func() { e.hold(conn); close(done) }()
	peer.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	_, err := peer.Read(make([]byte, 1))
	if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
		t.Fatalf("connection closed before cancellation: %v", err)
	}
	e.cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("hold did not exit after cancel")
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("peer did not observe close: %v", err)
	}
	if e.active.Load() != 0 || e.closed.Load() != 1 {
		t.Fatalf("active=%d closed=%d", e.active.Load(), e.closed.Load())
	}
	f, err := parser.ParseFile(token.NewFileSet(), "engine.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "hold" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && (strings.Contains(sel.Sel.Name, "Timer") || sel.Sel.Name == "After") {
					t.Errorf("per-connection timer: %s", sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestAllStopReasonsReleaseConnections(t *testing.T) {
	for _, mode := range []string{"natural", "manual", "failure"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, err := listener.Accept()
				if err == nil {
					accepted <- c
				}
			}()
			cfg := baseConfig()
			cfg.DurationSec, cfg.RatePerSec = 30, 2
			if mode == "natural" {
				cfg.DurationSec = 1
			}
			if mode == "failure" {
				cfg.FailStop = 1
				cfg.RatePerSec = 20
			}
			e := newTestEngine(t, cfg)
			e.targets = []target{{domain: "127.0.0.1", port: listener.Addr().(*net.TCPAddr).Port}}
			if mode == "failure" {
				e.targets = append(e.targets, target{domain: "127.0.0.1", port: closedTCPPort(t)})
			}
			e.Start()
			var peer net.Conn
			select {
			case peer = <-accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("no connection")
			}
			defer peer.Close()
			if mode == "manual" {
				deadline := time.Now().Add(time.Second)
				for e.success.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if e.success.Load() == 0 {
					t.Fatal("successful handshake was not recorded")
				}
				e.Stop("手动停止")
			}
			finished := make(chan struct{})
			go func() { e.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(4 * time.Second):
				t.Fatal("test did not finish")
			}
			peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("peer not closed: %v", err)
			}
			s := e.Snapshot()
			if s.Active != 0 || s.Closed != s.Success || s.Success == 0 {
				t.Fatalf("connection counts: %+v", s)
			}
			if mode == "failure" && !strings.Contains(s.StopReason, "失败") {
				t.Fatalf("reason: %s", s.StopReason)
			}
		})
	}
}
