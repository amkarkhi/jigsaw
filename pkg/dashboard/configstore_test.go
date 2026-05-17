package dashboard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// helper: POST JSON.
func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSaveRejectsUnsafePath(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: true, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	res := postJSON(t, ts.URL+"/api/files", SavePayload{
		Files: map[string]string{"../escape.yml": "tasks: []"},
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on path traversal, got %d", res.StatusCode)
	}
}

func TestSaveBlocksWhenEditOff(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: false, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	res := postJSON(t, ts.URL+"/api/files", SavePayload{
		Files: map[string]string{"tasks/t.yml": "tasks: []"},
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when edit is off, got %d", res.StatusCode)
	}
}

func TestSaveValidatesBeforeWriting(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: true, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	// Submit a flow that references a non-existent task; validation must fail
	// and the on-disk file must not change.
	original, _ := http.Get(ts.URL + "/api/file?path=flows/f.yml")
	origBody, _ := io.ReadAll(original.Body)
	original.Body.Close()

	bad := `flows:
  - name: greet
    description: bad
    tasks:
      - name: nonexistent
`
	res := postJSON(t, ts.URL+"/api/files", SavePayload{
		Files: map[string]string{"flows/f.yml": bad},
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "nonexistent") {
		t.Errorf("expected diagnostic mentioning nonexistent task, got: %s", body)
	}

	// Verify the file on disk is unchanged.
	after, _ := http.Get(ts.URL + "/api/file?path=flows/f.yml")
	afterBody, _ := io.ReadAll(after.Body)
	after.Body.Close()
	if !bytes.Equal(origBody, afterBody) {
		t.Errorf("file was modified despite validation failure")
	}
}

func TestSaveHappyPathWritesFile(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: true, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	newTask := `tasks:
  - name: hello
    description: greet (edited)
    logic: noop
    inputs: []
    outputs: []
`
	res := postJSON(t, ts.URL+"/api/files", SavePayload{
		Files: map[string]string{"tasks/t.yml": newTask},
	})
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}

	get, _ := http.Get(ts.URL + "/api/file?path=tasks/t.yml")
	body, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if !strings.Contains(string(body), "greet (edited)") {
		t.Errorf("expected updated content, got: %s", body)
	}
}

func TestBundleStreamsTarGz(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: true, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	newTask := `tasks:
  - name: hello
    description: greet
    logic: noop
    inputs: []
    outputs: []
`
	res := postJSON(t, ts.URL+"/api/bundle", SavePayload{
		Files: map[string]string{"tasks/t.yml": newTask},
	})
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, body)
	}
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	for _, want := range []string{"tasks/t.yml", "flows/f.yml"} {
		if !names[want] {
			t.Errorf("expected %q in bundle, got: %v", want, names)
		}
	}
}

func TestTreeListsKnownConfigFiles(t *testing.T) {
	root := scratchConfig(t)
	d, _ := New(Options{ConfigPath: root, Edit: true, Logger: zerolog.Nop()})
	ts := httptest.NewServer(d.Handler())
	defer ts.Close()

	res, _ := http.Get(ts.URL + "/api/tree")
	var paths []string
	_ = json.NewDecoder(res.Body).Decode(&paths)
	res.Body.Close()

	seen := strings.Join(paths, ",")
	for _, want := range []string{"tasks/t.yml", "flows/f.yml"} {
		if !strings.Contains(seen, want) {
			t.Errorf("expected %q in tree, got: %v", want, paths)
		}
	}
}
