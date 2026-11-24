package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type Options struct {
	RootIndex RootIndex
	Preview   Preview
}

type RootIndex struct {
	kind rootIndexKind
	hint string
}

type rootIndexKind uint8

const (
	rootIndexHinted rootIndexKind = iota + 1
	rootIndexOff
)

func RootIndexHint(name string) RootIndex { return RootIndex{kind: rootIndexHinted, hint: name} }
func RootIndexDisabled() RootIndex        { return RootIndex{kind: rootIndexOff} }

func (index RootIndex) validateHint() error {
	if index.kind == rootIndexHinted && !rootIndexHintPattern.MatchString(index.hint) {
		return fmt.Errorf("root index hint %q is not a safe index name", index.hint)
	}
	return nil
}

type Preview interface {
	render(ir.PhysicalPlan, RootIndex) (RenderedPhysicalPlan, error)
}

type RelatedExpandPreview struct{ Limit int }
type RootPageKeysPreview struct {
	BindKey            string
	RelatedExpandLimit int
}
type StreamingMissingCategoryPivotPreview struct{}
type SemanticGroupProjectionFactorizationPreview struct{}
type SemanticGroupProjectionAssessmentPreview struct{}

func Render(plan ir.PhysicalPlan, options Options) (RenderedPhysicalPlan, error) {
	switch preview := options.Preview.(type) {
	case RelatedExpandPreview, RootPageKeysPreview,
		StreamingMissingCategoryPivotPreview,
		SemanticGroupProjectionFactorizationPreview, SemanticGroupProjectionAssessmentPreview:
		return preview.render(plan, options.RootIndex)
	case nil:
	default:
		return RenderedPhysicalPlan{}, fmt.Errorf("unsupported preview option %T", preview)
	}
	index := options.RootIndex
	if err := index.validateHint(); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if index.kind != 0 && (len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population != nil) {
		if index.kind == rootIndexOff {
			return RenderedPhysicalPlan{}, fmt.Errorf("disabled root index requires one direct root scan")
		}
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint requires one direct root scan")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{rootIndex: index})
}

func (SemanticGroupProjectionFactorizationPreview) render(plan ir.PhysicalPlan, index RootIndex) (RenderedPhysicalPlan, error) {
	return renderSemanticGroupProjectionPreview(plan, index, false)
}
func (SemanticGroupProjectionAssessmentPreview) render(plan ir.PhysicalPlan, index RootIndex) (RenderedPhysicalPlan, error) {
	return renderSemanticGroupProjectionPreview(plan, index, true)
}

type renderPreview struct{ mode Preview }

func (p renderPreview) streamingMissingCategoryPivot() bool {
	_, ok := p.mode.(StreamingMissingCategoryPivotPreview)
	return ok
}

func (p renderPreview) relatedExpandLimit() int {
	switch preview := p.mode.(type) {
	case RelatedExpandPreview:
		return preview.Limit
	case RootPageKeysPreview:
		return preview.RelatedExpandLimit
	default:
		return 0
	}
}

func (p RelatedExpandPreview) render(plan ir.PhysicalPlan, index RootIndex) (RenderedPhysicalPlan, error) {
	if index.kind != 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related expansion preview does not support a root index override")
	}
	if p.Limit < 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related expansion preview limit must be positive")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{renderPreview: renderPreview{p}})
}

func (p RootPageKeysPreview) render(plan ir.PhysicalPlan, index RootIndex) (RenderedPhysicalPlan, error) {
	if index.kind != 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("root page key preview does not support a root index override")
	}
	if !previewSourcePathSegmentPattern.MatchString(p.BindKey) {
		return RenderedPhysicalPlan{}, fmt.Errorf("root page key bind %q is not a safe bind name", p.BindKey)
	}
	if p.RelatedExpandLimit < 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related expansion preview limit cannot be negative")
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("root page key lookup requires one direct root scan")
	}
	root := plan.Operations[0].RootScan
	if root.Population != nil || root.CohortSource != nil || root.PageCandidateSeed != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("root page key lookup requires an unmodified root scan")
	}
	if _, ok := plan.BindVars[p.BindKey].([]string); !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("root page key bind %q must contain []string", p.BindKey)
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
		previewRootKeyWindowVariable: fmt.Sprintf("SORTED_UNIQUE(@%s)", p.BindKey),
		renderPreview:                renderPreview{p},
	})
}

func (p StreamingMissingCategoryPivotPreview) render(plan ir.PhysicalPlan, index RootIndex) (RenderedPhysicalPlan, error) {
	if index.kind == rootIndexOff {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category preview does not support a disabled root index")
	}
	if err := index.validateHint(); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PopulationMappingReturn != nil || sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category preview requires one terminal Pivot preview")
	}
	stage := sequence.Stages[0]
	pivot := stage.GroupedPivot
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivot.OneInputRowPerGroup ||
		pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" || len(pivot.Categories) == 0 ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresence != nil || pivot.CategoryPresenceColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category preview requires a simple presence-aware Pivot")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
		rootIndex: index, renderPreview: renderPreview{p},
	})
}
