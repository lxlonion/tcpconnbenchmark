package tester

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tcplink/internal/syslimit"
)

// LogDirectory returns the directory where test logs are stored. It prefers
// the user's application data directory so that launching the app from Finder
// or a terminal both work.
func LogDirectory() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			return "", fmt.Errorf("找不到可写日志目录: %w", err)
		}
		base = filepath.Dir(exe)
	}
	dir := filepath.Join(base, "TCPLink", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建日志目录失败: %w", err)
	}
	return dir, nil
}

type resultLogger struct {
	csvPath     string
	summaryPath string
	sessionID   string
	createdAt   time.Time
	config      Config
	domains     []string
	ifaceLabel  string
	fdInfo      syslimit.Info

	mu        sync.Mutex
	summaryMu sync.Mutex
	file      *os.File
	writer    *csv.Writer
	lastFlush time.Time
	writeErr  error
	closed    bool
}

func newResultLogger(cfg Config, domains []string, ifaceLabel string, fdInfo syslimit.Info) (*resultLogger, error) {
	dir, err := LogDirectory()
	if err != nil {
		return nil, err
	}

	stamp := time.Now().Format("20060102-150405.000")
	baseName := "tcp-test-" + strings.ReplaceAll(stamp, ".", "-")
	var file *os.File
	var csvPath, summaryPath, sessionID string
	for i := 0; i < 1000; i++ {
		sessionID = baseName
		if i > 0 {
			sessionID = fmt.Sprintf("%s-%d", baseName, i+1)
		}
		csvPath = filepath.Join(dir, sessionID+".csv")
		f, openErr := os.OpenFile(csvPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if openErr == nil {
			file = f
			summaryPath = filepath.Join(dir, sessionID+".summary.txt")
			break
		}
		if errors.Is(openErr, fs.ErrExist) {
			continue
		}
		return nil, fmt.Errorf("创建日志文件失败: %w", openErr)
	}
	if file == nil {
		return nil, fmt.Errorf("创建日志文件失败: 文件名冲突")
	}

	l := &resultLogger{
		csvPath:     csvPath,
		summaryPath: summaryPath,
		sessionID:   sessionID,
		createdAt:   time.Now(),
		config:      cfg,
		domains:     append([]string(nil), domains...),
		ifaceLabel:  ifaceLabel,
		fdInfo:      fdInfo,
		file:        file,
		writer:      csv.NewWriter(file),
		lastFlush:   time.Now(),
	}

	// A UTF-8 BOM lets Excel open the Chinese headers correctly.
	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		file.Close()
		return nil, fmt.Errorf("写入日志文件失败: %w", err)
	}
	if err := l.writer.Write([]string{"时间", "域名", "IPv4", "端口", "结果", "耗时(ms)", "错误"}); err != nil {
		file.Close()
		return nil, fmt.Errorf("写入日志表头失败: %w", err)
	}
	l.writer.Flush()
	if err := l.writer.Error(); err != nil {
		file.Close()
		return nil, fmt.Errorf("写入日志表头失败: %w", err)
	}
	if err := l.writeSummary("运行中", nil); err != nil {
		l.writeErr = err
	}
	return l, nil
}

func (l *resultLogger) write(entry LogEntry, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}

	result := "failed"
	switch {
	case entry.Aborted:
		result = "aborted"
	case entry.OK:
		result = "success"
	}
	row := []string{
		at.Format("2006-01-02 15:04:05.000"),
		entry.Domain,
		entry.IP,
		strconv.Itoa(entry.Port),
		result,
		strconv.FormatFloat(entry.LatencyMS, 'f', 2, 64),
		entry.Error,
	}
	if err := l.writer.Write(row); err != nil {
		l.recordErrorLocked(fmt.Errorf("写入日志失败: %w", err))
	}
	if time.Since(l.lastFlush) >= time.Second {
		l.flushLocked()
	}
}

func (l *resultLogger) flushLocked() {
	if l.writer != nil {
		l.writer.Flush()
		if err := l.writer.Error(); err != nil {
			l.recordErrorLocked(fmt.Errorf("刷新日志失败: %w", err))
		}
	}
	l.lastFlush = time.Now()
}

func (l *resultLogger) recordErrorLocked(err error) {
	if err != nil && l.writeErr == nil {
		l.writeErr = err
	}
}

func (l *resultLogger) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.writeErr
	}
	l.closed = true
	l.flushLocked()
	if err := l.file.Sync(); err != nil {
		l.recordErrorLocked(fmt.Errorf("同步日志失败: %w", err))
	}
	if err := l.file.Close(); err != nil {
		l.recordErrorLocked(fmt.Errorf("关闭日志失败: %w", err))
	}
	return l.writeErr
}

func (l *resultLogger) err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeErr
}

func (l *resultLogger) writeSummary(status string, snap *Snapshot) error {
	l.summaryMu.Lock()
	defer l.summaryMu.Unlock()

	var b strings.Builder
	b.WriteString("TCPLink · TCP 并发连接测试\n")
	b.WriteString("========================================\n")
	fmt.Fprintf(&b, "会话 ID: %s\n", l.sessionID)
	fmt.Fprintf(&b, "状态: %s\n", status)
	fmt.Fprintf(&b, "开始时间: %s\n", l.createdAt.Format("2006-01-02 15:04:05"))
	if snap != nil && snap.EndedAt != "" {
		fmt.Fprintf(&b, "结束时间: %s\n", snap.EndedAt)
	} else {
		b.WriteString("结束时间: —\n")
	}
	fmt.Fprintf(&b, "测试模式: %s\n", ModeLabel(l.config.Mode))
	fmt.Fprintf(&b, "出口网卡: %s\n", l.ifaceLabel)
	fmt.Fprintf(&b, "测试域名: %d 个\n", len(l.domains))
	fmt.Fprintf(&b, "发起目标: %d 个（域名 × 端口）\n", len(l.domains)*len(modePorts(l.config.Mode)))
	fmt.Fprintf(&b, "测试时长: %d 秒\n", l.config.DurationSec)
	if l.config.FailStop > 0 {
		fmt.Fprintf(&b, "失败停止: %d 次\n", l.config.FailStop)
	} else {
		b.WriteString("失败停止: 不限制\n")
	}
	fmt.Fprintf(&b, "发起速率: %d 次/秒\n", l.config.RatePerSec)
	fmt.Fprintf(&b, "单连接最长保持: %d 秒\n", int(MaxConnectionHold.Seconds()))
	if l.fdInfo.Supported {
		fmt.Fprintf(&b, "文件句柄软上限: %d（硬上限 %d）\n", l.fdInfo.Soft, l.fdInfo.Hard)
	} else if l.fdInfo.Error != "" {
		fmt.Fprintf(&b, "文件句柄上限: %s\n", l.fdInfo.Error)
	} else {
		b.WriteString("文件句柄上限: 当前平台不适用\n")
	}
	fmt.Fprintf(&b, "CSV 日志: %s\n", l.csvPath)
	fmt.Fprintf(&b, "摘要文件: %s\n", l.summaryPath)
	b.WriteString("----------------------------------------\n")
	if snap == nil {
		b.WriteString("统计: 测试启动中\n")
	} else {
		fmt.Fprintf(&b, "尝试次数: %d\n", snap.Attempts)
		fmt.Fprintf(&b, "成功: %d\n", snap.Success)
		fmt.Fprintf(&b, "失败: %d\n", snap.Failed)
		fmt.Fprintf(&b, "中止: %d\n", snap.Aborted)
		fmt.Fprintf(&b, "成功率: %.2f%%\n", snap.SuccessRate)
		fmt.Fprintf(&b, "峰值并发: %d\n", snap.Max)
		fmt.Fprintf(&b, "平均发起速率: %.2f 次/秒\n", snap.AvgRate)
		fmt.Fprintf(&b, "停止原因: %s\n", snap.StopReason)
	}
	b.WriteString("----------------------------------------\n")
	fmt.Fprintf(&b, "测试域名（%d）:\n", len(l.domains))
	for i, domain := range l.domains {
		fmt.Fprintf(&b, "%3d. %s\n", i+1, domain)
	}
	b.WriteString("----------------------------------------\n")
	b.WriteString("说明: 仅解析 IPv4 并建立 TCP 连接；不发送 HTTP 请求、不进行 TLS 握手、不传输网页数据。\n")

	tmp := l.summaryPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("写入摘要失败: %w", err)
	}
	if err := os.Rename(tmp, l.summaryPath); err != nil {
		return fmt.Errorf("保存摘要失败: %w", err)
	}
	return nil
}
