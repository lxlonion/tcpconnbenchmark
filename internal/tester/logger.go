package tester

import (
	"bufio"
	"encoding/csv"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"tcpconnbenchmark/internal/i18n"
	"time"

	"tcpconnbenchmark/internal/appdir"
	"tcpconnbenchmark/internal/syslimit"
)

// LogDirectory computes the portable export location without creating it.
func LogDirectory() (string, error) {
	dir, err := appdir.Directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

// ExportResult identifies the two files created by an explicit export.
type ExportResult struct {
	LogPath, SummaryPath string
}

type journalRecord struct {
	Entry LogEntry
	At    time.Time
	Final bool
	Count uint64
}

const journalQueueSize = 1024

// resultLogger keeps only a bounded queue in memory; all complete results are
// staged in a private binary journal, not in the application's directory.
type resultLogger struct {
	sessionID  string
	createdAt  time.Time
	config     Config
	ifaceLabel string
	fdInfo     syslimit.Info

	tempDir, journalPath string
	file                 *os.File
	queue                chan journalRecord
	writerDone           chan struct{}
	onError              func()
	written              uint64 // read after writerDone closes

	stateMu  sync.Mutex // short-held: never protects filesystem operations
	writeErr error
	exported ExportResult

	lifecycleMu sync.Mutex // serializes export and discard
	exportDir   string
	discarded   bool
	createFile  func(string) (*os.File, error) // optional filesystem fault injection
}

func newResultLogger(cfg Config, ifaceLabel string, fdInfo syslimit.Info) (*resultLogger, error) {
	dir, err := os.MkdirTemp("", "tcpconnbenchmark-journal-")
	if err != nil {
		return nil, fmt.Errorf(i18n.T("创建日志暂存目录失败: %w"), err)
	}
	path := filepath.Join(dir, "journal.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf(i18n.T("创建日志暂存文件失败: %w"), err)
	}
	now := time.Now()
	cfg.Domains = append([]string(nil), cfg.Domains...)
	return &resultLogger{
		sessionID: "tcp-test-" + now.Format("20060102-150405-000") + "-" + filepath.Base(dir)[len("tcpconnbenchmark-journal-"):],
		createdAt: now, config: cfg, ifaceLabel: ifaceLabel, fdInfo: fdInfo,
		tempDir: dir, journalPath: path, file: f, queue: make(chan journalRecord, journalQueueSize), writerDone: make(chan struct{}),
	}, nil
}

func (l *resultLogger) start(onError func()) {
	l.onError = onError
	go l.runWriter()
}

func (l *resultLogger) fail(err error) {
	if err == nil {
		return
	}
	l.stateMu.Lock()
	first := l.writeErr == nil
	if first {
		l.writeErr = err
	}
	l.stateMu.Unlock()
	if first && l.onError != nil {
		l.onError()
	}
}

func (l *resultLogger) err() error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	return l.writeErr
}

func (l *resultLogger) paths() ExportResult {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	return l.exported
}

// Called in sequence order under Engine.logMu. Never waits for disk or a full queue.
func (l *resultLogger) write(entry LogEntry, at time.Time) error {
	if err := l.err(); err != nil {
		return err
	}
	select {
	case l.queue <- journalRecord{Entry: entry, At: at}:
		return nil
	default:
		err := errors.New(i18n.T("日志暂存队列已满，结果不完整"))
		l.fail(err)
		return err
	}
}

func (l *resultLogger) runWriter() {
	defer close(l.writerDone)
	buf := bufio.NewWriterSize(l.file, 64*1024)
	enc := gob.NewEncoder(buf)
	var count uint64
	for record := range l.queue {
		if l.err() != nil {
			continue
		}
		if err := enc.Encode(record); err != nil {
			l.fail(fmt.Errorf(i18n.T("写入日志暂存失败: %w"), err))
			continue
		}
		count++
	}
	if l.err() == nil {
		if err := enc.Encode(journalRecord{Final: true, Count: count}); err != nil {
			l.fail(fmt.Errorf(i18n.T("封存日志暂存失败: %w"), err))
		}
	}
	if err := buf.Flush(); err != nil {
		l.fail(fmt.Errorf(i18n.T("刷新日志暂存失败: %w"), err))
	}
	if err := l.file.Sync(); err != nil {
		l.fail(fmt.Errorf(i18n.T("同步日志暂存失败: %w"), err))
	}
	if err := l.file.Close(); err != nil {
		l.fail(fmt.Errorf(i18n.T("关闭日志暂存失败: %w"), err))
	}
	l.written = count
}

func (l *resultLogger) close(expected uint64) {
	close(l.queue)
	<-l.writerDone
	if l.written != expected && l.err() == nil {
		l.fail(fmt.Errorf(i18n.T("日志暂存数量不符: %d != %d"), l.written, expected))
	}
}

// Export writes a complete CSV and summary after the journal is sealed.
func (e *Engine) Export(dir string) (ExportResult, error) {
	if !e.Finished() {
		return ExportResult{}, errors.New(i18n.T("测试尚未完成，无法保存"))
	}
	if strings.TrimSpace(dir) == "" {
		return ExportResult{}, errors.New(i18n.T("保存目录不能为空"))
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return ExportResult{}, fmt.Errorf(i18n.T("保存目录无效: %w"), err)
	}
	l := e.logger
	l.lifecycleMu.Lock()
	defer l.lifecycleMu.Unlock()
	if l.discarded {
		return ExportResult{}, errors.New(i18n.T("测试结果暂存已清理"))
	}
	if err := l.err(); err != nil {
		return ExportResult{}, fmt.Errorf(i18n.T("日志暂存不完整，无法保存: %w"), err)
	}
	if previous := l.paths(); previous.LogPath != "" {
		if base != l.exportDir {
			return ExportResult{}, errors.New(i18n.T("本轮结果已保存到其他目录"))
		}
		for _, path := range []string{previous.LogPath, previous.SummaryPath} {
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				return ExportResult{}, fmt.Errorf(i18n.T("已保存文件缺失或不可用: %s"), path)
			}
		}
		return previous, nil
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return ExportResult{}, fmt.Errorf(i18n.T("创建保存目录失败: %w"), err)
	}
	outDir, err := os.MkdirTemp(base, l.sessionID+"-")
	if err != nil {
		return ExportResult{}, fmt.Errorf(i18n.T("创建导出目录失败: %w"), err)
	}
	paths := ExportResult{LogPath: filepath.Join(outDir, "results.csv"), SummaryPath: filepath.Join(outDir, "summary.txt")}
	if err := l.exportFiles(paths, e.Snapshot(), e.logSeq.Load()); err != nil {
		if cleanupErr := os.RemoveAll(outDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf(i18n.T("清理未完成导出 %s 失败: %w"), outDir, cleanupErr))
		}
		return ExportResult{}, err
	}
	l.exportDir = base
	l.stateMu.Lock()
	l.exported = paths
	l.stateMu.Unlock()
	return paths, nil
}

// Discard releases only the private journal; exported user files are untouched.
func (e *Engine) Discard() error {
	if !e.Finished() {
		return errors.New(i18n.T("测试尚未完成，无法清理暂存"))
	}
	l := e.logger
	l.lifecycleMu.Lock()
	defer l.lifecycleMu.Unlock()
	if l.discarded {
		return nil
	}
	if err := os.RemoveAll(l.tempDir); err != nil {
		return fmt.Errorf(i18n.T("清理日志暂存 %s 失败: %w"), l.tempDir, err)
	}
	l.discarded = true
	return nil
}

func (l *resultLogger) exportFiles(paths ExportResult, snap Snapshot, expected uint64) error {
	input, err := os.Open(l.journalPath)
	if err != nil {
		return fmt.Errorf(i18n.T("读取日志暂存失败: %w"), err)
	}
	defer input.Close()
	csvFile, err := l.createExportFile(paths.LogPath)
	if err != nil {
		return fmt.Errorf(i18n.T("创建 CSV 失败: %w"), err)
	}
	err = l.exportCSV(input, csvFile, expected)
	if err == nil {
		err = csvFile.Sync()
	}
	err = errors.Join(err, csvFile.Close())
	if err != nil {
		return fmt.Errorf(i18n.T("导出 CSV 失败: %w"), err)
	}
	summaryFile, err := l.createExportFile(paths.SummaryPath)
	if err != nil {
		return fmt.Errorf(i18n.T("创建摘要失败: %w"), err)
	}
	_, err = io.WriteString(summaryFile, l.summary(snap, paths))
	if err == nil {
		err = summaryFile.Sync()
	}
	err = errors.Join(err, summaryFile.Close())
	if err != nil {
		return fmt.Errorf(i18n.T("导出摘要失败: %w"), err)
	}
	return nil
}

func (l *resultLogger) createExportFile(path string) (*os.File, error) {
	if l.createFile != nil {
		return l.createFile(path)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
}

func (l *resultLogger) exportCSV(src io.Reader, dst io.Writer, expected uint64) error {
	if _, err := dst.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	w := csv.NewWriter(dst)
	if err := w.Write([]string{"时间", "域名", "IP", "端口", "结果", "耗时(ms)", "错误"}); err != nil {
		return err
	}
	dec := gob.NewDecoder(bufio.NewReaderSize(src, 64*1024))
	var count uint64
	for {
		var r journalRecord
		if err := dec.Decode(&r); err != nil {
			return fmt.Errorf(i18n.T("日志暂存截断或损坏: %w"), err)
		}
		if r.Final {
			if r.Count != count || count != expected {
				return fmt.Errorf(i18n.T("日志暂存数量不符: %d/%d/%d"), count, r.Count, expected)
			}
			var extra journalRecord
			if err := dec.Decode(&extra); err != io.EOF {
				return fmt.Errorf(i18n.T("日志暂存尾部损坏: %v"), err)
			}
			break
		}
		count++
		if r.Entry.Seq != count {
			return fmt.Errorf(i18n.T("日志暂存序号不连续: %d != %d"), r.Entry.Seq, count)
		}
		result := "failed"
		if r.Entry.Aborted {
			result = "aborted"
		} else if r.Entry.OK {
			result = "success"
		}
		if err := w.Write([]string{r.At.Format("2006-01-02 15:04:05.000"), r.Entry.Domain, r.Entry.IP, strconv.Itoa(r.Entry.Port), result, strconv.FormatFloat(r.Entry.LatencyMS, 'f', 2, 64), r.Entry.Error}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func (l *resultLogger) summary(snap Snapshot, paths ExportResult) string {
	var b strings.Builder
	b.WriteString(i18n.T("TcpConn · TCP 并发连接测试\n========================================\n"))
	fmt.Fprintf(&b, i18n.T("会话 ID: %s\n状态: %s\n开始时间: %s\n结束时间: %s\n"), l.sessionID, snap.StopReason, l.createdAt.Format("2006-01-02 15:04:05"), snap.EndedAt)
	fmt.Fprintf(&b, i18n.T("测试模式: %s\n出口网卡: %s\n测试域名: %d 个\n发起目标: %d 个（域名 × 端口）\n测试时长: %d 秒\n"), ModeLabel(l.config.Mode), l.ifaceLabel, len(l.config.Domains), len(l.config.Domains)*len(modePorts(l.config.Mode)), l.config.DurationSec)
	if l.config.FailStop > 0 {
		fmt.Fprintf(&b, i18n.T("失败停止: %d 次\n"), l.config.FailStop)
	} else {
		b.WriteString(i18n.T("失败停止: 不限制\n"))
	}
	fmt.Fprintf(&b, i18n.T("发起速率: %d 次/秒\n单连接保持: 至本轮测试结束或对端关闭\n"), l.config.RatePerSec)
	if l.fdInfo.Supported {
		fmt.Fprintf(&b, i18n.T("文件句柄软上限: %d（硬上限 %d）\n"), l.fdInfo.Soft, l.fdInfo.Hard)
	} else if l.fdInfo.Error != "" {
		fmt.Fprintf(&b, i18n.T("文件句柄上限: %s\n"), l.fdInfo.Error)
	} else {
		b.WriteString(i18n.T("文件句柄上限: 当前平台不适用\n"))
	}
	fmt.Fprintf(&b, i18n.T("CSV 日志: %s\n摘要文件: %s\n----------------------------------------\n"), paths.LogPath, paths.SummaryPath)
	fmt.Fprintf(&b, i18n.T("尝试次数: %d\n成功: %d\n失败: %d\n中止: %d\n"), snap.Attempts, snap.Success, snap.Failed, snap.Aborted)
	if snap.Success+snap.Failed > 0 {
		fmt.Fprintf(&b, i18n.T("成功率: %.2f%%\n"), snap.SuccessRate)
	} else {
		b.WriteString(i18n.T("成功率: —\n"))
	}
	fmt.Fprintf(&b, i18n.T("峰值并发: %d\n平均发起速率: %.2f 次/秒\n停止原因: %s\n----------------------------------------\n测试域名（%d）:\n"), snap.Max, snap.AvgRate, snap.StopReason, len(l.config.Domains))
	for i, domain := range l.config.Domains {
		fmt.Fprintf(&b, "%3d. %s\n", i+1, domain)
	}
	b.WriteString(i18n.T("----------------------------------------\n说明: 仅解析 IPv4 / IPv6 地址并建立 TCP 连接；不发送 HTTP 请求、不进行 TLS 握手、不传输网页数据。\n"))
	return b.String()
}
