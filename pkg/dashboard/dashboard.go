// Package dashboard implements the Jigsaw configuration manager dashboard:
// a single-page web app for browsing (and, in later phases, editing) a config
// tree.
//
// The package is the long-term home for the manager. The older pkg/ui/webui
// will be retired once feature parity is reached.
package dashboard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Mode controls how the dashboard treats writes.
type Mode int

const (
	// ModeLocal is the developer-on-laptop default: bypass auth, writes go to
	// the on-disk config tree.
	ModeLocal Mode = iota
	// ModeServer is the hosted variant: auth required, no in-place writes;
	// save produces a downloadable bundle.
	ModeServer
)

// Role classifies what an authenticated identity may do.
type Role int

const (
	RoleViewer Role = iota
	RoleAdmin
)

// Identity is the result of authenticating a request.
type Identity struct {
	Label string
	Role  Role
	// Access lists the resource types this identity may modify, e.g.
	// "flows", "tasks", "providers", "endpoints". Admins implicitly have
	// access to every resource regardless of this list. Viewers have no
	// edit access — Access is ignored for them.
	Access []string
}

// AllResources is the canonical resource-type catalogue used by the
// per-user access list. Admin identities implicitly hold all of these.
var AllResources = []string{"flows", "tasks", "providers", "endpoints"}

// HasAccess reports whether the identity may modify the given resource type.
// Admins always have access; viewers never do; editor-style identities have
// access only to the resources explicitly listed in Access.
func (i Identity) HasAccess(resource string) bool {
	if i.Role == RoleAdmin {
		return true
	}
	for _, a := range i.Access {
		if a == resource {
			return true
		}
	}
	return false
}

// AuthProvider lets the consumer plug in their own auth. Returning a non-nil
// error denies the request with 401. Local mode never calls into this.
type AuthProvider interface {
	Authenticate(r *http.Request) (Identity, error)
}

// Options configures a Dashboard.
type Options struct {
	// ConfigPath is the root of the config tree the dashboard reads (and, in
	// ModeLocal, writes back to).
	ConfigPath string

	// Mode controls write semantics. Defaults to ModeLocal.
	Mode Mode

	// Listen is the TCP address to bind, e.g. "127.0.0.1:3000". Required
	// unless the dashboard is mounted on a caller-supplied http.Handler.
	Listen string

	// AllowRemote suppresses the safety check that refuses non-loopback binds.
	// Required when Listen is something like "0.0.0.0:3000".
	AllowRemote bool

	// Auth is required in ModeServer. Nil in ModeLocal.
	Auth AuthProvider

	// GitLabOAuth, when fully populated, enables GitLab SSO as an
	// alternative to username+password login. Requires Auth to be a
	// *FileAuth (the bundled provider). Leave nil/empty to disable.
	GitLabOAuth *GitLabOAuthConfig

	// Logger receives operational logs (start, denied requests).
	Logger zerolog.Logger

	// Edit enables mutating endpoints. Phase 5 is read-only by default.
	Edit bool

	// ServiceName, if set, is shown in the dashboard footer instead of the
	// full config path. Use this when ConfigPath is long or sensitive.
	ServiceName string

	// Playground enables the in-browser flow playground (page + API). The
	// playground executes flows against user-supplied inputs in a sandbox,
	// which can be surprising in production deployments — it's off by
	// default and must be explicitly enabled.
	Playground bool
}

// Dashboard wires the HTTP handler. Use New() to construct.
type Dashboard struct {
	opts Options
	mux  *http.ServeMux
}

// New constructs a Dashboard and validates options.
//
// New is designed to never return a nil Dashboard, even on error. Callers
// who ignore the error and hand the dashboard to an http server will see
// a clear 503 on every request rather than a nil-pointer panic. We still
// return the error so well-behaved callers can fail fast.
func New(opts Options) (*Dashboard, error) {
	if opts.ConfigPath == "" {
		return degradedDashboard(opts, "ConfigPath is required"), fmt.Errorf("ConfigPath is required")
	}

	if opts.Mode == ModeServer && opts.Auth == nil {
		opts.Logger.Error().Msg("dashboard: ModeServer requires Auth")
		return degradedDashboard(opts, "ModeServer requires Auth to be configured"), fmt.Errorf(
			"ModeServer requires Auth to be configured",
		)
	}

	if opts.Listen != "" {
		host, _, err := net.SplitHostPort(opts.Listen)
		if err != nil {
			return degradedDashboard(opts, "invalid Listen"), fmt.Errorf("invalid Listen %q: %w", opts.Listen, err)
		}
		if !isLoopback(host) && !opts.AllowRemote {
			msg := fmt.Sprintf("refusing to bind to non-loopback host %q without AllowRemote=true", host)
			return degradedDashboard(opts, msg), fmt.Errorf("%s", msg)
		}
	}

	d := &Dashboard{opts: opts, mux: http.NewServeMux()}
	d.routes()
	return d, nil
}

// degradedDashboard returns a Dashboard whose handler responds with a
// permanent 503 carrying `reason`. Used when New encounters an invalid
// configuration but the caller doesn't fail-fast on the returned error.
func degradedDashboard(opts Options, reason string) *Dashboard {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "dashboard misconfigured: "+reason, http.StatusServiceUnavailable)
	})
	return &Dashboard{opts: opts, mux: mux}
}

// Handler returns the dashboard's HTTP handler so a consumer can mount it
// under their own router, with their own middleware.
func (d *Dashboard) Handler() http.Handler {
	return d
}

// ServeHTTP makes Dashboard itself an http.Handler. We wrap the mux with the
// auth and access-log middleware so consumers who mount it directly still get
// the right behavior.
func (d *Dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.withMiddleware(d.mux).ServeHTTP(w, r)
}

// ListenAndServe binds to opts.Listen and serves until ctx is cancelled.
func (d *Dashboard) ListenAndServe(ctx context.Context) error {
	if d.opts.Listen == "" {
		return fmt.Errorf("Listen address is empty")
	}
	srv := &http.Server{
		Addr:              d.opts.Listen,
		Handler:           d.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	d.opts.Logger.Info().Str("listen", d.opts.Listen).Str("mode", d.opts.Mode.String()).Bool("edit", d.opts.Edit).Msg("dashboard.start")
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// String renders a Mode for logging.
func (m Mode) String() string {
	switch m {
	case ModeLocal:
		return "local"
	case ModeServer:
		return "server"
	default:
		return "unknown"
	}
}

func isLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
