package provider

import (
	"sort"
	"sync"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Factory builds a ProviderInstance from a config block.
//
// Host programs register factories at startup to plug in real connection
// code for a provider type. The registry is process-global because a
// factory is code, not config.
type Factory func(*types.Provider, zerolog.Logger) (types.ProviderInstance, error)

var (
	factoryMu sync.RWMutex
	factories = map[string]Factory{}
)

// RegisterFactory registers a factory for a provider type. Re-registering
// overwrites — host programs intentionally override the built-ins by
// calling this with the same name at startup.
func RegisterFactory(typeName string, f Factory) {
	if typeName == "" || f == nil {
		return
	}
	factoryMu.Lock()
	defer factoryMu.Unlock()
	factories[typeName] = f
}

// LookupFactory returns the factory for a type, or nil if none registered.
func LookupFactory(typeName string) Factory {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	return factories[typeName]
}

// IsRegisteredType reports whether a factory is registered for typeName.
func IsRegisteredType(typeName string) bool {
	return LookupFactory(typeName) != nil
}

// RegisteredTypes returns the sorted list of registered provider type names.
func RegisteredTypes() []string {
	factoryMu.RLock()
	out := make([]string, 0, len(factories))
	for name := range factories {
		out = append(out, name)
	}
	factoryMu.RUnlock()
	sort.Strings(out)
	return out
}

// defaultFactory builds a BaseProvider — the placeholder implementation
// shared by the built-ins until a host program overrides them with real
// connection code.
func defaultFactory(p *types.Provider, logger zerolog.Logger) (types.ProviderInstance, error) {
	return NewBaseProvider(p, logger), nil
}

func init() {
	// Built-in provider types. These ship with placeholder connections;
	// host programs override them with real factories at startup.
	RegisterFactory("database", defaultFactory)
	RegisterFactory("api_call", defaultFactory)
}
