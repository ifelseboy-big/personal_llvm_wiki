package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunUpdatesExecutableAndVerifiesVersion(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("self-update is supported on macOS and Linux")
	}
	executable := writeVersionBinary(t, "0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`#!/bin/sh
set -eu
install_dir=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--install-dir" ]; then install_dir=$2; shift 2; else shift; fi
done
staged="$install_dir/.llm-wiki-test-new"
printf '#!/bin/sh\nprintf "llm-wiki version 0.0.2\\n"\n' > "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$install_dir/llm-wiki"
`))
	}))
	defer server.Close()

	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "updated" || result.PreviousVersion != "0.0.1" || result.CurrentVersion != "0.0.2" || result.Path != resolvedExecutable {
		t.Fatalf("unexpected result %#v", result)
	}
	if version, err := executableVersion(context.Background(), executable); err != nil || version != "0.0.2" {
		t.Fatalf("updated executable is invalid: version=%q err=%v", version, err)
	}
}

func TestDryRunDoesNotUseNetworkOrWrite(t *testing.T) {
	executable := writeVersionBinary(t, "0.0.1")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	before, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: server.URL, HTTPClient: server.Client(), DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "check" || !result.DryRun || requests.Load() != 0 || string(before) != string(after) {
		t.Fatalf("dry-run changed state: result=%#v requests=%d", result, requests.Load())
	}
}

func TestDownloadFailurePreservesExecutable(t *testing.T) {
	executable := writeVersionBinary(t, "0.0.1")
	before, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	_, err = Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: server.URL, HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("expected download error, got %v", err)
	}
	after, readErr := os.ReadFile(executable)
	if readErr != nil || string(before) != string(after) {
		t.Fatalf("download failure changed executable: err=%v", readErr)
	}
}

func TestRejectsNonShellInstaller(t *testing.T) {
	executable := writeVersionBinary(t, "0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not an installer</html>"))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: server.URL, HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrDownload) || !strings.Contains(err.Error(), "not the llm-wiki shell installer") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestProductionClientRequiresHTTPS(t *testing.T) {
	executable := writeVersionBinary(t, "0.0.1")
	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: "http://example.invalid/install.sh",
	})
	if !errors.Is(err, ErrDownload) || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestApplyFailurePreservesExecutable(t *testing.T) {
	executable := writeVersionBinary(t, "0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\nprintf 'build failed' >&2\nexit 7\n"))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		CurrentVersion: "0.0.1", ExecutablePath: executable,
		InstallerURL: server.URL, HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrApply) || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("unexpected error %v", err)
	}
	if version, versionErr := executableVersion(context.Background(), executable); versionErr != nil || version != "0.0.1" {
		t.Fatalf("failed update changed executable: version=%q err=%v", version, versionErr)
	}
}

func writeVersionBinary(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "llm-wiki")
	data := []byte("#!/bin/sh\nprintf 'llm-wiki version " + version + "\\n'\n")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
