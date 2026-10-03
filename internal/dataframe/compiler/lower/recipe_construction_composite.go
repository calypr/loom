package lower

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// ComposeClickHouseCombine adds a terminal typed Combine to an already lowered
// AQL construction prefix. Recipe authoring still decides which mixed plans
// are reachable; this internal compiler seam accepts only an exact typed stage
// reference and one supported authorization scope.
func ComposeClickHouseCombine(prefix ir.PhysicalPlan, combine ir.PhysicalClickHouseCombine, bindings recipe.RuntimeBindings) (ir.PhysicalPlan, error) {
	if prefix.Engine == ir.PhysicalEngineClickHouse || prefix.ClickHouseCombine != nil || prefix.ClickHousePrefix != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("ClickHouse prefix must be a standalone AQL physical plan")
	}
	if prefix.StageSequence == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("ClickHouse prefix requires a typed AQL construction stage sequence")
	}
	if prefix.StageSequence.PreviewLimitBindKey != "" {
		return ir.PhysicalPlan{}, fmt.Errorf("bounded preview cannot feed a private ClickHouse artifact")
	}
	for _, operation := range prefix.Operations {
		if operation.Kind == ir.PhysicalLimitOp {
			return ir.PhysicalPlan{}, fmt.Errorf("bounded AQL prefix cannot feed a private ClickHouse artifact")
		}
	}
	for _, stage := range prefix.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStageGroupOp || stage.Kind == ir.PhysicalStageCohortGroupOp {
			return ir.PhysicalPlan{}, fmt.Errorf("%s prefixes cannot feed a private ClickHouse Combine until authorization scope is preserved by grouping", stage.Kind)
		}
	}
	if err := validateClickHousePrefixBindings(prefix, bindings); err != nil {
		return ir.PhysicalPlan{}, err
	}
	if err := ir.ValidateGenericPhysicalPlanScope(prefix); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate AQL prefix scope: %w", err)
	}
	if err := combine.ValidateWithPrivateStage(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate composite ClickHouse combine: %w", err)
	}
	privateStageID := ""
	for _, input := range combine.Inputs {
		if input.PrivateStageID != "" {
			privateStageID = input.PrivateStageID
		}
	}
	if privateStageID != prefix.StageSequence.FinalStageID {
		return ir.PhysicalPlan{}, fmt.Errorf("private Combine input must reference exact final AQL stage %q", prefix.StageSequence.FinalStageID)
	}

	composite := ir.ClonePhysicalPlan(prefix)
	prefixContract := &ir.PhysicalClickHousePrefix{
		StageID:           prefix.StageSequence.FinalStageID,
		AuthScopeMode:     string(bindings.AuthScopeMode),
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
	}
	if bindings.AuthScopeMode == authscope.ReadScopeRestricted {
		if !bindings.IncludeAuthResourcePath {
			return ir.PhysicalPlan{}, fmt.Errorf("restricted AQL prefix must include its bound authorization path")
		}
		key := "construction_private_auth_resource_path"
		for suffix := 1; ; suffix++ {
			if _, exists := composite.BindVars[key]; !exists {
				break
			}
			key = fmt.Sprintf("construction_private_auth_resource_path_%d", suffix)
		}
		composite.BindVars[key] = bindings.AuthResourcePaths[0]
		prefixContract.IncludeAuthResourcePath = true
		prefixContract.AuthResourcePathBindKey = key
		composite.StageSequence.OutputAuthResourcePathBindKey = key
	}
	composite.Engine = ir.PhysicalEngineClickHouse
	composite.ClickHouseCombine = &combine
	composite.ClickHousePrefix = prefixContract
	if err := composite.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate composite ClickHouse physical plan: %w", err)
	}
	return composite, nil
}

func validateClickHousePrefixBindings(plan ir.PhysicalPlan, bindings recipe.RuntimeBindings) error {
	if strings.TrimSpace(bindings.Project) == "" || strings.TrimSpace(bindings.DatasetGeneration) == "" {
		return fmt.Errorf("private ClickHouse prefix requires exact project and dataset generation")
	}
	if plan.BindVars["project"] != bindings.Project || plan.BindVars["dataset_generation"] != bindings.DatasetGeneration {
		return fmt.Errorf("AQL prefix project or dataset generation differs from its runtime bindings")
	}
	if plan.BindVars["scope_allowed"] != true {
		return fmt.Errorf("AQL prefix is missing its authorization scope guard")
	}
	paths, ok := plan.BindVars["auth_resource_paths"].([]string)
	if !ok || !reflect.DeepEqual(paths, bindings.AuthResourcePaths) {
		return fmt.Errorf("AQL prefix authorization paths differ from its runtime bindings")
	}
	unrestricted, ok := plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	if !ok || unrestricted != (bindings.AuthScopeMode == authscope.ReadScopeUnrestricted) {
		return fmt.Errorf("AQL prefix authorization mode differs from its runtime bindings")
	}
	switch bindings.AuthScopeMode {
	case authscope.ReadScopeUnrestricted:
		if len(bindings.AuthResourcePaths) != 0 || bindings.IncludeAuthResourcePath {
			return fmt.Errorf("unrestricted private ClickHouse prefix cannot carry restricted path metadata")
		}
	case authscope.ReadScopeRestricted:
		if len(bindings.AuthResourcePaths) != 1 || strings.TrimSpace(bindings.AuthResourcePaths[0]) == "" || bindings.AuthResourcePaths[0] != strings.TrimSpace(bindings.AuthResourcePaths[0]) {
			return fmt.Errorf("restricted private ClickHouse prefix supports exactly one immutable authorization path")
		}
	default:
		return fmt.Errorf("private ClickHouse prefix requires an explicit supported authorization scope")
	}
	return nil
}
