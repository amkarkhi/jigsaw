package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/provider"
	"github.com/amkarkhi/jigsaw/pkg/server"
	"github.com/amkarkhi/jigsaw/pkg/symbols"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/rs/zerolog"
)

// ---- Logic types and implementations --------------------------------------

type parseInputs struct {
	Q string `json:"Q"`
}

type parseOutputs struct {
	ParsedQuery string `json:"parsed_query"`
}

type parseParams struct {
	MaxLength int `json:"max_length"`
}

type ParseLogic struct{}

func (ParseLogic) LogicMeta() engine.LogicMeta {
	return engine.LogicMeta{
		Name:        "parse_and_validate_params",
		Description: "Parse and validate search query parameters",
		Version:     "2.1.0",
	}
}

func (ParseLogic) Run(ctx *types.ExecutionContext, in parseInputs, p parseParams) (*parseOutputs, error) {
	ctx.Logger.Info().Str("Q", in.Q).Int("max_length", p.MaxLength).Msg("Parsing and validating parameters")
	if in.Q == "" {
		return nil, fmt.Errorf("Q required: provide a non-empty search query")
	}
	q := in.Q
	if p.MaxLength > 0 && len(q) > p.MaxLength {
		q = q[:p.MaxLength]
	}
	return &parseOutputs{ParsedQuery: q}, nil
}

type cacheInputs struct {
	ParsedQuery string `json:"parsed_query"`
}

type cacheOutputs struct {
	CacheHit     bool `json:"cache_hit"`
	CachedResult any  `json:"cached_result"`
}

type cacheParams struct{}

type CheckCacheLogic struct{}

func (CheckCacheLogic) LogicMeta() engine.LogicMeta {
	return engine.LogicMeta{
		Name:        "check_cache",
		Description: "Check cache for existing search results",
		Version:     "1.5.2",
	}
}

func (CheckCacheLogic) Run(
	ctx *types.ExecutionContext,
	in cacheInputs,
	_ cacheParams,
	prov types.ProviderInstance,
) (*cacheOutputs, error) {
	if prov == nil {
		return nil, fmt.Errorf("cache provider not configured")
	}
	if !prov.IsConnected() {
		if err := prov.Connect(ctx.Context); err != nil {
			return nil, fmt.Errorf("connect cache: %w", err)
		}
	}

	cfg := prov.GetProvider()
	ctx.Logger.Info().
		Str("provider", cfg.Name).
		Str("type", cfg.Type).
		Str("parsed_query", in.ParsedQuery).
		Msg("Cache lookup")

	// Real impl would do: client := prov.GetConnection().(*redis.Client); client.Get(ctx, key)
	return &cacheOutputs{
		CacheHit:     false,
		CachedResult: nil,
	}, nil
}

type buildInputs struct {
	ParsedQuery string `json:"parsed_query"`
	// Both fields are skippable: a flow that doesn't run the cache step
	// can omit them via `bind.skip: [cache_hit, cached_result]` and the
	// logic will see the Go zero values (false, nil).
	CacheHit     bool `json:"cache_hit"     jig:"skippable"`
	CachedResult any  `json:"cached_result" jig:"skippable"`
}

type buildOutputs struct {
	Response any `json:"response"`
}

type buildParams struct{}

type BuildResponseLogic struct{}

func (BuildResponseLogic) LogicMeta() engine.LogicMeta {
	return engine.LogicMeta{
		Name:        "build_response",
		Description: "Build final HTTP response",
		Version:     "3.0.1",
	}
}

func (BuildResponseLogic) Run(_ *types.ExecutionContext, in buildInputs, _ buildParams) (*buildOutputs, error) {
	return &buildOutputs{
		Response: map[string]any{
			"query":         in.ParsedQuery,
			"cache_hit":     in.CacheHit,
			"cached_result": in.CachedResult,
		},
	}, nil
}

// ---------------------------------------------------------------------------

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		Level(zerolog.DebugLevel).With().Timestamp().Caller().Logger()
	log.Info().Msg("Starting Jigsaw Server Example")

	loader := config.NewLoader(log)
	cfg, err := loader.Load("./configs")
	if err != nil {
		log.Error().Err(err).Msg("Failed to load config")
		os.Exit(1)
	}

	val := validator.New(log)
	if err := val.ValidateConfig(cfg); err != nil {
		log.Error().Err(err).Msg("Invalid configuration")
		os.Exit(1)
	}

	eng := engine.New(cfg, val, log)
	registerLogicHandlers(eng, log)

	// Post-registration flow-level validation.
	if err := eng.ValidateFlows(); err != nil {
		log.Error().Err(err).Msg("Flow validation failed")
		os.Exit(1)
	}

	// Validate logic handlers before starting server.
	log.Info().Msg("Validating logic handlers")
	validationErrors := eng.ValidateLogicHandlers()
	if len(validationErrors) > 0 {
		log.Error().Interface("errors", validationErrors).Msg("Logic validation failed")
		fmt.Println("\nMissing Logic Handlers:")
		for _, err := range validationErrors {
			fmt.Printf("   - %s (required by task: %s)\n", err.Logic, err.Task)
		}
		fmt.Println("\nRegister missing handlers in registerLogicHandlers() function")
		os.Exit(1)
	}
	log.Info().Int("total_handlers", len(eng.ListLogicHandlers())).Msg("All logic handlers validated successfully")

	// Write/refresh the symbols manifest so the dashboard (and any other
	// tool that reads <configPath>/.jigsaw/symbols.json) picks up the
	// current logic schemas, including each handler's skippable_inputs.
	if err := symbols.DumpToFile(eng, cfg, "./configs", "jig-test"); err != nil {
		log.Warn().Err(err).Msg("Failed to write symbols manifest")
	} else {
		log.Info().Msg("Wrote symbols manifest to ./configs/.jigsaw/symbols.json")
	}

	providerReg := createProviderRegistry(cfg, log)
	if err := providerReg.InitAllEager(context.Background()); err != nil {
		log.Warn().Err(err).Msg("Some providers failed to initialize")
	}

	opts := server.Options{
		Port:      8080,
		HotReload: true,
		LogLevel:  "info",
		Pretty:    true,
	}
	srv := server.NewWithEngine(eng, providerReg, cfg, log, opts)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	errChan := make(chan error, 1)

	go func() {
		fmt.Println("\nJigsaw Server Started!")
		fmt.Println("   URL: http://localhost:8080")
		fmt.Println("\nTry these commands:")
		fmt.Println("   curl \"http://localhost:8080/api/search?query=test&sub=1\"")
		fmt.Println("   curl http://localhost:8080/health")
		fmt.Println("   curl http://localhost:8080/api/_validate/logic")
		fmt.Println("   curl http://localhost:8080/api/_logic")
		fmt.Println("\n   Press Ctrl+C to stop")
		if err := srv.Start(8080, "./configs"); err != nil {
			errChan <- err
		}
	}()

	select {
	case <-sigChan:
		log.Info().Msg("Shutdown signal received")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := providerReg.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing providers")
		}
		if err := srv.Stop(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Error during shutdown")
		}
		log.Info().Msg("Server stopped gracefully")
	case err := <-errChan:
		log.Error().Err(err).Msg("Server error")
		os.Exit(1)
	}
}

func registerLogicHandlers(eng *engine.Engine, log zerolog.Logger) {
	log.Info().Msg("Registering logic handlers")
	engine.MustRegister(eng, ParseLogic{})
	engine.MustRegisterWithProvider(eng, CheckCacheLogic{})
	engine.MustRegister(eng, BuildResponseLogic{})
	log.Info().Interface("list", eng.ListLogicHandlers()).Msg("Logic handlers registered")
}

func createProviderRegistry(cfg *types.Config, log zerolog.Logger) *provider.Registry {
	log.Info().Msg("Creating provider registry")

	providerReg := provider.NewRegistry(log)

	for _, prov := range cfg.Providers {
		if err := providerReg.RegisterConfig(prov); err != nil {
			log.Error().Err(err).Str("provider", prov.Name).Msg("Failed to register provider")
		} else {
			log.Info().Str("name", prov.Name).Str("type", prov.Type).Str("mode", prov.InitMode).Msg("Provider registered")
		}
	}

	log.Info().Int("providers", len(cfg.Providers)).Msg("Provider registry created")
	return providerReg
}
