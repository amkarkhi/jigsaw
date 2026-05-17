package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Registry manages provider instances
type Registry struct {
	providers map[string]types.ProviderInstance
	configs   map[string]*types.Provider
	mu        sync.RWMutex
	logger    zerolog.Logger
}

// NewRegistry creates a new provider registry
func NewRegistry(logger zerolog.Logger) *Registry {
	return &Registry{
		providers: make(map[string]types.ProviderInstance),
		configs:   make(map[string]*types.Provider),
		logger:    logger,
	}
}

// Register registers a provider configuration
func (r *Registry) RegisterConfig(provider *types.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if provider.Name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	
	r.configs[provider.Name] = provider
	r.logger.Debug().Str("provider", provider.Name).Str("type", provider.Type).Str("mode", provider.InitMode).Msg("Provider configuration registered")
	
	return nil
}

// Get retrieves or initializes a provider instance
func (r *Registry) Get(name string) (types.ProviderInstance, error) {
	r.mu.RLock()
	instance, exists := r.providers[name]
	config, hasConfig := r.configs[name]
	r.mu.RUnlock()
	
	if !hasConfig {
		return nil, fmt.Errorf("provider '%s' not configured", name)
	}
	
	// Return existing instance if already initialized
	if exists && instance.IsConnected() {
		return instance, nil
	}
	
	// Initialize based on init mode
	switch config.InitMode {
	case "lazy":
		return r.initLazy(name, config)
	case "eager", "pooled":
		return r.initEager(name, config)
	default:
		return r.initLazy(name, config)
	}
}

// Register registers a provider instance directly
func (r *Registry) Register(name string, instance types.ProviderInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.providers[name] = instance
	r.logger.Debug().Str("provider", name).Msg("Provider instance registered")
	
	return nil
}

// Close closes all provider connections
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	var errs []error
	for name, instance := range r.providers {
		if instance.IsConnected() {
			if err := instance.Disconnect(context.Background()); err != nil {
				errs = append(errs, fmt.Errorf("failed to close %s: %w", name, err))
			} else {
				r.logger.Info().Str("provider", name).Msg("Provider disconnected")
			}
		}
	}
	
	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}
	
	return nil
}

// initLazy creates a lazy-loading provider instance
func (r *Registry) initLazy(name string, config *types.Provider) (types.ProviderInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Double-check if another goroutine already initialized
	if instance, exists := r.providers[name]; exists {
		return instance, nil
	}
	
	instance := NewBaseProvider(config, r.logger)
	r.providers[name] = instance
	
	r.logger.Debug().Str("provider", name).Str("type", config.Type).Msg("Lazy provider initialized")
	
	return instance, nil
}

// initEager creates and connects a provider immediately
func (r *Registry) initEager(name string, config *types.Provider) (types.ProviderInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Double-check if another goroutine already initialized
	if instance, exists := r.providers[name]; exists && instance.IsConnected() {
		return instance, nil
	}
	
	instance := NewBaseProvider(config, r.logger)
	
	// Connect immediately for eager/pooled mode
	if err := instance.Connect(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to connect provider %s: %w", name, err)
	}
	
	r.providers[name] = instance
	
	r.logger.Info().Str("provider", name).Str("type", config.Type).Str("mode", config.InitMode).Msg("Provider initialized and connected")
	
	return instance, nil
}

// InitAllEager initializes all providers configured as eager or pooled
func (r *Registry) InitAllEager(ctx context.Context) error {
	r.mu.RLock()
	configs := make([]*types.Provider, 0, len(r.configs))
	for _, cfg := range r.configs {
		if cfg.InitMode == "eager" || cfg.InitMode == "pooled" {
			configs = append(configs, cfg)
		}
	}
	r.mu.RUnlock()
	
	for _, cfg := range configs {
		if _, err := r.Get(cfg.Name); err != nil {
			return fmt.Errorf("failed to initialize provider %s: %w", cfg.Name, err)
		}
	}
	
	return nil
}
