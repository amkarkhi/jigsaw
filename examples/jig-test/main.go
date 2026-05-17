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
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/rs/zerolog"
)

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

	// Validate logic handlers before starting server
	log.Info().Msg("Validating logic handlers")
	validationErrors := eng.ValidateLogicHandlers()
	if len(validationErrors) > 0 {
		log.Error().Interface("errors", validationErrors).Msg("Logic validation failed")
		fmt.Println("\nMissing Logic Handlers:")
		for _, err := range validationErrors {
			fmt.Printf("   • %s (required by task: %s)\n", err.Logic, err.Task)
		}
		fmt.Println("\nRegister missing handlers in registerLogicHandlers() function")
		os.Exit(1)
	}
	log.Info().Int("total_handlers", len(eng.ListLogicHandlers())).Msg("All logic handlers validated successfully")

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
		fmt.Println("\n🚀 Jigsaw Server Started!")
		fmt.Println("   URL: http://localhost:8080")
		fmt.Println("\n📡 Try these commands:")
		fmt.Println("   # Test the search endpoint")
		fmt.Println("   curl \"http://localhost:8080/api/search?query=test\"")
		fmt.Println("\n   # Check health")
		fmt.Println("   curl http://localhost:8080/health")
		fmt.Println("\n   # Validate logic handlers")
		fmt.Println("   curl http://localhost:8080/api/_validate/logic")
		fmt.Println("\n   # List all logic handlers")
		fmt.Println("   curl http://localhost:8080/api/_logic")
		fmt.Println("\n   Press Ctrl+C to stop")
		if err := srv.Start(8080, "./configs"); err != nil {
			errChan <- err
		}
	}()

	select {
	case <-sigChan:
		log.Info().Msg("Shutdown signal received")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*1000000000)
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

	eng.MustRegisterLogic("parse_and_validate_params", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		ctx.Logger.Info().Interface("inputs", inputs).Msg("Parsing and validating parameters")
		query, _ := inputs["query"].(string)
		ctx.Logger.Info().Str("query", query).Msg("Parsing parameters")
		if query == "" {
			return nil, fmt.Errorf("query parameter is required")
		}
		return map[string]any{
			"parsed_query": query,
		}, nil
	})

	eng.MustRegisterLogic("check_cache", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		ctx.Logger.Info().Interface("inputs", inputs).Msg("Checking cache")
		parsedQuery, _ := inputs["parsed_query"].(string)
		ctx.Logger.Info().Str("parsed_query", parsedQuery).Msg("Cache check for query")
		// Simulate cache miss for this example
		return map[string]any{
			"cache_hit":     false,
			"cached_result": nil,
		}, nil
	})

	eng.MustRegisterLogic("build_response", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		return inputs, nil
	})
	log.Info().Interface("list", eng.ListLogicHandlers()).Msg("Logic handlers registered")
}

// createProviderRegistry creates and configures provider registry
func createProviderRegistry(cfg *types.Config, log zerolog.Logger) *provider.Registry {
	log.Info().Msg("Creating provider registry")

	providerReg := provider.NewRegistry(log)

	// Register all configured providers
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
