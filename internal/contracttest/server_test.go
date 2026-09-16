//go:build conformance

package contracttest

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func buildServer(t *testing.T, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "server")
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, out)
	}
	return bin
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func startServer(t *testing.T, bin string, env map[string]string) string {
	t.Helper()
	port := freePort(t)
	dir := t.TempDir()

	full := map[string]string{
		"PORT":              fmt.Sprintf("%d", port),
		"CACHE_DB_PATH":     filepath.Join(dir, "cache.db"),
		"STATS_DB_PATH":     filepath.Join(dir, "stats.db"),
		"CACHE_BACKUP_PATH": filepath.Join(dir, "backups"),
	}
	for k, v := range env {
		full[k] = v
	}

	envList := []string{}
	for _, k := range []string{"PATH", "HOME", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			envList = append(envList, k+"="+v)
		}
	}
	for k, v := range full {
		envList = append(envList, k+"="+v)
	}

	logs := &syncBuffer{}
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = envList
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server did not become healthy in time\nlogs:\n%s", logs.String())
	return ""
}

func generalProfileEnv() map[string]string {
	return map[string]string{
		"CACHE_ACCESS_TOKEN":            "test-admin-token",
		"API_KEY":                       "test-api-key",
		"API_KEY_REQUIRED":              "false",
		"TTML_STOREFRONT":               "us",
		"RATE_LIMIT_PER_SECOND":         "100000",
		"RATE_LIMIT_BURST_LIMIT":        "100000",
		"CACHED_RATE_LIMIT_PER_SECOND":  "100000",
		"CACHED_RATE_LIMIT_BURST_LIMIT": "100000",
	}
}
