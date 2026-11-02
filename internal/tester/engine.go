package tester

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"tcpconnbenchmark/internal/i18n"
	"time"

	"tcpconnbenchmark/internal/syslimit"
)

const (
	// DefaultRatePerSec is the default number of connection attempts per second.
	DefaultRatePerSec = 125
	// MinRatePerSec and MaxRatePerSec bound the configurable attempt rate.
	MinRatePerSec = 1
	MaxRatePerSec = 1000
	// DefaultDurationSec is the default test duration in the GUI and CLI.
	DefaultDurationSec = 360
	// DialTimeout bounds one TCP handshake.
	DialTimeout = 5 * time.Second
	// DNSLookupTimeout bounds one name resolution.
	DNSLookupTimeout = 3 * time.Second
	// dnsCacheTTL is how long a successful resolution is reused.
	dnsCacheTTL = 5 * time.Minute
	// dnsErrorTTL avoids hammering a name that failed to resolve.
	dnsErrorTTL = 15 * time.Second
	// FDSoftLimitTarget is attempted at startup.
	FDSoftLimitTarget = 65536
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
	Mode        Mode
	Interface   string
	SourceIP    string
	Domains     []string
	DurationSec int
	FailStop    int
	RatePerSec  int
}

type target struct {
	domain string
	port   int
}

// LogEntry is one line in the live log and the CSV file.
type LogEntry struct {
	Seq       uint64
	Time      string
	Domain    string
	IP        string
	Port      int
	OK        bool
	Aborted   bool
	LatencyMS float64
	Error     string
}

// Snapshot is the live state polled by the front end.
type Snapshot struct {
	SessionID string
	Stopping  bool
	Finished  bool

	EndedAt string

	DurationSec int
	ElapsedSec  float64
	Remaining   float64

	Attempts uint64
	Success  uint64
	Failed   uint64
	Aborted  uint64
	Active   int64
	Dialing  int64
	Closed   uint64
	Max      int64

	SuccessRate float64
	AvgRate     float64

	ModeLabel      string
	InterfaceLabel string
	DomainCount    int
	FailStop       int
	RatePerSec     int

	StopReason  string
	LogPath     string
	SummaryPath string
	LogError    string
}

// Engine owns one running test. Create it with New, then call Start.
type Engine struct {
	cfg     Config
	targets []target
	dialer  *net.Dialer
	sources interfaceSources
	iface   *net.Interface

	logger *resultLogger
	ring   *logRing
	logSeq atomic.Uint64
	logMu  sync.Mutex

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
	reason   atomic.Value // string
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

	connMu  sync.Mutex
	conns   map[uint64]net.Conn
	connSeq atomic.Uint64
	wg      sync.WaitGroup

	resolver *dnsResolver
}

// New validates cfg and prepares a private temporary journal. No connection is
// made and no user-facing log is exported until Start and Export respectively.
func New(cfg Config) (*Engine, error) {
	cfg.Mode = Mode(strings.TrimSpace(string(cfg.Mode)))
	ports := modePorts(cfg.Mode)
	if len(ports) == 0 {
		return nil, errors.New(i18n.T("测试模式无效，请选择 TCP 80、TCP 443 或 TCP 80 + 443"))
	}
	if cfg.DurationSec < 1 || cfg.DurationSec > 86400 {
		return nil, errors.New(i18n.T("测试时长需在 1 到 86400 秒之间"))
	}
	if cfg.FailStop < 0 {
		return nil, errors.New(i18n.T("失败停止次数不能为负数"))
	}
	if cfg.FailStop > 10_000_000 {
		return nil, errors.New(i18n.T("失败停止次数过大"))
	}
	if cfg.RatePerSec < MinRatePerSec || cfg.RatePerSec > MaxRatePerSec {
		return nil, fmt.Errorf(i18n.T("发起速率需在 %d 到 %d 次/秒之间"), MinRatePerSec, MaxRatePerSec)
	}
	if len(cfg.Domains) == 0 {
		return nil, errors.New(i18n.T("请至少填写一个测试域名"))
	}
	if len(cfg.Domains) > 5000 {
		return nil, errors.New(i18n.T("测试域名不能超过 5000 个"))
	}

	domains, invalid := NormalizeDomains(cfg.Domains)
	if len(invalid) > 0 {
		shown := invalid
		if len(shown) > 5 {
			shown = shown[:5]
		}
		suffix := ""
		if len(invalid) > len(shown) {
			suffix = fmt.Sprintf(i18n.T(" 等 %d 个"), len(invalid))
		}
		return nil, fmt.Errorf(i18n.T("存在无法识别的域名: %s%s"), strings.Join(shown, i18n.T("、")), suffix)
	}
	if len(domains) == 0 {
		return nil, errors.New(i18n.T("请至少填写一个有效的测试域名"))
	}
	cfg.Domains = domains

	iface, sources, ifaceLabel, err := selectInterface(cfg.Interface, cfg.SourceIP)
	if err != nil {
		return nil, err
	}
	var bindIP net.IP
	if sources.v4 != nil {
		bindIP = sources.v4.IP
	} else if sources.v6 != nil {
		bindIP = sources.v6.IP
	} else if sources.link6 != nil {
		bindIP = sources.link6.IP
	}
	// An explicit source must not silently bypass a failed interface binding.
	if strings.TrimSpace(cfg.SourceIP) != "" {
		bindIP = nil
	}
	dialer := &net.Dialer{
		Timeout:   DialTimeout,
		KeepAlive: 30 * time.Second,
		Control:   interfaceControl(iface, bindIP),
	}

	fdInfo := syslimit.Raise(FDSoftLimitTarget)
	logger, err := newResultLogger(cfg, ifaceLabel, fdInfo)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		cfg:      cfg,
		dialer:   dialer,
		sources:  sources,
		iface:    iface,
		logger:   logger,
		ring:     newLogRing(4096),
		done:     make(chan struct{}),
		conns:    make(map[uint64]net.Conn),
		resolver: newDNSResolver(),
	}
	if iface != nil {
		e.resolver.sourceCheck = e.checkSource
	}
	for _, domain := range domains {
		for _, port := range ports {
			e.targets = append(e.targets, target{domain: domain, port: port})
		}
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.reason.Store("")
	logger.start(func() { e.stopWithReason(i18n.T("日志暂存失败")) })
	return e, nil
}

// Start begins the test loop in a background goroutine.
func (e *Engine) Start() {
	e.startedAt.Store(time.Now().UnixNano())
	go e.run()
}

// Stop asks the running test to stop. reason is shown in the summary; it
// defaults to 手动停止.
func (e *Engine) Stop(reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = i18n.T("手动停止")
	}
	e.stopWithReason(reason)
}

// Finished reports whether all connections are closed and the journal is sealed.
func (e *Engine) Finished() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// Wait blocks until the test has fully stopped and the journal is sealed.
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
	logError := ""
	if err := e.logger.err(); err != nil {
		logError = err.Error()
	}
	paths := e.logger.paths()

	return Snapshot{
		SessionID:      e.logger.sessionID,
		Stopping:       e.stopping.Load() && !finished,
		Finished:       finished,
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
		ModeLabel:      ModeLabel(e.cfg.Mode),
		InterfaceLabel: e.logger.ifaceLabel,
		DomainCount:    len(e.cfg.Domains),
		FailStop:       e.cfg.FailStop,
		RatePerSec:     e.cfg.RatePerSec,
		StopReason:     reason,
		LogPath:        paths.LogPath,
		SummaryPath:    paths.SummaryPath,
		LogError:       logError,
	}
}

func (e *Engine) run() {
	interval := time.Second / time.Duration(e.cfg.RatePerSec)
	duration := time.Duration(e.cfg.DurationSec) * time.Second
	started := time.Now()
	ticker := time.NewTicker(interval)
	durationTimer := time.NewTimer(duration)
	defer ticker.Stop()
	defer durationTimer.Stop()

	index := 0
	launch := func() {
		t := e.targets[index%len(e.targets)]
		index++
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.attempt(t)
		}()
	}

	// Fire the first attempt immediately so that even a very short test does
	// real work, and so the rate is measured from t=0.
	launch()

	for {
		select {
		case <-e.ctx.Done():
			e.finish()
			return
		case <-durationTimer.C:
			e.stopWithReason(i18n.T("达到测试时长"))
			e.finish()
			return
		case <-ticker.C:
			if e.ctx.Err() != nil {
				e.finish()
				return
			}
			if time.Since(started) >= duration {
				e.stopWithReason(i18n.T("达到测试时长"))
				e.finish()
				return
			}
			launch()
		}
	}
}

func (e *Engine) finish() {
	e.cancel()
	e.closeAllConnections()
	e.wg.Wait()

	e.endedAt.Store(time.Now().UnixNano())

	reason, _ := e.reason.Load().(string)
	if reason == "" {
		reason = i18n.T("测试结束")
		e.reason.Store(reason)
	}

	e.logger.close(e.logSeq.Load())
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

	dialCtx, cancelDial := context.WithTimeout(e.ctx, DialTimeout)
	defer cancelDial()
	host, err := e.targetHost(t.domain, ip)
	if err != nil {
		return nil, host, err
	}
	network, local, err := e.dialSource(ip)
	if err != nil {
		return nil, host, err
	}
	dialer := *e.dialer
	if local != nil {
		dialer.LocalAddr = local
	}
	address := net.JoinHostPort(host, strconv.Itoa(t.port))
	conn, err := dialer.DialContext(dialCtx, network, address)
	if err != nil {
		return nil, host, err
	}
	return conn, host, nil
}

func (e *Engine) targetHost(domain string, ip net.IP) (string, error) {
	host := ip.String()
	if literal, err := netip.ParseAddr(domain); err == nil && literal.Zone() != "" {
		if e.iface != nil && literal.Zone() != e.iface.Name && literal.Zone() != strconv.Itoa(e.iface.Index) {
			return literal.String(), errors.New(i18n.T("目标 IP 的 zone 不属于所选网卡"))
		}
		return literal.String(), nil
	}
	if ip.To4() == nil && ip.IsLinkLocalUnicast() && e.iface != nil {
		host += "%" + e.iface.Name
	}
	return host, nil
}

func (e *Engine) checkSource(ip net.IP) error {
	_, err := e.sources.forIP(ip)
	return err
}

// dialSource uses the same family/scope rules as the resolver before dialing.
func (e *Engine) dialSource(ip net.IP) (string, *net.TCPAddr, error) {
	network := "tcp6"
	if ip.To4() != nil {
		network = "tcp4"
	}
	if e.iface == nil {
		return network, nil, nil
	}
	local, err := e.sources.forIP(ip)
	return network, local, err
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
		e.stopWithReason(i18n.T("失败次数达到上限"))
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
	err := e.logger.write(entry, now)
	e.logMu.Unlock()
	if err != nil {
		e.stopWithReason(i18n.T("日志暂存失败"))
	}
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

	<-e.ctx.Done()

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
	mu          sync.Mutex
	cache       map[string]*dnsCacheEntry
	inflight    map[string]*dnsCall
	lookupIP    func(context.Context, string) ([]net.IP, error)
	sourceCheck func(net.IP) error
}

func (r *dnsResolver) checkIP(ip net.IP) error {
	if ip.To4() == nil && ip.To16() == nil {
		return errors.New(i18n.T("目标 IP 地址族与所选源地址不匹配"))
	}
	if r.sourceCheck != nil {
		return r.sourceCheck(ip)
	}
	return nil
}

type dnsCacheEntry struct {
	ips     []net.IP
	err     error
	expires time.Time
	next    int
}

type dnsCall struct {
	done chan struct{}
	ip   net.IP
	err  error
}

func newDNSResolver() *dnsResolver {
	return &dnsResolver{
		cache:    make(map[string]*dnsCacheEntry),
		inflight: make(map[string]*dnsCall),
		lookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
	}
}

func (r *dnsResolver) lookup(ctx context.Context, host string) (net.IP, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		ip := net.IP(literal.Unmap().WithZone("").AsSlice())
		if err := r.checkIP(ip); err != nil {
			return nil, err
		}
		return ip, nil
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
	if call, ok := r.inflight[host]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.ip, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &dnsCall{done: make(chan struct{})}
	r.inflight[host] = call
	r.mu.Unlock()

	// Only the first caller for this host performs the lookup. Others wait on
	// call.done above instead of hammering the system resolver.
	ips, err := r.lookupIP(ctx, host)
	now = time.Now()

	var cacheEntry *dnsCacheEntry
	switch {
	case err != nil:
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				call.err = fmt.Errorf(i18n.T("DNS 解析超时: %w"), err)
				cacheEntry = &dnsCacheEntry{err: call.err, expires: now.Add(dnsErrorTTL)}
			} else {
				// The test was stopped; do not cache a cancellation.
				call.err = ctxErr
			}
		} else {
			call.err = fmt.Errorf(i18n.T("DNS 解析失败: %w"), err)
			cacheEntry = &dnsCacheEntry{err: call.err, expires: now.Add(dnsErrorTTL)}
		}
	default:
		var usable []net.IP
		for _, ip := range ips {
			if r.checkIP(ip) == nil {
				usable = append(usable, ip)
			}
		}
		if len(usable) == 0 {
			call.err = errors.New(i18n.T("DNS 未返回与所选源 IP 地址族或作用域匹配的 IP 地址"))
			cacheEntry = &dnsCacheEntry{err: call.err, expires: now.Add(dnsErrorTTL)}
		} else {
			call.ip = usable[0]
			cacheEntry = &dnsCacheEntry{ips: usable, expires: now.Add(dnsCacheTTL), next: 1}
		}
	}

	r.mu.Lock()
	if cacheEntry != nil {
		r.cache[host] = cacheEntry
	}
	delete(r.inflight, host)
	close(call.done)
	r.mu.Unlock()

	return call.ip, call.err
}
