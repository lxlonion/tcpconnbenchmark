package tester

import (
	"strings"
	"testing"
	"time"
)

func TestLocalizedMessagesAndSummary(t *testing.T) {
	for _, tc := range []struct {
		locale, domainError, rateError, summaryHeading, successRate string
	}{
		{"zh_CN.UTF-8", "域名为空", "发起速率需在 1 到 1000 次/秒之间", "TCP 并发连接测试", "成功率: —"},
		{"en_US.UTF-8", "Domain is empty", "Launch rate must be between 1 and 1000 per second", "Concurrent TCP Connection Test", "Success rate: —"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.locale)
			if _, err := NormalizeDomain(""); err == nil || err.Error() != tc.domainError {
				t.Fatalf("NormalizeDomain error = %v, want %q", err, tc.domainError)
			}
			if _, err := New(Config{Mode: Mode80, DurationSec: 1}); err == nil || err.Error() != tc.rateError {
				t.Fatalf("New error = %v, want %q", err, tc.rateError)
			}
			l := resultLogger{createdAt: time.Unix(0, 0), config: Config{Mode: Mode80}}
			summary := l.summary(Snapshot{}, ExportResult{})
			if !strings.Contains(summary, tc.summaryHeading) || !strings.Contains(summary, tc.successRate) || strings.Contains(summary, "%!") {
				t.Fatalf("unexpected summary: %s", summary)
			}
		})
	}
}
