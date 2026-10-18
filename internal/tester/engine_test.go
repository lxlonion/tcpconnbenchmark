package tester

import (
	"reflect"
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
	seen := map[string]bool{}
	for _, domain := range valid {
		if seen[domain] {
			t.Fatalf("内置域名重复: %s", domain)
		}
		seen[domain] = true
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
		{"2001:db8::1", "", true},
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
