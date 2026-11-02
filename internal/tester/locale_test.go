package tester

import (
	"os"
	"testing"
)

// Existing engine tests assert the original Chinese messages.
func TestMain(m *testing.M) {
	_ = os.Setenv("LC_ALL", "zh_CN.UTF-8")
	os.Exit(m.Run())
}
