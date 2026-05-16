package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/configlang"
	"github.com/amkarkhi/jigsaw/pkg/logger"
	"github.com/amkarkhi/jigsaw/pkg/symbols"
	"github.com/spf13/cobra"
)

// checkCmd runs all diagnostics over a config tree.
// Equivalent to what the LSP and the dashboard's "Pending changes" panel emit.
func checkCmd() *cobra.Command {
	var (
		format       string
		manifestPath string
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run diagnostics over the configuration tree",
		Long: `Runs the full validator over the configuration and prints any errors or warnings.

If a symbols manifest is found (default: <config>/.jigsaw/symbols.json), the
checker additionally warns on tasks that reference logic handlers not in the
manifest. Override the path with --manifest, or pass --manifest='' to skip.

Exits with status 1 if any error-severity diagnostics are produced.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logger.New("error", false) // quiet — only diagnostics in stdout

			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return printResult([]configlang.Diagnostic{{
					Severity: configlang.SeverityError,
					Message:  fmt.Sprintf("load: %v", err),
				}}, format)
			}

			// Resolve manifest:
			//   ""     → disabled, no logic-handler cross-check
			//   "auto" → default location, silently absent is fine
			//   other  → explicit path, missing file is a hard error
			var loaded *symbols.Manifest
			switch manifestPath {
			case "":
				// disabled
			case "auto":
				m, err := symbols.Read(filepath.Join(configPath, symbols.DefaultManifestPath))
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				}
				loaded = m
			default:
				m, err := symbols.Read(manifestPath)
				if err != nil {
					return fmt.Errorf("read manifest: %w", err)
				}
				if m == nil {
					return fmt.Errorf("manifest not found: %s", manifestPath)
				}
				loaded = m
			}

			checkOpts := configlang.CheckOptions{}
			if loaded != nil {
				specs := make([]configlang.LogicSpec, len(loaded.Logic))
				for i, l := range loaded.Logic {
					specs[i] = configlang.LogicSpec{
						Name:         l.Name,
						InputSchema:  l.InputSchema,
						OutputSchema: l.OutputSchema,
					}
				}
				checkOpts.LogicRegistry = specs
				checkOpts.RegistryProvided = true
				if age := loaded.Age(); age > 24*time.Hour {
					fmt.Fprintf(os.Stderr, "warning: symbols manifest is %s old\n", age.Truncate(time.Hour))
				}
			}

			diags := configlang.Check(cfg, checkOpts)
			return printResult(diags, format)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text | github")
	cmd.Flags().StringVar(&manifestPath, "manifest", "auto",
		"Symbols manifest path. 'auto' uses <config>/.jigsaw/symbols.json if present; '' disables.")
	return cmd
}

func printResult(diags []configlang.Diagnostic, format string) error {
	switch format {
	case "github":
		for _, d := range diags {
			level := "error"
			if d.Severity == configlang.SeverityWarning {
				level = "warning"
			}
			file := d.File
			if file == "" {
				file = "configs"
			}
			fmt.Printf("::%s file=%s::%s\n", level, file, d.Message)
		}
	default:
		for _, d := range diags {
			fmt.Println(d.String())
		}
	}

	errs, warns := configlang.Counts(diags)
	if format != "github" {
		if errs == 0 && warns == 0 {
			fmt.Println("ok: no diagnostics")
		} else {
			fmt.Fprintf(os.Stderr, "\n%d error(s), %d warning(s)\n", errs, warns)
		}
	}
	if errs > 0 {
		os.Exit(1)
	}
	return nil
}
