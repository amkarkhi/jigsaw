package server

// The request-parser registry lives in pkg/parsers so the engine's flow
// validator can read it without importing pkg/server. This file re-exports
// the names hosts use from pkg/server.

import "github.com/amkarkhi/jigsaw/pkg/parsers"

// RequestParserInput is an alias of parsers.RequestParserInput.
type RequestParserInput = parsers.RequestParserInput

// RequestParserFunc is an alias of parsers.RequestParserFunc.
type RequestParserFunc = parsers.RequestParserFunc

// RegisterRequestParser registers fn under name. Endpoints reference the
// name as `request_parser: <name>` in YAML.
func RegisterRequestParser(name string, fn RequestParserFunc) {
	parsers.Register(name, fn)
}
