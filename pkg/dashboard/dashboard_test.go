package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// silentLogger keeps test output clean.
type silentLogger struct{}

func (silentLogger) Trace(string, map[string]any)        {}
func (silentLogger) Debug(string, map[string]any)        {}
func (silentLogger) Info(string, map[string]any)         {}
func (silentLogger) Warn(string, map[string]any)         {}
func (silentLogger) Error(string, error, map[string]any) {}
func (l silentLogger) With(map[string]any) types.Logger  { return l }

// scratchConfig writes a minimal but valid config tree to a temp dir.
func scratchConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"tasks", "flows", "providers", "endpoints"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(name, data string) {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "tasks", "t.yml"), `tasks:
  - name: hello
    description: greet
    logic: noop
    inputs: []
    outputs: []
`)
	must(filepath.Join(root, "flows", "f.yml"), `flows:
  - name: greet
    description: example flow
    tasks:
      - name: hello
`)
	return root
}

func TestRefusesNonLoopbackWithoutFlag(t *testing.T) {
	_, err := New(Options{
		ConfigPath: "/tmp",
		Listen:     "0.0.0.0:0",
		Logger:     silentLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Errorf("expected non-loopback refusal, got: %v", err)
	}
}

func TestServerModeRequiresAuth(t *testing.T) {
	_, err := New(Options{
		ConfigPath: "/tmp",
		Mode:       ModeServer,
		Logger:     silentLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "Auth") {
		t.Errorf("expected ModeServer requires Auth, got: %v", err)
	}
}

func TestLocalModeServesReadAPIs(t *testing.T) {
	root := scratchConfig(t)
	d, err := New(Options{
		ConfigPath: root,
		Mode:       ModeLocal,
		Logger:     silentLogger{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/info", "/api/overview", "/api/flows", "/api/tasks", "/api/providers", "/api/endpoints", "/api/logic", "/api/diagnostics"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("%s: status %d, body=%s", path, res.StatusCode, body)
		}
		if !json.Valid(body) {
			t.Errorf("%s: response not valid JSON: %s", path, body)
		}
	}
}

func TestServerModeBlocksMutationsForViewer(t *testing.T) {
	root := scratchConfig(t)
	d, err := New(Options{
		ConfigPath: root,
		Mode:       ModeServer,
		Logger:     silentLogger{},
		Auth: BearerTokens(map[string]TokenInfo{
			"v": {Label: "v", Role: RoleViewer},
			"a": {Label: "a", Role: RoleAdmin},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	// No token → 401.
	res, _ := http.Get(ts.URL + "/api/info")
	if res.StatusCode != 401 {
		t.Errorf("missing token should be 401, got %d", res.StatusCode)
	}
	res.Body.Close()

	// Viewer GET → 200.
	req, _ := http.NewRequest("GET", ts.URL+"/api/info", nil)
	req.Header.Set("Authorization", "Bearer v")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Errorf("viewer GET should be 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	// Viewer POST → 403.
	req, _ = http.NewRequest("POST", ts.URL+"/api/flows", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer v")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 403 {
		t.Errorf("viewer POST should be 403, got %d", res.StatusCode)
	}
	res.Body.Close()

	// Admin POST → 404 (no handler registered yet, but auth passes).
	req, _ = http.NewRequest("POST", ts.URL+"/api/flows", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer a")
	res, _ = http.DefaultClient.Do(req)
	// Either 404 (no route) or 405 (route exists but doesn't accept POST) is
	// fine — the point is we got past auth.
	if res.StatusCode == 401 || res.StatusCode == 403 {
		t.Errorf("admin POST should not be denied by auth, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestListenAndServeShutsDownCleanly(t *testing.T) {
	root := scratchConfig(t)
	d, err := New(Options{
		ConfigPath: root,
		Listen:     "127.0.0.1:0", // any port
		Logger:     silentLogger{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.ListenAndServe(ctx) }()
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("unexpected error on shutdown: %v", err)
	}
}
