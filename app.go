package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/webview/webview_go"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

//go:embed web/index.html
var indexHTML string

const version = "1.0.0"

type bootstrapData struct {
	Domains           []string               `json:"domains"`
	Interfaces        []tester.InterfaceInfo `json:"interfaces"`
	LogDir            string                 `json:"logDir"`
	FDLimit           syslimit.Info          `json:"fdLimit"`
	DefaultRatePerSec int                    `json:"defaultRatePerSec"`
	MinRatePerSec     int                    `json:"minRatePerSec"`
	MaxRatePerSec     int                    `json:"maxRatePerSec"`
	MaxHoldSec        int                    `json:"maxHoldSec"`
	Version           string                 `json:"version"`
}

type startRequest struct {
	Mode        tester.Mode `json:"mode"`
	Interface   string      `json:"interface"`
	Domains     []string    `json:"domains"`
	DurationSec int         `json:"durationSec"`
	FailStop    int         `json:"failStop"`
	RatePerSec  int         `json:"ratePerSec"`
}

type apiResult struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	LogPath   string `json:"logPath,omitempty"`
}

type pollResult struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	State   tester.Snapshot   `json:"snapshot"`
	Logs    []tester.LogEntry `json:"logs"`
	Dropped bool              `json:"dropped"`
}

// App holds the single running test and exposes it to the embedded UI.
type App struct {
	mu        sync.Mutex
	engine    *tester.Engine
	fdInfo    syslimit.Info
	logDir    string
	bootstrap bootstrapData
	win       webview.WebView
}

func newApp(fdInfo syslimit.Info) *App {
	logDir, err := tester.LogDirectory()
	if err != nil {
		logDir = ""
	}
	return &App{
		fdInfo: fdInfo,
		logDir: logDir,
		bootstrap: bootstrapData{
			Domains:           append([]string(nil), tester.BuiltinDomains...),
			Interfaces:        tester.ListInterfaces(),
			LogDir:            logDir,
			FDLimit:           fdInfo,
			DefaultRatePerSec: tester.DefaultRatePerSec,
			MinRatePerSec:     tester.MinRatePerSec,
			MaxRatePerSec:     tester.MaxRatePerSec,
			MaxHoldSec:        int(tester.MaxConnectionHold.Seconds()),
			Version:           version,
		},
	}
}

func (a *App) run() error {
	debug := os.Getenv("TCPLINK_DEBUG") == "1"
	win := webview.New(debug)
	defer win.Destroy()
	a.win = win
	win.SetTitle("TCPLink · TCP 并发连接测试")
	win.SetSize(1180, 780, webview.HintNone)

	bindings := []struct {
		name string
		fn   any
	}{
		{"tcpBootstrap", a.bootstrapJSON},
		{"tcpStart", a.start},
		{"tcpStop", a.stop},
		{"tcpPoll", a.poll},
		{"tcpInterfaces", a.interfaces},
		{"tcpOpenLogs", a.openLogs},
	}
	for _, binding := range bindings {
		if err := win.Bind(binding.name, binding.fn); err != nil {
			return fmt.Errorf("绑定界面接口 %s 失败: %w", binding.name, err)
		}
	}

	win.SetHtml(indexHTML)
	win.Run()

	a.shutdown()
	return nil
}

func (a *App) shutdown() {
	a.mu.Lock()
	engine := a.engine
	a.mu.Unlock()
	if engine != nil && !engine.Finished() {
		engine.Stop("程序关闭")
		engine.Wait()
	}
}

func (a *App) bootstrapJSON() string {
	a.mu.Lock()
	data := a.bootstrap
	a.mu.Unlock()
	raw, _ := json.Marshal(data)
	return string(raw)
}

func (a *App) start(configJSON string) string {
	var req startRequest
	if err := json.Unmarshal([]byte(configJSON), &req); err != nil {
		return errorJSON("参数解析失败: %v", err)
	}

	a.mu.Lock()
	if a.engine != nil && !a.engine.Finished() {
		a.mu.Unlock()
		return errorJSON("当前已有测试正在运行")
	}
	engine, err := tester.New(tester.Config{
		Mode:        req.Mode,
		Interface:   req.Interface,
		Domains:     req.Domains,
		DurationSec: req.DurationSec,
		FailStop:    req.FailStop,
		RatePerSec:  req.RatePerSec,
	})
	if err != nil {
		a.mu.Unlock()
		return errorJSON("%s", err.Error())
	}
	a.engine = engine
	engine.Start()
	snap := engine.Snapshot()
	a.mu.Unlock()

	raw, _ := json.Marshal(apiResult{OK: true, SessionID: snap.SessionID, LogPath: snap.LogPath})
	return string(raw)
}

func (a *App) stop() string {
	a.mu.Lock()
	engine := a.engine
	a.mu.Unlock()
	if engine == nil {
		return errorJSON("当前没有正在运行的测试")
	}
	if engine.Finished() {
		raw, _ := json.Marshal(apiResult{OK: true})
		return string(raw)
	}
	engine.Stop("手动停止")
	raw, _ := json.Marshal(apiResult{OK: true})
	return string(raw)
}

func (a *App) poll(since int64) string {
	if since < 0 {
		since = 0
	}
	a.mu.Lock()
	engine := a.engine
	a.mu.Unlock()

	if engine == nil {
		raw, _ := json.Marshal(pollResult{
			OK:    true,
			State: tester.Snapshot{HasEngine: false},
			Logs:  []tester.LogEntry{},
		})
		return string(raw)
	}

	logs, dropped := engine.LogsSince(uint64(since), 400)
	if logs == nil {
		logs = []tester.LogEntry{}
	}
	raw, err := json.Marshal(pollResult{
		OK:      true,
		State:   engine.Snapshot(),
		Logs:    logs,
		Dropped: dropped,
	})
	if err != nil {
		return errorJSON("读取状态失败: %v", err)
	}
	return string(raw)
}

func (a *App) interfaces() string {
	raw, _ := json.Marshal(struct {
		OK         bool                   `json:"ok"`
		Interfaces []tester.InterfaceInfo `json:"interfaces"`
	}{
		OK:         true,
		Interfaces: tester.ListInterfaces(),
	})
	return string(raw)
}

func (a *App) openLogs() string {
	dir := a.logDir
	if dir == "" {
		var err error
		dir, err = tester.LogDirectory()
		if err != nil {
			return errorJSON("%s", err.Error())
		}
	}
	if err := openInFileManager(dir); err != nil {
		return errorJSON("打开日志目录失败: %v", err)
	}
	raw, _ := json.Marshal(apiResult{OK: true})
	return string(raw)
}

func openInFileManager(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func errorJSON(format string, args ...any) string {
	raw, _ := json.Marshal(apiResult{OK: false, Error: strings.TrimSpace(fmt.Sprintf(format, args...))})
	return string(raw)
}
