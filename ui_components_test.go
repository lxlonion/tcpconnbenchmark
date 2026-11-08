package main

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"tcpconnbenchmark/internal/i18n"
	"tcpconnbenchmark/internal/syslimit"
	"tcpconnbenchmark/internal/tester"
)

func polishView(state string) *nativeView {
	v := newNativeView(&viewStub{})
	v.boot.Version = "master@01b0d35+dirty"
	v.boot.FDLimit = syslimit.Info{Supported: true, Soft: 65536, Hard: 1<<63 - 1, Target: 65536}
	v.domains = append([]string(nil), tester.BuiltinDomains...)
	if state != "idle" {
		v.session, v.drained = "ui-demo", true
		v.snapshot = tester.Snapshot{SessionID: "ui-demo", DurationSec: 360, ElapsedSec: 120, Remaining: 240, Attempts: 50000, Success: 49950, Failed: 25, Aborted: 25, SuccessRate: 99.95, Active: 49000, Dialing: 12, Closed: 950, Max: 49500, AvgRate: 416.7, FailStop: 50}
		v.entries = []tester.LogEntry{{Seq: 49998, Time: "12:00:01.000", OK: true, Domain: "example.com", IP: "192.0.2.1", Port: 443, LatencyMS: 12.3}, {Seq: 49999, Time: "12:00:01.010", Domain: "example.org", IP: "192.0.2.2", Port: 80, LatencyMS: 5000, Error: "连接超时"}, {Seq: 50000, Time: "12:00:01.020", Aborted: true, Domain: "example.net", Port: 443, LatencyMS: 28.4, Error: "测试停止，已取消"}}
	}
	if state == "finished" {
		v.snapshot.Finished = true
		v.snapshot.Stopping = true
		v.snapshot.Active = 0
		v.snapshot.Dialing = 0
		v.snapshot.StopReason = "手动停止"
	}
	return v
}

func assertInViewport(t *testing.T, tt *ui.Tester, label string, width, height int) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+.5 || r.Y+r.H > float32(height)+.5 {
		t.Fatalf("%q missing or outside viewport: %+v found=%v", label, r, ok)
	}
	return r
}

func TestPolishedStateLayouts(t *testing.T) {
	for _, size := range [][2]int{{900, 650}, {1180, 780}, {2000, 900}} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"idle", "running", "finished"} {
				t.Run(fmt.Sprintf("%s-%dx%d-dark%v", state, size[0], size[1], dark), func(t *testing.T) {
					v := polishView(state)
					tt := ui.NewTester(v.view, size[0], size[1])
					tt.SetDark(dark)
					labels, absent := []string{"尝试", "成功", "失败", "中止", "成功率"}, []string{"进行中", "拨号中", "已关闭", "峰值", "平均速率"}
					if state == "running" {
						labels, absent = absent, labels
					}
					for i, label := range labels {
						r := assertInViewport(t, tt, "指标："+label, size[0], size[1])
						if i > 0 {
							prev, _ := tt.Find("指标：" + labels[i-1])
							if r.X < prev.X+prev.W || r.Y != prev.Y {
								t.Fatal("KPI cards overlap or are not aligned")
							}
						}
					}
					for _, label := range absent {
						if _, ok := tt.Find("指标：" + label); ok {
							t.Fatalf("both KPI groups shown: %s", label)
						}
					}
					for _, label := range []string{"发起速率（次/秒）", "测试时长（秒）", "失败停止（0 不限）", "导出", "文件句柄状态", "版本", "连接日志"} {
						assertInViewport(t, tt, label, size[0], size[1])
					}
					// Numeric inputs stay compact and equal-width even in wide windows.
					a, _ := tt.Find("发起速率（次/秒）")
					b, _ := tt.Find("测试时长（秒）")
					d, _ := tt.Find("失败停止（0 不限）")
					if a.W > 140 || math.Abs(float64(a.W-b.W)) > .01 || math.Abs(float64(a.W-d.W)) > .01 || a.Y != b.Y || a.Y != d.Y {
						t.Fatalf("uneven parameters: %+v %+v %+v", a, b, d)
					}
					primary := "开始测试"
					if state == "running" {
						primary = "停止测试"
					}
					p := assertInViewport(t, tt, primary, size[0], size[1])
					if p.H < 36 {
						t.Fatal("primary action too small")
					}
					if state != "running" && tt.FramePending() {
						t.Fatal("idle/finished must settle")
					}
					if dir := os.Getenv("TCPCONNBENCHMARK_POLISH_SCREENSHOTS"); dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							t.Fatal(err)
						}
						mode := "light"
						if dark {
							mode = "dark"
						}
						path := filepath.Join(dir, fmt.Sprintf("%s-%dx%d-%s.png", state, size[0], size[1], mode))
						f, err := os.Create(path)
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(f, tt.Image())
						closeErr := f.Close()
						if err != nil {
							t.Fatal(err)
						}
						if closeErr != nil {
							t.Fatal(closeErr)
						}
					}
				})
			}
		}
	}
}

func TestPolishedPathsCollapsedAndOneClickCopy(t *testing.T) {
	v := polishView("finished")
	v.snapshot.LogPath = "/portable/" + strings.Repeat("long-directory/", 20) + "results.csv"
	v.snapshot.SummaryPath = "/portable/" + strings.Repeat("long-directory/", 20) + "summary.txt"
	tt := ui.NewTester(v.view, 900, 650)
	for _, label := range []string{"应用目录", "域名配置路径", "CSV 日志路径", "摘要文件路径", "日志目录", "打开目录", "保存结果"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("path visible while collapsed: %s", label)
		}
	}
	if err := tt.Click("导出"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"打开目录", "保存结果"} {
		assertInViewport(t, tt, label, 900, 650)
	}
	for _, p := range []struct{ label, value string }{{"应用目录", v.boot.AppDir}, {"域名配置路径", v.boot.ConfigPath}, {"CSV 日志路径", v.snapshot.LogPath}, {"摘要文件路径", v.snapshot.SummaryPath}, {"日志目录", v.boot.LogDir}} {
		assertInViewport(t, tt, p.label, 900, 650)
		if err := tt.Click("复制" + p.label); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != p.value {
			t.Fatalf("wrong clipboard for %s", p.label)
		}
	}
	if err := tt.Click("导出"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"CSV 日志路径", "打开目录", "保存结果"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("details did not close: %s", label)
		}
	}
}

func TestContentWidthIsCappedAndCentered(t *testing.T) {
	for _, width := range []int{900, 1180, 1600, 2400} {
		v := polishView("idle")
		tt := ui.NewTester(v.view, width, 780)
		first := assertInViewport(t, tt, "指标：尝试", width, 780)
		last := assertInViewport(t, tt, "指标：成功率", width, 780)
		contentWidth := last.X + last.W - first.X
		want := float32(min(width-28, 1280))
		if math.Abs(float64(contentWidth-want)) > .5 || math.Abs(float64(first.X-(float32(width)-want)/2)) > .5 {
			t.Fatalf("content not capped/centered at %d: first=%+v last=%+v", width, first, last)
		}
		if err := tt.Click("导出"); err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"打开目录", "保存结果", "CSV 日志路径", "文件句柄状态", "连接日志"} {
			r := assertInViewport(t, tt, label, width, 780)
			if r.X < first.X-.5 || r.X+r.W > last.X+last.W+.5 {
				t.Fatalf("%s outside centered content: %+v", label, r)
			}
		}
	}
}

func TestPolishedEmptyStates(t *testing.T) {
	v := newNativeView(&viewStub{})
	tt := ui.NewTester(v.view, 900, 650)
	if !tt.HasText("暂无测试日志，点击「开始测试」") {
		t.Fatal("missing initial empty state")
	}
	v.entries = []tester.LogEntry{{Seq: 1, OK: true}}
	v.filter = 2
	tt.Frame()
	if !tt.HasText("当前筛选没有匹配记录") {
		t.Fatal("missing filtered empty state")
	}
	v.clearLogs()
	tt.Frame()
	if !tt.HasText("视图已清空，完整结果仍可在测试结束后保存") || tt.HasText("暂无测试日志，点击「开始测试」") {
		t.Fatal("cleared logs confused with initial state")
	}
}

func TestPolishedLifecycleAndMotion(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		v := polishView("idle")
		idle := v.lifecycle(theme, false)
		if idle.text != "空闲" || idle.pulse {
			t.Fatal("wrong idle lifecycle")
		}
		v.session = "s"
		if s := v.lifecycle(theme, false); s.color != theme.Success || !s.pulse {
			t.Fatal("running must use success and gentle pulse")
		}
		if v.lifecycle(theme, true).pulse {
			t.Fatal("reduce-motion must disable pulse")
		}
		v.snapshot.Stopping = true
		if s := v.lifecycle(theme, false); s.text != "正在停止" || s.pulse || s.color != theme.Warning {
			t.Fatal("wrong stopping lifecycle")
		}
		v.snapshot.Finished = true
		if s := v.lifecycle(theme, false); s.text != "已结束" || s.pulse {
			t.Fatal("finished/stopping precedence regressed")
		}
		v.snapshot.StopReason = "累计失败次数达到上限"
		if s := v.lifecycle(theme, false); s.text != "已终止" || s.color != theme.Danger {
			t.Fatal("failure not emphasized")
		}
		v.snapshot.StopReason = "达到测试时长"
		if s := v.lifecycle(theme, false); s.text != "已完成" || s.color != theme.Success {
			t.Fatal("completion not recognized")
		}
	}
	v := polishView("running")
	tt := ui.NewTester(v.view, 900, 650)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("reduce-motion drawing keeps requesting frames")
	}
	v.snapshot.Finished = true
	tt.Frame()
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("finished view keeps animating")
	}
}

func TestEnglishLifecycleReasons(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	theme := ui.LightTheme()
	v := polishView("finished")
	for _, tc := range []struct {
		reason, text string
		color        ui.Color
	}{
		{i18n.T("失败次数达到上限"), "Terminated", theme.Danger},
		{i18n.T("日志暂存失败"), "Terminated", theme.Danger},
		{"Details: " + i18n.T("日志暂存失败"), "Terminated", theme.Danger},
		{i18n.T("达到测试时长"), "Completed", theme.Success},
		{"Timer: " + i18n.T("达到测试时长"), "Completed", theme.Success},
		{i18n.T("手动停止"), "Stopped", theme.TextMuted},
		{"", "Ended", theme.TextMuted},
	} {
		v.snapshot.StopReason = tc.reason
		if got := v.lifecycle(theme, false); got.text != tc.text || got.color != tc.color {
			t.Errorf("reason %q: got %q, want %q", tc.reason, got.text, tc.text)
		}
	}
}

func TestEnglishGUIText(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	v := polishView("idle")
	tt := ui.NewTester(v.view, 900, 650)
	tt.Frame()
	for _, text := range []string{"Test mode", "Outgoing interface", "Start test", "Live connection log", "Idle"} {
		if !tt.HasText(text) {
			t.Errorf("missing English GUI text %q", text)
		}
	}
}

func TestPolishedLogBadgesAndColumns(t *testing.T) {
	if cols := logColumns(); cols[6].Align != ui.End || cols[6].Title != "耗时" {
		t.Fatal("latency header must align with numeric cells")
	}
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		for _, tc := range []struct {
			entry tester.LogEntry
			label string
			color ui.Color
		}{{tester.LogEntry{OK: true}, "成功", theme.Success}, {tester.LogEntry{}, "失败", theme.Danger}, {tester.LogEntry{Aborted: true}, "中止", theme.Warning}} {
			label, color := logStatus(theme, tc.entry)
			if label != tc.label || color != tc.color {
				t.Fatal("incorrect log status tone")
			}
		}
	}
	tt := ui.NewTester(polishView("finished").view, 900, 650)
	for _, label := range []string{"日志状态：成功", "日志状态：失败", "日志状态：中止"} {
		assertInViewport(t, tt, label, 900, 650)
	}
	first := assertInViewport(t, tt, "12.3 ms", 900, 650)
	for _, label := range []string{"5000.0 ms", "28.4 ms"} {
		r := assertInViewport(t, tt, label, 900, 650)
		if math.Abs(float64(first.X+first.W-r.X-r.W)) > .5 {
			t.Fatal("latency cells are not right-aligned")
		}
	}
}

func TestPolishedPulseOnlyRepaints(t *testing.T) {
	v := polishView("running")
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; v.view(c) }, 900, 650)
	before, n := append([]byte(nil), tt.Image().Pix...), builds
	tt.Advance(800 * time.Millisecond)
	if builds != n {
		t.Fatalf("pulse rebuilt the view: %d -> %d", n, builds)
	}
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("running indicator did not breathe")
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	before = append([]byte(nil), tt.Image().Pix...)
	tt.Advance(time.Second)
	if !bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("reduce-motion indicator should remain static")
	}
}

func TestPolishedWarningsStayVisible(t *testing.T) {
	v := polishView("finished")
	v.boot.ConfigError = "配置文件损坏，已回退内置域名"
	v.snapshot.LogError = "测试记录暂存异常"
	v.message = "保存结果失败：目录不可写"
	tt := ui.NewTester(v.view, 900, 650)
	warnings := []string{"域名配置读取失败：" + v.boot.ConfigError, "日志错误：" + v.snapshot.LogError, v.message}
	if tt.HasText("结果尚未保存，开始新测试或关闭后丢失") {
		t.Fatal("unsaved warning should not be shown")
	}
	for _, label := range warnings {
		assertInViewport(t, tt, label, 900, 650)
	}
	if v.detailsOpen {
		t.Fatal("warnings should not need expanded paths")
	}
	if err := tt.Click("导出"); err != nil {
		t.Fatal(err)
	}
	for _, label := range warnings {
		assertInViewport(t, tt, label, 900, 650)
	}
	for _, label := range []string{"应用目录", "域名配置路径", "CSV 日志路径", "摘要文件路径", "文件句柄状态", "版本"} {
		assertInViewport(t, tt, label, 900, 650)
	}
}

func TestStatusMessagesFollowFileLimit(t *testing.T) {
	for _, message := range []string{"测试已启动", "结果已保存", "保存结果失败：目录不可写"} {
		for _, width := range []int{900, 1180} {
			v := polishView("finished")
			v.message = message
			tt := ui.NewTester(v.view, width, 650)
			fd := assertInViewport(t, tt, "文件句柄状态", width, 650)
			status := assertInViewport(t, tt, message, width, 650)
			version := assertInViewport(t, tt, "版本", width, 650)
			if status.X < fd.X+fd.W || status.X+status.W > version.X || math.Abs(float64(status.Y+status.H/2-fd.Y-fd.H/2)) > 2 {
				t.Fatalf("status not beside file limit: fd=%+v status=%+v version=%+v", fd, status, version)
			}
		}
	}
}

func TestPolishedVersionAndLimits(t *testing.T) {
	if limitLabel(1<<63-1) != "∞" || limitLabel(65536) != "65536" {
		t.Fatal("incorrect compact limit")
	}
	v := polishView("idle")
	tt := ui.NewTester(v.view, 900, 650)
	r, _ := tt.Find("版本")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	if !tt.HasText("master@01b0d35+dirty") || tt.HasText("vmaster@01b0d35+dirty") {
		t.Fatalf("version not shown verbatim: %v", tt.Texts())
	}
}
