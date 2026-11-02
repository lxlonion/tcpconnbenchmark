// Package i18n provides translations for user-facing text.
package i18n

import (
	"os"
	"strings"
	"sync"
)

var (
	mu           sync.RWMutex
	translations = map[string]string{}
)

// Register adds or replaces translations. It is safe to call concurrently with T.
func Register(entries map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for source, translated := range entries {
		translations[source] = translated
	}
}

// T returns the English translation for an English locale, or the original text.
func T(s string) string {
	locale := os.Getenv("LC_ALL")
	if locale == "" {
		locale = os.Getenv("LC_MESSAGES")
	}
	if locale == "" {
		locale = os.Getenv("LANG")
	}
	locale = strings.ToLower(locale)
	if locale != "en" && !strings.HasPrefix(locale, "en_") && !strings.HasPrefix(locale, "en-") && !strings.HasPrefix(locale, "en.") && !strings.HasPrefix(locale, "en@") {
		return s
	}
	mu.RLock()
	defer mu.RUnlock()
	if translated, ok := translations[s]; ok {
		return translated
	}
	return s
}
