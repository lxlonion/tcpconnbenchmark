package main

import (
	"bytes"
	"debug/elf"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tcpconnbenchmark/internal/buildinfo"
)

func TestCollectTemporaryRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "feature/test")
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "first")
	first := collect(dir)
	if first.Branch != "feature/test" || first.Revision == "" || first.Dirty || first.Release != "" || first.Label() != "feature/test@"+first.Revision[:7] {
		t.Fatalf("no tag: %+v (%s)", first, first.Label())
	}
	run("tag", "release-not-semver")
	if got := collect(dir); got.Release != "" {
		t.Fatalf("non-version tag: %+v", got)
	}
	run("tag", "v1.2.3")
	if got := collect(dir); got.Label() != "v1.2.3" {
		t.Fatalf("exact tag: %+v", got)
	}
	if err := os.WriteFile(file, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := collect(dir); !got.Dirty || got.Label() != "feature/test@"+first.Revision[:7]+"+dirty" {
		t.Fatalf("dirty tag: %+v", got)
	}
	run("add", "file")
	run("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "second")
	if got := collect(dir); got.Release != "" || got.Dirty || !strings.HasPrefix(got.Label(), "feature/test@") {
		t.Fatalf("after tag: %+v", got)
	}
	run("checkout", "--detach", "-q")
	if got := collect(dir); got.Branch != "detached" || !strings.HasPrefix(got.Label(), "detached@") {
		t.Fatalf("detached: %+v", got)
	}
	run("switch", "-qc", "feature/a'$b")
	if got := collect(dir); got.Branch != "feature/a'$b" || strings.Contains(linkerFlags("", got), got.Branch) {
		t.Fatalf("special branch was not encoded safely: %+v", got)
	}
	if got := collect(t.TempDir()); got.Label() != "开发构建未知提交" {
		t.Fatalf("no repository: %+v", got)
	}
}

func TestPlistAndSafeBranchFlags(t *testing.T) {
	template, err := os.ReadFile("../../packaging/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	dev := buildinfo.Metadata{Branch: "feature/a&b", Revision: "0123456", Dirty: true}
	data, err := plist(template, dev)
	if err != nil || strings.Contains(string(data), "CFBundleShortVersionString") || !strings.Contains(string(data), "feature/a&amp;b@0123456+dirty") || strings.Contains(string(data), "1.0.0") {
		t.Fatalf("development plist: %s (%v)", data, err)
	}
	flags := linkerFlags("-s -w", dev)
	if strings.Contains(flags, dev.Branch) || !strings.Contains(flags, "tcpconnbenchmark/internal/buildinfo.branchHex=666561747572652f612662") || !strings.HasPrefix(flags, "-s -w ") {
		t.Fatalf("unsafe linker flags: %q", flags)
	}
	for _, want := range []string{"<string>tcpconnbenchmark</string>", "<string>app.tcpconnbenchmark.desktop</string>", "<key>tcpconnbenchmarkBuildLabel</key>", "<key>CFBundleIconFile</key>\n\t<string>tcpconn.icns</string>"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("development plist missing %q", want)
		}
	}
	data, err = plist(template, buildinfo.Metadata{Revision: "0123456", Release: "v1.2.3+build.1"})
	if err != nil || !strings.Contains(string(data), "<key>CFBundleShortVersionString</key>\n\t<string>1.2.3</string>") || !strings.Contains(string(data), "v1.2.3+build.1") {
		t.Fatalf("release plist: %s (%v)", data, err)
	}
}

// makeFixture uses a tiny local app so these tests never overwrite the user's
// binary or exercise the network or graphical interface.
func makeFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
	dir := t.TempDir()
	for _, path := range []string{"scripts/buildmeta", "internal/buildinfo", "portable", "packaging"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for src, dst := range map[string]string{
		"../../Makefile":                        "Makefile",
		"main.go":                               "scripts/buildmeta/main.go",
		"../../internal/buildinfo/buildinfo.go": "internal/buildinfo/buildinfo.go",
		"../../packaging/Info.plist":            "packaging/Info.plist",
		"../../packaging/tcpconn.icns":          "packaging/tcpconn.icns",
	} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := `package main
import ("os"; "path/filepath")
func main() {
  exe, err := os.Executable(); if err != nil { panic(err) }
  dir := filepath.Dir(exe)
  if err := os.MkdirAll(filepath.Join(dir, "logs"), 0755); err != nil { panic(err) }
  if err := os.WriteFile(filepath.Join(dir, "logs", "marker"), []byte("saved"), 0644); err != nil { panic(err) }
  if err := os.WriteFile(filepath.Join(dir, "domains.json"), []byte("saved"), 0644); err != nil { panic(err) }
}`
	for name, data := range map[string]string{"go.mod": "module tcpconnbenchmark\n\ngo 1.27.3\n", "main.go": app} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runMake(dir, targetOS, targetArch string, args ...string) ([]byte, error) {
	cmd := exec.Command("make", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off", "GOOS="+targetOS, "GOARCH="+targetArch, "GOFLAGS=")
	return cmd.CombinedOutput()
}

func TestMakeDefaultArtifacts(t *testing.T) {
	dir := makeFixture(t)
	out, err := runMake(dir, runtime.GOOS, runtime.GOARCH, "build")
	if err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tcpconnbenchmark")); err != nil {
		t.Fatalf("default binary: %v", err)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	out, err = runMake(dir, runtime.GOOS, runtime.GOARCH, "app")
	if err != nil {
		t.Fatalf("make app: %v\n%s", err, out)
	}
	for _, path := range []string{"tcpconnbenchmark.app/Contents/Info.plist", "tcpconnbenchmark.app/Contents/MacOS/tcpconnbenchmark", "tcpconnbenchmark.app/Contents/Resources/tcpconn.icns"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("default app artifact %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(filepath.Join(dir, "packaging/tcpconn.icns"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "tcpconnbenchmark.app/Contents/Resources/tcpconn.icns"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("bundled icon differs from source: %v", err)
	}
	if out, err := exec.Command("codesign", "--verify", "--strict", filepath.Join(dir, "tcpconnbenchmark.app")).CombinedOutput(); err != nil {
		t.Fatalf("app signature: %v\n%s", err, out)
	}
}

func TestMakeAppMissingIconPreservesExistingApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("app bundles require macOS")
	}
	dir := makeFixture(t)
	marker := filepath.Join(dir, "tcpconnbenchmark.app", "marker")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "packaging/tcpconn.icns")); err != nil {
		t.Fatal(err)
	}
	out, err := runMake(dir, runtime.GOOS, runtime.GOARCH, "app")
	if err == nil || !strings.Contains(string(out), "读取应用图标失败") {
		t.Fatalf("missing icon: %v\n%s", err, out)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "existing" {
		t.Fatalf("existing app was not preserved: %q (%v)", data, err)
	}
}

func TestMakeRunKeepsPortableData(t *testing.T) {
	dir := makeFixture(t)
	name := "probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join("portable", name)
	out, err := runMake(dir, runtime.GOOS, runtime.GOARCH, "run", "BINARY="+binary, "GOFLAGS=-mod=readonly", "LDFLAGS=-w")
	if err != nil {
		t.Fatalf("make run: %v\n%s", err, out)
	}
	for _, path := range []string{binary, filepath.Join("portable", "logs", "marker"), filepath.Join("portable", "domains.json")} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("make run removed %s: %v", path, err)
		}
	}
}

func TestMakeBuildCrossCompilesWithoutRunningTarget(t *testing.T) {
	dir := makeFixture(t)
	targetOS, targetArch, binary := "windows", "amd64", "cross.exe"
	if runtime.GOOS == "windows" {
		targetOS, binary = "linux", "cross-linux"
	}
	out, err := runMake(dir, targetOS, targetArch, "build", "BINARY="+binary, "GOFLAGS=-mod=readonly", "LDFLAGS=-w")
	if err != nil {
		t.Fatalf("cross make build: %v\n%s", err, out)
	}
	path := filepath.Join(dir, binary)
	if targetOS == "windows" {
		file, err := pe.Open(path)
		if err != nil {
			t.Fatalf("not PE: %v", err)
		}
		defer file.Close()
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Fatalf("PE machine = %x", file.Machine)
		}
	} else {
		file, err := elf.Open(path)
		if err != nil {
			t.Fatalf("not ELF: %v", err)
		}
		defer file.Close()
		if file.Machine != elf.EM_X86_64 {
			t.Fatalf("ELF machine = %v", file.Machine)
		}
	}
	out, err = runMake(dir, targetOS, targetArch, "run", "BINARY=must-not-run", "GOFLAGS=-mod=readonly")
	if err == nil || !strings.Contains(string(out), "非本机目标") {
		t.Fatalf("foreign make run accepted: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "must-not-run")); !os.IsNotExist(err) {
		t.Fatalf("foreign make run built a binary: %v", err)
	}
	out, err = runMake(dir, targetOS, targetArch, "app", "GOFLAGS=-mod=readonly")
	if err == nil || !strings.Contains(string(out), "仅支持 macOS") {
		t.Fatalf("foreign make app accepted: %v\n%s", err, out)
	}
}
