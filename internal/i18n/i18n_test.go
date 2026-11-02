package i18n

import (
	"fmt"
	"testing"
)

func TestLocaleAndTranslation(t *testing.T) {
	Register(map[string]string{"测试 %d": "Test %d"})
	for _, tt := range []struct{ all, messages, lang, want string }{
		{"", "", "", "测试 %d"},
		{"", "", "en_US.UTF-8", "Test %d"},
		{"", "en-GB", "zh_CN", "Test %d"},
		{"zh_CN", "en_US", "en_US", "测试 %d"},
		{"C", "en_US", "en_US", "测试 %d"},
		{"english", "", "", "测试 %d"},
	} {
		t.Run(tt.all+"/"+tt.messages+"/"+tt.lang, func(t *testing.T) {
			t.Setenv("LC_ALL", tt.all)
			t.Setenv("LC_MESSAGES", tt.messages)
			t.Setenv("LANG", tt.lang)
			if got := T("测试 %d"); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if got := T("unknown"); got != "unknown" {
				t.Fatalf("unknown = %q", got)
			}
		})
	}
}

func TestGUITranslationFormatting(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	got := fmt.Sprintf(T("已保存并应用 %d 个域名，忽略 %d 个无效项"), 12, 3)
	if want := "Saved and applied 12 domains; ignored 3 invalid entries"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
