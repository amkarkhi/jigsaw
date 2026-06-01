package types

import (
	"context"
	"maps"
)

// ParamOverlay is the host-supplied per-task parameter overlay applied
// to every task in the running flow. The outer key is the task name;
// the inner map is shallow-merged on top of (task defaults + flow-ref
// overrides), last write wins.
//
// Use the empty string as the task name to overlay every task
// indiscriminately. Per-task entries take precedence over the wildcard.
//
// Typical use: a host installs a request-time hook (FlowResolver on the
// server, PlaygroundPreExecute on the dashboard) that derives an
// overlay from a rule engine and attaches it via WithParamOverlay. The
// engine reads it back while computing task params.
type ParamOverlay map[string]map[string]any

type paramOverlayKey struct{}

// WithParamOverlay attaches an overlay to ctx. Nil overlays are
// ignored so callers can install unconditionally.
func WithParamOverlay(ctx context.Context, overlay ParamOverlay) context.Context {
	if overlay == nil {
		return ctx
	}
	return context.WithValue(ctx, paramOverlayKey{}, overlay)
}

// ParamOverlayFrom returns the overlay attached to ctx, or nil.
func ParamOverlayFrom(ctx context.Context) ParamOverlay {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(paramOverlayKey{}).(ParamOverlay)
	return o
}

// ParamOverlayForTask returns the merged overlay for the named task:
// the task-specific overlay shallow-merged on top of the wildcard
// overlay (key ""). Returns nil when no overlay is attached or no
// entries apply to this task.
func ParamOverlayForTask(ctx context.Context, taskName string) map[string]any {
	o := ParamOverlayFrom(ctx)
	if o == nil {
		return nil
	}
	wild := o[""]
	specific := o[taskName]
	if wild == nil && specific == nil {
		return nil
	}
	out := make(map[string]any, len(wild)+len(specific))
	maps.Copy(out, wild)
	maps.Copy(out, specific)
	return out
}
