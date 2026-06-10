// Package parsers holds the request-parser registry shared by the HTTP
// server (which executes parsers per request) and the flow validator
// (which uses parser presence to relax scope checks on the first task).
//
// Lives in its own package to keep the dependency graph clean — engine
// and server both depend on parsers; parsers depends on nothing else.
package parsers

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
)

// RequestParserInput carries the raw HTTP request payload handed to a
// registered request parser.
//
//   - Query: parsed URL query (repeated keys preserve every value).
//   - Body: decoded JSON body, or nil for GET/DELETE.
//   - Headers: full request header map.
//   - Raw: the default merge of query+body the server would have used
//     without a parser. Provided so a parser can adjust a few keys
//     instead of rebuilding the whole map.
type RequestParserInput struct {
	Query   url.Values
	Body    map[string]any
	Headers http.Header
	Raw     map[string]any
}

// RequestParserFunc shapes a raw HTTP request into the params map used to
// seed the flow's scope. Return (nil, nil) to fall back to the default
// merge. Return an error to fail the request with 400.
type RequestParserFunc func(in RequestParserInput) (map[string]any, error)

var (
	mu       sync.RWMutex
	registry = map[string]RequestParserFunc{}
)

// Register adds fn to the registry under name. Endpoints reference the
// name as `request_parser: <name>` in YAML. Duplicate names panic.
func Register(name string, fn RequestParserFunc) {
	if name == "" {
		panic("parsers.Register: empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("parsers.Register(%q): nil func", name))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("parsers.Register: %q already registered", name))
	}
	registry[name] = fn
}

// Lookup returns the parser registered under name, or ok=false.
func Lookup(name string) (RequestParserFunc, bool) {
	mu.RLock()
	defer mu.RUnlock()
	fn, ok := registry[name]
	return fn, ok
}
