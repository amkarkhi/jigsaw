package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/logger"
	"github.com/amkarkhi/jigsaw/pkg/provider"
	"github.com/amkarkhi/jigsaw/pkg/server"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
)

func main() {
	log := logger.New("debug", true)
	log.Info("Starting Jigsaw Server Example", nil)
	loader := config.NewLoader(log)
	cfg, err := loader.Load("./configs")
	if err != nil {
		log.Error("Failed to load config", err, nil)
		os.Exit(1)
	}
	val := validator.New(log)
	if err := val.ValidateConfig(cfg); err != nil {
		log.Error("Invalid configuration", err, nil)
		os.Exit(1)
	}
	eng := engine.New(cfg, val, log)
	registerLogicHandlers(eng, log)

	// ✅ Validate logic handlers before starting server
	log.Info("Validating logic handlers", nil)
	validationErrors := eng.ValidateLogicHandlers()
	if len(validationErrors) > 0 {
		log.Error("Logic validation failed", nil, map[string]any{
			"errors": validationErrors,
		})
		fmt.Println("\n❌ Missing Logic Handlers:")
		for _, err := range validationErrors {
			fmt.Printf("   • %s (required by task: %s)\n", err.Logic, err.Task)
		}
		fmt.Println("\n💡 Register missing handlers in registerLogicHandlers() function")
		os.Exit(1)
	}
	log.Info("✅ All logic handlers validated successfully", map[string]any{
		"total_handlers": len(eng.ListLogicHandlers()),
	})

	providerReg := createProviderRegistry(cfg, log)
	if err := providerReg.InitAllEager(context.Background()); err != nil {
		log.Warn("Some providers failed to initialize", map[string]any{
			"error": err.Error(),
		})
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
		log.Info("Shutdown signal received", nil)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*1000000000)
		defer shutdownCancel()
		if err := providerReg.Close(); err != nil {
			log.Error("Error closing providers", err, nil)
		}
		if err := srv.Stop(shutdownCtx); err != nil {
			log.Error("Error during shutdown", err, nil)
		}
		log.Info("Server stopped gracefully", nil)
	case err := <-errChan:
		log.Error("Server error", err, nil)
		os.Exit(1)
	}
}

func registerLogicHandlers(eng *engine.Engine, log types.Logger) {
	log.Info("Registering logic handlers", nil)

	eng.MustRegisterLogic("parse_and_validate_params", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		ctx.Logger.Info("Parsing and validating parameters", inputs)
		query, _ := inputs["query"].(string)
		ctx.Logger.Info("Parsing parameters", map[string]any{
			"query": query,
		})
		if query == "" {
			return nil, fmt.Errorf("query parameter is required")
		}
		return map[string]any{
			"parsed_query": query,
		}, nil
	})

	eng.MustRegisterLogic("check_cache", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		ctx.Logger.Info("Checking cache", inputs)
		parsedQuery, _ := inputs["parsed_query"].(string)
		ctx.Logger.Info("Cache check for query", map[string]any{
			"parsed_query": parsedQuery,
		})
		// Simulate cache miss for this example
		return map[string]any{
			"cache_hit":     false,
			"cached_result": nil,
		}, nil
	})

	eng.MustRegisterLogic("build_response", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
		return inputs, nil
	})
	log.Info("✅ Logic handlers registered", map[string]any{"list": eng.ListLogicHandlers()})
}

// createProviderRegistry creates and configures provider registry
func createProviderRegistry(cfg *types.Config, log types.Logger) *provider.Registry {
	log.Info("Creating provider registry", nil)

	providerReg := provider.NewRegistry(log)

	// Register all configured providers
	for _, prov := range cfg.Providers {
		if err := providerReg.RegisterConfig(prov); err != nil {
			log.Error("Failed to register provider", err, map[string]any{
				"provider": prov.Name,
			})
		} else {
			log.Info("Provider registered", map[string]any{
				"name": prov.Name,
				"type": prov.Type,
				"mode": prov.InitMode,
			})
		}
	}

	log.Info("✅ Provider registry created", map[string]any{
		"providers": len(cfg.Providers),
	})

	return providerReg
}
