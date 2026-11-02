package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"tcpconnbenchmark/internal/i18n"
	"tcpconnbenchmark/internal/tester"
)

type lifecycleStyle struct {
	text  string
	color ui.Color
	pulse bool
}

func (v *nativeView) lifecycle(t *ui.Theme, reduceMotion bool) lifecycleStyle {
	s := lifecycleStyle{text: "空闲", color: t.TextMuted.Mix(t.Accent, .25)}
	switch {
	case v.closing:
		s.text, s.color = "正在关闭", t.Warning
	case v.startPending != nil:
		s.text, s.color = "正在启动", t.Accent
	case v.snapshot.Finished:
		s.text = "已结束"
		s.color = t.TextMuted
		reason := v.snapshot.StopReason
		lowerReason := strings.ToLower(reason)
		if strings.Contains(reason, "失败") || strings.Contains(lowerReason, "fail") {
			s.text, s.color = "已终止", t.Danger
		} else if strings.Contains(reason, "达到测试时长") || strings.Contains(reason, i18n.T("达到测试时长")) {
			s.text, s.color = "已完成", t.Success
		} else if v.snapshot.StopReason != "" {
			s.text = "已停止"
		}
	case v.stopPending != nil || v.snapshot.Stopping:
		s.text, s.color = "正在停止", t.Warning
	case v.session != "":
		s.text, s.color, s.pulse = "运行中", t.Success, !reduceMotion
	}
	s.text = i18n.T(s.text)
	return s
}

func (v *nativeView) lifecyclePill(c *ui.Context) {
	s := v.lifecycle(c.Theme(), c.Preferences().ReduceMotion)
	ui.Row(c.Key("lifecycle")).Gap(7).Padding(6, 10).Radius(20).Background(s.color.Alpha(.10)).Children(func() {
		ui.Box(c).Size(9, 9).Draw(func(p *ui.Painter, r ui.Rect) {
			alpha := float32(1)
			if s.pulse {
				alpha = .70 + .30*float32(math.Sin(float64(p.Now().UnixMilli())/650))
				p.After(80 * time.Millisecond) // paint only; never rebuild the view for breathing
			}
			p.Fill(r, s.color.Alpha(alpha), r.W/2)
		})
		ui.Text(c, s.text).FontSize(12).Bold().TextColor(s.color)
	})
}

func (v *nativeView) kpiCards(c *ui.Context, s tester.Snapshot) {
	t := c.Theme()
	rate := "—"
	if s.Success+s.Failed > 0 {
		rate = fmt.Sprintf("%.1f%%", s.SuccessRate)
	}
	type metric struct {
		label, value string
		color        ui.Color
	}
	metrics := []metric{{"尝试", fmt.Sprint(s.Attempts), t.Accent}, {"成功", fmt.Sprint(s.Success), t.Success}, {"失败", fmt.Sprint(s.Failed), t.Danger}, {"中止", fmt.Sprint(s.Aborted), t.Warning}, {"成功率", rate, t.Accent}}
	if v.startPending != nil || (v.session != "" && !s.Finished) {
		metrics = []metric{{"进行中", fmt.Sprint(s.Active), t.Accent}, {"拨号中", fmt.Sprint(s.Dialing), t.Text}, {"已关闭", fmt.Sprint(s.Closed), t.TextMuted}, {"峰值", fmt.Sprint(s.Max), t.Accent}, {"平均速率", fmt.Sprintf("%.1f", s.AvgRate), t.Accent}}
	}
	ui.Grid(c.Key("kpis")).Columns(5).Gap(8).Height(78).Children(func() {
		for _, m := range metrics {
			ui.Column(c.Key("kpi-"+m.label)).Label(i18n.T("指标：")+i18n.T(m.label)).Padding(10, 12).Gap(4).Radius(9).Background(t.Surface.Mix(m.color, .035)).Border(1, t.Border.Alpha(.65)).Children(func() {
				label := i18n.T(m.label)
				if m.label == "平均速率" {
					label += i18n.T(" · 次/秒")
				}
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, m.value).Font("monospace").FontFeatures("tnum").FontSize(25).Bold().TextColor(m.color).SingleLine().Tooltip(m.value)
			})
		}
	})
}

func logColumns() []ui.TableColumn {
	return []ui.TableColumn{{Title: i18n.T("序号"), Width: 64}, {Title: i18n.T("时间"), Width: 124}, {Title: i18n.T("状态"), Width: 68}, {Title: i18n.T("域名"), Width: 140}, {Title: "IP", Width: 132}, {Title: i18n.T("端口"), Width: 56}, {Title: i18n.T("耗时"), Width: 104, Align: ui.End}, {Title: i18n.T("错误"), MinWidth: 100}}
}

func logStatus(t *ui.Theme, e tester.LogEntry) (string, ui.Color) {
	if e.Aborted {
		return i18n.T("中止"), t.Warning
	}
	if e.OK {
		return i18n.T("成功"), t.Success
	}
	return i18n.T("失败"), t.Danger
}

func logCell(c *ui.Context, e tester.LogEntry, col int) {
	status, color := logStatus(c.Theme(), e)
	if col == 2 {
		ui.Badge(c, status).Label(i18n.T("日志状态：")+status).Background(color.Alpha(.12)).TextColor(color).FontSize(11).Padding(3, 7).Radius(5)
		return
	}
	cells := []string{strconv.FormatUint(e.Seq, 10), e.Time, status, e.Domain, e.IP, strconv.Itoa(e.Port), fmt.Sprintf("%.1f ms", e.LatencyMS), e.Error}
	text := ui.Text(c, cells[col]).SingleLine().FontSize(12).Tooltip(cells[col])
	if col != 3 && col != 7 {
		text.Font("monospace").FontFeatures("tnum").TextColor(c.Theme().TextMuted)
	}
	if col == 6 {
		text.TextAlign(ui.End).FillWidth()
	}
}

func (v *nativeView) emptyLogText() string {
	if len(v.entries) > 0 {
		return i18n.T("当前筛选没有匹配记录")
	}
	if v.logsCleared {
		return i18n.T("视图已清空，完整结果仍可在测试结束后保存")
	}
	if v.session != "" {
		return i18n.T("等待连接记录…")
	}
	return i18n.T("暂无测试日志，点击「开始测试」")
}

func (v *nativeView) storageDetails(c *ui.Context) {
	ui.Grid(c.Key("storage-fields")).Columns(3).Gap(5).Children(func() {
		pathField(c, "app-dir", "应用目录", &v.boot.AppDir)
		pathField(c, "config-path", "域名配置路径", &v.boot.ConfigPath)
		pathField(c, "csv-path", "CSV 日志路径", &v.snapshot.LogPath)
		pathField(c, "summary-path", "摘要文件路径", &v.snapshot.SummaryPath)
		pathField(c, "log-dir", "日志目录", &v.boot.LogDir)
		v.storageActions(c)
	})
}

func pathField(c *ui.Context, key, label string, value *string) {
	label = i18n.T(label)
	ui.Row(c.Key(key + "-row")).Gap(5).Children(func() {
		short := map[string]string{"app-dir": "应用", "config-path": "配置", "csv-path": "CSV", "summary-path": "摘要", "log-dir": "日志"}[key]
		ui.Text(c, i18n.T(short)).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.TextInput(c.Key(key), value).Height(28).Label(label).ReadOnly(true).Tooltip(label + "：" + *value).Grow(1)
		if ui.Button(c.Key(key+"-copy"), i18n.T("复制")).Height(28).Label(i18n.T("复制") + label).Disabled(*value == "").Clicked() {
			c.WriteClipboard(*value)
		}
	})
}

func limitLabel(n uint64) string {
	if n >= 1<<60 {
		return "∞"
	}
	return strconv.FormatUint(n, 10)
}

func (v *nativeView) statusBar(c *ui.Context) {
	fd, full := "FD —", i18n.T("当前平台不支持文件句柄上限调整")
	if v.boot.FDLimit.Supported {
		fd = "FD " + limitLabel(v.boot.FDLimit.Soft) + " / " + limitLabel(v.boot.FDLimit.Hard)
		full = fmt.Sprintf(i18n.T("文件句柄软上限 %d；硬上限 %d；目标 %d"), v.boot.FDLimit.Soft, v.boot.FDLimit.Hard, v.boot.FDLimit.Target)
	}
	if v.boot.FDLimit.Error != "" {
		fd += i18n.T(" · 调整失败")
		full += "；" + v.boot.FDLimit.Error
	}
	ui.Row(c.Key("status-bar")).Gap(10).Children(func() {
		ui.Text(c, fd).Label(i18n.T("文件句柄状态")).Tooltip(full).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.Spacer(c)
		ui.Text(c, v.boot.Version).Label(i18n.T("版本")).Tooltip(v.boot.Version).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
	})
}
