package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llm-wiki/internal/fsutil"
)

const (
	gitIgnoreFileName = ".gitignore"
	gitIgnoreMarker   = "# llm-wiki: local runtime and reproducible outputs"
)

var gitIgnorePatterns = []string{
	".DS_Store",
	"llm-wiki/",
	".llm-wiki/index.sqlite*",
	".llm-wiki/locks/",
	".llm-wiki/logs/",
	".llm-wiki/cache/",
	".llm-wiki/transactions/",
}

type gitIgnorePlan struct {
	path     string
	content  []byte
	previous []byte
	mode     os.FileMode
	existed  bool
	changed  bool
}

func planGitIgnore(root string) (*gitIgnorePlan, error) {
	path := filepath.Join(root, gitIgnoreFileName)
	if err := fsutil.EnsureNoSymlinkPath(root, path); err != nil {
		return nil, err
	}
	plan := &gitIgnorePlan{path: path, mode: 0o600}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		plan.content = mergeGitIgnore(nil)
		plan.changed = true
		return plan, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", gitIgnoreFileName)
	}
	plan.existed = true
	plan.mode = info.Mode().Perm()
	plan.previous, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	plan.content = mergeGitIgnore(plan.previous)
	plan.changed = !bytes.Equal(plan.content, plan.previous)
	return plan, nil
}

func mergeGitIgnore(existing []byte) []byte {
	present := make(map[string]bool, len(gitIgnorePatterns))
	markerPresent := false
	for _, line := range strings.Split(string(existing), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == gitIgnoreMarker {
			markerPresent = true
		}
		present[line] = true
	}
	missing := make([]string, 0, len(gitIgnorePatterns))
	for _, pattern := range gitIgnorePatterns {
		if !present[pattern] {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return append([]byte(nil), existing...)
	}

	var result bytes.Buffer
	result.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		result.WriteByte('\n')
	}
	if !markerPresent {
		if result.Len() > 0 && !bytes.HasSuffix(result.Bytes(), []byte("\n\n")) {
			result.WriteByte('\n')
		}
		result.WriteString(gitIgnoreMarker)
		result.WriteByte('\n')
	}
	for _, pattern := range missing {
		result.WriteString(pattern)
		result.WriteByte('\n')
	}
	return result.Bytes()
}

func (p *gitIgnorePlan) apply() error {
	if p == nil || !p.changed {
		return nil
	}
	return fsutil.AtomicWrite(p.path, p.content, p.mode)
}

func (p *gitIgnorePlan) rollback() error {
	if p == nil || !p.changed {
		return nil
	}
	if p.existed {
		return fsutil.AtomicWrite(p.path, p.previous, p.mode)
	}
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
