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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"tcpconnbenchmark/internal/syslimit"
)

func TestSnapshotAndSummarySessionMetadata(t *testing.T) {
	run := func(t *testing.T, iface, source, wantLabel string) {
		t.Helper()
		cfg := Config{
			Mode:        ModeBoth,
			Interface:   iface,
			SourceIP:    source,
			Domains:     []string{" HTTPS://Example.com:443/path , example.org ", "example.com", "http://[fe80::1%25en0]:443/a", "192.0.2.1:8080"},
			DurationSec: 1,
			RatePerSec:  100,
		}
		wantDomains := []string{"example.com", "example.org", "fe80::1%en0", "192.0.2.1"}
		e := newTestEngine(t, cfg)
		if !reflect.DeepEqual(e.cfg.Domains, wantDomains) {
			t.Fatalf("normalized domains = %v, want %v", e.cfg.Domains, wantDomains)
		}
		initial := e.Snapshot()
		if !strings.HasPrefix(initial.SessionID, "tcp-test-") || initial.SessionID != e.logger.sessionID || initial.InterfaceLabel != wantLabel || initial.DomainCount != len(wantDomains) {
			t.Fatalf("initial snapshot metadata: %+v; want label %q, domains %d", initial, wantLabel, len(wantDomains))
		}

		// Neither the caller's input nor later engine config changes may alter the summary.
		cfg.Domains[0] = "caller-mutated.example"
		if !reflect.DeepEqual(e.cfg.Domains, wantDomains) {
			t.Fatalf("caller modified engine domains: %v", e.cfg.Domains)
		}
		e.cfg.Domains[1] = "engine-mutated.example"
		if !reflect.DeepEqual(e.logger.config.Domains, wantDomains) {
			t.Fatalf("engine or caller modified logger domains: %v", e.logger.config.Domains)
		}
		e.logger.config.Domains[2] = "logger-mutated.example"
		if e.cfg.Domains[2] != wantDomains[2] {
			t.Fatalf("logger modified engine domains: %v", e.cfg.Domains)
		}
		e.logger.config.Domains[2] = wantDomains[2]
		e.Stop("预先停止")
		e.Start()
		e.Wait()
		finished := e.Snapshot()
		if !finished.Finished || finished.SessionID != initial.SessionID || finished.InterfaceLabel != wantLabel || finished.DomainCount != len(wantDomains) || finished.StopReason != "预先停止" || finished.EndedAt == "" || finished.Attempts != 0 || finished.LogError != "" {
			t.Fatalf("finished snapshot metadata: %+v", finished)
		}
		result := exportTestResult(t, e)
		published := e.Snapshot()
		if published.SessionID != initial.SessionID || published.InterfaceLabel != wantLabel || published.DomainCount != len(wantDomains) || published.LogPath != result.LogPath || published.SummaryPath != result.SummaryPath {
			t.Fatalf("published snapshot metadata: %+v", published)
		}
		if filepath.Dir(result.LogPath) != filepath.Dir(result.SummaryPath) || !strings.HasPrefix(filepath.Base(filepath.Dir(result.LogPath)), initial.SessionID+"-") || filepath.Base(result.LogPath) != "results.csv" || filepath.Base(result.SummaryPath) != "summary.txt" {
			t.Fatalf("unexpected session export paths: %+v", result)
		}

		fdLine := "文件句柄上限: 当前平台不适用\n"
		if e.logger.fdInfo.Supported {
			fdLine = fmt.Sprintf("文件句柄软上限: %d（硬上限 %d）\n", e.logger.fdInfo.Soft, e.logger.fdInfo.Hard)
		} else if e.logger.fdInfo.Error != "" {
			fdLine = fmt.Sprintf("文件句柄上限: %s\n", e.logger.fdInfo.Error)
		}
		wantSummary := fmt.Sprintf("tcpconnbenchmark · TCP 并发连接测试\n========================================\n"+
			"会话 ID: %s\n状态: 预先停止\n开始时间: %s\n结束时间: %s\n"+
			"测试模式: TCP 80 + 443\n出口网卡: %s\n测试域名: 4 个\n发起目标: 8 个（域名 × 端口）\n测试时长: 1 秒\n"+
			"失败停止: 不限制\n发起速率: 100 次/秒\n单连接保持: 至本轮测试结束\n%s"+
			"CSV 日志: %s\n摘要文件: %s\n----------------------------------------\n"+
			"尝试次数: 0\n成功: 0\n失败: 0\n中止: 0\n成功率: —\n"+
			"峰值并发: 0\n平均发起速率: 0.00 次/秒\n停止原因: 预先停止\n----------------------------------------\n"+
			"测试域名（4）:\n  1. example.com\n  2. example.org\n  3. fe80::1%%en0\n  4. 192.0.2.1\n"+
			"----------------------------------------\n说明: 仅解析 IPv4 / IPv6 地址并建立 TCP 连接；不发送 HTTP 请求、不进行 TLS 握手、不传输网页数据。\n",
			initial.SessionID, e.logger.createdAt.Format("2006-01-02 15:04:05"), finished.EndedAt, wantLabel, fdLine, result.LogPath, result.SummaryPath)
		raw, err := os.ReadFile(result.SummaryPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != wantSummary {
			t.Fatalf("summary mismatch\ngot:\n%s\nwant:\n%s", raw, wantSummary)
		}
		csv, err := os.ReadFile(result.LogPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := "\xef\xbb\xbf时间,域名,IP,端口,结果,耗时(ms),错误\n"; string(csv) != want {
			t.Fatalf("empty CSV = %q, want %q", csv, want)
		}
	}

	t.Run("automatic", func(t *testing.T) {
		run(t, "", "", "自动（系统默认路由）")
	})
	t.Run("selected", func(t *testing.T) {
		var selected InterfaceInfo
		for _, iface := range ListInterfaces() {
			if iface.IsUp && len(iface.IPv4) > 0 {
				selected = iface
				if iface.IsLoop {
					break
				}
			}
		}
		if selected.Name == "" {
			t.Skip("no active interface with an IPv4 address")
		}
		run(t, selected.Name, selected.IPv4[0], selected.Name+" · "+selected.IPv4[0])
	})
}

func TestResultLoggerCopiesInputDomains(t *testing.T) {
	cfg := Config{Domains: []string{"first.example", "second.example"}}
	logger, err := newResultLogger(cfg, "test interface", syslimit.Info{})
	if err != nil {
		t.Fatal(err)
	}
	logger.start(nil)
	t.Cleanup(func() {
		logger.close(0)
		if err := os.RemoveAll(logger.tempDir); err != nil {
			t.Errorf("remove journal: %v", err)
		}
	})
	cfg.Domains[0] = "caller-mutated.example"
	if want := []string{"first.example", "second.example"}; !reflect.DeepEqual(logger.config.Domains, want) {
		t.Fatalf("caller modified logger domains: %v, want %v", logger.config.Domains, want)
	}
	logger.config.Domains[1] = "logger-mutated.example"
	if cfg.Domains[1] != "second.example" {
		t.Fatalf("logger modified caller domains: %v", cfg.Domains)
	}
}

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
