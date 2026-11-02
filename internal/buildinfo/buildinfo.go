// Package buildinfo formats build-time version metadata without invoking Git.
package buildinfo

import (
	"encoding/hex"
	"regexp"
	"runtime/debug"
	"strings"
)

// These values are set together by the build helper through -ldflags.
var (
	injected  string
	branchHex string
	revision  string
	dirty     string
	release   string
)

var releasePattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// Metadata is the Git state captured at build time. An empty Branch means
// that no branch was known; "detached" explicitly denotes a detached HEAD.
type Metadata struct {
	Branch   string
	Revision string
	Dirty    bool
	Release  string
}

// ReleaseVersion accepts stable semantic-version tags and returns the numeric
// component suitable for CFBundleShortVersionString. Prereleases are not
// treated as official releases.
func ReleaseVersion(tag string) (string, bool) {
	match := releasePattern.FindStringSubmatch(tag)
	if match == nil {
		return "", false
	}
	return strings.Join(match[1:4], "."), true
}

// Label returns an official tag only for a clean, known revision.
func (m Metadata) Label() string {
	if revisionPattern.MatchString(m.Revision) && !m.Dirty {
		if _, ok := ReleaseVersion(m.Release); ok {
			return m.Release
		}
	}
	if !revisionPattern.MatchString(m.Revision) {
		return "开发构建未知提交"
	}
	branch := m.Branch
	if branch == "" {
		branch = "dev"
	}
	sha := strings.ToLower(m.Revision)
	if len(sha) > 7 {
		sha = sha[:7]
	}
	label := branch + "@" + sha
	if m.Dirty {
		label += "+dirty"
	}
	return label
}

// FromDebug uses Go's VCS settings only, never the module's pseudo-version as
// a release tag. The build record does not contain a branch or release tag.
func FromDebug(info *debug.BuildInfo) Metadata {
	var m Metadata
	if info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				m.Revision = setting.Value
			case "vcs.modified":
				m.Dirty = setting.Value == "true"
			}
		}
	}
	return m
}

// Label returns the linked build metadata, or Go's embedded VCS information
// when built directly without the helper. It never accesses the filesystem.
func Label() string {
	if injected == "1" {
		branch, err := hex.DecodeString(branchHex)
		if err == nil {
			return (Metadata{Branch: string(branch), Revision: revision, Dirty: dirty == "true", Release: release}).Label()
		}
	}
	info, _ := debug.ReadBuildInfo()
	return FromDebug(info).Label()
}
