package main

import (
	"os"
	"testing"
)

// Existing UI and controller tests assert the original Chinese text.
// Individual localization tests can override this environment.
func TestMain(m *testing.M) {
	_ = os.Setenv("LC_ALL", "zh_CN.UTF-8")
	os.Exit(m.Run())
}
