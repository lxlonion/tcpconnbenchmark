package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"tcplink/internal/syslimit"
	"tcplink/internal/tester"
)

const (
	version            = "1.0.0"
	defaultGUIDuration = 60
	defaultGUIFailStop = 50
)

type bootstrapData struct {
	Domains            []string
	Interfaces         []tester.InterfaceInfo
	LogDir             string
	FDLimit            syslimit.Info
	DefaultRatePerSec  int
	DefaultDurationSec int
	DefaultFailStop    int
	MinRatePerSec      int
	MaxRatePerSec      int
	MaxHoldSec         int
	Version            string
}

type startRequest struct {
	Mode        tester.Mode
	Interface   string
	Domains     []string
	DurationSec int
	FailStop    int
	RatePerSec  int
}

type result[T any] struct {
	Value T
	Err   error
}

type sampleData struct {
	State   tester.Snapshot
	Logs    []tester.LogEntry
	Dropped bool
	NextSeq uint64
}

type guiController interface {
	Bootstrap() bootstrapData
	Start(startRequest) <-chan result[tester.Snapshot]
	Stop(sessionID string) <-chan result[struct{}]
	Sample(sessionID string, since uint64) <-chan result[sampleData]
	Interfaces() <-chan result[[]tester.InterfaceInfo]
	OpenLogs() <-chan result[struct{}]
	Tail(sessionID string) (uint64, error)
	Shutdown() <-chan struct{}
}

// engine exposes just the engine operations needed by the controller. The
// factory is injectable so lifecycle races can be tested without network I/O.
type engine interface {
	Start()
	Stop(string)
	Wait()
	Finished() bool
	Snapshot() tester.Snapshot
	LogsSince(uint64, int) ([]tester.LogEntry, bool)
}

type App struct {
	mu        sync.Mutex
	engine    engine
	sessionID string
	starting  bool
	closing   bool
	jobs      sync.WaitGroup
	done      chan struct{}
	bootstrap bootstrapData
	newEngine func(tester.Config) (engine, error)
}

func newApp(fdInfo syslimit.Info) *App {
	logDir, _ := tester.LogDirectory()
	return &App{
		done: make(chan struct{}),
		newEngine: func(cfg tester.Config) (engine, error) {
			return tester.New(cfg)
		},
		bootstrap: bootstrapData{
			Domains:            append([]string(nil), tester.BuiltinDomains...),
			Interfaces:         tester.ListInterfaces(),
			LogDir:             logDir,
			FDLimit:            fdInfo,
			DefaultRatePerSec:  tester.DefaultRatePerSec,
			DefaultDurationSec: defaultGUIDuration,
			DefaultFailStop:    defaultGUIFailStop,
			MinRatePerSec:      tester.MinRatePerSec,
			MaxRatePerSec:      tester.MaxRatePerSec,
			MaxHoldSec:         int(tester.MaxConnectionHold.Seconds()),
			Version:            version,
		},
	}
}

func (a *App) run() error {
	view := newNativeView(a)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: "TCPLink · TCP 并发连接测试", Width: 1180, Height: 780,
			MinWidth: 900, MinHeight: 650, Content: ui.View(view.view),
		})
		view.attach(win)
		win.OnClose(func(event *mygo.CloseEvent) {
			if !view.canClose() {
				event.PreventDefault()
				view.requestClose()
			}
		})
		mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
			if !view.canClose() {
				event.PreventDefault()
				view.requestClose()
			}
		})
	})
	err := mygo.App.Run()
	<-a.Shutdown()
	return err
}

func cloneInterfaces(src []tester.InterfaceInfo) []tester.InterfaceInfo {
	dst := append([]tester.InterfaceInfo(nil), src...)
	for i := range dst {
		dst[i].IPv4 = append([]string(nil), dst[i].IPv4...)
	}
	return dst
}

func (a *App) Bootstrap() bootstrapData {
	data := a.bootstrap
	data.Domains = append([]string(nil), data.Domains...)
	data.Interfaces = cloneInterfaces(data.Interfaces)
	return data
}

// reply creates a buffered, single-result channel even for immediate errors.
func reply[T any](value T, err error) <-chan result[T] {
	ch := make(chan result[T], 1)
	ch <- result[T]{Value: value, Err: err}
	close(ch)
	return ch
}

func (a *App) Start(req startRequest) <-chan result[tester.Snapshot] {
	req.Domains = append([]string(nil), req.Domains...)
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return reply(tester.Snapshot{}, fmt.Errorf("程序正在关闭"))
	}
	if a.starting || (a.engine != nil && !a.engine.Finished()) {
		a.mu.Unlock()
		return reply(tester.Snapshot{}, fmt.Errorf("当前已有测试正在运行"))
	}
	a.starting = true
	a.jobs.Add(1)
	a.mu.Unlock()

	ch := make(chan result[tester.Snapshot], 1)
	go func() {
		defer a.jobs.Done()
		defer close(ch)
		e, err := a.newEngine(tester.Config{
			Mode: req.Mode, Interface: req.Interface, Domains: req.Domains,
			DurationSec: req.DurationSec, FailStop: req.FailStop, RatePerSec: req.RatePerSec,
		})
		if err != nil {
			a.mu.Lock()
			a.starting = false
			a.mu.Unlock()
			ch <- result[tester.Snapshot]{Err: err}
			return
		}
		e.Start()
		snap := e.Snapshot()
		a.mu.Lock()
		a.engine, a.sessionID, a.starting = e, snap.SessionID, false
		closing := a.closing
		a.mu.Unlock()
		if closing {
			e.Stop("程序关闭")
		}
		ch <- result[tester.Snapshot]{Value: snap}
	}()
	return ch
}

// sessionJob captures an engine and registers its work atomically with shutdown.
func (a *App) sessionJob(sessionID string) (engine, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return nil, fmt.Errorf("程序正在关闭")
	}
	if a.engine == nil || sessionID == "" || sessionID != a.sessionID {
		return nil, fmt.Errorf("测试会话不存在或已更换")
	}
	a.jobs.Add(1)
	return a.engine, nil
}

func (a *App) Stop(sessionID string) <-chan result[struct{}] {
	e, err := a.sessionJob(sessionID)
	if err != nil {
		return reply(struct{}{}, err)
	}
	ch := make(chan result[struct{}], 1)
	go func() {
		defer a.jobs.Done()
		defer close(ch)
		if !e.Finished() {
			e.Stop("手动停止")
		}
		ch <- result[struct{}]{}
	}()
	return ch
}

func (a *App) Sample(sessionID string, since uint64) <-chan result[sampleData] {
	e, err := a.sessionJob(sessionID)
	if err != nil {
		return reply(sampleData{}, err)
	}
	ch := make(chan result[sampleData], 1)
	go func() {
		defer a.jobs.Done()
		defer close(ch)
		snap := e.Snapshot()
		logs, dropped := e.LogsSince(since, 400)
		next := since
		for _, entry := range logs {
			if entry.Seq > next {
				next = entry.Seq
			}
		}
		ch <- result[sampleData]{Value: sampleData{State: snap, Logs: logs, Dropped: dropped, NextSeq: next}}
	}()
	return ch
}

func (a *App) Tail(sessionID string) (uint64, error) {
	e, err := a.sessionJob(sessionID)
	if err != nil {
		return 0, err
	}
	defer a.jobs.Done()
	logs, _ := e.LogsSince(0, 1)
	if len(logs) == 0 {
		return 0, nil
	}
	return logs[len(logs)-1].Seq, nil
}

func (a *App) registerJob() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return false
	}
	a.jobs.Add(1)
	return true
}

func (a *App) Interfaces() <-chan result[[]tester.InterfaceInfo] {
	if !a.registerJob() {
		return reply([]tester.InterfaceInfo(nil), fmt.Errorf("程序正在关闭"))
	}
	ch := make(chan result[[]tester.InterfaceInfo], 1)
	go func() {
		defer a.jobs.Done()
		defer close(ch)
		ch <- result[[]tester.InterfaceInfo]{Value: tester.ListInterfaces()}
	}()
	return ch
}

func (a *App) OpenLogs() <-chan result[struct{}] {
	if !a.registerJob() {
		return reply(struct{}{}, fmt.Errorf("程序正在关闭"))
	}
	ch := make(chan result[struct{}], 1)
	go func() {
		defer a.jobs.Done()
		defer close(ch)
		dir := a.bootstrap.LogDir
		var err error
		if dir == "" {
			dir, err = tester.LogDirectory()
		}
		if err == nil {
			err = openInFileManager(dir)
			if err != nil {
				err = fmt.Errorf("打开日志目录失败: %w", err)
			}
		}
		ch <- result[struct{}]{Err: err}
	}()
	return ch
}

func (a *App) Shutdown() <-chan struct{} {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return a.done
	}
	a.closing = true
	e := a.engine
	a.mu.Unlock()
	go func() {
		if e != nil && !e.Finished() {
			e.Stop("程序关闭")
		}
		// A pending Start may create a new engine after the capture above.
		a.jobs.Wait()
		a.mu.Lock()
		e = a.engine
		a.mu.Unlock()
		if e != nil {
			if !e.Finished() {
				e.Stop("程序关闭")
			}
			e.Wait()
		}
		close(a.done)
	}()
	return a.done
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
