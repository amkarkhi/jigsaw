package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/logger"
	"github.com/amkarkhi/jigsaw/pkg/symbols"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/spf13/cobra"
)

// dumpSymbolsCmd writes a symbols manifest for the current configuration.
//
// Note: the standalone jigsaw binary doesn't import user code, so the logic
// section will be empty. The interesting consumer is a downstream binary
// that embeds jigsaw and registers handlers — that binary should call
// symbols.BuildFromEngine + symbols.Write itself, typically behind its own
// --dump-symbols flag. This subcommand exists so the workflow can still be
// driven end-to-end with the stock CLI (provider list, schema check, etc.).
func dumpSymbolsCmd() *cobra.Command {
	var (
		output string
	)

	cmd := &cobra.Command{
		Use:   "dump-symbols",
		Short: "Write a symbols manifest from the current configuration",
		Long: `Writes a JSON manifest describing the providers in the config tree
and the logic handlers registered in the running binary.

The standalone jigsaw binary has no user-registered logic, so the logic
section will be empty when run via the CLI. Consumer binaries that embed
jigsaw should call symbols.BuildFromEngine + symbols.Write directly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logger.New("error", false)

			loader := config.NewLoader(log)
			cfg, err := loader.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			eng := engine.New(cfg, validator.New(log), log)
			m := symbols.BuildFromEngine(eng, cfg, "jigsaw")

			if output == "" {
				output = filepath.Join(configPath, symbols.DefaultManifestPath)
			}
			if err := symbols.Write(output, m); err != nil {
				return err
			}
			fmt.Printf("wrote manifest: %s\n", output)
			fmt.Printf("  logic:     %d\n", len(m.Logic))
			fmt.Printf("  providers: %d\n", len(m.Providers))

			// If the config references logic handlers but the manifest has
			// none, the standalone CLI can't see them — they live in the
			// consumer's binary. Make this explicit so the user doesn't
			// silently get half a manifest.
			if len(m.Logic) == 0 && configReferencesLogic(cfg) {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, "⚠  No logic handlers captured. The standalone `jigsaw` CLI doesn't import")
				fmt.Fprintln(os.Stderr, "   your code, so it can't see the handlers you register at runtime.")
				fmt.Fprintln(os.Stderr, "   To capture them, call this one-liner from your own main:")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, `       import "github.com/amkarkhi/jigsaw/pkg/symbols"`)
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "       if *dumpSymbols {")
				fmt.Fprintln(os.Stderr, `           _ = symbols.DumpToFile(eng, cfg, configPath, "myapp")`)
				fmt.Fprintln(os.Stderr, "           return")
				fmt.Fprintln(os.Stderr, "       }")
				fmt.Fprintln(os.Stderr, "")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output path (default: <config>/.jigsaw/symbols.json)")
	return cmd
}

// configReferencesLogic reports whether any task in the loaded config declares
// a logic handler. Used to decide whether to print the "no logic captured"
// warning on `jigsaw dump-symbols`.
func configReferencesLogic(cfg *types.Config) bool {
	for _, t := range cfg.Tasks {
		if t.Logic != "" {
			return true
		}
	}
	return false
}
