package provider

import (
	"context"
	"sync"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// BaseProvider is a base implementation of ProviderInstance
// This is a placeholder that will be extended with actual implementations
type BaseProvider struct {
	provider    *types.Provider
	connection  any
	connected   bool
	mu          sync.RWMutex
	logger      zerolog.Logger
	connectedAt time.Time
	lastUsed    time.Time
}

// NewBaseProvider creates a new base provider
func NewBaseProvider(provider *types.Provider, logger zerolog.Logger) *BaseProvider {
	return &BaseProvider{
		provider: provider,
		logger:   logger.With().Str("provider", provider.Name).Str("type", provider.Type).Logger(),
	}
}

// Connect establishes connection to the provider
// NOTE: This is a placeholder implementation
// Actual implementations will be added based on provider type
func (b *BaseProvider) Connect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if b.connected {
		return nil
	}
	
	b.logger.Info().Str("type", b.provider.Type).Msg("Connecting to provider (placeholder)")

	// BaseProvider is a generic placeholder. Real connection logic lives
	// in factories registered via provider.RegisterFactory; host programs
	// override the built-ins at startup.
	b.connection = map[string]any{
		"type":   b.provider.Type,
		"config": b.provider.Config,
		"status": "placeholder",
	}
	
	b.connected = true
	b.connectedAt = time.Now()
	b.lastUsed = time.Now()
	
	b.logger.Info().Msg("Provider connected successfully")
	
	return nil
}

// Disconnect closes the provider connection
func (b *BaseProvider) Disconnect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if !b.connected {
		return nil
	}
	
	b.logger.Info().Msg("Disconnecting provider")
	
	// Placeholder for actual disconnection logic
	b.connection = nil
	b.connected = false
	
	b.logger.Info().Msg("Provider disconnected")
	
	return nil
}

// IsConnected returns whether provider is connected
func (b *BaseProvider) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// GetConnection returns the underlying connection
func (b *BaseProvider) GetConnection() any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	b.lastUsed = time.Now()
	return b.connection
}

// GetProvider returns the provider configuration
func (b *BaseProvider) GetProvider() *types.Provider {
	return b.provider
}

