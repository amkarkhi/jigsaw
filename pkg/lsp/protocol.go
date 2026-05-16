package lsp

import "encoding/json"

// Minimal LSP type subset. We only model the fields we actually read or write.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/

type initializeParams struct {
	RootURI string `json:"rootUri,omitempty"`
	RootPath string `json:"rootPath,omitempty"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   *serverInfo        `json:"serverInfo,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	// 1 = full document sync. We re-check the whole workspace on every change,
	// so we don't need incremental.
	TextDocumentSync int `json:"textDocumentSync"`
}

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type versionedTextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []textDocumentContentChangeEvent `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	Text         string                          `json:"text,omitempty"`
}

type didCloseParams struct {
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1=Error, 2=Warning
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// notification wraps a method+params as a JSON-RPC notification (no id).
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// response wraps a method response (with id, no method).
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

