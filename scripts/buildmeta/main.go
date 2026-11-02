// Command buildmeta captures Git metadata once for a build and its app bundle.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"tcplink/internal/buildinfo"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func collect(dir string) buildinfo.Metadata {
	var m buildinfo.Metadata
	sha, err := git(dir, "rev-parse", "--verify", "--short=7", "HEAD")
	if err != nil {
		return m
	}
	m.Revision = sha
	m.Branch, err = git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || m.Branch == "" {
		m.Branch = "detached"
	}
	status, err := git(dir, "status", "--porcelain", "--untracked-files=normal")
	// An unknown worktree state must never be represented as a release.
	m.Dirty = err != nil || status != ""
	if !m.Dirty {
		tags, err := git(dir, "tag", "--points-at", "HEAD")
		if err == nil {
			for _, tag := range strings.Split(tags, "\n") {
				if _, ok := buildinfo.ReleaseVersion(tag); ok {
					m.Release = tag
					break
				}
			}
		}
	}
	return m
}

func linkerFlags(base string, m buildinfo.Metadata) string {
	if m.Revision == "" {
		return base
	}
	return strings.TrimSpace(base + " -X tcplink/internal/buildinfo.injected=1" +
		" -X tcplink/internal/buildinfo.branchHex=" + hex.EncodeToString([]byte(m.Branch)) +
		" -X tcplink/internal/buildinfo.revision=" + m.Revision +
		fmt.Sprintf(" -X tcplink/internal/buildinfo.dirty=%t", m.Dirty) +
		" -X tcplink/internal/buildinfo.release=" + m.Release)
}

func build(output string, m buildinfo.Metadata, goflags []string, targetOS, targetArch string) error {
	args := append([]string{"build"}, goflags...)
	args = append(args, "-trimpath")
	if m.Revision == "" {
		// Git may be absent even when a .git directory is present.
		args = append(args, "-buildvcs=false")
	}
	args = append(args, "-ldflags", linkerFlags(os.Getenv("TCPLINK_BUILD_LDFLAGS"), m), "-o", output, ".")
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOOS="+targetOS, "GOARCH="+targetArch)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func plist(template []byte, m buildinfo.Metadata) ([]byte, error) {
	if bytes.Count(template, []byte("__BUILD_LABEL__")) != 1 || bytes.Count(template, []byte("<!-- RELEASE_VERSION -->")) != 1 {
		return nil, errors.New("Info.plist 模板缺少版本标记")
	}
	var label bytes.Buffer
	if err := xml.EscapeText(&label, []byte(m.Label())); err != nil {
		return nil, err
	}
	out := bytes.Replace(template, []byte("__BUILD_LABEL__"), label.Bytes(), 1)
	var version string
	if m.Label() == m.Release && m.Release != "" {
		if numeric, ok := buildinfo.ReleaseVersion(m.Release); ok {
			version = "<key>CFBundleShortVersionString</key>\n\t<string>" + numeric + "</string>"
		}
	}
	return bytes.Replace(out, []byte("<!-- RELEASE_VERSION -->"), []byte(version), 1), nil
}

func bundle(binary, app string, m buildinfo.Metadata) error {
	if runtime.GOOS != "darwin" {
		return errors.New("make app 仅支持 macOS")
	}
	content, err := os.ReadFile("packaging/Info.plist")
	if err != nil {
		return err
	}
	content, err = plist(content, m)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(app); err != nil {
		return err
	}
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), content, 0o644); err != nil {
		return err
	}
	input, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(filepath.Join(macos, "TCPLink"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return err
	}
	// Match the existing best-effort ad-hoc signing behaviour.
	_ = exec.Command("codesign", "--force", "--deep", "--sign", "-", app).Run()
	fmt.Printf("已生成 %s，运行: open %s\n", app, app)
	return nil
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: buildmeta build|app|run")
	}
	mode := os.Args[1]
	if mode != "build" && mode != "app" && mode != "run" {
		return errors.New("usage: buildmeta build|app|run")
	}
	targetOS, targetArch := os.Getenv("TCPLINK_TARGET_GOOS"), os.Getenv("TCPLINK_TARGET_GOARCH")
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	if targetArch == "" {
		targetArch = runtime.GOARCH
	}
	if mode == "app" && (runtime.GOOS != "darwin" || targetOS != "darwin") {
		return errors.New("make app 仅支持 macOS")
	}
	if mode == "run" && (targetOS != runtime.GOOS || targetArch != runtime.GOARCH) {
		return fmt.Errorf("make run 无法运行非本机目标 %s/%s（本机 %s/%s）", targetOS, targetArch, runtime.GOOS, runtime.GOARCH)
	}
	m := collect(".")
	binary := os.Getenv("TCPLINK_BUILD_BINARY")
	if binary == "" {
		binary = "TCPLink"
	}
	if err := build(binary, m, os.Args[2:], targetOS, targetArch); err != nil {
		return err
	}
	if mode == "app" {
		app := os.Getenv("TCPLINK_BUILD_APP")
		if app == "" {
			app = "TCPLink.app"
		}
		return bundle(binary, app, m)
	}
	if mode == "run" {
		path, err := filepath.Abs(binary)
		if err != nil {
			return fmt.Errorf("解析可执行文件路径失败: %w", err)
		}
		cmd := exec.Command(path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
