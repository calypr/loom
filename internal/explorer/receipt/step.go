package receipt

import (
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type constructionOutputStep struct {
	document          authoringv2.Document
	step              authoringv2.ConstructionStep
	stepIndex         int
	compiledOutput    facade.CompiledRecipeOutput
	resolvedInputs    map[int]facade.ResolvedClickHouseTable
	rowSourceSnapshot capability.Snapshot
	authored          map[string]AuthoredOutputColumn
	nextAuthored      map[string]AuthoredOutputColumn
	prior             map[string]string
	sourceOnlyIDs     map[string]struct{}
}

func (stage constructionOutputStep) reconcile() error {
	switch stage.step.Operation.Kind {
	case authoringv2.ConstructionOperationGroup:
		return stage.reconcileGroup()
	case authoringv2.ConstructionOperationCodedGroup:
		return stage.reconcileCodedGroup()
	case authoringv2.ConstructionOperationExpand:
		return stage.reconcileExpand()
	case authoringv2.ConstructionOperationDerive:
		return stage.reconcileDerive()
	case authoringv2.ConstructionOperationPivot:
		return stage.reconcilePivot()
	case authoringv2.ConstructionOperationCodedPivot:
		return stage.reconcileCodedPivot()
	case authoringv2.ConstructionOperationUnpivot:
		return stage.reconcileUnpivot()
	case authoringv2.ConstructionOperationFilter:
		return stage.reconcileFilter()
	case authoringv2.ConstructionOperationCombine:
		return stage.reconcileCombine()
	case authoringv2.ConstructionOperationRelatedSource:
		return stage.reconcileRelatedSource()
	case authoringv2.ConstructionOperationRelatedExpand:
		return stage.reconcileRelatedExpand()
	case authoringv2.ConstructionOperationRelatedEligibility:
		return stage.reconcileRelatedEligibility()
	case authoringv2.ConstructionOperationRelatedField:
		return stage.reconcileRelatedField()
	default:
		return fmt.Errorf("step %q has unsupported operation kind %q", stage.step.ID, stage.step.Operation.Kind)
	}
}

func (stage constructionOutputStep) addOutput(outputColumnID, constructionID string, inputIDs []string, quality ConstructedOutputQuality) error {
	step := stage.step
	document := stage.document
	nextAuthored := stage.nextAuthored
	prior := stage.prior
	stepIndex := stage.stepIndex
	authored := stage.authored
	sourceOnlyIDs := stage.sourceOnlyIDs
	if strings.TrimSpace(constructionID) == "" {
		return fmt.Errorf("steps[%d] has no construction identity", stepIndex)
	}
	var output authoringv2.StageColumn
	found := false
	for _, candidate := range step.Outputs {
		if candidate.ID == outputColumnID {
			output, found = candidate, true
			break
		}
	}
	if !found || !requiredConstructionID(output.ID) || strings.TrimSpace(output.Name) == "" || strings.TrimSpace(output.Label) == "" {
		return fmt.Errorf("step %q generated output %q is missing a declared stable ID, name, or label", step.ID, outputColumnID)
	}
	if _, exists := prior[outputColumnID]; exists {
		return fmt.Errorf("step %q reuses existing output column ID %q", step.ID, outputColumnID)
	}
	if _, duplicate := nextAuthored[output.Name]; duplicate {
		return fmt.Errorf("construction output %q is duplicated for document %q", output.Name, document.Output.ID)
	}
	publicInputIDs := make([]string, 0, len(inputIDs))
	for _, inputID := range inputIDs {
		if _, sourceOnly := sourceOnlyIDs[inputID]; sourceOnly {
			continue
		}
		publicInputIDs = append(publicInputIDs, inputID)
	}
	inputColumns, err := ConstructionInputNames(prior, publicInputIDs)
	if err != nil {
		var missing *ConstructionInputMissingError
		if errors.As(err, &missing) {
			missing.StepID = step.ID
			missing.OutputID = outputColumnID
			missing.OutputName = output.Name
		}
		return fmt.Errorf("step %q output %q: %w", step.ID, output.Name, err)
	}
	lineageInputs := make([]string, 0, len(inputColumns))
	for _, name := range inputColumns {
		if metadata, constructed := authored[name]; constructed {
			lineageInputs = append(lineageInputs, metadata.InputColumns...)
		} else {
			lineageInputs = append(lineageInputs, name)
		}
	}
	priorStageSelfInput := false
	for _, input := range lineageInputs {
		if input == output.Name {
			priorStageSelfInput = true
			break
		}
	}
	nextAuthored[output.Name] = AuthoredOutputColumn{
		ConstructionID: constructionID, Label: output.Label,
		InputColumns: lineageInputs, TypedStageOutput: true,
		PriorStageSelfInput: priorStageSelfInput, Quality: quality,
	}
	return nil
}
