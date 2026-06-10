package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/parsers"
	"github.com/amkarkhi/jigsaw/pkg/provider"
	"github.com/amkarkhi/jigsaw/pkg/router"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Server is the HTTP server
type Server struct {
	engine           *engine.Engine
	router           *router.Router
	providerRegistry *provider.Registry
	configLoader     *config.Loader
	validator        *validator.Validator
	logger           zerolog.Logger
	config           *types.Config
	ginEngine        *gin.Engine
	httpServer       *http.Server
	mu               sync.RWMutex
	hotReload        bool
	flowResolver     FlowResolver
}

// Options for server configuration
type Options struct {
	Port      int
	HotReload bool
	LogLevel  string
	Pretty    bool
	// Middleware is appended to the Gin engine after the built-in recovery
	// and logging middleware, but before route registration. Hosts use this
	// to inject auth, rate limiting, tracing, etc.
	Middleware []gin.HandlerFunc

	// FlowResolver, when set, is invoked between the static `sub →
	// flow` lookup and the engine call. It lets the host override
	// which flow runs, mutate the request context (e.g. attach a
	// matched rule), and supply a per-task parameter overlay. Nil =
	// static-map-only routing, jigsaw's original behaviour.
	FlowResolver FlowResolver
}

// FlowResolver is the host hook that decides which flow actually runs
// for a request. Implementations should be fast (it runs on every
// request) and concurrency-safe.
type FlowResolver func(ctx context.Context, req FlowResolveRequest) (FlowResolveResponse, error)

// FlowResolveRequest carries everything jigsaw knows about an incoming
// request after the static lookup has picked a default flow.
type FlowResolveRequest struct {
	Endpoint *types.Endpoint
	Sub      int
	Headers  map[string]string
	Params   map[string]any
	Default  *types.Flow
}

// FlowResolveResponse describes how the host wants the request to run.
// All fields are optional; zero values mean "keep what jigsaw picked."
type FlowResolveResponse struct {
	// Flow, when non-nil, replaces the default flow.
	Flow *types.Flow
	// Sub, when non-zero, replaces the request's sub for downstream
	// logging and ctx attachment (does NOT change the dispatched
	// flow — Flow does that). Use this when the host's policy maps
	// sub to a canonical backend identifier.
	Sub int
	// Context, when non-nil, replaces the request context. Hosts use
	// this to attach matched rules or audit metadata that handlers
	// can read.
	Context context.Context
	// ParamOverlay, when non-nil, is applied as the final layer when
	// jigsaw computes each task's params. See types.WithParamOverlay.
	ParamOverlay types.ParamOverlay
}

// New creates a new server instance
func New(cfg *types.Config, logger zerolog.Logger, opts Options) *Server {
	// Create validator
	val := validator.New(logger)
	
	// Create engine
	eng := engine.New(cfg, val, logger)
	
	// Create router
	rtr := router.New(cfg, logger)
	
	// Create provider registry
	providerReg := provider.NewRegistry(logger)
	
	// Register all providers
	for _, prov := range cfg.Providers {
		providerReg.RegisterConfig(prov)
	}
	
	// Create config loader
	configLoader := config.NewLoader(logger)
	
	s := &Server{
		engine:           eng,
		router:           rtr,
		providerRegistry: providerReg,
		configLoader:     configLoader,
		validator:        val,
		logger:           logger,
		config:           cfg,
		hotReload:        opts.HotReload,
		flowResolver:     opts.FlowResolver,
	}
	
	// Setup Gin
	if !opts.Pretty {
		gin.SetMode(gin.ReleaseMode)
	}
	
	s.ginEngine = gin.New()
	s.ginEngine.Use(gin.Recovery())
	s.ginEngine.Use(s.loggingMiddleware())
	for _, mw := range opts.Middleware {
		s.ginEngine.Use(mw)
	}

	// Register routes
	s.registerRoutes()

	return s
}

// NewWithEngine creates a new server instance with a pre-configured engine
// Use this when you want to register custom logic handlers
func NewWithEngine(eng *engine.Engine, providerReg *provider.Registry, cfg *types.Config, logger zerolog.Logger, opts Options) *Server {
	// Create router
	rtr := router.New(cfg, logger)
	
	// Create config loader
	configLoader := config.NewLoader(logger)
	
	// Create validator
	val := validator.New(logger)
	
	s := &Server{
		engine:           eng,
		router:           rtr,
		providerRegistry: providerReg,
		configLoader:     configLoader,
		validator:        val,
		logger:           logger,
		config:           cfg,
		hotReload:        opts.HotReload,
		flowResolver:     opts.FlowResolver,
	}
	
	// Setup Gin
	if !opts.Pretty {
		gin.SetMode(gin.ReleaseMode)
	}
	
	s.ginEngine = gin.New()
	s.ginEngine.Use(gin.Recovery())
	s.ginEngine.Use(s.loggingMiddleware())
	for _, mw := range opts.Middleware {
		s.ginEngine.Use(mw)
	}

	// Register routes
	s.registerRoutes()

	return s
}

// GetEngine returns the server's engine (for registering logic handlers)
func (s *Server) GetEngine() *engine.Engine {
	return s.engine
}

// Start starts the HTTP server
func (s *Server) Start(port int, configPath string) error {
	s.logger.Info().Int("port", port).Bool("hot_reload", s.hotReload).Msg("Starting Jigsaw server")
	
	// Initialize eager providers
	if err := s.providerRegistry.InitAllEager(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize providers: %w", err)
	}
	
	// Start hot-reload watcher if enabled
	if s.hotReload {
		if err := s.configLoader.Watch(configPath, s.onConfigChange); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to start config watcher")
		} else {
			s.logger.Info().Msg("Hot-reload enabled")
		}
	}
	
	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: s.ginEngine,
	}
	
	s.logger.Info().Str("address", s.httpServer.Addr).Msg("Server started successfully")
	
	return s.httpServer.ListenAndServe()
}

// Stop stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info().Msg("Stopping server")
	
	// Stop config watcher
	if s.hotReload {
		s.configLoader.StopWatch()
	}
	
	// Close provider connections
	if err := s.providerRegistry.Close(); err != nil {
		s.logger.Error().Err(err).Msg("Error closing providers")
	}
	
	// Shutdown HTTP server
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	
	return nil
}

// registerRoutes registers all endpoint routes
func (s *Server) registerRoutes() {
	// Health check
	s.ginEngine.GET("/health", s.healthHandler)
	
	// Validation endpoints for UI
	s.ginEngine.GET("/api/_validate/logic", s.validateLogicHandlers)
	s.ginEngine.GET("/api/_validate/logic/:name", s.getLogicHandlerInfo)
	s.ginEngine.GET("/api/_logic", s.listLogicHandlers)
	
	// Register all configured endpoints
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	for _, endpoint := range s.config.Endpoints {
		s.registerEndpoint(endpoint)
	}
}

// registerEndpoint registers a single endpoint
func (s *Server) registerEndpoint(endpoint *types.Endpoint) {
	handler := s.createEndpointHandler(endpoint)
	
	switch endpoint.Method {
	case "GET":
		s.ginEngine.GET(endpoint.Path, handler)
	case "POST":
		s.ginEngine.POST(endpoint.Path, handler)
	case "PUT":
		s.ginEngine.PUT(endpoint.Path, handler)
	case "DELETE":
		s.ginEngine.DELETE(endpoint.Path, handler)
	case "PATCH":
		s.ginEngine.PATCH(endpoint.Path, handler)
	default:
		s.ginEngine.POST(endpoint.Path, handler)
	}
	
	s.logger.Info().Str("path", endpoint.Path).Str("method", endpoint.Method).Str("name", endpoint.Name).Msg("Endpoint registered")
}

// createEndpointHandler creates a Gin handler for an endpoint
func (s *Server) createEndpointHandler(endpoint *types.Endpoint) gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		
	// Parse body once for POST/PUT/PATCH requests
	var body map[string]any
	if c.Request.Method != "GET" && c.Request.Method != "DELETE" {
		if err := c.BindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid JSON body",
			})
			return
		}
	}
	
	// Extract sub parameter
	subStr := c.Query("sub")
	if subStr == "" && body != nil {
		if subVal, ok := body["sub"]; ok {
			switch v := subVal.(type) {
			case float64:
				subStr = strconv.Itoa(int(v))
			case int:
				subStr = strconv.Itoa(v)
			case string:
				subStr = v
			}
		}
	}
	
	if subStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "sub parameter is required",
		})
		return
	}
	
	sub, err := strconv.Atoi(subStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "sub parameter must be an integer",
		})
		return
	}
	
	// Route to flow
	s.mu.RLock()
	flow, err := s.router.Route(endpoint, sub)
	s.mu.RUnlock()
	
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error(),
		})
		return
	}
	
	// Extract parameters
	params := make(map[string]any)
	
	// Query parameters. Repeated keys (e.g. ?facets=a&facets=b) are kept as
	// a []any so downstream handlers can read every value; single-value
	// keys stay as plain strings for backwards compatibility.
	for key, values := range c.Request.URL.Query() {
		switch len(values) {
		case 0:
			// skip
		case 1:
			params[key] = values[0]
		default:
			anyVals := make([]any, len(values))
			for i, v := range values {
				anyVals[i] = v
			}
			params[key] = anyVals
		}
	}
	
	// Body parameters (already parsed above)
	if body != nil {
		for k, v := range body {
			params[k] = v
		}
	}

	// Endpoint-level request parser. When the endpoint declares a parser by
	// name, hand the raw query/body/headers to it and use its output as the
	// flow's params, replacing the default merge above. This lets a host app
	// own parameter shaping end-to-end (e.g. normalize multi-value `sort`
	// into a typed slice) without each task re-doing the work.
	if endpoint.RequestParser != "" {
		parser, ok := parsers.Lookup(endpoint.RequestParser)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("request_parser %q not registered", endpoint.RequestParser),
			})
			return
		}
		parsed, perr := parser(RequestParserInput{
			Query:   c.Request.URL.Query(),
			Body:    body,
			Headers: c.Request.Header,
			Raw:     params,
		})
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": perr.Error(),
			})
			return
		}
		if parsed != nil {
			params = parsed
		}
	}
		
		// Extract headers
		headers := make(map[string]string)
		for key, values := range c.Request.Header {
			if len(values) > 0 {
				headers[key] = values[0]
			}
		}

		// Give the host (if it installed a FlowResolver) a chance to
		// override the routed flow, decorate the context, and attach
		// a per-task parameter overlay. The static map's pick is the
		// input default; the resolver's response is the source of
		// truth from here on.
		ctx := c.Request.Context()
		if s.flowResolver != nil {
			resp, rerr := s.flowResolver(ctx, FlowResolveRequest{
				Endpoint: endpoint,
				Sub:      sub,
				Headers:  headers,
				Params:   params,
				Default:  flow,
			})
			if rerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"status": "error",
					"error":  rerr.Error(),
				})
				return
			}
			if resp.Flow != nil {
				flow = resp.Flow
			}
			if resp.Sub != 0 {
				sub = resp.Sub
			}
			if resp.Context != nil {
				ctx = resp.Context
			}
			if resp.ParamOverlay != nil {
				ctx = types.WithParamOverlay(ctx, resp.ParamOverlay)
			}
		}

		// Execute flow
		result, err := s.engine.ExecuteFlow(
			ctx,
			flow.Name,
			sub,
			params,
			headers,
			s.providerRegistry,
		)
		
		executionTime := time.Since(startTime).Milliseconds()
		
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":         "error",
				"error":          err.Error(),
				"execution_time": executionTime,
			})
			return
		}
		
		c.JSON(http.StatusOK, result)
	}
}

// healthHandler handles health check requests
func (s *Server) healthHandler(c *gin.Context) {
	s.mu.RLock()
	taskCount := len(s.config.Tasks)
	flowCount := len(s.config.Flows)
	providerCount := len(s.config.Providers)
	endpointCount := len(s.config.Endpoints)
	s.mu.RUnlock()
	
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"config": gin.H{
			"tasks":     taskCount,
			"flows":     flowCount,
			"providers": providerCount,
			"endpoints": endpointCount,
		},
	})
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		
		c.Next()
		
		duration := time.Since(start)
		
		s.logger.Info().Str("method", c.Request.Method).Str("path", path).Int("status", c.Writer.Status()).Int64("duration", duration.Milliseconds()).Str("ip", c.ClientIP()).Msg("HTTP request")
	}
}

// onConfigChange handles configuration reload
func (s *Server) onConfigChange(newConfig *types.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.logger.Info().Msg("Reloading configuration")

	// Validate new configuration
	if err := s.validator.ValidateConfig(newConfig); err != nil {
		s.logger.Error().Err(err).Msg("Invalid configuration, keeping old config")
		return
	}
	
	// Update configuration
	s.config = newConfig
	
	// Update router
	s.router.UpdateConfig(newConfig)
	
	// Re-register providers
	for _, prov := range newConfig.Providers {
		s.providerRegistry.RegisterConfig(prov)
	}
	
	// NOTE: We do NOT recreate the engine here because that would lose
	// all registered logic handlers. The engine's config is updated internally.
	// If you need to reload logic handlers, restart the server.
	
	s.logger.Info().Int("tasks", len(newConfig.Tasks)).Int("flows", len(newConfig.Flows)).Int("providers", len(newConfig.Providers)).Int("endpoints", len(newConfig.Endpoints)).Msg("Configuration reloaded successfully")
	s.logger.Warn().Msg("Logic handlers are NOT reloaded (restart server to reload handlers)")

	// Note: Endpoints are not re-registered in Gin as that would require
	// recreating the entire Gin engine. For endpoint changes, restart is required.
	s.logger.Warn().Msg("Endpoint changes require server restart")
}

// validateLogicHandlers validates all logic handlers
func (s *Server) validateLogicHandlers(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	errors := s.engine.ValidateLogicHandlers()
	
	response := map[string]any{
		"valid": len(errors) == 0,
		"total_handlers": len(s.engine.ListLogicHandlers()),
	}
	
	if len(errors) > 0 {
		response["errors"] = errors
		c.JSON(http.StatusOK, response)
	} else {
		response["message"] = "All logic handlers are properly registered"
		c.JSON(http.StatusOK, response)
	}
}

// getLogicHandlerInfo returns info about a specific logic handler
func (s *Server) getLogicHandlerInfo(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	name := c.Param("name")
	
	info, err := s.engine.GetLogicHandlerInfo(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": err.Error(),
		})
		return
	}
	
	c.JSON(http.StatusOK, info)
}

// listLogicHandlers lists all registered logic handlers with metadata
func (s *Server) listLogicHandlers(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	handlers := s.engine.ListLogicHandlersWithInfo()
	
	c.JSON(http.StatusOK, gin.H{
		"handlers": handlers,
		"total": len(handlers),
	})
}
