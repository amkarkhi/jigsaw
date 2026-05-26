package engine

import (
	"testing"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

type seedIn struct {
	Q string `json:"q"`
}
type seedOut struct{}
type seedParams struct{}

type seedLogic struct{}

func (seedLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "seed_logic"} }
func (seedLogic) Run(_ *types.ExecutionContext, _ seedIn, _ seedParams) (*seedOut, error) {
	return &seedOut{}, nil
}

// TestValidatorSeedsScopeFromEndpointRequestParams verifies that a flow whose
// first task reads a required request parameter validates cleanly when an
// endpoint declares that parameter under `request_params`.
func TestValidatorSeedsScopeFromEndpointRequestParams(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t_seed": {Name: "t_seed", Logic: "seed_logic"},
		},
		Flows: map[string]*types.Flow{
			"f_seed": {Name: "f_seed", Tasks: []types.TaskRef{
				{Name: "t_seed", Bind: &types.Bind{In: map[string]string{"q": "query"}}},
			}},
		},
		Endpoints: map[string]*types.Endpoint{
			"search": {
				Name:          "search",
				Path:          "/api/search",
				Method:        "GET",
				RequestParams: []string{"query"},
				Flows:         []types.FlowMapping{{Sub: 1, FlowName: "f_seed"}},
			},
		},
	}

	eng := New(cfg, nil, zerolog.Nop())
	MustRegister(eng, seedLogic{})

	if err := eng.ValidateFlows(); err != nil {
		t.Errorf("ValidateFlows: expected nil with pre-seeded request_params, got %v", err)
	}
}

// TestValidatorStillFailsWithoutDeclaredParam confirms the validator only
// loosens once an endpoint actually declares the param — undeclared params
// still surface as errors (preserves today's behavior for un-migrated configs).
func TestValidatorStillFailsWithoutDeclaredParam(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t_seed": {Name: "t_seed", Logic: "seed_logic"},
		},
		Flows: map[string]*types.Flow{
			"f_seed": {Name: "f_seed", Tasks: []types.TaskRef{
				{Name: "t_seed", Bind: &types.Bind{In: map[string]string{"q": "query"}}},
			}},
		},
		Endpoints: map[string]*types.Endpoint{
			"search": {
				Name:   "search",
				Path:   "/api/search",
				Method: "GET",
				Flows:  []types.FlowMapping{{Sub: 1, FlowName: "f_seed"}},
			},
		},
	}

	eng := New(cfg, nil, zerolog.Nop())
	MustRegister(eng, seedLogic{})

	if err := eng.ValidateFlows(); err == nil {
		t.Error("expected error when endpoint omits request_params, got nil")
	}
}

// TestValidatorAlwaysSeedsFrameworkKeys verifies that `sub` and `tag` are in
// scope by default — they're set unconditionally by Engine.ExecuteFlow.
func TestValidatorAlwaysSeedsFrameworkKeys(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t_sub": {Name: "t_sub", Logic: "sub_logic"},
		},
		Flows: map[string]*types.Flow{
			"f_sub": {Name: "f_sub", Tasks: []types.TaskRef{
				{Name: "t_sub"},
			}},
		},
	}

	eng := New(cfg, nil, zerolog.Nop())
	MustRegister(eng, subLogicForTest{})

	if err := eng.ValidateFlows(); err != nil {
		t.Errorf("ValidateFlows: expected nil for sub/tag-only task, got %v", err)
	}
}

// TestValidatorSchemalessHandlerHonorsBindOut verifies that a task whose
// handler has no declared output schema (e.g. dynamic wrappers like
// cached_call with I/O = map[string]any) contributes the right-hand sides of
// its bind.out map to the simulated scope, so downstream tasks reading those
// keys validate cleanly.
func TestValidatorSchemalessHandlerHonorsBindOut(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"wrap":   {Name: "wrap", Logic: "schemaless_wrap"},
			"reader": {Name: "reader", Logic: "seed_logic"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{
				{Name: "wrap", Bind: &types.Bind{
					Out: map[string]string{"parsed_query": "pq"},
				}},
				{Name: "reader", Bind: &types.Bind{
					In: map[string]string{"q": "pq"},
				}},
			}},
		},
	}

	eng := New(cfg, nil, zerolog.Nop())
	MustRegister(eng, schemalessWrapLogic{})
	MustRegister(eng, seedLogic{})

	if err := eng.ValidateFlows(); err != nil {
		t.Errorf("ValidateFlows: expected nil for schemaless wrapper publishing via bind.out, got %v", err)
	}
}

type schemalessWrapIn map[string]any
type schemalessWrapOut map[string]any
type schemalessWrapParams struct{}

type schemalessWrapLogic struct{}

func (schemalessWrapLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "schemaless_wrap"} }
func (schemalessWrapLogic) Run(_ *types.ExecutionContext, in schemalessWrapIn, _ schemalessWrapParams) (*schemalessWrapOut, error) {
	out := schemalessWrapOut(in)
	return &out, nil
}

type subInForTest struct {
	Sub int    `json:"sub"`
	Tag string `json:"tag"`
}
type subOutForTest struct{}
type subPForTest struct{}
type subLogicForTest struct{}

func (subLogicForTest) LogicMeta() LogicMeta { return LogicMeta{Name: "sub_logic"} }
func (subLogicForTest) Run(_ *types.ExecutionContext, _ subInForTest, _ subPForTest) (*subOutForTest, error) {
	return &subOutForTest{}, nil
}
