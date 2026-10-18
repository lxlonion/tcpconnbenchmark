package tester

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
}
