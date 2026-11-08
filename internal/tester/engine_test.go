package tester

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestBuiltinDomainCount(t *testing.T) {
	if got := len(BuiltinDomains); got != 84 {
		t.Fatalf("内置域名数量 = %d，期望 84", got)
	}
	valid, invalid := NormalizeDomains(BuiltinDomains)
	if len(invalid) != 0 {
		t.Fatalf("内置域名存在无效项: %v", invalid)
	}
	if len(valid) != 84 {
		t.Fatalf("去重后内置域名数量 = %d，期望 84", len(valid))
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"baidu.com", "baidu.com", false},
		{" HTTPS://WWW.Example.COM/path?q=1 ", "www.example.com", false},
		{"example.com:8443", "example.com", false},
		{"example.com.", "example.com", false},
		{"1.2.3.4", "1.2.3.4", false},
		{"例子.中国", "xn--fsqu00a.xn--fiqs8s", false},
		{"bad..example.com", "", true},
		{"-bad.example.com", "", true},
		{"exa mple.com", "", true},
		{"2001:db8::1", "2001:db8::1", false},
		{"[2001:db8::1]", "2001:db8::1", false},
		{"[2001:db8::1]:443", "2001:db8::1", false},
		{"http://[fe80::1%25en0]:443/a", "fe80::1%en0", false},
		{"fe80::1%en0", "fe80::1%en0", false},
	}
	for _, tt := range tests {
		got, err := NormalizeDomain(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%q: 期望错误，得到 %q", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: 意外错误: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%q: 得到 %q，期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeDomainPortAndZoneRegression(t *testing.T) {
	tests := []struct {
		in, want, wantErr string
	}{
		{"example.com:", "example.com", ""},
		{"example.com:abc", "example.com", ""},
		{"example.com:123abc", "example.com", ""},
		{"example.com:443", "example.com", ""},
		{"example.com:65536", "example.com", ""},
		{":443", "", "域名为空"},
		{":abc", "", "域名为空"},
		{"http://example.com:/path", "example.com", ""},
		{"http://example.com:abc/path", "", "无法解析网址"},
		{"https://EXAMPLE.com:65536/path", "example.com", ""},
		{"192.0.2.1:", "192.0.2.1", ""},
		{"192.0.2.1:not-a-port", "192.0.2.1", ""},
		{"192.0.2.1:443", "192.0.2.1", ""},
		{"2001:db8::1", "2001:db8::1", ""},
		{"[2001:db8::1]:", "2001:db8::1", ""},
		{"[2001:db8::1]:abc", "2001:db8::1", ""},
		{"[2001:db8::1]:443", "2001:db8::1", ""},
		{"[::ffff:192.0.2.1]:443", "192.0.2.1", ""},
		{"fe80::1%en0", "fe80::1%en0", ""},
		{"[fe80::1%en0]", "fe80::1%en0", ""},
		{"[fe80::1%en0]:", "fe80::1%en0", ""},
		{"[fe80::1%en0]:abc", "fe80::1%en0", ""},
		{"http://[fe80::1%25en0]:/path", "fe80::1%en0", ""},
		{"http://[fe80::1%25en0]:443/path", "fe80::1%en0", ""},
		{"http://[fe80::1%25en0]:abc/path", "", "无法解析网址"},
		{"[example.com:443", "", "域名格式无效"},
		{"example.com]:443", "", "域名格式无效"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeDomain(tt.in)
			if tt.wantErr != "" {
				if got != "" || err == nil || err.Error() != tt.wantErr {
					t.Fatalf("NormalizeDomain(%q) = %q, %v; want error %q", tt.in, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeDomain(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
	t.Run("single-colon-fallback", func(t *testing.T) {
		// An extra ']' makes SplitHostPort fail, forcing the single-colon fallback.
		for _, input := range []string{
			"example.com:abc]",
			"example.com:]",
			"example.com:443]",
			"example.com]:",
			"example.com]:abc",
			"example.com]:443",
		} {
			t.Run(input, func(t *testing.T) {
				if strings.Count(input, ":") != 1 {
					t.Fatalf("fixture must contain exactly one colon: %q", input)
				}
				if _, _, err := net.SplitHostPort(input); err == nil {
					t.Fatalf("SplitHostPort(%q) unexpectedly succeeded", input)
				}
				got, err := NormalizeDomain(input)
				if got != "" || err == nil || err.Error() != "域名格式无效" {
					t.Fatalf("NormalizeDomain(%q) = %q, %v; want error %q", input, got, err, "域名格式无效")
				}
			})
		}
	})
}

func TestAllDigits(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", true},
		{"0123456789", true},
		{"443abc", false},
		{"-1", false},
		{"４４３", false},
	} {
		if got := allDigits(tt.in); got != tt.want {
			t.Errorf("allDigits(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeDomainsSplitsAndDeduplicates(t *testing.T) {
	input := []string{
		"baidu.com, qq.com\n# 注释\ntaobao.com",
		"https://jd.com/index.html",
		"baidu.com",
	}
	valid, invalid := NormalizeDomains(input)
	want := []string{"baidu.com", "qq.com", "taobao.com", "jd.com"}
	if !reflect.DeepEqual(valid, want) {
		t.Fatalf("得到 %v，期望 %v", valid, want)
	}
	if len(invalid) != 0 {
		t.Fatalf("意外无效项: %v", invalid)
	}
}

func TestLogRingSince(t *testing.T) {
	ring := newLogRing(3)
	for i := 1; i <= 5; i++ {
		ring.add(LogEntry{Seq: uint64(i)})
	}
	got, dropped := ring.since(4, 10)
	if dropped {
		t.Fatal("since(4) 不应标记截断")
	}
	if len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("since(4) = %+v", got)
	}
	got, dropped = ring.since(1, 10)
	if !dropped {
		t.Fatal("期望标记日志被截断")
	}
	if len(got) != 3 || got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("since(1) = %+v", got)
	}
	got, _ = ring.since(3, 10)
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("since(3) = %+v", got)
	}
	got, _ = ring.since(0, 2)
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("since(0) = %+v", got)
	}
}

func TestModePorts(t *testing.T) {
	if got := modePorts(Mode80); !reflect.DeepEqual(got, []int{80}) {
		t.Fatalf("80 模式: %v", got)
	}
	if got := modePorts(Mode443); !reflect.DeepEqual(got, []int{443}) {
		t.Fatalf("443 模式: %v", got)
	}
	if got := modePorts(ModeBoth); !reflect.DeepEqual(got, []int{80, 443}) {
		t.Fatalf("both 模式: %v", got)
	}
	if got := modePorts(Mode("bad")); got != nil {
		t.Fatalf("非法模式: %v", got)
	}
}

func TestNewRejectsInvalidRate(t *testing.T) {
	base := Config{
		Mode:        Mode443,
		Domains:     []string{"example.com"},
		DurationSec: 1,
		RatePerSec:  0,
	}
	if _, err := New(base); err == nil {
		t.Fatal("rate=0 应被拒绝")
	}
	base.RatePerSec = MaxRatePerSec + 1
	if _, err := New(base); err == nil {
		t.Fatal("过大的 rate 应被拒绝")
	}
}

func TestNewChecksNormalizedDomainCount(t *testing.T) {
	domains := make([]string, MaxDomains+1)
	for i := range domains {
		domains[i] = fmt.Sprintf("host-%d.example", i)
	}
	for _, separator := range []string{",", "\n"} {
		t.Run(fmt.Sprintf("separator=%q", separator), func(t *testing.T) {
			cfg := baseConfig()
			cfg.Domains = []string{strings.Join(domains, separator)}
			e, err := New(cfg)
			if err == nil {
				e.Stop("测试清理")
				e.Start()
				e.Wait()
				_ = e.Discard()
				t.Fatal("more than MaxDomains normalized domains were accepted")
			}
			if !strings.Contains(err.Error(), "5000") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	t.Run("boundary", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Domains = []string{strings.Join(domains[:MaxDomains], ",")}
		e := newTestEngine(t, cfg)
		if got := e.Snapshot().DomainCount; got != MaxDomains {
			t.Fatalf("DomainCount = %d, want %d", got, MaxDomains)
		}
	})
	t.Run("duplicates", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Domains = make([]string, MaxDomains+1)
		for i := range cfg.Domains {
			cfg.Domains[i] = "EXAMPLE.com"
		}
		e := newTestEngine(t, cfg)
		if got := e.Snapshot().DomainCount; got != 1 {
			t.Fatalf("DomainCount = %d, want 1", got)
		}
	})
}
