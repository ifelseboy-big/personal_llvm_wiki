package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	InstallerURL      = "https://raw.githubusercontent.com/ifelseboy-big/personal_llvm_wiki/main/install.sh"
	maxInstallerBytes = 512 << 10
	maxCommandOutput  = 8 << 10
)

var (
	ErrUnsupported = errors.New("self-update is unsupported")
	ErrDownload    = errors.New("installer download failed")
	ErrApply       = errors.New("installer execution failed")
)

type Options struct {
	CurrentVersion string
	ExecutablePath string
	InstallerURL   string
	HTTPClient     *http.Client
	DryRun         bool
}

type Result struct {
	Action          string `json:"action"`
	Path            string `json:"path"`
	PreviousVersion string `json:"previous_version"`
	CurrentVersion  string `json:"current_version"`
	DryRun          bool   `json:"dry_run"`
}

func Run(ctx context.Context, opts Options) (*Result, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("%w on %s", ErrUnsupported, runtime.GOOS)
	}
	executable, err := resolveExecutable(opts.ExecutablePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	result := &Result{
		Action: "check", Path: executable, PreviousVersion: opts.CurrentVersion,
		CurrentVersion: opts.CurrentVersion, DryRun: opts.DryRun,
	}
	if opts.DryRun {
		return result, nil
	}
	if filepath.Base(executable) != "llm-wiki" {
		return nil, fmt.Errorf("%w: executable must be named llm-wiki", ErrUnsupported)
	}

	installerURL := opts.InstallerURL
	if installerURL == "" {
		installerURL = InstallerURL
	}
	client := opts.HTTPClient
	if client == nil {
		if !strings.HasPrefix(installerURL, "https://") {
			return nil, fmt.Errorf("%w: installer URL must use HTTPS", ErrDownload)
		}
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL.Scheme != "https" {
					return errors.New("installer redirect must use HTTPS")
				}
				return nil
			},
		}
	}
	installer, err := downloadInstaller(ctx, client, installerURL)
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "llm-wiki-update.*")
	if err != nil {
		return nil, fmt.Errorf("%w: create temporary directory: %v", ErrApply, err)
	}
	defer os.RemoveAll(tempDir)
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: secure temporary directory: %v", ErrApply, err)
	}
	installerPath := filepath.Join(tempDir, "install.sh")
	if err := os.WriteFile(installerPath, installer, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write temporary installer: %v", ErrApply, err)
	}

	var output limitedBuffer
	output.limit = maxCommandOutput
	cmd := exec.CommandContext(ctx, "/bin/sh", installerPath, "--install-dir", filepath.Dir(executable), "--quiet")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%w: %s", ErrApply, message)
	}

	installedVersion, err := executableVersion(ctx, executable)
	if err != nil {
		return nil, fmt.Errorf("%w: verify installed binary: %v", ErrApply, err)
	}
	result.CurrentVersion = installedVersion
	if installedVersion == opts.CurrentVersion {
		result.Action = "current"
	} else {
		result.Action = "updated"
	}
	return result, nil
}

func resolveExecutable(configured string) (string, error) {
	path := configured
	var err error
	if path == "" {
		path, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("executable is not a regular file")
	}
	return path, nil
}

func downloadInstaller(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%w: HTTP %d", ErrDownload, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInstallerBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	if len(data) == 0 || len(data) > maxInstallerBytes {
		return nil, fmt.Errorf("%w: installer size is invalid", ErrDownload)
	}
	if !bytes.HasPrefix(data, []byte("#!/bin/sh\n")) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("%w: response is not the llm-wiki shell installer", ErrDownload)
	}
	return data, nil
}

func executableVersion(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(output))
	const prefix = "llm-wiki version "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("unexpected version output %q", line)
	}
	version := strings.TrimPrefix(line, prefix)
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected version %q", version)
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return "", fmt.Errorf("unexpected version %q", version)
		}
	}
	return version, nil
}

type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.data = append(b.data, data...)
	}
	return written, nil
}

func (b *limitedBuffer) String() string { return string(b.data) }
