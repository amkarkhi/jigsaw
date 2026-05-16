package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frame wraps a JSON-RPC body in LSP framing.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// readAllFrames splits an output stream into JSON bodies and returns them.
func readAllFrames(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	r := bytes.NewReader(raw)
	for {
		// Find headers terminator.
		var header bytes.Buffer
		var contentLength int
		for {
			b, err := r.ReadByte()
			if err == io.EOF {
				return out
			}
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			header.WriteByte(b)
			if bytes.HasSuffix(header.Bytes(), []byte("\r\n\r\n")) {
				break
			}
		}
		for _, line := range strings.Split(header.String(), "\r\n") {
			if strings.HasPrefix(line, "Content-Length:") {
				_, _ = fmt.Sscanf(line, "Content-Length: %d", &contentLength)
			}
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, body)
		}
		out = append(out, m)
	}
}

func TestServerInitializeAndDiagnostics(t *testing.T) {
	// Build a minimal config tree with one broken flow (refers to a task that
	// doesn't exist) so we expect at least one error diagnostic.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A flow that references a missing task.
	flowYAML := `flows:
  - name: broken
    description: refers to a missing task
    tasks:
      - name: ghost
`
	if err := os.WriteFile(filepath.Join(root, "flows", "broken.yml"), []byte(flowYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Drive the server: initialize → didOpen → expect diagnostics → shutdown → exit.
	rootURI := "file://" + root
	openURI := "file://" + filepath.Join(root, "flows", "broken.yml")

	initBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":%q}}`, rootURI)
	openBody := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"text":%q}}}`,
		openURI, flowYAML)
	shutBody := `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`
	exitBody := `{"jsonrpc":"2.0","method":"exit"}`

	input := frame(initBody) + frame(openBody) + frame(shutBody) + frame(exitBody)

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input), &out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	frames := readAllFrames(t, out.Bytes())
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (initialize response + diagnostics), got %d:\n%s", len(frames), out.String())
	}

	// First frame should be the initialize response.
	if frames[0]["id"] == nil {
		t.Errorf("expected initialize response with id, got: %+v", frames[0])
	}

	// One of the later frames must be a publishDiagnostics with our missing-task error.
	found := false
	for _, f := range frames {
		if f["method"] == "textDocument/publishDiagnostics" {
			params, _ := f["params"].(map[string]any)
			diags, _ := params["diagnostics"].([]any)
			for _, d := range diags {
				dm, _ := d.(map[string]any)
				msg, _ := dm["message"].(string)
				if strings.Contains(msg, "ghost") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected a diagnostic referring to the missing task 'ghost', got frames:\n%s", out.String())
	}
}

func TestServerHandlesUnknownMethod(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"textDocument/hover"}`
	input := frame(body) + frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input), &out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	frames := readAllFrames(t, out.Bytes())
	if len(frames) == 0 || frames[0]["error"] == nil {
		t.Errorf("expected method-not-found error for hover, got: %s", out.String())
	}
}
