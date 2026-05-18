package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/provider"
	"github.com/amkarkhi/jigsaw/pkg/server"
	"github.com/amkarkhi/jigsaw/pkg/validator"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

func newLogger(level string, pretty bool) zerolog.Logger {
	var output io.Writer = os.Stdout
	if pretty {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(output).Level(lvl).With().Timestamp().Caller().Logger()
}

var (
	configPath string
	port       int
	logLevel   string
	pretty     bool
	hotReload  bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "jigsaw",
		Short: "Jigsaw - A modular task orchestration engine",
		Long: `Jigsaw is a configuration-driven workflow orchestration framework
that allows you to build complex data pipelines and business logic flows
using simple YAML configurations.`,
	}
	
	// Global flags
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "./configs", "Path to configuration directory")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVar(&pretty, "pretty", false, "Pretty print logs")
	
	// Add commands
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(validateCmd())
	rootCmd.AddCommand(listCmd())
	rootCmd.AddCommand(describeCmd())
	rootCmd.AddCommand(testCmd())
	rootCmd.AddCommand(uiCmd())
	rootCmd.AddCommand(checkCmd())
	rootCmd.AddCommand(fmtCmd())
	rootCmd.AddCommand(dumpSymbolsCmd())
	rootCmd.AddCommand(lspCmd())
	rootCmd.AddCommand(dashboardCmd())
	rootCmd.AddCommand(userCmd())
	rootCmd.AddCommand(tokenCmd())
	
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// serveCmd starts the HTTP server
func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Jigsaw HTTP server",
		Long:  "Starts the Jigsaw HTTP server to handle flow execution requests",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create logger
			log := newLogger(logLevel, pretty)
			
			log.Info().Str("config", configPath).Int("port", port).Msg("Starting Jigsaw server")
			
			// Load configuration
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			
			// Validate configuration
			val := validator.New(log)
			if err := val.ValidateConfig(cfg); err != nil {
				return fmt.Errorf("invalid configuration: %w", err)
			}
			
			// Create server
			opts := server.Options{
				Port:      port,
				HotReload: hotReload,
				LogLevel:  logLevel,
				Pretty:    pretty,
			}
			
			srv := server.New(cfg, log, opts)
			
			// Setup graceful shutdown
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			
			// Start server in goroutine
			errChan := make(chan error, 1)
			go func() {
				if err := srv.Start(port, configPath); err != nil {
					errChan <- err
				}
			}()
			
			// Wait for shutdown signal or error
			select {
			case <-sigChan:
				log.Info().Msg("Shutdown signal received")
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*1000000000) // 30 seconds
				defer shutdownCancel()
				return srv.Stop(shutdownCtx)
			case err := <-errChan:
				return err
			}
		},
	}
	
	cmd.Flags().IntVar(&port, "port", 8080, "HTTP server port")
	cmd.Flags().BoolVar(&hotReload, "reload", true, "Enable hot-reload of configurations")
	
	return cmd
}

// validateCmd validates configuration files
func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration files",
		Long:  "Validates all configuration files for syntax and logic errors",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, pretty)
			
			fmt.Printf("Validating configuration in: %s\n", configPath)
			
			// Load configuration
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			
			// Validate
			val := validator.New(log)
			if err := val.ValidateConfig(cfg); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			
			fmt.Printf("\n✓ Configuration is valid\n")
			fmt.Printf("  Tasks:     %d\n", len(cfg.Tasks))
			fmt.Printf("  Flows:     %d\n", len(cfg.Flows))
			fmt.Printf("  Providers: %d\n", len(cfg.Providers))
			fmt.Printf("  Endpoints: %d\n", len(cfg.Endpoints))
			
			return nil
		},
	}
}

// listCmd lists available resources
func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available resources",
		Long:  "Lists all available flows, tasks, providers, or endpoints",
	}
	
	cmd.AddCommand(listFlowsCmd())
	cmd.AddCommand(listTasksCmd())
	cmd.AddCommand(listProvidersCmd())
	cmd.AddCommand(listEndpointsCmd())
	
	return cmd
}

func listFlowsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "flows",
		Short: "List all flows",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			fmt.Printf("Available Flows (%d):\n\n", len(cfg.Flows))
			for name, flow := range cfg.Flows {
				fmt.Printf("  • %s\n", name)
				if flow.Description != "" {
					fmt.Printf("    %s\n", flow.Description)
				}
				fmt.Printf("    Tasks: %d\n", len(flow.Tasks))
				if flow.Inherits != "" {
					fmt.Printf("    Inherits: %s\n", flow.Inherits)
				}
				fmt.Println()
			}
			
			return nil
		},
	}
}

func listTasksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tasks",
		Short: "List all tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			fmt.Printf("Available Tasks (%d):\n\n", len(cfg.Tasks))
			for name, task := range cfg.Tasks {
				fmt.Printf("  • %s\n", name)
				if task.Description != "" {
					fmt.Printf("    %s\n", task.Description)
				}
				if task.Provider != "" {
					fmt.Printf("    Provider: %s\n", task.Provider)
				}
				fmt.Printf("    Params: %d\n", len(task.Params))
				if task.Inherits != "" {
					fmt.Printf("    Inherits: %s\n", task.Inherits)
				}
				fmt.Println()
			}
			
			return nil
		},
	}
}

func listProvidersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List all providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			fmt.Printf("Available Providers (%d):\n\n", len(cfg.Providers))
			for name, prov := range cfg.Providers {
				fmt.Printf("  • %s\n", name)
				fmt.Printf("    Type: %s\n", prov.Type)
				fmt.Printf("    Init Mode: %s\n", prov.InitMode)
				if prov.PoolSize > 0 {
					fmt.Printf("    Pool Size: %d\n", prov.PoolSize)
				}
				fmt.Println()
			}
			
			return nil
		},
	}
}

func listEndpointsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "endpoints",
		Short: "List all endpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			fmt.Printf("Available Endpoints (%d):\n\n", len(cfg.Endpoints))
			for name, endpoint := range cfg.Endpoints {
				fmt.Printf("  • %s\n", name)
				fmt.Printf("    %s %s\n", endpoint.Method, endpoint.Path)
				if endpoint.Description != "" {
					fmt.Printf("    %s\n", endpoint.Description)
				}
				fmt.Printf("    Flow Mappings: %d\n", len(endpoint.Flows))
				for _, mapping := range endpoint.Flows {
					fmt.Printf("      sub=%d → %s\n", mapping.Sub, mapping.FlowName)
				}
				fmt.Println()
			}
			
			return nil
		},
	}
}

// describeCmd describes a specific resource
func describeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe a specific resource",
		Long:  "Shows detailed information about a flow or task",
	}
	
	cmd.AddCommand(describeFlowCmd())
	cmd.AddCommand(describeTaskCmd())
	
	return cmd
}

func describeFlowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "flow [name]",
		Short: "Describe a flow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			flowName := args[0]
			flow, ok := cfg.Flows[flowName]
			if !ok {
				return fmt.Errorf("flow '%s' not found", flowName)
			}
			
			data, _ := json.MarshalIndent(flow, "", "  ")
			fmt.Println(string(data))
			
			return nil
		},
	}
}

func describeTaskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task [name]",
		Short: "Describe a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, false)
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			taskName := args[0]
			task, ok := cfg.Tasks[taskName]
			if !ok {
				return fmt.Errorf("task '%s' not found", taskName)
			}
			
			data, _ := json.MarshalIndent(task, "", "  ")
			fmt.Println(string(data))
			
			return nil
		},
	}
}

// testCmd tests flow execution
func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test flow execution",
		Long:  "Tests a flow or task with sample input",
	}
	
	cmd.AddCommand(testFlowCmd())
	
	return cmd
}

func testFlowCmd() *cobra.Command {
	var (
		inputJSON string
		sub       int
	)
	
	cmd := &cobra.Command{
		Use:   "flow [name]",
		Short: "Test a flow execution",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, pretty)
			
			// Load configuration
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return err
			}
			
			// Validate
			val := validator.New(log)
			if err := val.ValidateConfig(cfg); err != nil {
				return err
			}
			
			// Create engine
			eng := engine.New(cfg, val, log)
			
			// Create provider registry
			providerReg := provider.NewRegistry(log)
			for _, prov := range cfg.Providers {
				providerReg.RegisterConfig(prov)
			}
			
			// Parse input
			var params map[string]any
			if inputJSON != "" {
				if err := json.Unmarshal([]byte(inputJSON), &params); err != nil {
					return fmt.Errorf("invalid input JSON: %w", err)
				}
			} else {
				params = make(map[string]any)
			}
			
			flowName := args[0]
			
			fmt.Printf("Testing flow: %s\n", flowName)
			fmt.Printf("Sub: %d\n", sub)
			fmt.Printf("Input: %s\n\n", inputJSON)
			
			// Execute flow
			result, err := eng.ExecuteFlow(
				context.Background(),
				flowName,
				sub,
				params,
				make(map[string]string),
				providerReg,
			)
			
			if err != nil {
				return fmt.Errorf("flow execution failed: %w", err)
			}
			
			// Print result
			resultJSON, _ := json.MarshalIndent(result, "", "  ")
			fmt.Printf("Result:\n%s\n", string(resultJSON))
			
			return nil
		},
	}
	
	cmd.Flags().StringVar(&inputJSON, "input", "{}", "Input parameters as JSON")
	cmd.Flags().IntVar(&sub, "sub", 1, "Sub parameter")
	
	return cmd
}
