package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// integrationTestGarnetPath returns the path to the GarnetServer executable when
// integration tests are enabled (GARNET_INTEGRATION=1) and a binary is available.
func integrationTestGarnetPath(t *testing.T) (string, bool) {
	t.Helper()
	if os.Getenv("GARNET_INTEGRATION") != "1" {
		t.Skip("set GARNET_INTEGRATION=1 to run integration tests against a real Garnet")
		return "", false
	}
	candidates := []string{
		os.Getenv("GARNET_BINARY"),
		filepath.Join(os.TempDir(), "opencode", "garnet-test", "garnet", "net8.0", "GarnetServer.exe"),
		filepath.Join(os.TempDir(), "opencode", "garnet-test", "garnet", "net10.0", "GarnetServer.exe"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	t.Skip("GarnetServer binary not found; set GARNET_BINARY or download the portable Garnet release")
	return "", false
}

// garnetInstance wraps a running Garnet process and its address.
type garnetInstance struct {
	cmd  *exec.Cmd
	addr string
}

// startGarnet launches a Garnet server on a free port and waits until it accepts
// TCP connections. It fails the test if Garnet cannot be started.
func startGarnet(t *testing.T, binary string) *garnetInstance {
	t.Helper()

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	logDir := t.TempDir()
	stdoutFile, err := os.Create(filepath.Join(logDir, "garnet.stdout"))
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	stderrFile, err := os.Create(filepath.Join(logDir, "garnet.stderr"))
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}

	cmd := exec.Command(binary,
		"--port", fmt.Sprintf("%d", port),
		"--bind", "127.0.0.1",
		"--no-obj",
		"--no-pubsub",
	)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start Garnet: %v", err)
	}

	gi := &garnetInstance{cmd: cmd, addr: addr}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	})

	// Wait for Garnet to accept connections (max ~10s).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return gi
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			stderr, _ := os.ReadFile(stderrFile.Name())
			t.Fatalf("Garnet exited prematurely. stderr: %s", string(stderr))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Garnet did not become ready on %s", addr)
	return nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// integrationEnv holds the wired server, its base URL, and a direct redis client.
type integrationEnv struct {
	baseURL string
	rdb     *redis.Client
}

// newIntegrationEnv starts a fresh Garnet + fiber app per test and flushes the DB.
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	binary, ok := integrationTestGarnetPath(t)
	if !ok {
		t.Fatal("garnet binary unavailable")
	}
	gi := startGarnet(t, binary)

	rdb := redis.NewClient(&redis.Options{Addr: gi.addr, DialTimeout: 2 * time.Second})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flushdb: %v", err)
	}

	port := freePort(t)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cfg := config{
		Port:            fmt.Sprintf("%d", port),
		GarnetAddr:      gi.addr,
		RequestTTL:      2 * time.Second,
		LRUIdleTTL:      500 * time.Millisecond,
		RedisPoolSize:   4,
		HTTPConcurrency: 1024,
	}
	srv := &server{cfg: cfg, db: rdb, log: slog.Default()}
	app := newApp(srv)
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })
	go func() {
		if err := app.Listener(ln); err != nil {
			t.Errorf("fiber listener: %v", err)
		}
	}()

	return &integrationEnv{
		baseURL: "http://" + ln.Addr().String(),
		rdb:     rdb,
	}
}

// --- HTTP helpers ---

func (e *integrationEnv) do(t *testing.T, method, path string, body any) (status int, payload []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.baseURL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, payload
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %T: %v (body=%s)", v, err, string(b))
	}
	return v
}

func valPtr(s string) *string { return &s }
func intPtr(i int64) *int64   { return &i }

// ============================================================
// Tests
// ============================================================

func TestIntegration_Health(t *testing.T) {
	e := newIntegrationEnv(t)
	status, body := e.do(t, http.MethodGet, "/healthz", nil)
	if status != 200 {
		t.Fatalf("health status = %d, want 200 (body=%s)", status, string(body))
	}
	r := decode[healthResponse](t, body)
	if r.Status != "ok" || r.Garnet != "pong" {
		t.Fatalf("health response = %+v, want ok/pong", r)
	}
}

func TestIntegration_SetGet(t *testing.T) {
	e := newIntegrationEnv(t)

	// Set
	status, body := e.do(t, http.MethodPut, "/keys/test-key", setRequest{Value: valPtr("hello"), TTLSeconds: intPtr(60)})
	if status != 200 {
		t.Fatalf("set status = %d (body=%s)", status, string(body))
	}
	sr := decode[statusResponse](t, body)
	if sr.Status != "ok" {
		t.Fatalf("set response = %+v", sr)
	}

	// Get
	status, body = e.do(t, http.MethodGet, "/keys/test-key", nil)
	if status != 200 {
		t.Fatalf("get status = %d (body=%s)", status, string(body))
	}
	gr := decode[valueResponse](t, body)
	if gr.Key != "test-key" || gr.Value != "hello" {
		t.Fatalf("get response = %+v, want key=test-key value=hello", gr)
	}

	// Get missing
	status, _ = e.do(t, http.MethodGet, "/keys/nope", nil)
	if status != 404 {
		t.Fatalf("get missing status = %d, want 404", status)
	}

	// Overwrite: PUT on the same key replaces the value
	status, _ = e.do(t, http.MethodPut, "/keys/test-key", setRequest{Value: valPtr("world")})
	if status != 200 {
		t.Fatalf("overwrite status = %d", status)
	}
	status, body = e.do(t, http.MethodGet, "/keys/test-key", nil)
	gr = decode[valueResponse](t, body)
	if gr.Value != "world" {
		t.Fatalf("overwrite value = %q, want world", gr.Value)
	}
}

func TestIntegration_SetValidation(t *testing.T) {
	e := newIntegrationEnv(t)

	// empty value
	status, _ := e.do(t, http.MethodPut, "/keys/k", setRequest{Value: valPtr("")})
	if status != 400 {
		t.Fatalf("empty value status = %d, want 400", status)
	}

	// missing value field
	status, _ = e.do(t, http.MethodPut, "/keys/k", struct{}{})
	if status != 400 {
		t.Fatalf("missing value status = %d, want 400", status)
	}

	// negative TTL
	status, _ = e.do(t, http.MethodPut, "/keys/k", setRequest{Value: valPtr("v"), TTLSeconds: intPtr(-1)})
	if status != 400 {
		t.Fatalf("negative ttl status = %d, want 400", status)
	}
}

func TestIntegration_KeyValidation(t *testing.T) {
	e := newIntegrationEnv(t)
	// Only keys that an HTTP client can build a request URL for; control characters
	// (NUL, tab) can't be placed in a URL and are covered by main_test.go unit cases.
	for _, key := range []string{"bad key", `key"quote`, "key;inj"} {
		status, _ := e.do(t, http.MethodGet, "/keys/"+key, nil)
		if status != 400 {
			t.Fatalf("get bad key %q status = %d, want 400", key, status)
		}
	}
}

func TestIntegration_GetRefreshesTTL(t *testing.T) {
	e := newIntegrationEnv(t)

	// Set with 1s TTL, then sleep ~700ms, GETEX should refresh the TTL back to ~500ms
	// (LRUIdleTTL configured at 500ms).
	status, _ := e.do(t, http.MethodPut, "/keys/ttl", setRequest{Value: valPtr("v"), TTLSeconds: intPtr(1)})
	if status != 200 {
		t.Fatalf("set status = %d", status)
	}
	time.Sleep(700 * time.Millisecond)

	ttlBefore, err := e.rdb.PTTL(context.Background(), "ttl").Result()
	if err != nil {
		t.Fatalf("pttl before: %v", err)
	}
	if ttlBefore <= 0 {
		t.Fatalf("key expired before read; ttl=%v", ttlBefore)
	}

	status, _ = e.do(t, http.MethodGet, "/keys/ttl", nil)
	if status != 200 {
		t.Fatalf("get status = %d", status)
	}
	ttlAfter, err := e.rdb.PTTL(context.Background(), "ttl").Result()
	if err != nil {
		t.Fatalf("pttl after: %v", err)
	}
	// After GETEX the TTL should be reset close to LRUIdleTTL (500ms).
	if ttlAfter < 300*time.Millisecond || ttlAfter > 600*time.Millisecond {
		t.Fatalf("ttl after get = %v, want ~500ms (refreshed)", ttlAfter)
	}
}

func TestIntegration_TTLExpireDeletion(t *testing.T) {
	e := newIntegrationEnv(t)

	// Set with a short TTL, wait for expiration, then GET should return 404.
	status, _ := e.do(t, http.MethodPut, "/keys/expiring", setRequest{Value: valPtr("v"), TTLSeconds: intPtr(1)})
	if status != 200 {
		t.Fatalf("set status = %d", status)
	}

	// Key exists right now.
	exists, err := e.rdb.Exists(context.Background(), "expiring").Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 1 {
		t.Fatalf("key should exist immediately after set; exists=%d", exists)
	}

	// Wait for Garnet to expire it (TTL=1s, give 1.2s headroom).
	time.Sleep(1200 * time.Millisecond)

	exists, err = e.rdb.Exists(context.Background(), "expiring").Result()
	if err != nil {
		t.Fatalf("exists after expiry: %v", err)
	}
	if exists != 0 {
		t.Fatalf("key should be expired; exists=%d", exists)
	}

	status, _ = e.do(t, http.MethodGet, "/keys/expiring", nil)
	if status != 404 {
		t.Fatalf("get expired status = %d, want 404", status)
	}
}
