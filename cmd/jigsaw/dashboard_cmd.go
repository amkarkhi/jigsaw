package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/amkarkhi/jigsaw/pkg/dashboard"
	"github.com/spf13/cobra"
)

// dashboardCmd launches the new Phase-5+ configuration dashboard.
//
// This will eventually replace `jigsaw ui web`. For now it is a separate
// command so the older read-only UI stays available until feature parity.
func dashboardCmd() *cobra.Command {
	var (
		listen           string
		mode             string
		edit             bool
		allowRemote      bool
		adminTokens      []string
		viewTokens       []string
		serviceName      string
		gitlabBaseURL    string
		gitlabClientID   string
		gitlabSecret     string
		gitlabRedirect   string
		gitlabDefaultRol string
		playground       bool
	)

	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the configuration dashboard",
		Long: `Starts the Jigsaw configuration dashboard: a single-page web app for
browsing (and, in later phases, editing) the configuration tree.

Modes:
  --mode=local   (default) bypasses auth, writes go to ./configs in place.
  --mode=server  requires bearer-token auth and writes produce a downloadable
                 tar bundle instead of mutating files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger(logLevel, pretty)

			// Install the AES key used to seal per-user PATs at rest.
			// Empty disables the GitLab-push feature.
			dashboard.SetGitSecretKey(os.Getenv("JIGSAW_GIT_SECRET_KEY"))

			var m dashboard.Mode
			switch mode {
			case "", "local":
				m = dashboard.ModeLocal
			case "server":
				m = dashboard.ModeServer
			default:
				return fmt.Errorf("invalid --mode %q (want 'local' or 'server')", mode)
			}

			var auth dashboard.AuthProvider
			if m == dashboard.ModeServer {
				// Prefer the auth file when present — it's the modern path:
				// real users with passwords + bearer tokens, manageable via
				// `jigsaw user`/`jigsaw token`.
				fa := dashboard.NewFileAuth(configPath)
				if err := fa.EnsureInitialized(); err == nil {
					auth = fa
					log.Info().Msg("dashboard.auth_file_loaded")
				} else {
					// Fall back to ad-hoc tokens on flags / env. Kept for
					// compatibility and quick CI smoke tests.
					tokens := map[string]dashboard.TokenInfo{}
					for _, t := range collectTokens(adminTokens, "JIGSAW_ADMIN_TOKENS") {
						tokens[t] = dashboard.TokenInfo{Label: "admin", Role: dashboard.RoleAdmin}
					}
					for _, t := range collectTokens(viewTokens, "JIGSAW_VIEWER_TOKENS") {
						tokens[t] = dashboard.TokenInfo{Label: "viewer", Role: dashboard.RoleViewer}
					}
					if len(tokens) == 0 {
						return fmt.Errorf(
							"--mode=server requires either an initialized auth file (`jigsaw user init`) or --admin-token / $JIGSAW_ADMIN_TOKENS; auth file check: %v",
							err,
						)
					}
					auth = dashboard.BearerTokens(tokens)
				}
			}

			gl := &dashboard.GitLabOAuthConfig{
				BaseURL:      firstNonEmpty(gitlabBaseURL, os.Getenv("JIGSAW_GITLAB_BASE_URL")),
				ClientID:     firstNonEmpty(gitlabClientID, os.Getenv("JIGSAW_GITLAB_CLIENT_ID")),
				ClientSecret: firstNonEmpty(gitlabSecret, os.Getenv("JIGSAW_GITLAB_CLIENT_SECRET")),
				RedirectURL:  firstNonEmpty(gitlabRedirect, os.Getenv("JIGSAW_GITLAB_REDIRECT_URL")),
				DefaultRole:  firstNonEmpty(gitlabDefaultRol, os.Getenv("JIGSAW_GITLAB_DEFAULT_ROLE")),
			}
			if gl.Enabled() {
				log.Info().Str("base", gl.BaseURL).Msg("dashboard.gitlab_oauth_enabled")
			}

			d, err := dashboard.New(dashboard.Options{
				ConfigPath:  configPath,
				Mode:        m,
				Listen:      listen,
				AllowRemote: allowRemote,
				Edit:        edit,
				Auth:        auth,
				Logger:      log,
				ServiceName: serviceName,
				GitLabOAuth: gl,
				Playground:  playground || strings.EqualFold(os.Getenv("JIGSAW_PLAYGROUND"), "true") || os.Getenv("JIGSAW_PLAYGROUND") == "1",
			})
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			fmt.Printf("Jigsaw dashboard: http://%s\n", listen)
			return d.ListenAndServe(ctx)
		},
	}

	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:3300", "Bind address (host:port)")
	cmd.Flags().StringVar(&mode, "mode", "local", "Operating mode: local | server")
	cmd.Flags().BoolVar(&edit, "edit", false, "Enable mutating endpoints (Phase 6+)")
	cmd.Flags().BoolVar(&allowRemote, "allow-remote", false, "Permit binding to a non-loopback address")
	cmd.Flags().StringSliceVar(&adminTokens, "admin-token", nil, "Bearer token granting admin role (repeatable; also: JIGSAW_ADMIN_TOKENS comma-separated)")
	cmd.Flags().StringSliceVar(&viewTokens, "viewer-token", nil, "Bearer token granting viewer role (repeatable; also: JIGSAW_VIEWER_TOKENS comma-separated)")
	cmd.Flags().StringVar(&serviceName, "service", "", "Service name shown in the dashboard footer (defaults to config path)")
	cmd.Flags().StringVar(&gitlabBaseURL, "gitlab-base-url", "", "GitLab origin for SSO, e.g. https://gitlab.example.com (env: JIGSAW_GITLAB_BASE_URL)")
	cmd.Flags().StringVar(&gitlabClientID, "gitlab-client-id", "", "GitLab OAuth application client id (env: JIGSAW_GITLAB_CLIENT_ID)")
	cmd.Flags().StringVar(&gitlabSecret, "gitlab-client-secret", "", "GitLab OAuth application client secret (env: JIGSAW_GITLAB_CLIENT_SECRET)")
	cmd.Flags().StringVar(&gitlabRedirect, "gitlab-redirect-url", "", "OAuth redirect URL — must match the application's registered URI (env: JIGSAW_GITLAB_REDIRECT_URL)")
	cmd.Flags().StringVar(&gitlabDefaultRol, "gitlab-default-role", "viewer", "Role granted to users on first SSO login: admin|viewer (env: JIGSAW_GITLAB_DEFAULT_ROLE)")
	cmd.Flags().BoolVar(&playground, "playground", false, "Enable the in-browser flow playground page and API (env: JIGSAW_PLAYGROUND=true)")
	return cmd
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// collectTokens merges --flag values with the comma-separated contents of the
// given environment variable. Empty entries are dropped.
func collectTokens(flagVals []string, envVar string) []string {
	out := make([]string, 0, len(flagVals))
	for _, v := range flagVals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	for _, v := range strings.Split(os.Getenv(envVar), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
