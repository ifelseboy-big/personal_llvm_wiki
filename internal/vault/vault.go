package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"llm-wiki/internal/config"
	"llm-wiki/internal/fsutil"
)

type Lock struct {
	file *flock.Flock
}

var ErrLocked = errors.New("wiki is locked by another writer")

func AcquireWrite(cfg *config.Instance, timeout time.Duration) (*Lock, error) {
	path := filepath.Join(cfg.RuntimeDir(), "locks", "write.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f := flock.New(path)
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()
	ok, err := f.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrLocked
		}
		return nil, err
	}
	if !ok {
		return nil, ErrLocked
	}
	return &Lock{file: f}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Unlock()
}

func EnsureSafeManagedPaths(cfg *config.Instance) error {
	rootInfo, err := os.Lstat(cfg.Root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("wiki root must be a real directory: %s", cfg.Root)
	}
	for _, relative := range []string{
		cfg.Paths.Raw, cfg.Paths.Knowledge, cfg.Paths.Derived,
		cfg.Paths.Templates, cfg.Paths.Rules, cfg.Paths.Runtime,
	} {
		target := filepath.Join(cfg.Root, relative)
		if err := EnsureInside(cfg.Root, target); err != nil {
			return err
		}
		if err := rejectSymlinkComponents(cfg.Root, relative); err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil && !info.IsDir() {
			return fmt.Errorf("managed path must be a directory: %s", target)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func EnsureInside(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes wiki root: %s", target)
	}
	return nil
}

func rejectSymlinkComponents(root, relative string) error {
	return fsutil.EnsureNoSymlinkPath(root, filepath.Join(root, relative))
}

var sensitiveNames = map[string]bool{
	".env": true, "id_rsa": true, "id_ed25519": true,
	"credentials": true, "credentials.json": true,
	"secrets.json": true, "keychain.db": true, "credentials.db": true,
	".netrc": true, ".npmrc": true, ".pypirc": true, ".git-credentials": true,
	".zsh_history": true, ".bash_history": true, "cookies.sqlite": true,
	"login data": true, "key4.db": true, "auth.json": true,
}

func IsSensitiveFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	if sensitiveNames[name] {
		return true
	}
	if strings.HasPrefix(name, ".env.") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	for _, marker := range []string{"private_key", "access_token", "refresh_token", "api_key", "password", "secret"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}
