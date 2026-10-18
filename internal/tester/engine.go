package tester

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tcplink/internal/syslimit"
)

const (
	// DefaultRatePerSec is the default number of connection attempts per second.
	DefaultRatePerSec = 125
	// MinRatePerSec and MaxRatePerSec bound the configurable attempt rate.
	MinRatePerSec = 1
	MaxRatePerSec = 1000
	// MaxConnectionHold is how long one successful connection is kept open.
	MaxConnectionHold = 60 * time.Second
	// DialTimeout bounds one TCP handshake.
	DialTimeout = 5 * time.Second
	// DNSLookupTimeout bounds one name resolution.
	DNSLookupTimeout = 3 * time.Second
	// dnsCacheTTL is how long a successful resolution is reused.
	dnsCacheTTL = 5 * time.Minute
	// dnsErrorTTL avoids hammering a name that failed to resolve.
	dnsErrorTTL = 15 * time.Second
	// FDSoftLimitTarget is attempted at startup.
	FDSoftLimitTarget = 32768
)

// Mode selects which TCP ports are tested.
type Mode string

const (
	Mode80   Mode = "80"
	Mode443  Mode = "443"
	ModeBoth Mode = "both"
)

// ModeLabel returns the Chinese label shown in the UI and summary.
func ModeLabel(m Mode) string {
	switch m {
	case Mode80:
		return "TCP 80"
	case Mode443:
		return "TCP 443"
	case ModeBoth:
		return "TCP 80 + 443"
	default:
		return string(m)
	}
}

func modePorts(m Mode) []int {
	switch m {
	case Mode80:
		return []int{80}
	case Mode443:
		return []int{443}
	case ModeBoth:
		return []int{80, 443}
	default:
		return nil
	}
}

// Config describes one test session.
type Config struct {
	Mode        Mode     `json:"mode"`
	Interface   string   `json:"interface"`
	Domains     []string `json:"domains"`
	DurationSec int      `json:"durationSec"`
	FailStop    int      `json:"failStop"`
	RatePerSec  int      `json:"ratePerSec"`
}

type target struct {
	domain string
	port   int
}

// LogEntry is one line in the live log and the CSV file.
type LogEntry struct {
	Seq       uint64  `json:"seq"`
	Time      string  `json:"time"`
	Domain    string  `json:"domain"`
	IP        string  `json:"ip"`
	Port      int     `json:"port"`
	OK        bool    `json:"ok"`
	Aborted   bool    `json:"aborted"`
	LatencyMS float64 `json:"latencyMs"`
	Error     string  `json:"error,omitempty"`
}

// Snapshot is the live state polled by the front end.
type Snapshot struct {
	SessionID string `json:"sessionId"`
	Running   bool   `json:"running"`
	Stopping  bool   `json:"stopping"`
	Finished  bool   `json:"finished"`
	HasEngine bool   `json:"hasEngine"`

	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`

	DurationSec int     `json:"durationSec"`
	ElapsedSec  float64 `json:"elapsedSec"`
	Remaining   float64 `json:"remainingSec"`

	Attempts uint64 `json:"attempts"`
	Success  uint64 `json:"success"`
	Failed   uint64 `json:"failed"`
	Aborted  uint64 `json:"aborted"`
	Active   int64  `json:"active"`
	Dialing  int64  `json:"dialing"`
	Closed   uint64 `json:"closed"`
	Max      int64  `json:"maxActive"`

	SuccessRate float64 `json:"successRate"`
	AvgRate     float64 `json:"avgRate"`

	Mode           string `json:"mode"`
	ModeLabel      string `json:"modeLabel"`
	Interface      string `json:"interface"`
	InterfaceLabel string `json:"interfaceLabel"`
	DomainCount    int    `json:"domainCount"`
	TargetCount    int    `json:"targetCount"`
	FailStop       int    `json:"failStop"`
	RatePerSec     int    `json:"ratePerSec"`

	StopReason  string `json:"stopReason"`
	LogPath     string `json:"logPath"`
	SummaryPath string `json:"summaryPath"`
	LogError    string `json:"logError,omitempty"`
}

// Engine owns one running test. Create it with New, then call Start.
type Engine struct {
	cfg        Config
	domains    []string
	targets    []target
	iface      *net.Interface
	localIP    net.IP
	ifaceLabel string
	fdInfo     syslimit.Info

	logger    *resultLogger
	sessionID string
	ring      *logRing
	logSeq    atomic.Uint64
	logMu     sync.Mutex

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
	reason   atomic.Value // string
	running  atomic.Bool
	stopping atomic.Bool

	startedAt atomic.Int64
	endedAt   atomic.Int64

	attempts  atomic.Uint64
	success   atomic.Uint64
	failed    atomic.Uint64
	aborted   atomic.Uint64
	closed    atomic.Uint64
	active    atomic.Int64
	dialing   atomic.Int64
	maxActive atomic.Int64

	summaryErr atomic.Value // error

	connMu  sync.Mutex
	conns   map[uint64]net.Conn
	connSeq atomic.Uint64
	wg      sync.WaitGroup

	resolver *dnsResolver
}

// New validates cfg, opens the automatically saved CSV log, and prepares a
// test engine. No connection is made until Start is called.
func New(cfg Config) (*Engine, error) {
	cfg.Mode = Mode(strings.TrimSpace(string(cfg.Mode)))
	ports := modePorts(cfg.Mode)
	if len(ports) == 0 {
		return nil, fmt.Errorf("测试模式无效，请选择 TCP 80、TCP 443 或 TCP 80 + 443")
	}
	if cfg.DurationSec < 1 || cfg.DurationSec > 86400 {
		return nil, fmt.Errorf("测试时长需在 1 到 86400 秒之间")
	}
	if cfg.FailStop < 0 {
		return nil, fmt.Errorf("失败停止次数不能为负数")
	}
	if cfg.FailStop > 10_000_000 {
		return nil, fmt.Errorf("失败停止次数过大")
	}
	if cfg.RatePerSec < MinRatePerSec || cfg.RatePerSec > MaxRatePerSec {
		return nil, fmt.Errorf("发起速率需在 %d 到 %d 次/秒之间", MinRatePerSec, MaxRatePerSec)
	}
	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("请至少填写一个测试域名")
	}
	if len(cfg.Domains) > 5000 {
		return nil, fmt.Errorf("测试域名不能超过 5000 个")
	}

	domains, invalid := NormalizeDomains(cfg.Domains)
	if len(invalid) > 0 {
		shown := invalid
		if len(shown) > 5 {
			shown = shown[:5]
		}
		suffix := ""
		if len(invalid) > len(shown) {
			suffix = fmt.Sprintf(" 等 %d 个", len(invalid))
		}
		return nil, fmt.Errorf("存在无法识别的域名: %s%s", strings.Join(shown, "、"), suffix)
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("请至少填写一个有效的测试域名")
	}
	cfg.Domains = domains

	iface, localIP, ifaceLabel, err := selectInterface(cfg.Interface)
	if err != nil {
		return nil, err
	}
	cfg.Interface = ifaceName(iface)

	fdInfo := syslimit.Raise(FDSoftLimitTarget)
	logger, err := newResultLogger(cfg, domains, ifaceLabel, fdInfo)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		cfg:        cfg,
		domains:    domains,
		iface:      iface,
		localIP:    localIP,
		ifaceLabel: ifaceLabel,
		fdInfo:     fdInfo,
		logger:     logger,
		sessionID:  logger.sessionID,
		ring:       newLogRing(4096),
		done:       make(chan struct{}),
		conns:      make(map[uint64]net.Conn),
		resolver:   newDNSResolver(),
	}
	for _, domain := range domains {
		for _, port := range ports {
			e.targets = append(e.targets, target{domain: domain, port: port})
		}
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.reason.Store("")
	return e, nil
}

func ifaceName(iface *net.Interface) string {
	if iface == nil {
		return ""
	}
	return iface.Name
}

// Start begins the test loop in a background goroutine.
func (e *Engine) Start() {
	e.startedAt.Store(time.Now().UnixNano())
	e.running.Store(true)
	go e.run()
}

// Stop asks the running test to stop. reason is shown in the summary; it
// defaults to 手动停止.
func (e *Engine) Stop(reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "手动停止"
	}
	e.stopWithReason(reason)
}

// Finished reports whether all connections are closed and logs are saved.
func (e *Engine) Finished() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// Wait blocks until the test has fully stopped and the summary is written.
func (e *Engine) Wait() {
	<-e.done
}

// LogsSince returns new log entries with a sequence number greater than since.
// If entries were evicted from the in-memory ring, dropped is true.
func (e *Engine) LogsSince(since uint64, limit int) ([]LogEntry, bool) {
	return e.ring.since(since, limit)
}

// Snapshot returns a consistent-enough view of the live counters for the GUI.
func (e *Engine) Snapshot() Snapshot {
	finished := e.Finished()
	startNs := e.startedAt.Load()
	endNs := e.endedAt.Load()
	nowNs := time.Now().UnixNano()
	if endNs > 0 {
		nowNs = endNs
	}

	elapsed := 0.0
	if startNs > 0 && nowNs > startNs {
		elapsed = float64(nowNs-startNs) / float64(time.Second)
	}
	remaining := float64(e.cfg.DurationSec) - elapsed
	if remaining < 0 {
		remaining = 0
	}

	attempts := e.attempts.Load()
	success := e.success.Load()
	failed := e.failed.Load()
	aborted := e.aborted.Load()
	completed := success + failed
	successRate := 0.0
	if completed > 0 {
		successRate = float64(success) / float64(completed) * 100
	}
	avgRate := 0.0
	if elapsed > 0 {
		avgRate = float64(attempts) / elapsed
	}

	reason, _ := e.reason.Load().(string)
	endedAt := ""
	if endNs > 0 {
		endedAt = time.Unix(0, endNs).Format("2006-01-02 15:04:05")
	}
	startedAt := ""
	if startNs > 0 {
		startedAt = time.Unix(0, startNs).Format("2006-01-02 15:04:05")
	}

	logError := ""
	if err := e.logger.err(); err != nil {
		logError = err.Error()
	}
	if v := e.summaryErr.Load(); v != nil {
		if err, ok := v.(error); ok && err != nil {
			if logError != "" {
				logError += "；"
			}
			logError += err.Error()
		}
	}

	return Snapshot{
		SessionID:      e.sessionID,
		Running:        e.running.Load(),
		Stopping:       e.stopping.Load() && !finished,
		Finished:       finished,
		HasEngine:      true,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		DurationSec:    e.cfg.DurationSec,
		ElapsedSec:     elapsed,
		Remaining:      remaining,
		Attempts:       attempts,
		Success:        success,
		Failed:         failed,
		Aborted:        aborted,
		Active:         e.active.Load(),
		Dialing:        e.dialing.Load(),
		Closed:         e.closed.Load(),
		Max:            e.maxActive.Load(),
		SuccessRate:    successRate,
		AvgRate:        avgRate,
		Mode:           string(e.cfg.Mode),
		ModeLabel:      ModeLabel(e.cfg.Mode),
		Interface:      e.cfg.Interface,
		InterfaceLabel: e.ifaceLabel,
		DomainCount:    len(e.domains),
		TargetCount:    len(e.targets),
		FailStop:       e.cfg.FailStop,
		RatePerSec:     e.cfg.RatePerSec,
		StopReason:     reason,
		LogPath:        e.logger.csvPath,
		SummaryPath:    e.logger.summaryPath,
		LogError:       logError,
	}
}

func (e *Engine) run() {
	interval := time.Second / time.Duration(e.cfg.RatePerSec)
	ticker := time.NewTicker(interval)
	durationTimer := time.NewTimer(time.Duration(e.cfg.DurationSec) * time.Second)
	defer ticker.Stop()
	defer durationTimer.Stop()

	index := 0
	for {
		select {
		case <-e.ctx.Done():
			e.finish()
			return
		case <-durationTimer.C:
			e.stopWithReason("达到测试时长")
			e.finish()
			return
		case <-ticker.C:
			if e.ctx.Err() != nil {
				e.finish()
				return
			}
			t := e.targets[index%len(e.targets)]
			index++
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				e.attempt(t)
			}()
		}
	}
}

func (e *Engine) finish() {
	e.cancel()
	e.closeAllConnections()
	e.wg.Wait()

	e.endedAt.Store(time.Now().UnixNano())
	e.running.Store(false)

	reason, _ := e.reason.Load().(string)
	if reason == "" {
		reason = "测试结束"
		e.reason.Store(reason)
	}

	snap := e.Snapshot()
	snap.Finished = true
	if err := e.logger.writeSummary(reason, &snap); err != nil {
		e.summaryErr.Store(err)
	}
	if err := e.logger.close(); err != nil && e.summaryErr.Load() == nil {
		e.summaryErr.Store(err)
	}
	close(e.done)
}

func (e *Engine) stopWithReason(reason string) {
	e.stopOnce.Do(func() {
		e.reason.Store(reason)
		e.stopping.Store(true)
		e.cancel()
	})
}

func (e *Engine) attempt(t target) {
	// A tick may be selected just before the duration timer cancels the
	// context. If this worker has not actually started yet, it must not be
	// counted as an attempt (and especially not as an aborted connection).
	if e.ctx.Err() != nil {
		return
	}
	started := time.Now()
	e.attempts.Add(1)
	e.dialing.Add(1)
	conn, ip, err := e.connect(t)
	e.dialing.Add(-1)

	if err != nil {
		if e.ctx.Err() != nil {
			e.recordAborted(t, ip, started)
			return
		}
		e.recordResult(t, ip, started, false, err)
		return
	}

	if e.ctx.Err() != nil {
		conn.Close()
		e.recordAborted(t, ip, started)
		return
	}

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}

	e.incrementActive()
	e.recordResult(t, ip, started, true, nil)
	e.hold(conn)
}

func (e *Engine) connect(t target) (net.Conn, string, error) {
	lookupCtx, cancelLookup := context.WithTimeout(e.ctx, DNSLookupTimeout)
	ip, err := e.resolver.lookup(lookupCtx, t.domain)
	cancelLookup()
	if err != nil {
		return nil, "", err
	}
	if e.ctx.Err() != nil {
		return nil, ip.String(), e.ctx.Err()
	}

	dialer := &net.Dialer{
		Timeout: DialTimeout,
		Control: interfaceControl(e.iface, e.localIP),
	}
	if e.localIP != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: e.localIP}
	}

	dialCtx, cancelDial := context.WithTimeout(e.ctx, DialTimeout)
	defer cancelDial()
	address := net.JoinHostPort(ip.String(), strconv.Itoa(t.port))
	conn, err := dialer.DialContext(dialCtx, "tcp4", address)
	if err != nil {
		return nil, ip.String(), err
	}
	return conn, ip.String(), nil
}

func (e *Engine) recordResult(t target, ip string, started time.Time, ok bool, err error) {
	if ok {
		e.success.Add(1)
	} else {
		e.failed.Add(1)
	}

	now := time.Now()
	message := ""
	if err != nil {
		message = truncateRunes(err.Error(), 240)
	}
	entry := LogEntry{
		Time:      now.Format("15:04:05.000"),
		Domain:    t.domain,
		IP:        ip,
		Port:      t.port,
		OK:        ok,
		LatencyMS: float64(now.Sub(started).Microseconds()) / 1000,
		Error:     message,
	}
	e.writeEntry(entry, now)

	if !ok && e.cfg.FailStop > 0 && e.failed.Load() >= uint64(e.cfg.FailStop) {
		e.stopWithReason("失败次数达到上限")
	}
}

func (e *Engine) recordAborted(t target, ip string, started time.Time) {
	e.aborted.Add(1)
	now := time.Now()
	entry := LogEntry{
		Time:      now.Format("15:04:05.000"),
		Domain:    t.domain,
		IP:        ip,
		Port:      t.port,
		Aborted:   true,
		LatencyMS: float64(now.Sub(started).Microseconds()) / 1000,
	}
	e.writeEntry(entry, now)
}

func (e *Engine) writeEntry(entry LogEntry, now time.Time) {
	// Sequence assignment and ring insertion must be atomic: the UI resumes
	// from the highest sequence it has seen, so inserting a lower sequence
	// after a higher one would make that entry invisible to the live log.
	e.logMu.Lock()
	entry.Seq = e.logSeq.Add(1)
	e.ring.add(entry)
	e.logMu.Unlock()

	e.logger.write(entry, now)
}

func (e *Engine) incrementActive() {
	n := e.active.Add(1)
	for {
		max := e.maxActive.Load()
		if n <= max || e.maxActive.CompareAndSwap(max, n) {
			return
		}
	}
}

func (e *Engine) hold(conn net.Conn) {
	id := e.connSeq.Add(1)
	e.connMu.Lock()
	e.conns[id] = conn
	e.connMu.Unlock()

	timer := time.NewTimer(MaxConnectionHold)
	select {
	case <-e.ctx.Done():
	case <-timer.C:
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	_ = conn.Close()
	e.connMu.Lock()
	delete(e.conns, id)
	e.connMu.Unlock()
	e.active.Add(-1)
	e.closed.Add(1)
}

func (e *Engine) closeAllConnections() {
	e.connMu.Lock()
	conns := make([]net.Conn, 0, len(e.conns))
	for _, conn := range e.conns {
		conns = append(conns, conn)
	}
	e.connMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// ---- in-memory live log ----

type logRing struct {
	mu    sync.Mutex
	buf   []LogEntry
	start int
	size  int
}

func newLogRing(capacity int) *logRing {
	if capacity < 1 {
		capacity = 1
	}
	return &logRing{buf: make([]LogEntry, capacity)}
}

func (r *logRing) add(entry LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size < len(r.buf) {
		index := (r.start + r.size) % len(r.buf)
		r.buf[index] = entry
		r.size++
		return
	}
	r.buf[r.start] = entry
	r.start = (r.start + 1) % len(r.buf)
}

func (r *logRing) since(seq uint64, limit int) ([]LogEntry, bool) {
	if limit <= 0 {
		limit = 400
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil, false
	}

	firstSeq := r.buf[r.start].Seq
	dropped := seq > 0 && seq+1 < firstSeq

	if seq == 0 {
		from := r.size - limit
		if from < 0 {
			from = 0
		}
		out := make([]LogEntry, 0, r.size-from)
		for i := from; i < r.size; i++ {
			out = append(out, r.buf[(r.start+i)%len(r.buf)])
		}
		return out, dropped
	}

	out := make([]LogEntry, 0, limit)
	for i := 0; i < r.size && len(out) < limit; i++ {
		entry := r.buf[(r.start+i)%len(r.buf)]
		if entry.Seq > seq {
			out = append(out, entry)
		}
	}
	return out, dropped
}

// ---- DNS cache ----

type dnsResolver struct {
	mu    sync.Mutex
	cache map[string]*dnsCacheEntry
}

type dnsCacheEntry struct {
	ips     []net.IP
	err     error
	expires time.Time
	next    int
}

func newDNSResolver() *dnsResolver {
	return &dnsResolver{cache: make(map[string]*dnsCacheEntry)}
}

func (r *dnsResolver) lookup(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, nil
		}
		return nil, fmt.Errorf("仅支持 IPv4")
	}

	now := time.Now()
	r.mu.Lock()
	if entry, ok := r.cache[host]; ok && now.Before(entry.expires) {
		if entry.err != nil {
			err := entry.err
			r.mu.Unlock()
			return nil, err
		}
		if len(entry.ips) > 0 {
			ip := entry.ips[entry.next%len(entry.ips)]
			entry.next++
			r.mu.Unlock()
			return ip, nil
		}
	}
	r.mu.Unlock()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wrapped := fmt.Errorf("DNS 解析失败: %w", err)
		r.store(host, &dnsCacheEntry{err: wrapped, expires: now.Add(dnsErrorTTL)})
		return nil, wrapped
	}

	var v4 []net.IP
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			v4 = append(v4, ip4)
		}
	}
	if len(v4) == 0 {
		wrapped := fmt.Errorf("DNS 未返回 IPv4 地址")
		r.store(host, &dnsCacheEntry{err: wrapped, expires: now.Add(dnsErrorTTL)})
		return nil, wrapped
	}

	entry := &dnsCacheEntry{ips: v4, expires: now.Add(dnsCacheTTL), next: 1}
	r.store(host, entry)
	return v4[0], nil
}

func (r *dnsResolver) store(host string, entry *dnsCacheEntry) {
	r.mu.Lock()
	r.cache[host] = entry
	r.mu.Unlock()
}
