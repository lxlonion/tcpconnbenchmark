package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"tcpconnbenchmark/internal/i18n"
	"tcpconnbenchmark/internal/tester"
)

type nativeView struct {
	controller          guiController
	window              *mygo.Window
	domainWindow        *mygo.Window
	boot                bootstrapData
	closing, allowClose bool

	domains                                         []string
	draft                                           string
	domainPath, loadedDomainPath, loadedDomainDraft string
	loadedDomainUsable                              bool
	domainDialogOpen                                bool
	editing                                         bool
	mode                                            int
	iface                                           string
	sourceIP                                        string
	interfacePickerOpen                             bool
	interfaceExpanded                               map[string]*bool
	interfaces                                      []tester.InterfaceInfo
	rate, duration, failStop                        string
	message                                         string
	detailsOpen                                     bool
	logsCleared                                     bool

	snapshot                                 tester.Snapshot
	session                                  string
	generation, logEpoch, since              uint64
	entries                                  []tester.LogEntry
	filter                                   int
	follow                                   bool
	followResumeAt                           time.Time
	table                                    ui.ListState
	tableBuilt, scrollRequested, awaitingEnd bool
	drained                                  bool
	nextSampleAt                             time.Time
	startPending                             <-chan result[tester.Snapshot]
	stopPending                              <-chan result[struct{}]
	samplePending                            <-chan result[sampleData]
	sampleGeneration, sampleEpoch            uint64
	sampleSession                            string
	interfacesPending                        <-chan result[[]tester.InterfaceInfo]
	openPending                              <-chan result[struct{}]
	saveDomainsPending                       <-chan result[[]string]
	saveDomainsIgnored                       int
	exportPending                            <-chan result[tester.ExportResult]
	exportGeneration                         uint64
	exportSession                            string
	exported                                 tester.ExportResult
}

func newNativeView(controller guiController) *nativeView {
	b := controller.Bootstrap()
	v := &nativeView{controller: controller, boot: b, domains: append([]string(nil), b.Domains...),
		interfaces: b.Interfaces, rate: strconv.Itoa(b.DefaultRatePerSec), duration: strconv.Itoa(b.DefaultDurationSec),
		failStop: strconv.Itoa(b.DefaultFailStop), follow: true}
	if b.ConfigError != "" {
		v.message = i18n.T("域名配置读取失败：") + b.ConfigError
	}
	return v
}

func (v *nativeView) attach(win *mygo.Window) { v.window = win }
func (v *nativeView) canClose() bool          { return v.allowClose }
func (v *nativeView) requestClose() {
	if v.closing {
		return
	}
	v.closing = true
	done := v.controller.Shutdown()
	win := v.window
	go func() {
		<-done
		if win != nil {
			win.Update(func() { v.allowClose = true; mygo.App.Quit() })
		}
	}()
}

func parseInteger(s string, min, max int) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= min && n <= max
}

func (v *nativeView) locked() bool {
	return v.closing || v.startPending != nil || v.stopPending != nil || v.interfacesPending != nil || v.saveDomainsPending != nil || v.exportPending != nil || (v.session != "" && !v.snapshot.Finished)
}

func (v *nativeView) start() {
	if v.locked() || v.editing {
		return
	}
	r, okR := parseInteger(v.rate, v.boot.MinRatePerSec, v.boot.MaxRatePerSec)
	d, okD := parseInteger(v.duration, 1, 86400)
	f, okF := parseInteger(v.failStop, 0, 10000000)
	if !okR || !okD || !okF || len(v.domains) < 1 || len(v.domains) > 5000 {
		v.message = fmt.Sprintf(i18n.T("请输入有效整数：速率 %d–%d、时长 1–86400、失败停止 0–10000000；域名须为 1–5000 个"), v.boot.MinRatePerSec, v.boot.MaxRatePerSec)
		return
	}
	modes := []tester.Mode{tester.ModeBoth, tester.Mode80, tester.Mode443}
	v.interfacePickerOpen = false
	v.startPending = v.controller.Start(startRequest{Mode: modes[v.mode], Interface: v.iface, SourceIP: v.sourceIP, Domains: append([]string(nil), v.domains...), DurationSec: d, FailStop: f, RatePerSec: r})
	v.message = i18n.T("正在启动…")
}

func (v *nativeView) stop() {
	if v.closing || v.session == "" || v.stopPending != nil || v.snapshot.Finished {
		return
	}
	v.stopPending = v.controller.Stop(v.session)
	v.message = i18n.T("正在停止…")
}

func (v *nativeView) clearLogs() {
	if v.closing {
		return
	}
	var tail uint64
	if v.session != "" {
		var err error
		tail, err = v.controller.Tail(v.session)
		if err != nil {
			v.message = i18n.T("清空视图失败：") + err.Error()
			return
		}
	}
	v.logEpoch++
	v.entries = nil
	v.logsCleared = true
	v.since = tail
	v.message = i18n.T("已清空视图，完整结果未受影响")
	if v.session != "" {
		v.drained = false
		v.nextSampleAt = time.Time{}
	}
}

func (v *nativeView) acceptSample(r result[sampleData]) {
	if v.sampleGeneration != v.generation || v.sampleSession != v.session || v.sampleEpoch != v.logEpoch {
		return
	}
	if r.Err != nil {
		v.message = i18n.T("读取测试状态失败：") + r.Err.Error()
		return
	}
	d := r.Value
	if d.State.SessionID != v.session {
		return
	}
	v.snapshot = d.State
	if v.exported.LogPath != "" {
		v.snapshot.LogPath = v.exported.LogPath
	}
	if v.exported.SummaryPath != "" {
		v.snapshot.SummaryPath = v.exported.SummaryPath
	}
	if d.NextSeq > v.since {
		v.since = d.NextSeq
	}
	if len(d.Logs) > 0 {
		v.logsCleared = false
		for _, entry := range d.Logs {
			v.entries = append(v.entries, entry)
		}
		if len(v.entries) > liveLogCapacity {
			v.entries = append([]tester.LogEntry(nil), v.entries[len(v.entries)-liveLogCapacity:]...)
		}
		if v.follow {
			v.scrollRequested = true
		}
	}
	if d.Dropped {
		v.message = i18n.T("部分实时记录已被覆盖；测试完成后可保存完整结果")
	}
	v.drained = d.State.Finished && len(d.Logs) == 0
}

// poll is called only by the UI thread. Background operations communicate via channels.
func (v *nativeView) poll(now time.Time) {
	if v.startPending != nil {
		select {
		case r := <-v.startPending:
			v.startPending = nil
			if r.Err != nil {
				v.message = i18n.T("启动失败：") + r.Err.Error()
			} else {
				v.generation++
				v.logEpoch++
				v.session = r.Value.SessionID
				v.snapshot = r.Value
				v.exported = tester.ExportResult{}
				v.entries = nil
				v.logsCleared = false
				v.since = 0
				v.drained = false
				v.nextSampleAt = time.Time{}
				v.message = i18n.T("测试已启动")
			}
		default:
		}
	}
	if v.stopPending != nil {
		select {
		case r := <-v.stopPending:
			v.stopPending = nil
			if r.Err != nil {
				v.message = i18n.T("停止失败：") + r.Err.Error()
			} else {
				v.message = i18n.T("停止请求已完成")
			}
		default:
		}
	}
	if v.samplePending != nil {
		select {
		case r := <-v.samplePending:
			v.samplePending = nil
			v.acceptSample(r)
		default:
		}
	}
	if v.interfacesPending != nil {
		select {
		case r := <-v.interfacesPending:
			v.interfacesPending = nil
			if r.Err != nil {
				v.message = i18n.T("刷新网卡失败：") + r.Err.Error()
			} else {
				v.interfaces = r.Value
				if !v.interfaceSelectionAvailable() {
					v.iface, v.sourceIP = "", ""
					v.message = i18n.T("所选网卡或地址已不可用，已切换为自动")
				} else {
					v.message = i18n.T("网卡列表已刷新")
				}
			}
		default:
		}
	}
	if v.openPending != nil {
		select {
		case r := <-v.openPending:
			v.openPending = nil
			if r.Err != nil {
				v.message = i18n.T("打开日志目录失败：") + r.Err.Error()
			} else {
				v.message = i18n.T("已请求打开日志目录")
			}
		default:
		}
	}
	if v.saveDomainsPending != nil {
		select {
		case r := <-v.saveDomainsPending:
			v.saveDomainsPending = nil
			if !v.closing {
				if r.Err != nil {
					v.message = i18n.T("保存域名失败：") + r.Err.Error()
				} else {
					v.domains = append([]string(nil), r.Value...)
					v.boot.ConfigPath = v.loadedDomainPath
					v.boot.ConfigError = ""
					v.editing = false
					v.message = fmt.Sprintf(i18n.T("已保存并应用 %d 个域名，忽略 %d 个无效项"), len(v.domains), v.saveDomainsIgnored)
					v.closeDomainWindow()
				}
			}
		default:
		}
	}
	if v.exportPending != nil {
		select {
		case r := <-v.exportPending:
			v.exportPending = nil
			if !v.closing && v.exportGeneration == v.generation && v.exportSession == v.session {
				if r.Err != nil {
					v.message = i18n.T("保存结果失败：") + r.Err.Error()
				} else {
					v.exported = r.Value
					v.snapshot.LogPath, v.snapshot.SummaryPath = r.Value.LogPath, r.Value.SummaryPath
					v.message = i18n.T("结果已保存")
				}
			}
		default:
		}
	}
	if !v.closing && v.session != "" && !v.drained && v.samplePending == nil && !now.Before(v.nextSampleAt) {
		v.sampleGeneration, v.sampleEpoch, v.sampleSession = v.generation, v.logEpoch, v.session
		v.samplePending = v.controller.Sample(v.session, v.since)
		v.nextSampleAt = now.Add(200 * time.Millisecond)
	}
}

func (v *nativeView) needsWake() bool {
	return !v.closing && (v.startPending != nil || v.stopPending != nil || v.samplePending != nil || v.interfacesPending != nil || v.openPending != nil || v.saveDomainsPending != nil || v.exportPending != nil || (v.session != "" && !v.drained))
}

func (v *nativeView) shownEntries() []tester.LogEntry {
	out := make([]tester.LogEntry, 0, len(v.entries))
	for _, e := range v.entries {
		match := v.filter == 0 || (v.filter == 1 && e.OK && !e.Aborted) || (v.filter == 2 && !e.OK && !e.Aborted) || (v.filter == 3 && e.Aborted)
		if match {
			out = append(out, e)
		}
	}
	return out
}

func (v *nativeView) editDomains() {
	if !v.locked() && !v.editing {
		v.draft = strings.Join(v.domains, "\n")
		v.domainPath, v.loadedDomainPath, v.loadedDomainDraft = v.boot.ConfigPath, v.boot.ConfigPath, v.draft
		v.loadedDomainUsable = v.boot.ConfigError == "" || v.boot.ConfigPath == filepath.Join(v.boot.AppDir, "domains.json")
		v.editing = true
		if v.window != nil {
			v.window.Update(func() {
				if !v.editing || v.closing {
					return
				}
				child := mygo.NewWindow(mygo.WindowOptions{
					Title: i18n.T("编辑测试域名"), Width: 800, Height: 650,
					MinWidth: 600, MinHeight: 450, Parent: v.window, Modal: true,
					Content: ui.View(v.domainEditorView),
				})
				v.domainWindow = child
				child.OnClose(func(event *mygo.CloseEvent) {
					if v.saveDomainsPending != nil || v.domainDialogOpen {
						event.PreventDefault()
					}
				})
				child.OnClosed(func() {
					v.domainWindow = nil
					v.editing = false
					v.window.Invalidate()
				})
			})
		}
	}
}

// Loading another file replaces the draft only after explicit confirmation.
func (v *nativeView) loadDomainPath(path string, choose bool) {
	child := v.domainWindow
	if child == nil || v.domainDialogOpen {
		return
	}
	v.domainDialogOpen = true
	dirty := v.draft != v.loadedDomainDraft
	go func() {
		selection := prepareDomainLoad(path, choose, dirty,
			func(path string) ([]string, error) {
				return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: child, Title: i18n.T("打开域名配置文件"), DefaultPath: path})
			},
			func() (bool, error) {
				result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: child, Title: i18n.T("放弃当前草稿？"), Message: i18n.T("加载其他配置文件将替换尚未保存的草稿。"), Buttons: []string{i18n.T("取消"), i18n.T("继续加载")}, CancelButton: 0})
				return result.Button == 1, err
			})
		child.Update(func() {
			v.finishDomainLoad(selection)
		})
	}()
}

type domainLoadResult struct {
	path     string
	domains  []string
	canceled bool
	err      error
}

func prepareDomainLoad(path string, choose, dirty bool, open func(string) ([]string, error), confirm func() (bool, error)) domainLoadResult {
	if choose {
		paths, err := open(path)
		if err != nil {
			return domainLoadResult{err: err}
		}
		if len(paths) == 0 {
			return domainLoadResult{canceled: true}
		}
		path = paths[0]
	}
	if dirty {
		ok, err := confirm()
		if err != nil {
			return domainLoadResult{err: err}
		}
		if !ok {
			return domainLoadResult{canceled: true}
		}
	}
	if !filepath.IsAbs(path) {
		return domainLoadResult{err: fmt.Errorf("%s", i18n.T("配置文件路径须为绝对路径"))}
	}
	path = filepath.Clean(path)
	domains, err := loadDomainConfigFile(path, nil, false)
	return domainLoadResult{path: path, domains: domains, err: err}
}

func (v *nativeView) finishDomainLoad(r domainLoadResult) {
	v.domainDialogOpen = false
	if r.canceled {
		return
	}
	if r.err != nil {
		v.message = i18n.T("打开域名配置失败：") + r.err.Error()
		return
	}
	v.domainPath, v.loadedDomainPath = r.path, r.path
	v.loadedDomainUsable = true
	v.draft = strings.Join(r.domains, "\n")
	v.loadedDomainDraft = v.draft
	v.message = i18n.T("已加载配置；保存并应用后记住该位置")
}

func (v *nativeView) closeDomainWindow() {
	if child := v.domainWindow; child != nil {
		child.Update(func() { child.Close() })
	}
}

func (v *nativeView) domainEditorView(c *ui.Context) {
	v.poll(c.Now())
	if v.needsWake() {
		c.After(200 * time.Millisecond)
	}
	ui.Column(c).Fill().Padding(14).Gap(8).Children(func() {
		v.domainEditor(c)
	})
}
func (v *nativeView) applyDomains() {
	if v.closing || v.saveDomainsPending != nil {
		return
	}
	valid, invalid := tester.NormalizeDomains([]string{v.draft})
	if len(valid) < 1 || len(valid) > 5000 {
		v.message = i18n.T("有效域名须为 1–5000 个")
		return
	}
	v.saveDomainsIgnored = len(invalid)
	if v.domainPath != v.loadedDomainPath || !v.loadedDomainUsable {
		v.message = i18n.T("请先加载指定的配置文件")
		return
	}
	v.saveDomainsPending = v.controller.SaveDomainsAt(v.loadedDomainPath, valid)
	v.message = i18n.T("正在保存域名…")
}

func (v *nativeView) exportResults() {
	if v.locked() || v.editing || v.session == "" || !v.snapshot.Finished {
		return
	}
	v.exportGeneration, v.exportSession = v.generation, v.session
	v.exportPending = v.controller.Export(v.session)
	v.message = i18n.T("正在保存结果…")
}

func (v *nativeView) view(c *ui.Context) {
	// AtEnd describes the last laid-out table, before fresh rows are appended.
	if v.follow && v.tableBuilt && !v.scrollRequested {
		if v.awaitingEnd {
			if v.table.AtEnd() {
				v.awaitingEnd = false
			}
		} else if !v.table.AtEnd() {
			v.follow = false
			v.followResumeAt = c.Now().Add(5 * time.Second)
		}
	}
	if !v.follow && !v.followResumeAt.IsZero() {
		if !c.Now().Before(v.followResumeAt) {
			v.follow = true
			v.followResumeAt = time.Time{}
			v.scrollRequested = true
		} else {
			c.After(v.followResumeAt.Sub(c.Now()))
		}
	}
	v.poll(c.Now())
	if v.needsWake() {
		delay := 200 * time.Millisecond
		if v.session != "" && !v.drained && v.samplePending == nil && v.nextSampleAt.After(c.Now()) {
			delay = min(delay, v.nextSampleAt.Sub(c.Now()))
		}
		c.After(delay)
	}
	ui.Column(c).Fill().Padding(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c.Key("main-content")).Fill().MaxWidth(1280).Gap(8).Children(func() {
			v.header(c)
			v.form(c)
			v.stats(c)
			v.logs(c)
			v.footer(c)
		})
	})
}

func (v *nativeView) header(c *ui.Context) {
	ui.Row(c).Height(42).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "tcpconnbenchmark").FontSize(22).Bold()
			ui.Text(c, i18n.T("TCP 并发连接测试 · 仅握手，不发送 HTTP / TLS 数据")).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
		})
		v.lifecyclePill(c)
	})
}

func (v *nativeView) form(c *ui.Context) {
	locked := v.locked()
	ui.Column(c.Key("controls")).Padding(12).Gap(10).Radius(10).Border(1, c.Theme().Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
			ui.Column(c).Width(235).Gap(5).Children(func() {
				ui.Text(c, i18n.T("测试模式")).FontSize(11).TextColor(c.Theme().TextMuted)
				ui.Segmented(c.Key("mode"), &v.mode, "80 + 443", "80", "443").Height(32).Label(i18n.T("测试模式")).Disabled(locked)
			})
			ui.Column(c).Grow(1).Gap(5).Children(func() {
				ui.Text(c, i18n.T("出口网卡")).FontSize(11).TextColor(c.Theme().TextMuted)
				v.interfacePicker(c, locked)
			})
			if ui.Button(c.Key("refresh"), i18n.T("刷新网卡")).Height(32).Disabled(locked || v.interfacesPending != nil).Clicked() {
				v.interfacesPending = v.controller.Interfaces()
			}
		})
		ui.Row(c).Gap(16).AlignItems(ui.End).Children(func() {
			ui.Grid(c).Columns(3).Gap(12).Width(444).Children(func() {
				v.numberField(c, i18n.T("发起速率（次/秒）"), "rate", &v.rate, locked)
				v.numberField(c, i18n.T("测试时长（秒）"), "duration", &v.duration, locked)
				v.numberField(c, i18n.T("失败停止（0 不限）"), "fail-stop", &v.failStop, locked)
			})
			ui.Spacer(c)
			if ui.Button(c.Key("domains"), fmt.Sprintf(i18n.T("域名 %d 个 · 编辑"), len(v.domains))).Height(36).Width(132).Border(1, c.Theme().Border).Disabled(locked).Clicked() {
				v.editDomains()
			}
			if v.session != "" && !v.snapshot.Finished {
				if ui.PrimaryButton(c.Key("stop"), i18n.T("停止测试")).Label(i18n.T("停止测试")).Height(36).Width(110).FontSize(14).Bold().Disabled(v.closing || v.stopPending != nil || v.snapshot.Stopping).Clicked() {
					v.stop()
				}
			} else if ui.PrimaryButton(c.Key("start"), i18n.T("开始测试")).Label(i18n.T("开始测试")).Height(36).Width(110).FontSize(14).Bold().Disabled(locked).Clicked() {
				v.start()
			}
		})
	})
}

func (v *nativeView) numberField(c *ui.Context, label, key string, value *string, locked bool) {
	ui.Column(c).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.TextInput(c.Key(key), value).Height(32).Font("monospace").Label(label).Disabled(locked)
	})
}

func (v *nativeView) domainEditor(c *ui.Context) {
	valid, invalid := tester.NormalizeDomains([]string{v.draft})
	locked := v.closing || v.saveDomainsPending != nil || v.domainDialogOpen
	ui.Text(c, i18n.T("测试域名：每行一个，也可粘贴网址；空行和 # 注释会被忽略")).Bold()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("配置文件："))
		if ui.TextInput(c.Key("domain-path"), &v.domainPath).Label(i18n.T("配置文件路径（输入后按回车加载）")).Grow(1).Disabled(locked).Submitted() && !locked {
			v.loadDomainPath(v.domainPath, false)
		}
		if ui.Button(c.Key("domain-open"), i18n.T("打开")).Disabled(locked).Clicked() {
			v.loadDomainPath(v.domainPath, true)
		}
	})
	ui.Text(c, fmt.Sprintf(i18n.T("有效 %d 个 · 无效 %d 个（最多 5000 个）"), len(valid), len(invalid)))
	if len(invalid) > 0 {
		ui.Text(c, i18n.T("无效：")+strings.Join(invalid[:min(3, len(invalid))], "、")).SingleLine()
	}
	ui.TextArea(c.Key("domain-draft"), &v.draft).Label(i18n.T("域名草稿")).Disabled(locked).Grow(1)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c.Key("domain-reset"), i18n.T("恢复内置")).Disabled(locked).Clicked() {
			v.draft = strings.Join(v.boot.BuiltinDomains, "\n")
		}
		if ui.Button(c.Key("domain-clear"), i18n.T("清空草稿")).Disabled(locked).Clicked() {
			v.draft = ""
		}
		ui.Spacer(c)
		if ui.Button(c.Key("domain-cancel"), i18n.T("取消")).Disabled(locked).Clicked() {
			v.editing = false
			v.closeDomainWindow()
		}
		label := "保存并应用"
		if v.saveDomainsPending != nil {
			label = "正在保存…"
		}
		if ui.PrimaryButton(c.Key("domain-apply"), i18n.T(label)).Disabled(locked || v.domainPath != v.loadedDomainPath || !v.loadedDomainUsable || len(valid) == 0 || len(valid) > 5000).Clicked() {
			v.applyDomains()
		}
	})
	if v.message != "" {
		ui.Text(c, v.message).SingleLine()
	}
}

func (v *nativeView) stats(c *ui.Context) {
	s := v.snapshot
	if v.session == "" {
		if duration, ok := parseInteger(v.duration, 1, 86400); ok {
			s.DurationSec, s.Remaining = duration, float64(duration)
		}
		if failStop, ok := parseInteger(v.failStop, 0, 10000000); ok {
			s.FailStop = failStop
		}
	}
	v.kpiCards(c, s)
	progress := 0.0
	if s.DurationSec > 0 {
		progress = min(1, max(0, s.ElapsedSec/float64(s.DurationSec)))
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf(i18n.T("已运行 %.0f 秒 · 剩余 %.0f 秒"), s.ElapsedSec, s.Remaining)).FontFeatures("tnum").Bold().TextColor(c.Theme().Accent)
		ui.Progress(c.Key("progress"), progress).Height(6).Label(i18n.T("测试进度")).Grow(1)
		if s.FailStop > 0 {
			ui.Text(c, fmt.Sprintf(i18n.T("失败 %d / %d"), s.Failed, s.FailStop))
			ui.Progress(c.Key("failure-progress"), min(1, float64(s.Failed)/float64(s.FailStop))).Label(i18n.T("失败上限进度")).Width(100)
		} else {
			ui.Text(c, i18n.T("失败上限：不限"))
		}
	})
	if s.StopReason != "" {
		ui.Text(c, i18n.T("停止原因：")+i18n.T(s.StopReason)).SingleLine()
	}
	if s.LogError != "" {
		ui.Text(c, i18n.T("日志错误：")+s.LogError).TextColor(c.Theme().Danger).SingleLine().Tooltip(s.LogError)
	}
}

func (v *nativeView) logs(c *ui.Context) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, i18n.T("实时连接日志")).Bold()
		ui.Segmented(c.Key("filter"), &v.filter, i18n.T("全部"), i18n.T("成功"), i18n.T("失败"), i18n.T("中止")).Label(i18n.T("日志筛选"))
		ui.Spacer(c)
		if ui.Button(c.Key("clear-logs"), i18n.T("清空视图")).Disabled(v.closing).Clicked() {
			v.clearLogs()
		}
	})
	rows := v.shownEntries()
	v.table.Key = func(i int) any { return rows[i].Seq }
	v.table.FollowEnd = v.follow
	if v.scrollRequested {
		v.table.ScrollToEnd()
		v.scrollRequested = false
		v.awaitingEnd = true
	}
	ui.Box(c.Key("log-area")).Grow(1).MinHeight(50).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputScroll && ev.DY != 0 && ev.Mods&ui.Shift == 0 && (ev.DY < 0 || !v.follow) {
			v.follow = false
			v.followResumeAt = ev.Time.Add(5 * time.Second)
			v.scrollRequested = false
			v.awaitingEnd = false
		}
		return false
	}).Children(func() {
		ui.Table(c.Key("log-table"), &v.table, logColumns(), len(rows), func(row, col int) {
			e := rows[row]
			logCell(c, e, col)
		}).Label(i18n.T("连接日志")).Fill()
		if len(rows) == 0 {
			ui.Box(c.Key("log-empty")).Absolute().Top(30).Bottom(0).Left(0).Right(0).Center().PassThrough().Children(func() {
				ui.Text(c, v.emptyLogText()).TextColor(c.Theme().TextMuted).FontSize(13).SingleLine()
			})
		}
	})
	v.tableBuilt = true
}

func (v *nativeView) footer(c *ui.Context) {
	ui.Column(c.Key("footer")).Gap(3).Children(func() {
		v.footerContents(c)
	})
}

func (v *nativeView) footerContents(c *ui.Context) {
	if v.message != "" {
		color := c.Theme().TextMuted
		if strings.Contains(v.message, "失败") || strings.Contains(v.message, "Failed") {
			color = c.Theme().Danger
		}
		ui.Text(c, v.message).TextColor(color).SingleLine().Tooltip(v.message)
	}
	if v.boot.ConfigError != "" && v.message != i18n.T("域名配置读取失败：")+v.boot.ConfigError {
		ui.Text(c, i18n.T("域名配置读取失败：")+v.boot.ConfigError).TextColor(c.Theme().Danger).SingleLine().Tooltip(v.boot.ConfigError)
	}
	if v.session != "" && v.snapshot.Finished && (v.snapshot.LogPath == "" || v.snapshot.SummaryPath == "") {
		ui.Text(c, i18n.T("结果尚未保存，开始新测试或关闭后丢失")).TextColor(c.Theme().Accent).SingleLine()
	}
	ui.Row(c).Children(func() {
		label := "▸ 日志"
		if v.detailsOpen {
			label = "▾ 日志"
		}
		if ui.Button(c.Key("storage-details"), i18n.T(label)).Label(i18n.T("日志")).Tooltip(i18n.T("展开或收起日志路径与结果操作")).Clicked() {
			v.detailsOpen = !v.detailsOpen
		}
	})
	if v.detailsOpen {
		v.storageDetails(c)
	}
	v.statusBar(c)
}

func (v *nativeView) storageActions(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c.Key("open-logs"), i18n.T("打开目录")).Disabled(v.closing || v.openPending != nil).Clicked() {
			v.openPending = v.controller.OpenLogs()
		}
		label := "保存结果"
		if v.exportPending != nil {
			label = "保存中…"
		}
		if ui.Button(c.Key("export-results"), i18n.T(label)).Disabled(v.locked() || v.editing || v.session == "" || !v.snapshot.Finished).Clicked() {
			v.exportResults()
		}
	})
}
