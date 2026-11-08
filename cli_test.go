package main

import (
	"fmt"
	"strings"
	"testing"

	"tcpconnbenchmark/internal/tester"
)

type cliEngineStub struct {
	*fakeEngine
	logError    string
	exportCalls int
}

func (e *cliEngineStub) Start() {
	e.fakeEngine.Start()
	e.fakeEngine.Stop("finished")
}

func (e *cliEngineStub) Snapshot() tester.Snapshot {
	snap := e.fakeEngine.Snapshot()
	snap.LogError = e.logError
	return snap
}

func (e *cliEngineStub) Export(dir string) (tester.ExportResult, error) {
	e.exportCalls++
	return e.fakeEngine.Export(dir)
}

func TestCLILogFailureReturnsError(t *testing.T) {
	for _, locale := range []string{"zh_CN.UTF-8", "en_US.UTF-8"} {
		for _, save := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/save=%t", locale, save), func(t *testing.T) {
				t.Setenv("LC_ALL", locale)
				e := &cliEngineStub{fakeEngine: newFakeEngine("cli"), logError: "journal write failed"}
				err := runCLIEngine(e, save)
				if err == nil || !strings.Contains(err.Error(), e.logError) {
					t.Fatalf("log failure returned %v", err)
				}
				if locale == "en_US.UTF-8" && !strings.HasPrefix(err.Error(), "Log error: ") {
					t.Fatalf("untranslated CLI error: %v", err)
				}
				if e.exportCalls != 0 || !e.discarded {
					t.Fatalf("exports=%d discarded=%v", e.exportCalls, e.discarded)
				}
			})
		}
	}
}

func TestCLISuccessDiscardsJournal(t *testing.T) {
	for _, save := range []bool{false, true} {
		e := &cliEngineStub{fakeEngine: newFakeEngine("cli")}
		if err := runCLIEngine(e, save); err != nil {
			t.Fatal(err)
		}
		wantExports := 0
		if save {
			wantExports = 1
		}
		if e.exportCalls != wantExports || !e.discarded {
			t.Fatalf("save=%v exports=%d discarded=%v", save, e.exportCalls, e.discarded)
		}
	}
}

func TestCLIRejectsInvalidConfig(t *testing.T) {
	if err := runCLI(tester.Config{}, false); err == nil {
		t.Fatal("invalid CLI configuration was accepted")
	}
}
