package router

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Router handles flow routing based on parameters
type Router struct {
	config *types.Config
	logger zerolog.Logger
}

// New creates a new router
func New(config *types.Config, logger zerolog.Logger) *Router {
	return &Router{
		config: config,
		logger: logger,
	}
}

// Route selects a flow based on endpoint and sub parameter
func (r *Router) Route(endpoint *types.Endpoint, sub int) (*types.Flow, error) {
	r.logger.Debug().Str("endpoint", endpoint.Name).Int("sub", sub).Msg("Routing request")
	
	// Find matching flow mapping
	var flowName string
	for _, mapping := range endpoint.Flows {
		if mapping.Sub == sub {
			flowName = mapping.FlowName
			break
		}
	}
	
	if flowName == "" {
		return nil, fmt.Errorf("no flow found for endpoint '%s' with sub=%d", endpoint.Name, sub)
	}
	
	// Get flow
	flow, ok := r.config.Flows[flowName]
	if !ok {
		return nil, fmt.Errorf("flow '%s' not found", flowName)
	}
	
	r.logger.Info().Str("endpoint", endpoint.Name).Int("sub", sub).Str("flow", flowName).Msg("Request routed to flow")
	
	return flow, nil
}

// UpdateConfig updates the router configuration
func (r *Router) UpdateConfig(config *types.Config) {
	r.config = config
	r.logger.Info().Msg("Router configuration updated")
}
