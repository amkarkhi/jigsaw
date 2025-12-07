package main

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/logger"
	"github.com/amkarkhi/jigsaw/pkg/ui"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

// uiCmd provides UI commands
func uiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Launch configuration UI",
		Long:  "Launch a user interface (TUI or Web) to manage Jigsaw configuration",
	}

	cmd.AddCommand(tuiCmd())
	cmd.AddCommand(webCmd())

	return cmd
}

// tuiCmd launches the terminal UI
func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch Terminal UI",
		Long:  "Launch an interactive terminal user interface to browse and manage configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create logger
			log := logger.New(logLevel, false)

			// Load configuration
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Validate
			val := validator.New(log)
			if err := val.ValidateConfig(cfg); err != nil {
				return fmt.Errorf("invalid configuration: %w", err)
			}

			// Create engine to get logic registry
			eng := engine.New(cfg, val, log)
			logicRegistry := eng.ListLogicHandlers()

			// Create TUI
			tui := ui.NewTUI(cfg, logicRegistry)

			// Run TUI
			p := tea.NewProgram(tui, tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("failed to run TUI: %w", err)
			}

			return nil
		},
	}
}

// webCmd launches the web UI
func webCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Launch Web UI",
		Long:  "Launch a web-based user interface to browse and manage configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create logger
			log := logger.New(logLevel, pretty)

			// Load configuration
			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Validate
			val := validator.New(log)
			if err := val.ValidateConfig(cfg); err != nil {
				return fmt.Errorf("invalid configuration: %w", err)
			}

			// Create engine to get logic registry
			eng := engine.New(cfg, val, log)
			logicRegistry := eng.ListLogicHandlers()

			// Create Web UI
			webUI := ui.NewWebUI(cfg, logicRegistry, log)

			fmt.Printf("\n🌐 Jigsaw Web UI\n")
			fmt.Printf("   URL: http://localhost:%d\n", port)
			fmt.Printf("   Press Ctrl+C to stop\n\n")

			// Start Web UI
			if err := webUI.Start(port); err != nil {
				return fmt.Errorf("failed to start web UI: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 3000, "Web UI port")

	return cmd
}
