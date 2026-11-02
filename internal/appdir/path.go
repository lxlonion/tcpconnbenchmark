// Package appdir locates the directory beside the application executable.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Directory returns the application directory using the resolved executable
// path. It does not create the directory or any application data.
func Directory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return FromExecutable(resolved)
}

// FromExecutable computes the application directory from an absolute
// executable path without accessing the filesystem. For an executable in an
// .app/Contents/MacOS directory, it returns the parent of the .app bundle.
func FromExecutable(executable string) (string, error) {
	if executable == "" || strings.IndexByte(executable, 0) >= 0 || !filepath.IsAbs(executable) || os.IsPathSeparator(executable[len(executable)-1]) {
		return "", fmt.Errorf("invalid executable path %q", executable)
	}
	executable = filepath.Clean(executable)
	dir := filepath.Dir(executable)
	if dir == executable {
		return "", fmt.Errorf("invalid executable path %q", executable)
	}
	contents := filepath.Dir(dir)
	bundle := filepath.Dir(contents)
	if filepath.Base(dir) == "MacOS" && filepath.Base(contents) == "Contents" && strings.HasSuffix(filepath.Base(bundle), ".app") && filepath.Base(bundle) != ".app" {
		return filepath.Dir(bundle), nil
	}
	return dir, nil
}
