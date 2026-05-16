package lsp

import (
	"bufio"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/configlang"
	"github.com/amkarkhi/jigsaw/pkg/symbols"
	"github.com/amkarkhi/jigsaw/pkg/types"
)

const ServerName = "jigsaw-lsp"

// silentLogger keeps the engine quiet while running embedded in the LSP.
type silentLogger struct{}

func (silentLogger) Trace(string, map[string]any)        {}
func (silentLogger) Debug(string, map[string]any)        {}
func (silentLogger) Info(string, map[string]any)         {}
func (silentLogger) Warn(string, map[string]any)         {}
func (silentLogger) Error(string, error, map[string]any) {}
func (l silentLogger) With(map[string]any) types.Logger  { return l }

// Server is a stateful LSP server bound to one config root. Editing any file
// in the workspace re-runs configlang.Check over the whole tree; diagnostics
// are routed back per-file using the workspace's known files.
type Server struct {
	in  io.Reader
	out io.Writer

	mu sync.Mutex
	// rootPath is the directory we treat as the config tree. Discovered from
	// initialize.rootUri/rootPath, or inferred from the first opened file.
	rootPath string
	// openDocs maps file URI → in-memory text for files the editor has open.
	// We do not persist changes back to disk — that's the dashboard's job.
	openDocs map[string]string
	// lastPublished tracks which URIs we've published diagnostics to, so we
	// can clear stale findings when an issue moves elsewhere.
	lastPublished map[string]struct{}
}

// NewServer wires a server to stdin/stdout (the standard LSP transport).
func NewServer(in io.Reader, out io.Writer) *Server {
	return &Server{
		in:            in,
		out:           out,
		openDocs:      make(map[string]string),
		lastPublished: make(map[string]struct{}),
	}
}

// Serve runs the read loop until the client closes the connection or sends
// "exit". Returns nil on clean shutdown.
func (s *Server) Serve() error {
	br := bufio.NewReader(s.in)
	for {
		msg, err := readMessage(br)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := s.dispatch(msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) dispatch(msg *rpcMessage) error {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "initialized":
		return nil // no-op
	case "shutdown":
		return s.reply(msg.ID, nil)
	case "exit":
		return io.EOF
	case "textDocument/didOpen":
		return s.handleDidOpen(msg)
	case "textDocument/didChange":
		return s.handleDidChange(msg)
	case "textDocument/didSave":
		return s.handleDidSave(msg)
	case "textDocument/didClose":
		return s.handleDidClose(msg)
	default:
		// Reply with method-not-found only for requests (those with an id).
		// Unknown notifications are silently ignored per spec.
		if msg.ID != nil {
			return s.replyError(msg.ID, -32601, "method not found: "+msg.Method)
		}
		return nil
	}
}

func (s *Server) handleInitialize(msg *rpcMessage) error {
	var p initializeParams
	if len(msg.Params) > 0 {
		_ = json.Unmarshal(msg.Params, &p)
	}
	s.mu.Lock()
	if p.RootURI != "" {
		s.rootPath = uriToPath(p.RootURI)
	} else if p.RootPath != "" {
		s.rootPath = p.RootPath
	}
	s.mu.Unlock()

	return s.reply(msg.ID, initializeResult{
		Capabilities: serverCapabilities{TextDocumentSync: 1},
		ServerInfo:   &serverInfo{Name: ServerName, Version: "0.1.0"},
	})
}

func (s *Server) handleDidOpen(msg *rpcMessage) error {
	var p didOpenParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil
	}
	s.mu.Lock()
	s.openDocs[p.TextDocument.URI] = p.TextDocument.Text
	if s.rootPath == "" {
		s.rootPath = inferRoot(uriToPath(p.TextDocument.URI))
	}
	s.mu.Unlock()
	return s.publish()
}

func (s *Server) handleDidChange(msg *rpcMessage) error {
	var p didChangeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil
	}
	// We only requested full document sync, so the last change holds the
	// complete buffer.
	if len(p.ContentChanges) > 0 {
		s.mu.Lock()
		s.openDocs[p.TextDocument.URI] = p.ContentChanges[len(p.ContentChanges)-1].Text
		s.mu.Unlock()
	}
	return s.publish()
}

func (s *Server) handleDidSave(_ *rpcMessage) error {
	// Re-check on save in case the saved file differs from our in-memory view
	// (e.g. external formatter ran).
	return s.publish()
}

func (s *Server) handleDidClose(msg *rpcMessage) error {
	var p didCloseParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil
	}
	s.mu.Lock()
	delete(s.openDocs, p.TextDocument.URI)
	s.mu.Unlock()
	return s.publish()
}

// publish recomputes diagnostics for the whole workspace and emits a
// publishDiagnostics notification per open document (plus an empty publish to
// clear any URI we previously sent findings to but no longer do).
func (s *Server) publish() error {
	s.mu.Lock()
	root := s.rootPath
	openURIs := make([]string, 0, len(s.openDocs))
	for uri := range s.openDocs {
		openURIs = append(openURIs, uri)
	}
	prev := s.lastPublished
	s.lastPublished = make(map[string]struct{})
	s.mu.Unlock()

	if root == "" {
		return nil
	}

	diagnostics := s.computeWorkspaceDiagnostics(root)
	// Group by URI. For now, every diagnostic is attached to every open
	// document; this v0 keeps the user informed even without per-file
	// attribution. Per-file attribution lands with provenance tracking.
	for _, uri := range openURIs {
		params := publishDiagnosticsParams{URI: uri, Diagnostics: diagnostics}
		if err := s.notify("textDocument/publishDiagnostics", params); err != nil {
			return err
		}
		s.mu.Lock()
		s.lastPublished[uri] = struct{}{}
		s.mu.Unlock()
	}
	// Clear diagnostics for any URI we previously sent findings to but is no
	// longer open.
	for uri := range prev {
		s.mu.Lock()
		_, stillOpen := s.lastPublished[uri]
		s.mu.Unlock()
		if !stillOpen {
			_ = s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
				URI: uri, Diagnostics: []lspDiagnostic{},
			})
		}
	}
	return nil
}

// computeWorkspaceDiagnostics loads the on-disk config tree and runs Check.
// In-memory edits from the editor are not yet overlaid — that requires
// dispatching by file kind into the loader, which is Phase 5 territory.
// For v0 the LSP gives instant feedback on saved state plus a "save to see
// updated diagnostics" model.
func (s *Server) computeWorkspaceDiagnostics(root string) []lspDiagnostic {
	loader := config.NewLoader(silentLogger{})
	cfg, err := loader.Load(root)
	if err != nil {
		return []lspDiagnostic{{
			Range:    lspRange{}, // line 0
			Severity: 1,
			Source:   ServerName,
			Message:  "load: " + err.Error(),
		}}
	}

	opts := configlang.CheckOptions{}
	if m, _ := symbols.Read(filepath.Join(root, symbols.DefaultManifestPath)); m != nil {
		specs := make([]configlang.LogicSpec, len(m.Logic))
		for i, l := range m.Logic {
			specs[i] = configlang.LogicSpec{
				Name:         l.Name,
				InputSchema:  l.InputSchema,
				OutputSchema: l.OutputSchema,
			}
		}
		opts.LogicRegistry = specs
		opts.RegistryProvided = true
	}

	diags := configlang.Check(cfg, opts)
	out := make([]lspDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, lspDiagnostic{
			Range:    lspRange{},
			Severity: severityToLSP(d.Severity),
			Source:   ServerName,
			Message:  d.Message,
		})
	}
	return out
}

func severityToLSP(s configlang.Severity) int {
	switch s {
	case configlang.SeverityError:
		return 1
	case configlang.SeverityWarning:
		return 2
	default:
		return 3
	}
}

func (s *Server) reply(id *json.RawMessage, result any) error {
	if id == nil {
		return nil
	}
	return writeMessage(s.out, response{
		JSONRPC: "2.0",
		ID:      *id,
		Result:  result,
	})
}

func (s *Server) replyError(id *json.RawMessage, code int, msg string) error {
	if id == nil {
		return nil
	}
	return writeMessage(s.out, response{
		JSONRPC: "2.0",
		ID:      *id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

func (s *Server) notify(method string, params any) error {
	return writeMessage(s.out, notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

// uriToPath converts a file:// URI to an absolute filesystem path.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return u.Path
}

// inferRoot walks up from a file path looking for a directory containing any
// of the standard subfolders. Falls back to the file's parent.
func inferRoot(path string) string {
	dir := filepath.Dir(path)
	for cur := dir; cur != "/" && cur != "."; cur = filepath.Dir(cur) {
		for _, sub := range []string{"tasks", "flows", "providers", "endpoints"} {
			if info, err := os.Stat(filepath.Join(cur, sub)); err == nil && info.IsDir() {
				return cur
			}
		}
	}
	return dir
}

