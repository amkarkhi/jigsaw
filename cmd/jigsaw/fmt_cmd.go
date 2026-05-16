package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/amkarkhi/jigsaw/pkg/configlang"
	"github.com/spf13/cobra"
)

// fmtCmd applies the canonical formatter to every config file under configPath.
// Comments and key order are preserved by the underlying yaml.v3 Node round-trip.
func fmtCmd() *cobra.Command {
	var (
		check bool
	)

	cmd := &cobra.Command{
		Use:   "fmt",
		Short: "Format configuration files",
		Long: `Rewrites every config file under --config in canonical style (2-space indent).
Comments and key order from the original are preserved.

Use --check to exit non-zero if any file would be changed, without writing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var changed []string

			err := configlang.WalkTree(configPath, func(path string) error {
				file, err := configlang.LoadFile(path)
				if err != nil {
					return err
				}
				out, err := configlang.Format(file)
				if err != nil {
					return err
				}
				original, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Equal(original, out) {
					return nil
				}
				changed = append(changed, path)
				if check {
					return nil
				}
				// Atomic-ish write: write tmp then rename.
				tmp := path + ".jigfmt.tmp"
				if err := os.WriteFile(tmp, out, 0o644); err != nil {
					return err
				}
				return os.Rename(tmp, path)
			})
			if err != nil {
				return err
			}

			for _, p := range changed {
				if check {
					fmt.Println("would format:", p)
				} else {
					fmt.Println("formatted:", p)
				}
			}
			if check && len(changed) > 0 {
				os.Exit(1)
			}
			if len(changed) == 0 {
				fmt.Println("ok: already canonical")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Report files that would be changed; exit non-zero if any")
	return cmd
}
