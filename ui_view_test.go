package main

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

type viewStub struct {
	start            chan result[tester.Snapshot]
	samples          []chan result[sampleData]
	tail             uint64
	requests         []startRequest
	minRate, maxRate int
	save             chan result[[]string]
	savedDomains     [][]string
	exports          []chan result[tester.ExportResult]
	exportSessions   []string
	configError      string
}

func (s *viewStub) Bootstrap() bootstrapData {
	minRate, maxRate := s.minRate, s.maxRate
	if minRate == 0 {
		minRate = tester.MinRatePerSec
	}
	if maxRate == 0 {
		maxRate = tester.MaxRatePerSec
	}
	return bootstrapData{Domains: []string{"example.com", "example.org"}, BuiltinDomains: []string{"builtin.example"}, AppDir: "/portable", LogDir: "/portable/logs", ConfigPath: "/portable/domains.json", ConfigError: s.configError, DefaultRatePerSec: 125, DefaultDurationSec: 360, DefaultFailStop: 50, MinRatePerSec: minRate, MaxRatePerSec: maxRate, Version: "test"}
}
func (s *viewStub) Start(r startRequest) <-chan result[tester.Snapshot] {
	s.requests = append(s.requests, r)
	s.start = make(chan result[tester.Snapshot], 1)
	return s.start
}
func (*viewStub) Stop(string) <-chan result[struct{}] { return reply(struct{}{}, nil) }
func (s *viewStub) Sample(string, uint64) <-chan result[sampleData] {
	ch := make(chan result[sampleData], 1)
	s.samples = append(s.samples, ch)
	return ch
}
func (*viewStub) Interfaces() <-chan result[[]tester.InterfaceInfo] {
	return reply([]tester.InterfaceInfo(nil), nil)
}
func (*viewStub) OpenLogs() <-chan result[struct{}] { return reply(struct{}{}, nil) }
func (s *viewStub) SaveDomains(domains []string) <-chan result[[]string] {
	s.savedDomains = append(s.savedDomains, append([]string(nil), domains...))
	s.save = make(chan result[[]string], 1)
	return s.save
}
func (s *viewStub) Export(session string) <-chan result[tester.ExportResult] {
	s.exportSessions = append(s.exportSessions, session)
	ch := make(chan result[tester.ExportResult], 1)
	s.exports = append(s.exports, ch)
	return ch
}
func (s *viewStub) Tail(string) (uint64, error) { return s.tail, nil }
func (*viewStub) Shutdown() <-chan struct{}     { ch := make(chan struct{}); close(ch); return ch }

func TestNativeViewLayoutAndDefaults(t *testing.T) {
	v := newNativeView(&viewStub{})
	tt := ui.NewTester(v.view, 1180, 780)
	for _, label := range []string{"TCPLink", "测试模式", "出口网卡", "发起速率（次/秒）", "测试时长（秒）", "失败停止（0 不限）", "连接日志"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("missing %q; texts: %v", label, tt.Texts())
		}
	}
	if v.rate != "125" || v.duration != "360" || v.failStop != "50" || v.mode != 0 || !v.follow {
		t.Fatalf("wrong defaults: %#v", v)
	}
	if !tt.HasText("已运行 0 秒 · 剩余 360 秒") || !tt.HasText("失败 0 / 50") {
		t.Fatalf("incorrect default progress: %v", tt.Texts())
	}
	tt.SetSize(900, 650)
	tt.SetDark(true)
}

func TestNativeViewScreenshot(t *testing.T) {
	if path := os.Getenv("TCPLINK_TEST_SCREENSHOT"); path != "" {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		app := newAppInDirectory(syslimit.Raise(tester.FDSoftLimitTarget), t.TempDir())
		v := newNativeView(app)
		tt := ui.NewTester(v.view, 900, 650)
		tt.SetDark(true)
		if len(v.domains) != 84 || !tt.HasText("已运行 0 秒 · 剩余 360 秒") || !tt.HasText("失败 0 / 50") {
			t.Fatalf("incorrect bootstrap defaults: %v", tt.Texts())
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeViewFinishedWhileStoppingCanRestart(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	v.session = "old"
	v.snapshot = tester.Snapshot{SessionID: "old", Finished: true, Stopping: true}
	v.drained = true
	tt := ui.NewTester(v.view, 900, 650)
	if !tt.HasText("已结束") || tt.HasText("正在停止") {
		t.Fatalf("incorrect finished status: %v", tt.Texts())
	}
	if err := tt.Click("开始测试"); err != nil {
		t.Fatal(err)
	}
	if len(s.requests) != 1 {
		t.Fatal("finished session cannot restart")
	}
	s.start <- result[tester.Snapshot]{Value: tester.Snapshot{SessionID: "new"}}
	tt.Advance(200 * time.Millisecond)
	if v.session != "new" {
		t.Fatalf("new session did not start: %q", v.session)
	}
}

func TestNativeViewLongPathsAccessible(t *testing.T) {
	v := newNativeView(&viewStub{})
	v.session = "finished"
	v.drained = true
	v.snapshot.Finished = true
	v.snapshot.StopReason = "手动停止"
	v.snapshot.LogError = "示例日志错误"
	v.message = "测试提示"
	v.snapshot.LogPath = "/portable/TCPLink/logs/" + strings.Repeat("very-long-directory/", 8) + "result.csv"
	v.snapshot.SummaryPath = "/portable/TCPLink/logs/" + strings.Repeat("very-long-directory/", 8) + "result.summary.txt"
	csvPath, summaryPath := v.snapshot.LogPath, v.snapshot.SummaryPath
	tt := ui.NewTester(v.view, 900, 650)
	if _, ok := tt.Find("CSV 日志路径"); ok {
		t.Fatal("paths should be collapsed by default")
	}
	if err := tt.Click("日志"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ label, want string }{{"CSV 日志路径", v.snapshot.LogPath}, {"摘要文件路径", v.snapshot.SummaryPath}} {
		r, ok := tt.Find(item.label)
		if !ok || r.X < 0 || r.Y < 0 || r.W <= 0 || r.H <= 0 || r.X+r.W > 900 || r.Y+r.H > 650 {
			t.Fatalf("%s outside window: %+v found=%v", item.label, r, ok)
		}
		if err := tt.Click(item.label); err != nil {
			t.Fatal(err)
		}
		tt.Command("selectAll")
		tt.Command("copy")
		if got := tt.Clipboard(); got != item.want {
			t.Fatalf("%s copied %q, want %q", item.label, got, item.want)
		}
		tt.Type("should-not-edit")
		if v.snapshot.LogPath != csvPath || v.snapshot.SummaryPath != summaryPath {
			t.Fatal("read-only path was modified")
		}
	}
	for _, label := range []string{"打开目录", "文件句柄状态", "版本"} {
		r, ok := tt.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 650 {
			t.Fatalf("footer item %q outside window: %+v found=%v", label, r, ok)
		}
	}
}

func TestNativeViewStrictInputs(t *testing.T) {
	for _, tc := range []struct {
		s        string
		min, max int
		ok       bool
	}{
		{"1", 1, 1000, true}, {"1000", 1, 1000, true}, {"0", 0, 10000000, true}, {"10000000", 0, 10000000, true},
		{"", 0, 1000, false}, {"1.2", 0, 1000, false}, {"NaN", 0, 1000, false}, {"+1", 0, 1000, false}, {" 1", 0, 1000, false}, {"99999999999999999999999999", 0, 1000, false}, {"1001", 1, 1000, false},
	} {
		if _, ok := parseInteger(tc.s, tc.min, tc.max); ok != tc.ok {
			t.Errorf("%q: got %v", tc.s, ok)
		}
	}
	s := &viewStub{}
	v := newNativeView(s)
	v.duration = "3.5"
	v.start()
	if len(s.requests) != 0 {
		t.Fatal("invalid form started")
	}
	v.duration = "60"
	v.start()
	if len(s.requests) != 1 || s.requests[0].Mode != tester.ModeBoth {
		t.Fatal("valid form did not start")
	}
}

func TestNativeViewBootstrapRateBounds(t *testing.T) {
	for _, tc := range []struct {
		rate     string
		accepted bool
	}{{"9", false}, {"10", true}, {"20", true}, {"21", false}} {
		t.Run(tc.rate, func(t *testing.T) {
			s := &viewStub{minRate: 10, maxRate: 20}
			v := newNativeView(s)
			v.rate = tc.rate
			v.start()
			if got := len(s.requests) == 1; got != tc.accepted {
				t.Fatalf("rate %s accepted=%v", tc.rate, got)
			}
			if tc.accepted {
				if fmt.Sprint(s.requests[0].RatePerSec) != tc.rate {
					t.Fatalf("wrong submitted rate: %d", s.requests[0].RatePerSec)
				}
			} else if !strings.Contains(v.message, "速率 10–20") || strings.Contains(v.message, "速率 1–1000") {
				t.Fatalf("wrong validation message: %q", v.message)
			}
		})
	}
}

func TestNativeViewDomainDraft(t *testing.T) {
	v := newNativeView(&viewStub{})
	tt := ui.NewTester(v.view, 1180, 780)
	if err := tt.Click("域名 2 个 · 编辑"); err != nil {
		t.Fatal(err)
	}
	if !v.editing {
		t.Fatal("editor did not open")
	}
	v.draft = "changed.com"
	tt.Frame()
	if err := tt.Click("恢复内置"); err != nil {
		t.Fatal(err)
	}
	if v.draft != "builtin.example" {
		t.Fatalf("reset: %q", v.draft)
	}
	if err := tt.Click("清空草稿"); err != nil {
		t.Fatal(err)
	}
	if v.draft != "" {
		t.Fatal("clear failed")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if v.editing || len(v.domains) != 2 {
		t.Fatal("cancel changed applied domains")
	}
	v.editDomains()
	v.draft = "https://example.net/path\ninvalid_thing"
	v.applyDomains()
	if len(v.domains) != 2 || !v.editing {
		t.Fatal("domain save changed state before success")
	}
	s := v.controller.(*viewStub)
	s.save <- result[[]string]{Value: []string{"example.net"}}
	v.poll(time.Unix(100, 0))
	if len(v.domains) != 1 || v.domains[0] != "example.net" || v.editing {
		t.Fatalf("apply: %#v", v.domains)
	}
	v.editDomains()
	var many strings.Builder
	for i := range 5001 {
		fmt.Fprintf(&many, "domain%d.example\n", i)
	}
	v.draft = many.String()
	v.applyDomains()
	if !v.editing || len(v.domains) != 1 {
		t.Fatal("more than 5000 domains were applied")
	}
}

func TestNativeViewDomainEditorDisabledWhenClosing(t *testing.T) {
	v := newNativeView(&viewStub{})
	v.editDomains()
	v.draft = "edited.example"
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("域名草稿"); err != nil {
		t.Fatal(err)
	}
	v.closing = true
	tt.Frame()
	tt.Type("unwanted")
	for _, label := range []string{"恢复内置", "清空草稿", "保存并应用"} {
		_ = tt.Click(label)
	}
	v.applyDomains()
	if v.draft != "edited.example" || !v.editing || len(v.domains) != 2 || v.domains[0] != "example.com" {
		t.Fatalf("closing editor was modified: draft=%q domains=%v", v.draft, v.domains)
	}
}

func TestNativeViewDomainSaveFailureAndRetry(t *testing.T) {
	s := &viewStub{configError: "配置损坏"}
	v := newNativeView(s)
	if !strings.Contains(v.message, "配置损坏") {
		t.Fatal("configuration error not shown")
	}
	v.editDomains()
	v.draft = "https://example.net/path\ninvalid_thing"
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("域名草稿"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("保存并应用"); err != nil {
		t.Fatal(err)
	}
	if len(s.savedDomains) != 1 || len(s.savedDomains[0]) != 1 || s.savedDomains[0][0] != "example.net" {
		t.Fatalf("bad save request: %v", s.savedDomains)
	}
	if !v.editing || v.domains[0] != "example.com" {
		t.Fatal("applied before persistence")
	}
	for _, label := range []string{"恢复内置", "清空草稿", "取消", "正在保存…"} {
		_ = tt.Click(label)
	}
	_ = tt.Click("域名草稿")
	tt.Type("should-not-edit")
	if v.draft != "https://example.net/path\ninvalid_thing" || !v.editing || len(s.savedDomains) != 1 {
		t.Fatal("pending save did not lock editor")
	}
	s.save <- result[[]string]{Err: errors.New("磁盘不可写")}
	tt.Advance(200 * time.Millisecond)
	if !v.editing || v.domains[0] != "example.com" || !strings.Contains(v.message, "磁盘不可写") {
		t.Fatal("failed save did not retain draft and old domains")
	}
	if err := tt.Click("保存并应用"); err != nil {
		t.Fatal(err)
	}
	s.save <- result[[]string]{Value: []string{"example.net"}}
	tt.Advance(200 * time.Millisecond)
	if v.editing || len(v.domains) != 1 || v.domains[0] != "example.net" || !strings.Contains(v.message, "忽略 1") {
		t.Fatalf("save did not apply: %v %q", v.domains, v.message)
	}
}

func TestNativeViewManualExportAndStaleSample(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	tt := ui.NewTester(v.view, 900, 650)
	if err := tt.Click("日志"); err != nil {
		t.Fatal(err)
	}
	_ = tt.Click("保存结果")
	if len(s.exports) != 0 {
		t.Fatal("idle exported")
	}
	v.session = "s"
	v.generation = 1
	v.snapshot = tester.Snapshot{SessionID: "s"}
	tt.Frame()
	_ = tt.Click("保存结果")
	if len(s.exports) != 0 {
		t.Fatal("running exported")
	}
	v.snapshot.Finished = true
	tt.Frame()
	if !tt.HasText("结果尚未保存，开始新测试或关闭后丢失") {
		t.Fatal("missing unsaved warning")
	}
	if err := tt.Click("保存结果"); err != nil {
		t.Fatal(err)
	}
	if len(s.exports) != 1 || s.exportSessions[0] != "s" || !v.locked() || !tt.HasText("保存中…") {
		t.Fatal("export not pending or form not locked")
	}
	_ = tt.Click("开始测试")
	if len(s.requests) != 0 {
		t.Fatal("started while export pending")
	}
	s.exports[0] <- result[tester.ExportResult]{Err: errors.New("磁盘错误")}
	tt.Advance(200 * time.Millisecond)
	if !strings.Contains(v.message, "磁盘错误") || v.snapshot.LogPath != "" {
		t.Fatal("failed export not retryable")
	}
	if err := tt.Click("保存结果"); err != nil {
		t.Fatal(err)
	}
	s.exports[1] <- result[tester.ExportResult]{Value: tester.ExportResult{LogPath: "/portable/logs/a.csv", SummaryPath: "/portable/logs/a.summary.txt"}}
	tt.Advance(200 * time.Millisecond)
	if v.snapshot.LogPath != "/portable/logs/a.csv" || v.snapshot.SummaryPath == "" {
		t.Fatal("export paths not applied")
	}
	if len(s.samples) == 0 {
		t.Fatal("expected in-flight sample")
	}
	s.samples[0] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "s", Finished: true}}}
	tt.Advance(200 * time.Millisecond)
	if v.snapshot.LogPath != "/portable/logs/a.csv" || v.snapshot.SummaryPath == "" {
		t.Fatal("stale sample erased export paths")
	}
	if tt.HasText("结果尚未保存，开始新测试或关闭后丢失") {
		t.Fatal("saved result still marked unsaved")
	}
	if err := tt.Click("开始测试"); err != nil {
		t.Fatal(err)
	}
	s.start <- result[tester.Snapshot]{Value: tester.Snapshot{SessionID: "next"}}
	tt.Advance(200 * time.Millisecond)
	if v.session != "next" || v.snapshot.LogPath != "" || v.exported.LogPath != "" || len(s.exports) != 2 {
		t.Fatal("new session kept old paths or exported automatically")
	}
}

func TestNativeViewStaleExportIsIgnored(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	v.session, v.generation = "old", 1
	v.snapshot = tester.Snapshot{SessionID: "old", Finished: true}
	v.drained = true
	v.exportResults()
	v.session, v.generation = "new", 2
	v.snapshot = tester.Snapshot{SessionID: "new", Finished: true}
	s.exports[0] <- result[tester.ExportResult]{Value: tester.ExportResult{LogPath: "/old.csv", SummaryPath: "/old.txt"}}
	v.poll(time.Unix(100, 0))
	if v.snapshot.LogPath != "" || v.snapshot.SummaryPath != "" || v.exported.LogPath != "" {
		t.Fatal("stale export contaminated new session")
	}
}

func TestNativeViewStatsExclusiveAndLayout(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	tt := ui.NewTester(v.view, 900, 650)
	width, height := float32(900), float32(650)
	finished := "指标：尝试"
	running := "指标：进行中"
	check := func(want, absent string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(want)
		if !ok || tt.HasText(absent) || r.X < 0 || r.Y < 0 || r.X+r.W > width || r.Y+r.H > height {
			t.Fatalf("stats missing, overlapping or out of bounds: %+v %v texts=%v", r, ok, tt.Texts())
		}
		return r
	}
	idle := check(finished, running)
	v.start()
	tt.Frame()
	starting := check(running, finished)
	v.startPending = nil
	v.session = "s"
	v.snapshot = tester.Snapshot{SessionID: "s"}
	tt.Frame()
	active := check(running, finished)
	v.snapshot.Stopping = true
	tt.Frame()
	check(running, finished)
	v.snapshot.Finished = true
	v.drained = true
	tt.Frame()
	end := check(finished, running)
	if idle.Y != starting.Y || idle.Y != active.Y || idle.Y != end.Y {
		t.Fatalf("stats shifted: %v %v %v %v", idle, starting, active, end)
	}
	tt.SetDark(true)
	check(finished, running)
	tt.SetSize(1180, 780)
	width, height = 1180, 780
	check(finished, running)
	tt.SetDark(false)
	check(finished, running)
}

func TestNativeViewFiltersAndCap(t *testing.T) {
	v := newNativeView(&viewStub{})
	v.entries = []tester.LogEntry{{Seq: 1, OK: true}, {Seq: 2}, {Seq: 3, Aborted: true}}
	tt := ui.NewTester(v.view, 900, 650)
	for i, label := range []string{"全部", "成功", "失败", "中止"} {
		r, ok := tt.Find("日志筛选")
		if !ok {
			t.Fatal("missing log filter")
		}
		tt.ClickAt(r.X+r.W*(float32(i)+.5)/4, r.Y+r.H/2)
		if v.filter != i {
			t.Fatalf("filter %q selected %d", label, v.filter)
		}
	}
	for filter, want := range []int{3, 1, 1, 1} {
		v.filter = filter
		got := v.shownEntries()
		if len(got) != want || (filter > 0 && got[0].Seq != uint64(filter)) {
			t.Fatalf("filter %d: %#v", filter, got)
		}
	}
	v.generation = 1
	v.session = "s"
	v.sampleGeneration = 1
	v.sampleSession = "s"
	logs := make([]tester.LogEntry, 800)
	for i := range logs {
		logs[i].Seq = uint64(i + 1)
	}
	v.acceptSample(result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "s"}, Logs: logs, NextSeq: 800}})
	if len(v.entries) != 600 || v.entries[0].Seq != 201 || !v.scrollRequested {
		t.Fatal("log cap or follow failed")
	}
}

func TestNativeViewFollowStopsOnScrollAndCanResume(t *testing.T) {
	v := newNativeView(&viewStub{})
	for i := range 100 {
		v.entries = append(v.entries, tester.LogEntry{Seq: uint64(i + 1), Domain: fmt.Sprintf("example-%d.com", i)})
	}
	v.scrollRequested = true
	tt := ui.NewTester(v.view, 900, 650)
	if !v.follow || !v.table.AtEnd() {
		t.Fatal("initial follow did not reach the end")
	}
	tt.Scroll(400, 400, 0, -220)
	if v.follow {
		t.Fatal("scrolling away should disable follow")
	}
	if err := tt.Click("自动跟随"); err != nil {
		t.Fatal(err)
	}
	if !v.follow || !v.table.AtEnd() {
		t.Fatalf("enabling follow should return to the end: follow=%v end=%v", v.follow, v.table.AtEnd())
	}
}

func TestNativeViewClearIgnoresLateSamplesAndDrains(t *testing.T) {
	s := &viewStub{tail: 5}
	v := newNativeView(s)
	v.session = "s"
	v.generation = 1
	v.snapshot = tester.Snapshot{SessionID: "s", Finished: true}
	v.entries = []tester.LogEntry{{Seq: 1}}
	now := time.Unix(100, 0)
	v.poll(now)
	if len(s.samples) != 1 {
		t.Fatal("expected sample")
	}
	v.clearLogs()
	s.samples[0] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "s", Finished: true}, Logs: []tester.LogEntry{{Seq: 4}}, NextSeq: 4}}
	v.poll(now.Add(200 * time.Millisecond))
	if len(v.entries) != 0 || v.since != 5 || len(s.samples) != 2 {
		t.Fatalf("stale epoch returned: %v %d", v.entries, v.since)
	}
	s.samples[1] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "s", Finished: true}, Logs: []tester.LogEntry{{Seq: 6}}, NextSeq: 6}}
	v.poll(now.Add(400 * time.Millisecond))
	if v.drained || len(v.entries) != 1 || len(s.samples) != 3 {
		t.Fatal("finished sample must drain tail")
	}
	s.samples[2] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "s", Finished: true}, NextSeq: 6}}
	v.poll(now.Add(600 * time.Millisecond))
	if !v.drained || v.since != 6 {
		t.Fatal("empty finished sample must finish draining")
	}
}

func TestNativeViewStartFailureRetainsSession(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	v.session = "old"
	v.snapshot = tester.Snapshot{SessionID: "old", Finished: true}
	v.drained = true
	v.entries = []tester.LogEntry{{Seq: 9}}
	v.start()
	s.start <- result[tester.Snapshot]{Err: errors.New("failure")}
	v.poll(time.Unix(100, 0))
	if v.session != "old" || len(v.entries) != 1 || v.entries[0].Seq != 9 {
		t.Fatal("failed start erased old session")
	}
}

func TestNativeViewTimedPollingAndIdle(t *testing.T) {
	s := &viewStub{}
	v := newNativeView(s)
	tt := ui.NewTester(v.view, 900, 650)
	if tt.FramePending() {
		t.Fatal("idle view should settle")
	}
	if err := tt.Click("开始测试"); err != nil {
		t.Fatal(err)
	}
	if s.start == nil {
		t.Fatal("click did not start")
	}
	s.start <- result[tester.Snapshot]{Value: tester.Snapshot{SessionID: "new"}}
	tt.Advance(200 * time.Millisecond)
	if v.session != "new" || len(s.samples) != 1 {
		t.Fatal("start was not consumed on timed frame")
	}
	s.samples[0] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "new", Finished: true}, Logs: []tester.LogEntry{{Seq: 1}}, NextSeq: 1}}
	tt.Advance(200 * time.Millisecond)
	if v.drained || len(s.samples) != 2 {
		t.Fatalf("finished sample did not continue polling: drained=%v samples=%d since=%d now=%s next=%s", v.drained, len(s.samples), v.since, tt.Now(), v.nextSampleAt)
	}
	s.samples[1] <- result[sampleData]{Value: sampleData{State: tester.Snapshot{SessionID: "new", Finished: true}, NextSeq: 1}}
	tt.Advance(200 * time.Millisecond)
	if !v.drained {
		t.Fatal("finished tail did not drain")
	}
	if tt.FramePending() {
		t.Fatal("drained view should settle")
	}
}

func TestNativeViewInterfaceRefresh(t *testing.T) {
	v := newNativeView(&viewStub{})
	v.iface = "en0"
	ch := make(chan result[[]tester.InterfaceInfo], 1)
	v.interfacesPending = ch
	ch <- result[[]tester.InterfaceInfo]{Value: []tester.InterfaceInfo{{Name: "en0", IsUp: true, IsLoop: true}}}
	v.poll(time.Unix(100, 0))
	if v.iface != "" || v.message == "" {
		t.Fatal(fmt.Sprintf("interface not reset: %q", v.iface))
	}
}
