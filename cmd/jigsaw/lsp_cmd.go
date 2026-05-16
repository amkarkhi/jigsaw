package main

import (
	"os"

	"github.com/amkarkhi/jigsaw/pkg/lsp"
	"github.com/spf13/cobra"
)

// lspCmd runs the Jigsaw LSP over stdio. Editors that speak LSP (VS Code,
// neovim, helix, emacs lsp-mode) attach to this command and receive
// diagnostics for the workspace's config tree.
func lspCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lsp",
		Short: "Run the Jigsaw language server over stdio",
		Long: `Speaks LSP on stdin/stdout. Editors should launch this command as
the language server for *.jig.yml / *.jig.yaml (or *.yml under a Jigsaw
config root). The server runs configlang.Check over the workspace and
publishes diagnostics on every open/change/save.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lsp.NewServer(os.Stdin, os.Stdout).Serve()
		},
	}
}
