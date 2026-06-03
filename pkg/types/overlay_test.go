package types

import (
	"context"
	"testing"
)

func TestParamOverlay_NilCtxAndOverlay(t *testing.T) {
	// nil is deliberately passed here: the accessors document
	// nil-safety, and callers in the wild may pre-allocate hooks
	// before the request ctx exists.
	//lint:ignore SA1012 testing documented nil-safety contract
	nilctx := context.TODO()
	if got := ParamOverlayFrom(nilctx); got != nil {
		t.Errorf("ParamOverlayFrom(nil) = %v, want nil", got)
	}
	//lint:ignore SA1012 testing documented nil-safety contract
	if got := ParamOverlayForTask(nilctx, "x"); got != nil {
		t.Errorf("ParamOverlayForTask(nil, …) = %v, want nil", got)
	}
	ctx := context.Background()
	if got := ParamOverlayFrom(ctx); got != nil {
		t.Errorf("ParamOverlayFrom(empty ctx) = %v, want nil", got)
	}
}

func TestParamOverlay_WithAndForTask(t *testing.T) {
	overlay := ParamOverlay{
		"":            {"eligible": true, "shared": "wildcard"},
		"image_check": {"eligible": false, "specific": 1},
	}
	ctx := WithParamOverlay(context.Background(), overlay)

	// Wildcard-only task: just the wildcard entries.
	got := ParamOverlayForTask(ctx, "other_task")
	if v, ok := got["eligible"].(bool); !ok || v != true {
		t.Errorf("other_task eligible = %v, want true", got["eligible"])
	}
	if v, ok := got["shared"].(string); !ok || v != "wildcard" {
		t.Errorf("other_task shared = %v, want wildcard", got["shared"])
	}

	// Specific task: specific wins over wildcard for shared keys, both
	// contribute non-overlapping keys.
	got = ParamOverlayForTask(ctx, "image_check")
	if v, ok := got["eligible"].(bool); !ok || v != false {
		t.Errorf("image_check eligible = %v, want false (specific wins)", got["eligible"])
	}
	if v, ok := got["shared"].(string); !ok || v != "wildcard" {
		t.Errorf("image_check shared = %v, want wildcard (inherited)", got["shared"])
	}
	if v, ok := got["specific"].(int); !ok || v != 1 {
		t.Errorf("image_check specific = %v, want 1", got["specific"])
	}
}

func TestParamOverlay_NilOverlayIsNoOp(t *testing.T) {
	parent := context.Background()
	ctx := WithParamOverlay(parent, nil)
	// WithParamOverlay(nil) must not attach a value, otherwise readers
	// see a typed-nil and a later overlay can't override it.
	if got := ParamOverlayFrom(ctx); got != nil {
		t.Errorf("WithParamOverlay(nil) attached %v, want nothing", got)
	}
}
