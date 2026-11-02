package appdir

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestFromExecutable(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "some", "test dir")
	if volume := filepath.VolumeName(os.TempDir()); volume != "" {
		root = volume + root
	}
	tests := []struct {
		name, executable, want string
	}{
		{"binary", filepath.Join(root, "bin", "TCPLink"), filepath.Join(root, "bin")},
		{"spaces", filepath.Join(root, "with spaces", "TCP Link"), filepath.Join(root, "with spaces")},
		{"app", filepath.Join(root, "TCPLink.app", "Contents", "MacOS", "TCPLink"), root},
		{"app with spaces", filepath.Join(root, "My TCPLink.app", "Contents", "MacOS", "TCP Link"), root},
		{"not an app", filepath.Join(root, "TCPLink", "Contents", "MacOS", "TCPLink"), filepath.Join(root, "TCPLink", "Contents", "MacOS")},
		{"missing contents", filepath.Join(root, "TCPLink.app", "Other", "MacOS", "TCPLink"), filepath.Join(root, "TCPLink.app", "Other", "MacOS")},
		{"missing macos", filepath.Join(root, "TCPLink.app", "Contents", "Other", "TCPLink"), filepath.Join(root, "TCPLink.app", "Contents", "Other")},
		{"app suffix required", filepath.Join(root, "TCPLink.app.backup", "Contents", "MacOS", "TCPLink"), filepath.Join(root, "TCPLink.app.backup", "Contents", "MacOS")},
		{"clean path", filepath.Join(root, "folder", "..", "TCPLink"), root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FromExecutable(tt.executable)
			if err != nil || got != tt.want {
				t.Fatalf("FromExecutable(%q) = %q, %v; want %q", tt.executable, got, err, tt.want)
			}
		})
	}
}

func TestFromExecutableRejectsInvalidPaths(t *testing.T) {
	for _, path := range []string{"", "TCPLink", filepath.Join("relative", "TCPLink"), string(filepath.Separator), string(filepath.Separator) + "bad\x00path", string(filepath.Separator) + "directory" + string(filepath.Separator)} {
		if got, err := FromExecutable(path); err == nil {
			t.Errorf("FromExecutable(%q) = %q, want error", path, got)
		}
	}
}

func TestDirectoryDoesNotCreateData(t *testing.T) {
	const helperEnv = "TCPLINK_APPDIR_TEST_HELPER"
	if expected := os.Getenv(helperEnv); expected != "" {
		got, err := Directory()
		if err != nil || !filepath.IsAbs(got) || got != expected {
			t.Fatalf("Directory() = %q, %v; want absolute %q", got, err, expected)
		}
		return
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	copyPath := filepath.Join(dir, "TCPLink-test"+filepath.Ext(executable))
	dst, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(copyPath, "-test.run=^TestDirectoryDoesNotCreateData$")
	cmd.Env = append(os.Environ(), helperEnv+"="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, output)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "TCPLink-link")
		if err := os.Symlink(copyPath, link); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command(link, "-test.run=^TestDirectoryDoesNotCreateData$")
		cmd.Env = append(os.Environ(), helperEnv+"="+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("symlink helper: %v\n%s", err, output)
		}
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := func(entries []os.DirEntry) []string {
		var result []string
		for _, entry := range entries {
			result = append(result, entry.Name())
		}
		return result
	}
	if !reflect.DeepEqual(names(before), names(after)) {
		t.Fatalf("Directory created data: before %v, after %v", names(before), names(after))
	}
}
