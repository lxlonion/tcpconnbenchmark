package buildinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Metadata
		want string
	}{
		{"branch", Metadata{Branch: "feature/test", Revision: "01b0d351234"}, "feature/test@01b0d35"},
		{"release", Metadata{Branch: "master", Revision: "01b0d35", Release: "v1.2.3"}, "v1.2.3"},
		{"dirty release", Metadata{Branch: "master", Revision: "01b0d35", Dirty: true, Release: "v1.2.3"}, "master@01b0d35+dirty"},
		{"invalid tag", Metadata{Branch: "master", Revision: "01b0d35", Release: "release-test"}, "master@01b0d35"},
		{"prerelease", Metadata{Branch: "master", Revision: "01b0d35", Release: "v1.2.3-rc.1"}, "master@01b0d35"},
		{"detached", Metadata{Branch: "detached", Revision: "01b0d35"}, "detached@01b0d35"},
		{"unknown branch", Metadata{Revision: "01b0d35"}, "dev@01b0d35"},
		{"unknown commit", Metadata{Release: "v1.2.3"}, "开发构建未知提交"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.Label(); got != tc.want {
				t.Fatalf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		tag, numeric string
		ok           bool
	}{
		{"v1.2.3", "1.2.3", true}, {"1.2.3+build.1", "1.2.3", true},
		{"v01.2.3", "", false}, {"v1.2", "", false}, {"v1.2.3-rc.1", "", false},
		{"v0.0.0-20260101000000-abcdefabcdef", "", false}, {"release", "", false},
	} {
		got, ok := ReleaseVersion(tc.tag)
		if got != tc.numeric || ok != tc.ok {
			t.Errorf("ReleaseVersion(%q) = %q, %v", tc.tag, got, ok)
		}
	}
}

func TestDebugFallbackDoesNotUsePseudoVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260101000000-abcdefabcdef"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef0123456789"}, {Key: "vcs.modified", Value: "true"}}}
	if got := FromDebug(info).Label(); got != "dev@abcdef0+dirty" {
		t.Fatalf("fallback = %q", got)
	}
	if got := FromDebug(nil).Label(); got != "开发构建未知提交" {
		t.Fatalf("missing metadata = %q", got)
	}
}

func TestInjectedBinaryWorksAwayFromRepositoryWithoutGit(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not available")
	}
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "buildinfo")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("buildinfo.go")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		filepath.Join(pkg, "buildinfo.go"): source,
		filepath.Join(dir, "go.mod"):       []byte("module tcplink\n\ngo 1.27.3\n"),
		filepath.Join(dir, "main.go"):      []byte("package main\nimport (\"fmt\"; \"tcplink/internal/buildinfo\")\nfunc main(){fmt.Print(buildinfo.Label())}\n"),
	} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	name := "probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	cmd := exec.Command(goBin, "build", "-ldflags", "-X tcplink/internal/buildinfo.injected=1 -X tcplink/internal/buildinfo.branchHex=666561747572652f74657374 -X tcplink/internal/buildinfo.revision=01b0d35 -X tcplink/internal/buildinfo.dirty=true", "-o", binary, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	moved := filepath.Join(t.TempDir(), name)
	if err := os.Rename(binary, moved); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(moved)
	cmd.Dir = filepath.Dir(moved)
	cmd.Env = []string{"PATH="}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "feature/test@01b0d35+dirty" {
		t.Fatalf("relocated probe = %q, %v", out, err)
	}
}
