package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/amkarkhi/jigsaw/pkg/dashboard"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// jigsaw user … and jigsaw token …
//
// All management commands require the master key. We read it from
// JIGSAW_MASTER_KEY by default (so it can sit in your prod .env), or via
// --master-key. The key gates the operation only; it isn't used to encrypt
// the file. Passwords are bcrypted, tokens are stored as sha256 hashes.

const envMasterKey = "JIGSAW_MASTER_KEY"

func userCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage dashboard users (server-mode auth)",
	}
	cmd.AddCommand(userInitCmd())
	cmd.AddCommand(userCreateCmd())
	cmd.AddCommand(userListCmd())
	cmd.AddCommand(userDeleteCmd())
	return cmd
}

func tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API bearer tokens (server-mode auth)",
	}
	cmd.AddCommand(tokenCreateCmd())
	cmd.AddCommand(tokenListCmd())
	cmd.AddCommand(tokenRevokeCmd())
	return cmd
}

// ----- user init ---------------------------------------------------------

func userInitCmd() *cobra.Command {
	var masterKey string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the auth file and set the master key",
		Long: `Initializes <config>/.jigsaw/auth.json. The master key is the secret
that gates further user/token management; store it in your production
.env as JIGSAW_MASTER_KEY.

If --master-key is omitted, you'll be prompted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if masterKey == "" {
				masterKey = os.Getenv(envMasterKey)
			}
			if masterKey == "" {
				k, err := promptHidden("Choose master key (min 16 chars): ")
				if err != nil {
					return err
				}
				masterKey = k
				k2, err := promptHidden("Confirm master key: ")
				if err != nil {
					return err
				}
				if k != k2 {
					return fmt.Errorf("master keys did not match")
				}
			}
			if err := dashboard.InitAuthFile(configPath, masterKey); err != nil {
				return err
			}
			fmt.Println("✓ wrote auth file at", configPath+"/.jigsaw/auth.json")
			fmt.Println("  store the master key in $" + envMasterKey + " for future commands.")
			return nil
		},
	}
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

// ----- user create -------------------------------------------------------

func userCreateCmd() *cobra.Command {
	var (
		username, password, role, masterKey string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a dashboard user",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if username == "" {
				username, _ = prompt("Username: ")
			}
			if password == "" {
				p, err := promptHidden("Password: ")
				if err != nil {
					return err
				}
				password = p
			}
			if role == "" {
				role, _ = prompt("Role [admin/viewer] (default viewer): ")
				if role == "" {
					role = "viewer"
				}
			}
			if err := af.CreateUser(username, password, role); err != nil {
				return err
			}
			if err := dashboard.SaveAuthFile(configPath, af); err != nil {
				return err
			}
			fmt.Printf("✓ created user %q (%s)\n", username, role)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "Username")
	cmd.Flags().StringVar(&password, "password", "", "Password (omit to be prompted)")
	cmd.Flags().StringVar(&role, "role", "", "Role: admin or viewer")
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

// ----- user list ---------------------------------------------------------

func userListCmd() *cobra.Command {
	var masterKey string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List dashboard users",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if len(af.Users) == 0 {
				fmt.Println("(no users)")
				return nil
			}
			fmt.Printf("%-24s  %-8s  %s\n", "USERNAME", "ROLE", "CREATED")
			for _, u := range af.Users {
				fmt.Printf("%-24s  %-8s  %s\n", u.Username, u.Role, u.CreatedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

// ----- user delete -------------------------------------------------------

func userDeleteCmd() *cobra.Command {
	var (
		username, masterKey string
	)
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a dashboard user",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if username == "" {
				return fmt.Errorf("--username required")
			}
			if !af.DeleteUser(username) {
				return fmt.Errorf("user %q not found", username)
			}
			if err := dashboard.SaveAuthFile(configPath, af); err != nil {
				return err
			}
			fmt.Println("✓ deleted user", username)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "Username to delete")
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

// ----- token create / list / revoke --------------------------------------

func tokenCreateCmd() *cobra.Command {
	var (
		name, role, masterKey string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API bearer token",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--name required (e.g. 'ci-bot')")
			}
			if role == "" {
				role = "viewer"
			}
			token, err := af.CreateToken(name, role)
			if err != nil {
				return err
			}
			if err := dashboard.SaveAuthFile(configPath, af); err != nil {
				return err
			}
			fmt.Println("✓ created token", name)
			fmt.Println()
			fmt.Println("  ", token)
			fmt.Println()
			fmt.Println("Copy it now — it will not be shown again.")
			fmt.Println("Send as: Authorization: Bearer <token>")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Friendly token name (e.g. 'ci-bot')")
	cmd.Flags().StringVar(&role, "role", "viewer", "Role: admin or viewer")
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

func tokenListCmd() *cobra.Command {
	var masterKey string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens (names only; values are hashed and not recoverable)",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if len(af.Tokens) == 0 {
				fmt.Println("(no tokens)")
				return nil
			}
			fmt.Printf("%-24s  %-8s  %s\n", "NAME", "ROLE", "CREATED")
			for _, t := range af.Tokens {
				fmt.Printf("%-24s  %-8s  %s\n", t.Name, t.Role, t.CreatedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

func tokenRevokeCmd() *cobra.Command {
	var (
		name, masterKey string
	)
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke an API token by name",
		RunE: func(cmd *cobra.Command, args []string) error {
			af, err := requireMasterKey(masterKey)
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--name required")
			}
			if !af.RevokeToken(name) {
				return fmt.Errorf("token %q not found", name)
			}
			if err := dashboard.SaveAuthFile(configPath, af); err != nil {
				return err
			}
			fmt.Println("✓ revoked token", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Token name to revoke")
	cmd.Flags().StringVar(&masterKey, "master-key", "", "Master key (also via $JIGSAW_MASTER_KEY)")
	return cmd
}

// ----- shared helpers ----------------------------------------------------

// requireMasterKey loads the auth file, demands the master key (flag or env
// or prompt), verifies it, and returns the AuthFile ready for mutation.
func requireMasterKey(flagVal string) (*dashboard.AuthFile, error) {
	af, err := dashboard.LoadAuthFile(configPath)
	if err != nil {
		return nil, err
	}
	if af == nil {
		return nil, fmt.Errorf("no auth file at %s/.jigsaw/auth.json — run `jigsaw user init` first", configPath)
	}
	key := flagVal
	if key == "" {
		key = os.Getenv(envMasterKey)
	}
	if key == "" {
		k, err := promptHidden("Master key: ")
		if err != nil {
			return nil, err
		}
		key = k
	}
	if !af.VerifyMasterKey(key) {
		return nil, fmt.Errorf("invalid master key")
	}
	return af, nil
}

func prompt(label string) (string, error) {
	fmt.Print(label)
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptHidden(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
