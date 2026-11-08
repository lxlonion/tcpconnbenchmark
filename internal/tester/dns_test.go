package tester

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomaticRoutingUsesIPv4Only(t *testing.T) {
	for _, name := range []string{"", "auto"} {
		t.Run("interface="+name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Interface = name
			e := newTestEngine(t, cfg)
			if e.resolver.network != "ip4" {
				t.Fatalf("DNS network = %q, want ip4", e.resolver.network)
			}
			e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("192.0.2.1")}, nil
			}
			for i := 0; i < 2; i++ {
				ip, err := e.resolver.lookup(context.Background(), "dual.example")
				if err != nil || !ip.Equal(net.ParseIP("192.0.2.1")) {
					t.Fatalf("lookup = %v, %v; want IPv4", ip, err)
				}
			}
			if _, err := e.resolver.lookup(context.Background(), "2001:db8::1"); err == nil {
				t.Fatal("IPv6 literal should be rejected in automatic mode")
			}
			e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("2001:db8::1")}, nil
			}
			if _, err := e.resolver.lookup(context.Background(), "ipv6-only.example"); err == nil {
				t.Fatal("IPv6-only result should be rejected in automatic mode")
			}
		})
	}
}

func TestDNSLookupCoalescesConcurrentRequests(t *testing.T) {
	r := newDNSResolver()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	want := net.IPv4(192, 0, 2, 42)

	r.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return []net.IP{want}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]net.IP, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.lookup(context.Background(), "example.com")
		}(i)
	}

	<-started
	time.Sleep(20 * time.Millisecond) // let the other callers join the in-flight lookup
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver called %d times, want 1", got)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if !results[i].Equal(want) {
			t.Fatalf("caller %d: ip=%v, want %v", i, results[i], want)
		}
	}
}

func TestDNSLookupCachesTimeoutFailure(t *testing.T) {
	r := newDNSResolver()
	var calls atomic.Int32
	r.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.lookup(ctx, "slow.invalid"); err == nil {
		t.Fatal("timeout lookup returned no error")
	} else if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1", calls.Load())
	}

	start := time.Now()
	if _, err := r.lookup(context.Background(), "slow.invalid"); err == nil {
		t.Fatal("cached timeout lookup returned no error")
	}
	if calls.Load() != 1 {
		t.Fatalf("timeout failure was not cached: resolver called %d times", calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("cached failure took %v", elapsed)
	}
}

func TestDNSLookupDoesNotCacheCancellation(t *testing.T) {
	r := newDNSResolver()
	var calls atomic.Int32
	want := net.IPv4(192, 0, 2, 7)
	r.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		if calls.Add(1) == 1 {
			return nil, context.Canceled
		}
		return []net.IP{want}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.lookup(ctx, "cancel.example"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, err := r.lookup(context.Background(), "cancel.example"); err != nil {
		t.Fatalf("after cancellation: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("resolver called %d times, want 2 (cancellation must not be cached)", calls.Load())
	}
}

func TestDNSLookupRotatesCachedAddresses(t *testing.T) {
	r := newDNSResolver()
	var calls atomic.Int32
	ips := []net.IP{net.IPv4(192, 0, 2, 1), net.IPv4(192, 0, 2, 2)}
	r.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		return ips, nil
	}

	first, err := r.lookup(context.Background(), "rotate.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.lookup(context.Background(), "rotate.example")
	if err != nil {
		t.Fatal(err)
	}
	third, err := r.lookup(context.Background(), "rotate.example")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1", calls.Load())
	}
	if !first.Equal(ips[0]) || !second.Equal(ips[1]) || !third.Equal(ips[0]) {
		t.Fatalf("rotation = %v, %v, %v", first, second, third)
	}
}

func TestDNSLookupIPLiteralSkipsResolver(t *testing.T) {
	r := newDNSResolver()
	r.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		t.Fatal("IP literal must not be resolved")
		return nil, nil
	}
	ip, err := r.lookup(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !ip.Equal(net.IPv4(1, 2, 3, 4)) {
		t.Fatalf("ip = %v", ip)
	}
	if ip, err := r.lookup(context.Background(), "fe80::1%en0"); err != nil || ip.String() != "fe80::1" {
		t.Fatalf("scoped literal: %v, %v", ip, err)
	}
}

func TestDNSProductionDualStackFilteringAndCache(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"auto", "", []string{"192.0.2.10", "2001:db8::10", "192.0.2.11", "2001:db8::11"}},
		{"ipv4 source", "192.0.2.4", []string{"192.0.2.10", "192.0.2.11"}},
		{"ipv6 source", "2001:db8::1", []string{"2001:db8::10", "2001:db8::11"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Automatic routing leaves sourceCheck unset; a selected interface installs it.
			r := newDNSResolver()
			checks := 0
			if tc.source != "" {
				e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"2001:db8::1"}, tc.source)
				r = e.resolver
				checkSource := r.sourceCheck
				r.sourceCheck = func(ip net.IP) error {
					checks++
					return checkSource(ip)
				}
			} else if r.sourceCheck != nil {
				t.Fatal("automatic routing unexpectedly has a source check")
			}
			calls := 0
			r.lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
				if host != "dual-production.example" {
					t.Fatalf("unexpected lookup host %q", host)
				}
				calls++
				return []net.IP{
					net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::10"),
					net.ParseIP("192.0.2.11"), net.ParseIP("2001:db8::11"),
				}, nil
			}
			assertDNSCachedRotation(t, r, "dual-production.example", tc.want)
			wantChecks := 0
			if tc.source != "" {
				wantChecks = 4
			}
			if calls != 1 || checks != wantChecks {
				t.Fatalf("lookup calls = %d, source checks = %d; want 1, %d", calls, checks, wantChecks)
			}
		})
	}
}

func TestDNSProductionIPv6ScopeFilteringAndCache(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"both scopes", "", []string{"fe80::20", "2001:db8::20", "fe80::21", "2001:db8::21"}},
		{"global source", "2001:db8::1", []string{"2001:db8::20", "2001:db8::21"}},
		{"link-local source", "fe80::1%test0", []string{"fe80::20", "fe80::21"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testScopedEngine(t, nil, []string{"2001:db8::1", "fe80::1"}, tc.source)
			calls := 0
			e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
				calls++
				return []net.IP{
					net.ParseIP("fe80::20"), net.ParseIP("2001:db8::20"),
					net.ParseIP("fe80::21"), net.ParseIP("2001:db8::21"),
				}, nil
			}
			assertDNSCachedRotation(t, e.resolver, "scope-production.example", tc.want)
			if calls != 1 {
				t.Fatalf("lookup calls = %d, want 1", calls)
			}
		})
	}
}

func TestDNSProductionMismatchAndFailureCache(t *testing.T) {
	for _, tc := range []struct {
		name, source, literal, literalError, validLiteral string
		answers                                           []string
	}{
		{"ipv4 source", "192.0.2.4", "2001:db8::20", "目标 IPv6 不是链路本地地址，但所选源 IP 不包含非链路本地 IPv6 地址", "192.0.2.20", []string{"2001:db8::20", "fe80::20"}},
		{"ipv6 source", "2001:db8::1", "192.0.2.20", "目标 IP 地址族与所选源地址不匹配", "2001:db8::20", []string{"192.0.2.20"}},
		{"global source and link-local target", "2001:db8::1", "fe80::20%test0", "目标 IPv6 为链路本地地址，但所选源 IP 不包含链路本地地址", "2001:db8::20", []string{"fe80::20"}},
		{"link-local source and global target", "fe80::1%test0", "2001:db8::20", "目标 IPv6 不是链路本地地址，但所选源 IP 不包含非链路本地 IPv6 地址", "fe80::20%test0", []string{"2001:db8::20"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"2001:db8::1", "fe80::1"}, tc.source)
			checks := 0
			checkSource := e.resolver.sourceCheck
			e.resolver.sourceCheck = func(ip net.IP) error {
				checks++
				return checkSource(ip)
			}
			calls := 0
			e.resolver.lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
				if host != "no-match.example" {
					t.Fatalf("unexpected lookup host %q", host)
				}
				calls++
				var ips []net.IP
				for _, answer := range tc.answers {
					ips = append(ips, net.ParseIP(answer))
				}
				return ips, nil
			}
			const dnsError = "DNS 未返回与所选源 IP 地址族或作用域匹配的 IP 地址"
			for i := 0; i < 2; i++ {
				ip, err := e.resolver.lookup(context.Background(), "no-match.example")
				if ip != nil || err == nil || err.Error() != dnsError {
					t.Fatalf("lookup %d = %v, %v; want nil, %q", i, ip, err, dnsError)
				}
			}
			if calls != 1 || checks != len(tc.answers) {
				t.Fatalf("cached failure: lookup calls = %d, source checks = %d", calls, checks)
			}
			ip, err := e.resolver.lookup(context.Background(), tc.literal)
			if ip != nil || err == nil || err.Error() != tc.literalError {
				t.Fatalf("literal %q = %v, %v; want nil, %q", tc.literal, ip, err, tc.literalError)
			}
			ip, err = e.resolver.lookup(context.Background(), tc.validLiteral)
			plain, _, _ := strings.Cut(tc.validLiteral, "%")
			if err != nil || !ip.Equal(net.ParseIP(plain)) {
				t.Fatalf("literal %q = %v, %v", tc.validLiteral, ip, err)
			}
			if calls != 1 || checks != len(tc.answers)+2 {
				t.Fatalf("literal handling: lookup calls = %d, source checks = %d", calls, checks)
			}
		})
	}
}

func TestDNSProductionScopedLiteralZones(t *testing.T) {
	for _, source := range []string{"fe80::1%test0", "fe80::1%42"} {
		t.Run(source, func(t *testing.T) {
			e := testScopedEngine(t, nil, []string{"fe80::1"}, source)
			e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
				t.Fatal("scoped literal must not use DNS")
				return nil, nil
			}
			for _, tc := range []struct {
				literal, host, hostError string
			}{
				{"fe80::20", "fe80::20%test0", ""},
				{"fe80::20%test0", "fe80::20%test0", ""},
				{"fe80::20%42", "fe80::20%42", ""},
				{"fe80::20%other", "fe80::20%other", "目标 IP 的 zone 不属于所选网卡"},
			} {
				// lookup checks the unzoned IP; targetHost checks the destination zone later.
				ip, err := e.resolver.lookup(context.Background(), tc.literal)
				if err != nil || !ip.Equal(net.ParseIP("fe80::20")) {
					t.Fatalf("literal %q = %v, %v", tc.literal, ip, err)
				}
				host, err := e.targetHost(tc.literal, ip)
				if host != tc.host || (tc.hostError == "" && err != nil) || (tc.hostError != "" && (err == nil || err.Error() != tc.hostError)) {
					t.Fatalf("targetHost(%q) = %q, %v; want %q, %q", tc.literal, host, err, tc.host, tc.hostError)
				}
				network, local, err := e.dialSource(ip)
				if err != nil || network != "tcp6" || local == nil || !local.IP.Equal(net.ParseIP("fe80::1")) || local.Zone != "test0" {
					t.Fatalf("dialSource(%q) = %q, %v, %v", tc.literal, network, local, err)
				}
			}
		})
	}
}

func TestDNSProductionRejectsInvalidIPsBeforeSourceCheck(t *testing.T) {
	e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"2001:db8::1"}, "")
	checks := 0
	checkSource := e.resolver.sourceCheck
	e.resolver.sourceCheck = func(ip net.IP) error {
		checks++
		return checkSource(ip)
	}
	invalid := []net.IP{nil, {}, {1}, {192, 0, 2}, {192, 0, 2, 1, 2}, make(net.IP, 15), make(net.IP, 17)}
	const invalidError = "目标 IP 地址族与所选源地址不匹配"
	for _, ip := range invalid {
		if err := e.resolver.checkIP(ip); err == nil || err.Error() != invalidError {
			t.Fatalf("checkIP(%v, len=%d) = %v; want %q", ip, len(ip), err, invalidError)
		}
	}
	if checks != 0 {
		t.Fatalf("sourceCheck called %d times for invalid addresses", checks)
	}
	calls := 0
	e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
		calls++
		return invalid, nil
	}
	const dnsError = "DNS 未返回与所选源 IP 地址族或作用域匹配的 IP 地址"
	for i := 0; i < 2; i++ {
		ip, err := e.resolver.lookup(context.Background(), "invalid-only.example")
		if ip != nil || err == nil || err.Error() != dnsError {
			t.Fatalf("lookup %d = %v, %v; want nil, %q", i, ip, err, dnsError)
		}
	}
	if calls != 1 || checks != 0 {
		t.Fatalf("lookup calls = %d, source checks = %d; want 1, 0", calls, checks)
	}
}

func TestDNSProductionFiltersInvalidIPsFromMixedResults(t *testing.T) {
	e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"2001:db8::1"}, "")
	v4, v6 := net.IP{192, 0, 2, 20}, net.ParseIP("2001:db8::20")
	var checked []net.IP
	checkSource := e.resolver.sourceCheck
	e.resolver.sourceCheck = func(ip net.IP) error {
		checked = append(checked, append(net.IP(nil), ip...))
		return checkSource(ip)
	}
	calls := 0
	e.resolver.lookupIP = func(context.Context, string) ([]net.IP, error) {
		calls++
		return []net.IP{nil, {1, 2, 3}, v4, {1, 2, 3, 4, 5}, v6, make(net.IP, 17)}, nil
	}
	want := []net.IP{v4, v6, v4, v6, v4}
	for i, expected := range want {
		ip, err := e.resolver.lookup(context.Background(), "invalid-mixed.example")
		if err != nil || !reflect.DeepEqual(ip, expected) {
			t.Fatalf("lookup %d = %v, %v; want original IP %v", i, ip, err, expected)
		}
	}
	if calls != 1 || !reflect.DeepEqual(checked, []net.IP{v4, v6}) {
		t.Fatalf("lookup calls = %d, source-checked IPs = %v; want 1, %v", calls, checked, []net.IP{v4, v6})
	}
}

func TestDNSProductionCancellationRetriesWithSourceFiltering(t *testing.T) {
	e := testScopedEngine(t, []string{"192.0.2.4"}, []string{"2001:db8::1"}, "192.0.2.4")
	checks := 0
	checkSource := e.resolver.sourceCheck
	e.resolver.sourceCheck = func(ip net.IP) error {
		checks++
		return checkSource(ip)
	}
	calls := 0
	e.resolver.lookupIP = func(ctx context.Context, _ string) ([]net.IP, error) {
		calls++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []net.IP{net.ParseIP("2001:db8::20"), net.ParseIP("192.0.2.20")}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ip, err := e.resolver.lookup(ctx, "cancel-source.example"); ip != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup = %v, %v; want nil, context.Canceled", ip, err)
	}
	if calls != 1 || checks != 0 {
		t.Fatalf("after cancellation: lookup calls = %d, source checks = %d", calls, checks)
	}
	assertDNSCachedRotation(t, e.resolver, "cancel-source.example", []string{"192.0.2.20"})
	if calls != 2 || checks != 2 {
		t.Fatalf("after retry: lookup calls = %d, source checks = %d; want 2, 2", calls, checks)
	}
}

func assertDNSCachedRotation(t *testing.T, r *dnsResolver, host string, want []string) {
	t.Helper()
	for i := 0; i < 2*len(want)+1; i++ {
		ip, err := r.lookup(context.Background(), host)
		if err != nil || ip.String() != want[i%len(want)] {
			t.Fatalf("lookup %d = %v, %v; want %s", i, ip, err, want[i%len(want)])
		}
	}
}
