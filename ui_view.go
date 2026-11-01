package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"tcplink/internal/tester"
)

const logViewLimit = 600

type nativeView struct {
	controller          guiController
	window              *mygo.Window
	boot                bootstrapData
	closing, allowClose bool

	domains                  []string
	draft                    string
	editing                  bool
	mode                     int
	iface                    string
	interfaces               []tester.InterfaceInfo
	rate, duration, failStop string
	message                  string

	snapshot                                 tester.Snapshot
	session                                  string
	generation, logEpoch, since              uint64
	entries                                  []tester.LogEntry
	filter                                   int
	follow                                   bool
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
}

func newNativeView(controller guiController) *nativeView {
	b := controller.Bootstrap()
	return &nativeView{controller: controller, boot: b, domains: append([]string(nil), b.Domains...),
		interfaces: b.Interfaces, rate: strconv.Itoa(b.DefaultRatePerSec), duration: strconv.Itoa(b.DefaultDurationSec),
		failStop: strconv.Itoa(b.DefaultFailStop), follow: true}
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
	return v.closing || v.startPending != nil || v.stopPending != nil || v.interfacesPending != nil || (v.session != "" && !v.snapshot.Finished)
}

func (v *nativeView) start() {
	if v.locked() || v.editing {
		return
	}
	r, okR := parseInteger(v.rate, v.boot.MinRatePerSec, v.boot.MaxRatePerSec)
	d, okD := parseInteger(v.duration, 1, 86400)
	f, okF := parseInteger(v.failStop, 0, 10000000)
	if !okR || !okD || !okF || len(v.domains) < 1 || len(v.domains) > 5000 {
		v.message = fmt.Sprintf("请输入有效整数：速率 %d–%d、时长 1–86400、失败停止 0–10000000；域名须为 1–5000 个", v.boot.MinRatePerSec, v.boot.MaxRatePerSec)
		return
	}
	modes := []tester.Mode{tester.ModeBoth, tester.Mode80, tester.Mode443}
	v.startPending = v.controller.Start(startRequest{Mode: modes[v.mode], Interface: v.iface, Domains: append([]string(nil), v.domains...), DurationSec: d, FailStop: f, RatePerSec: r})
	v.message = "正在启动…"
}

func (v *nativeView) stop() {
	if v.closing || v.session == "" || v.stopPending != nil || v.snapshot.Finished {
		return
	}
	v.stopPending = v.controller.Stop(v.session)
	v.message = "正在停止…"
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
			v.message = "清空视图失败：" + err.Error()
			return
		}
	}
	v.logEpoch++
	v.entries = nil
	v.since = tail
	v.message = "已清空视图，日志文件不受影响"
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
		v.message = "读取测试状态失败：" + r.Err.Error()
		return
	}
	d := r.Value
	if d.State.SessionID != v.session {
		return
	}
	v.snapshot = d.State
	if d.NextSeq > v.since {
		v.since = d.NextSeq
	}
	if len(d.Logs) > 0 {
		for _, entry := range d.Logs {
			v.entries = append(v.entries, entry)
		}
		if len(v.entries) > logViewLimit {
			v.entries = append([]tester.LogEntry(nil), v.entries[len(v.entries)-logViewLimit:]...)
		}
		if v.follow {
			v.scrollRequested = true
		}
	}
	if d.Dropped {
		v.message = "部分实时记录已被覆盖；请查看日志文件"
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
				v.message = "启动失败：" + r.Err.Error()
			} else {
				v.generation++
				v.logEpoch++
				v.session = r.Value.SessionID
				v.snapshot = r.Value
				v.entries = nil
				v.since = 0
				v.drained = false
				v.nextSampleAt = time.Time{}
				v.message = "测试已启动"
			}
		default:
		}
	}
	if v.stopPending != nil {
		select {
		case r := <-v.stopPending:
			v.stopPending = nil
			if r.Err != nil {
				v.message = "停止失败：" + r.Err.Error()
			} else {
				v.message = "停止请求已完成"
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
				v.message = "刷新网卡失败：" + r.Err.Error()
			} else {
				v.interfaces = r.Value
				found := v.iface == ""
				for _, i := range v.interfaces {
					if i.IsUp && !i.IsLoop && i.Name == v.iface {
						found = true
					}
				}
				if !found {
					v.iface = ""
					v.message = "所选网卡已不可用，已切换为自动"
				} else {
					v.message = "网卡列表已刷新"
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
				v.message = "打开日志目录失败：" + r.Err.Error()
			} else {
				v.message = "已请求打开日志目录"
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
	return !v.closing && (v.startPending != nil || v.stopPending != nil || v.samplePending != nil || v.interfacesPending != nil || v.openPending != nil || (v.session != "" && !v.drained))
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
	if !v.locked() {
		v.draft = strings.Join(v.domains, "\n")
		v.editing = true
	}
}
func (v *nativeView) applyDomains() {
	if v.closing {
		return
	}
	valid, invalid := tester.NormalizeDomains([]string{v.draft})
	if len(valid) < 1 || len(valid) > 5000 {
		v.message = "有效域名须为 1–5000 个"
		return
	}
	v.domains = valid
	v.editing = false
	v.message = fmt.Sprintf("已应用 %d 个域名，忽略 %d 个无效项", len(valid), len(invalid))
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
	ui.Column(c).Fill().Padding(14).Gap(7).Children(func() {
		v.header(c)
		if v.editing {
			v.domainEditor(c)
			return
		}
		v.form(c)
		v.stats(c)
		v.logs(c)
		v.footer(c)
	})
}

func (v *nativeView) header(c *ui.Context) {
	status := "空闲"
	switch {
	case v.closing:
		status = "正在关闭"
	case v.startPending != nil:
		status = "正在启动"
	case v.snapshot.Finished:
		status = "已结束"
	case v.stopPending != nil || v.snapshot.Stopping:
		status = "正在停止"
	case v.session != "":
		status = "运行中"
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "TCPLink").FontSize(22).Bold()
		ui.Text(c, "TCP 并发连接测试 · 仅握手，不发送 HTTP / TLS 数据").Grow(1).SingleLine()
		ui.Text(c, fmt.Sprintf("单连接最多保持 %d 秒", v.boot.MaxHoldSec)).SingleLine()
		ui.Text(c, status).Bold()
	})
}

func (v *nativeView) form(c *ui.Context) {
	locked := v.locked()
	ui.Row(c).Gap(10).Children(func() {
		ui.Column(c).Width(250).Gap(3).Children(func() {
			ui.Text(c, "测试模式")
			ui.Segmented(c.Key("mode"), &v.mode, "80 + 443", "80", "443").Label("测试模式").Disabled(locked)
		})
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, "出口网卡")
			labels := []string{"自动（系统默认路由）"}
			names := []string{""}
			chosen := labels[0]
			for _, i := range v.interfaces {
				if i.IsUp && !i.IsLoop {
					label := i.Label
					if label == "" {
						label = i.Name
					}
					labels = append(labels, label)
					names = append(names, i.Name)
					if v.iface == i.Name {
						chosen = label
					}
				}
			}
			if ui.Select(c.Key("interface"), &chosen, labels).Label("出口网卡").Disabled(locked).Changed() {
				for j, label := range labels {
					if label == chosen {
						v.iface = names[j]
						break
					}
				}
			}
		})
		if ui.Button(c.Key("refresh"), "刷新网卡").Disabled(locked || v.interfacesPending != nil).Clicked() {
			v.interfacesPending = v.controller.Interfaces()
		}
	})
	ui.Row(c).Gap(10).Children(func() {
		v.numberField(c, "发起速率（次/秒）", "rate", &v.rate, locked)
		v.numberField(c, "测试时长（秒）", "duration", &v.duration, locked)
		v.numberField(c, "失败停止（0 不限）", "fail-stop", &v.failStop, locked)
		if ui.Button(c.Key("domains"), fmt.Sprintf("域名 %d 个 · 编辑", len(v.domains))).Disabled(locked).Clicked() {
			v.editDomains()
		}
		if v.session != "" && !v.snapshot.Finished {
			if ui.Button(c.Key("stop"), "停止测试").Disabled(v.closing || v.stopPending != nil || v.snapshot.Stopping).Clicked() {
				v.stop()
			}
		} else if ui.PrimaryButton(c.Key("start"), "开始测试").Disabled(locked).Clicked() {
			v.start()
		}
	})
}

func (v *nativeView) numberField(c *ui.Context, label, key string, value *string, locked bool) {
	ui.Column(c).Grow(1).Gap(3).Children(func() {
		ui.Text(c, label)
		ui.TextInput(c.Key(key), value).Label(label).Disabled(locked)
	})
}

func (v *nativeView) domainEditor(c *ui.Context) {
	valid, invalid := tester.NormalizeDomains([]string{v.draft})
	ui.Text(c, "测试域名：每行一个，也可粘贴网址；空行和 # 注释会被忽略").Bold()
	ui.Text(c, fmt.Sprintf("有效 %d 个 · 无效 %d 个（最多 5000 个）", len(valid), len(invalid)))
	if len(invalid) > 0 {
		ui.Text(c, "无效："+strings.Join(invalid[:min(3, len(invalid))], "、")).SingleLine()
	}
	ui.TextArea(c.Key("domain-draft"), &v.draft).Label("域名草稿").Disabled(v.closing).Grow(1)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c.Key("domain-reset"), "恢复内置").Disabled(v.closing).Clicked() {
			v.draft = strings.Join(v.boot.Domains, "\n")
		}
		if ui.Button(c.Key("domain-clear"), "清空草稿").Disabled(v.closing).Clicked() {
			v.draft = ""
		}
		ui.Spacer(c)
		if ui.Button(c.Key("domain-cancel"), "取消").Disabled(v.closing).Clicked() {
			v.editing = false
		}
		if ui.PrimaryButton(c.Key("domain-apply"), "应用域名").Disabled(v.closing || len(valid) == 0 || len(valid) > 5000).Clicked() {
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
	rate := "—"
	if s.Success+s.Failed > 0 {
		rate = fmt.Sprintf("%.1f%%", s.SuccessRate)
	}
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, fmt.Sprintf("尝试 %d    成功 %d    失败 %d    中止 %d    成功率 %s", s.Attempts, s.Success, s.Failed, s.Aborted, rate)).SingleLine()
	})
	ui.Text(c, fmt.Sprintf("进行中 %d    拨号中 %d    已关闭 %d    峰值 %d    平均 %.1f 次/秒", s.Active, s.Dialing, s.Closed, s.Max, s.AvgRate)).SingleLine()
	progress := 0.0
	if s.DurationSec > 0 {
		progress = min(1, max(0, s.ElapsedSec/float64(s.DurationSec)))
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf("已运行 %.0f 秒 · 剩余 %.0f 秒", s.ElapsedSec, s.Remaining))
		ui.Progress(c.Key("progress"), progress).Label("测试进度").Grow(1)
		if s.FailStop > 0 {
			ui.Text(c, fmt.Sprintf("失败 %d / %d", s.Failed, s.FailStop))
			ui.Progress(c.Key("failure-progress"), min(1, float64(s.Failed)/float64(s.FailStop))).Label("失败上限进度").Width(100)
		} else {
			ui.Text(c, "失败上限：不限")
		}
	})
	if s.StopReason != "" {
		ui.Text(c, "停止原因："+s.StopReason).SingleLine()
	}
	if s.LogError != "" {
		ui.Text(c, "日志错误："+s.LogError).SingleLine()
	}
}

func (v *nativeView) logs(c *ui.Context) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, "实时连接日志").Bold()
		ui.Segmented(c.Key("filter"), &v.filter, "全部", "成功", "失败", "中止").Label("日志筛选")
		ui.Spacer(c)
		if ui.Switch(c.Key("follow"), &v.follow).Label("自动跟随").Changed() && v.follow {
			v.scrollRequested = true
		}
		ui.Text(c, "自动跟随")
		if ui.Button(c.Key("clear-logs"), "清空视图").Disabled(v.closing).Clicked() {
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
	columns := []ui.TableColumn{{Title: "序号", Width: 65}, {Title: "时间", Width: 100}, {Title: "状态", Width: 60}, {Title: "域名", Width: 170}, {Title: "IP", Width: 130}, {Title: "端口", Width: 60}, {Title: "耗时", Width: 80}, {Title: "错误"}}
	ui.Table(c.Key("log-table"), &v.table, columns, len(rows), func(row, col int) {
		e := rows[row]
		status := "失败"
		if e.Aborted {
			status = "中止"
		} else if e.OK {
			status = "成功"
		}
		cells := []string{strconv.FormatUint(e.Seq, 10), e.Time, status, e.Domain, e.IP, strconv.Itoa(e.Port), fmt.Sprintf("%.1f ms", e.LatencyMS), e.Error}
		ui.Text(c, cells[col]).SingleLine()
	}).Label("连接日志").Grow(1).MinHeight(100)
	v.tableBuilt = true
}

func (v *nativeView) footer(c *ui.Context) {
	if v.message != "" {
		ui.Text(c, v.message).SingleLine()
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "目录："+v.boot.LogDir).Grow(1).SingleLine()
		if ui.Button(c.Key("open-logs"), "打开目录").Disabled(v.closing || v.openPending != nil).Clicked() {
			v.openPending = v.controller.OpenLogs()
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "CSV：").Width(55)
		ui.TextInput(c.Key("csv-path"), &v.snapshot.LogPath).Label("CSV 日志路径").ReadOnly(true).Grow(1)
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "摘要：").Width(55)
		ui.TextInput(c.Key("summary-path"), &v.snapshot.SummaryPath).Label("摘要文件路径").ReadOnly(true).Grow(1)
	})
	fd := "文件句柄：当前平台不适用"
	if v.boot.FDLimit.Supported {
		fd = fmt.Sprintf("文件句柄 %d / 硬上限 %d", v.boot.FDLimit.Soft, v.boot.FDLimit.Hard)
	}
	if v.boot.FDLimit.Error != "" {
		fd += " · " + v.boot.FDLimit.Error
	}
	ui.Text(c, fd+"    TCPLink v"+v.boot.Version).SingleLine()
}
