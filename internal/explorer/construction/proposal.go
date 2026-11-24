package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"
	"golang.org/x/sync/errgroup"
)

func importedSourceChoiceUnavailable(document ws.Document) bool {

	return document.ImportedRecipe != nil
}

func importedSourceRootChoiceInventoryMissing(base Base) bool {
	if base.Document.ImportedRecipe == nil {
		return false
	}
	for _, node := range base.Snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == base.Document.RootResourceType {
			return false
		}
	}
	return true
}

func constructionNodeOccurrences(route ws.RouteNode, snapshot capability.Snapshot) map[string]string {
	result := map[string]string{}
	var root capability.Node
	found := false
	for _, node := range snapshot.Nodes {
		if node.ResourceType == route.ResourceType && node.RowRootEligible {
			root, found = node, true
			break
		}
	}
	if !found {
		for _, node := range snapshot.Nodes {
			if node.ResourceType == route.ResourceType {
				root, found = node, true
				break
			}
		}
	}
	if !found {
		return result
	}

	result[root.ID] = route.OccurrenceID
	ambiguous := map[string]struct{}{}
	var walk func(parent capability.Node, node ws.RouteNode)
	walk = func(parent capability.Node, node ws.RouteNode) {
		for _, child := range node.Children {
			var target capability.Node
			ok := false
			if child.CatalogEdgeID != "" {
				if edge, found := snapshot.Edge(child.CatalogEdgeID); found {
					target, ok = snapshot.Node(edge.ToNodeID)
				}
			}
			if !ok {
				matches := 0
				for _, edge := range snapshot.Edges {
					if edge.FromNodeID != parent.ID || edge.BlockedReason != "" ||
						(edge.Label != child.Relationship && edge.Label != child.Relationship+"_"+child.ResourceType) {
						continue
					}
					candidate, candidateOK := snapshot.Node(edge.ToNodeID)
					if !candidateOK || candidate.ResourceType != child.ResourceType {
						continue
					}
					target, ok = candidate, true
					matches++
				}
				if matches != 1 {
					continue
				}
			}
			if !ok || target.ResourceType != child.ResourceType {
				continue
			}
			if target.ID != root.ID {
				if _, isAmbiguous := ambiguous[target.ID]; !isAmbiguous {
					if _, exists := result[target.ID]; exists {
						delete(result, target.ID)
						ambiguous[target.ID] = struct{}{}
					} else {
						result[target.ID] = child.OccurrenceID
					}
				}
			}
			walk(target, child)
		}
	}
	walk(root, route)
	return result
}

func runConstructionCapabilityBuilders(ctx context.Context, limit int, builders []func(context.Context) error) []error {
	errs := make([]error, len(builders))
	group := new(errgroup.Group)
	group.SetLimit(limit)
	for index, build := range builders {
		index, build := index, build
		group.Go(func() error {
			errs[index] = build(ctx)
			return errs[index]
		})
	}
	_ = group.Wait()
	return errs
}

func constructionSourceProjectionAvailability(construction ws.Construction, stageID string) ConstructionSourceProjectionAvailability {
	availability := ConstructionSourceProjectionAvailability{StageID: stageID}
	if !constructionRootProjectionCanReachStage(construction, stageID) {
		availability.ReasonCode = "ROOT_SOURCE_PROJECTION_UNAVAILABLE"
		availability.Reason = "the current construction prefix does not carry root scalar source fields to this stage"
		return availability
	}
	availability.Supported = true
	return availability
}

func constructionWorkspaceInputs(base Base, targetOutputID string) ([]ConstructionWorkspaceInput, error) {
	inputs := []ConstructionWorkspaceInput{}
	if base.Receipt == nil || len(base.Workspace.Documents) < 2 {
		return inputs, nil
	}
	if len(base.Receipt.CompiledOutputSchemas) == 0 {
		return nil, conflict("construction-capabilities", "WORKSPACE_OUTPUT_SCHEMA_UNAVAILABLE", "the current compilation receipt has no finalized workspace output schemas", nil, nil)
	}
	blocked, err := workspaceOutputConsumerClosure(base.Workspace, targetOutputID)
	if err != nil {
		return nil, conflict("construction-capabilities", "WORKSPACE_OUTPUT_SCHEMA_UNAVAILABLE", "the selected output is missing from the current workspace", nil, err)
	}
	for _, document := range base.Workspace.Documents {
		if _, excluded := blocked[document.Output.ID]; excluded {
			continue
		}
		compiled, exists := base.Receipt.CompiledOutputSchemas[document.Output.ID]
		if !exists {
			return nil, conflict("construction-capabilities", "WORKSPACE_OUTPUT_SCHEMA_UNAVAILABLE", "a sibling output is missing from the current compilation receipt schema", map[string]any{"outputId": document.Output.ID}, nil)
		}
		columns := make([]ConstructionWorkspaceInputColumn, 0, len(compiled))
		for _, column := range compiled {
			if column.Internal || column.ID == "" {
				continue
			}
			joinKey, appendKey := constructionCombineCompatibilityKeys(column.LogicalType, column.Cardinality, column.Nullable)
			columns = append(columns, ConstructionWorkspaceInputColumn{
				ID: column.ID, Name: column.Name, Label: column.Label,
				LogicalType: column.LogicalType, Cardinality: column.Cardinality, Nullable: column.Nullable,
				JoinCompatibilityKey: joinKey, AppendCompatibilityKey: appendKey,
			})
		}
		if len(columns) == 0 {
			continue
		}
		title := strings.TrimSpace(document.Output.Title)
		if title == "" {
			title = document.Output.ID
		}
		inputs = append(inputs, ConstructionWorkspaceInput{OutputID: document.Output.ID, Title: title, Columns: columns})
	}
	return inputs, nil
}

func constructionCapabilitiesWorkspace(workspace ws.Workspace, targetOutputID string) (ws.Workspace, error) {
	if workspace.DocumentIndex(targetOutputID) < 0 {
		return ws.Workspace{}, fmt.Errorf("selected output %q is missing or duplicated", targetOutputID)
	}
	blocked, err := workspaceOutputConsumerClosure(workspace, targetOutputID)
	if err != nil {
		return ws.Workspace{}, err
	}
	projected := workspace
	projected.Documents = make([]ws.Document, 0, len(workspace.Documents))
	outputIDs := make(map[string]struct{}, len(workspace.Documents))
	for _, document := range workspace.Documents {
		if document.Output.ID != targetOutputID {
			if _, isConsumer := blocked[document.Output.ID]; isConsumer {
				continue
			}
		}
		projected.Documents = append(projected.Documents, document)
		outputIDs[document.Output.ID] = struct{}{}
	}
	tabs := append([]ws.Tab(nil), workspace.Tabs...)
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].Order < tabs[j].Order })
	projected.Tabs = make([]ws.Tab, 0, len(projected.Documents))
	for _, tab := range tabs {
		if _, included := outputIDs[tab.OutputID]; !included {
			continue
		}
		tab.Order = len(projected.Tabs)
		projected.Tabs = append(projected.Tabs, tab)
	}
	if workspace.SharedFilters != nil {
		projected.SharedFilters = make(map[string][]ws.SharedFilterBinding, len(workspace.SharedFilters))
		for name, bindings := range workspace.SharedFilters {
			for _, binding := range bindings {
				if _, included := outputIDs[binding.OutputID]; included {
					projected.SharedFilters[name] = append(projected.SharedFilters[name], binding)
				}
			}
			if len(projected.SharedFilters[name]) == 0 {
				delete(projected.SharedFilters, name)
			}
		}
	}
	return projected, nil
}

func workspaceOutputConsumerClosure(workspace ws.Workspace, targetOutputID string) (map[string]struct{}, error) {
	if workspace.DocumentIndex(targetOutputID) < 0 {
		return nil, fmt.Errorf("selected output %q is missing or duplicated", targetOutputID)
	}
	blocked := map[string]struct{}{targetOutputID: {}}
	changed := true
	for changed {
		changed = false
		for _, document := range workspace.Documents {
			if _, alreadyBlocked := blocked[document.Output.ID]; alreadyBlocked || document.Construction == nil {
				continue
			}
			dependsOnBlocked := false
			for _, step := range document.Construction.Steps {
				for _, input := range step.Inputs {
					if input.Kind != ws.ConstructionInputWorkspaceOutput {
						continue
					}
					if _, dependencyBlocked := blocked[input.OutputID]; dependencyBlocked {
						dependsOnBlocked = true
						break
					}
				}
				if dependsOnBlocked {
					break
				}
			}
			if dependsOnBlocked {
				blocked[document.Output.ID] = struct{}{}
				changed = true
			}
		}
	}
	return blocked, nil
}

func constructionCombineCompatibilityKeys(logicalType, cardinality string, nullable bool) (joinKey, appendKey string) {
	if cardinality == "many" {
		return "", ""
	}
	logical, physical, err := facade.ConstructionCombineColumnType(recipe.StageColumn{Type: logicalType})
	if err != nil {
		return "", ""
	}
	physicalWithNullability := physical
	if nullable {
		physicalWithNullability = "Nullable(" + physical + ")"
	}
	if base, ok := facade.ClickHouseCombineScalarBaseType(physicalWithNullability, facade.PhysicalCombineKeyJoin); ok {
		joinKey = base
	}
	if base, ok := facade.ClickHouseCombineScalarBaseType(physicalWithNullability, facade.PhysicalCombineAppend); ok {
		appendKey = logical + ":" + base
	}
	return joinKey, appendKey
}

func constructionWorkspaceCandidateDigest(workspace ws.Workspace) (string, error) {
	encoded, err := json.Marshal(workspace.NormalizePresentationOrders())
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func sameConstructionDocument(left, right ws.Document) bool {
	left.Construction, right.Construction = nil, nil
	left.ArchiveRetiredShape, right.ArchiveRetiredShape = nil, nil
	return reflect.DeepEqual(left, right)
}

func constructionHasGroupFieldSource(construction ws.Construction) bool {
	for _, step := range construction.Steps {
		if step.Operation.Kind != ws.ConstructionOperationGroup || step.Operation.Group == nil {
			continue
		}
		if groupStepHasRowValueFieldSource(step) {
			return true
		}
		for _, key := range step.Operation.Group.Keys {
			if key.FieldSource != nil {
				return true
			}
		}
	}
	return false
}

func checkConstructionProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	if receipt == nil || receipt.ConstructionProposal == nil {
		return conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied construction workspace: %w", err)
	}
	if digest != receipt.ConstructionProposal.CandidateWorkspaceDigest || digest != receipt.IntentDigest {
		return conflict("commands", "CONSTRUCTION_PROPOSAL_MISMATCH", "the applied construction does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}

func constructionProposalPivotProbeDiagnostic(stepID, outcome, message string) ConstructionProposalDiagnostic {
	return ConstructionProposalDiagnostic{
		Code:    "PIVOT_CATEGORIES_UNVERIFIABLE",
		Message: message,
		StepID:  stepID,
		Outcome: outcome,
	}
}

func constructionProposalCategoryKey(value ws.TableScalar) string {
	switch value.Kind {
	case ws.TableScalarString:
		if value.String == nil {
			return "STRING:<nil>"
		}
		return "STRING:" + strconv.Quote(*value.String)
	case ws.TableScalarInteger:
		if value.Integer == nil {
			return "INTEGER:<nil>"
		}
		return "INTEGER:" + strconv.FormatInt(*value.Integer, 10)
	case ws.TableScalarDecimal:
		if value.Decimal == nil {
			return "DECIMAL:<nil>"
		}
		return "DECIMAL:" + strconv.FormatFloat(*value.Decimal, 'g', -1, 64)
	case ws.TableScalarBoolean:
		if value.Boolean == nil {
			return "BOOLEAN:<nil>"
		}
		return "BOOLEAN:" + strconv.FormatBool(*value.Boolean)
	case ws.TableScalarNull:
		return "NULL"
	case ws.TableScalarMissing:
		return "MISSING"
	default:
		return "UNKNOWN:" + string(value.Kind)
	}
}

func constructionPivotLabelOnlyEdit(base, candidate ws.Construction, stepID string) bool {
	if base.Version != candidate.Version || !reflect.DeepEqual(base.SourceProjections, candidate.SourceProjections) {
		return false
	}
	before, after := map[string]ws.ConstructionStep{}, map[string]ws.ConstructionStep{}
	for _, step := range base.Steps {
		before[step.ID] = step
	}
	for _, step := range candidate.Steps {
		after[step.ID] = step
	}
	if len(before) != len(base.Steps) || len(after) != len(candidate.Steps) {
		return false
	}
	stripLabels := func(step ws.ConstructionStep) ws.ConstructionStep {
		step.Outputs = append([]ws.StageColumn(nil), step.Outputs...)
		for i := range step.Outputs {
			step.Outputs[i].Label = ""
		}
		return step
	}
	prior, exists := before[stepID]
	current, found := after[stepID]
	if !exists || !found || reflect.DeepEqual(prior.Outputs, current.Outputs) {
		return false
	}
	active := map[string]bool{}
	done := map[string]bool{}
	var unchanged func(string) bool
	unchanged = func(id string) bool {
		if active[id] {
			return false
		}
		if done[id] {
			return true
		}
		old, ok := before[id]
		next, present := after[id]
		if !ok || !present || !reflect.DeepEqual(stripLabels(old), stripLabels(next)) {
			return false
		}
		active[id] = true
		for _, input := range next.Inputs {
			if input.Kind == ws.ConstructionInputStepOutput && !unchanged(input.StepID) {
				return false
			}
		}
		delete(active, id)
		done[id] = true
		return true
	}
	return unchanged(stepID)
}

func previewWorkspace(ctx context.Context, store Store, owner *explorer.Explorer) (ws.Workspace, error) {
	return support.PreviewWorkspace(ctx, store, owner)
}

func verifyPreviewReceiptIntent(receipt *receiptmodel.CompilationReceipt, workspace ws.Workspace) error {
	return support.VerifyPreviewReceiptIntent(receipt, workspace)
}

func (s *Service) GetConstructionCapabilities(ctx context.Context, request ConstructionCapabilitiesRequest) (ConstructionCapabilitiesResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionCapabilitiesResponse{}, malformed("construction-capabilities", err.Error(), err)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, err
	}

	stages := append([]receiptmodel.ReceiptConstructionStage(nil), base.Stages...)
	if ws.CanPopulateConstructionRows(&base.Construction) && len(stages) > 0 {
		last := len(stages) - 1
		stages[last].Capabilities = append(append([]receiptmodel.ReceiptConstructionOperationChoice(nil), stages[last].Capabilities...), receiptmodel.ReceiptConstructionOperationChoice{Kind: "ROW_VALUES", Supported: true})
	}

	if len(stages) == 0 {
		return ConstructionCapabilitiesResponse{}, conflict("construction-capabilities", "STAGE_CAPABILITIES_UNAVAILABLE", "the compiler receipt has no stage descriptors for this output", nil, nil)
	}
	var selected *receiptmodel.ReceiptConstructionStage
	for index := range stages {
		if stages[index].ID == request.StageID {
			selected = &stages[index]
			break
		}
	}
	if selected == nil {
		return ConstructionCapabilitiesResponse{}, conflict("construction-capabilities", "STALE_STAGE_REFERENCE", "the selected stage is not present in the current compiled output", nil, nil)
	}
	choices, err := constructionCodedGroupChoices(base, request.OutputID, *selected)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile coded-group choices: %w", err)
	}
	selected.CodedGroupChoices = choices
	includeGroupChoices := request.IncludeGroupChoices || request.GroupStepID != ""
	semanticGroupChoices := []ConstructionSemanticGroupChoice{}
	groupFieldChoices := []ConstructionGroupFieldChoice{}
	groupFieldChoicesIncomplete := false
	semanticGroupChoicesIncomplete := false
	if includeGroupChoices {
		semanticGroupChoices, semanticGroupChoicesIncomplete, err = s.constructionSemanticGroupChoices(ctx, base, request.OutputID, *selected)
		if err != nil {
			return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile semantic group choices: %w", err)
		}
		groupFieldChoices, groupFieldChoicesIncomplete, err = s.constructionGroupFieldChoices(
			ctx, base, *selected, semanticGroupChoices, semanticGroupChoicesIncomplete, "", request.GroupStepID,
		)
		if err != nil {
			return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile Group field choices: %w", err)
		}
	}
	var (
		groupRowValueChoices []ConstructionGroupFieldChoice
		sourceInput          ConstructionGroupSourceCapability
		pivotSourceInput     ConstructionPivotSourceCapability
		filterSourceInput    ConstructionFilterSourceCapability
		deriveSourceInput    ConstructionDeriveSourceCapability
		unpivotSourceInput   ConstructionUnpivotSourceCapability
		expandSourceInput    ConstructionExpandSourceCapability
	)
	builders := []func(context.Context) error{
		func(ctx context.Context) error {
			var err error
			groupRowValueChoices, err = s.constructionGroupRowValueChoices(ctx, base, request.OutputID, *selected, request.GroupStepID)
			if err != nil {
				return fmt.Errorf("compile Group row-value choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			sourceInput, err = s.constructionSources().constructionGroupSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile scalar group source choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			pivotSourceInput, err = s.constructionSources().constructionPivotSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile pivot source choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			filterSourceInput, err = s.constructionFilterSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile filter source choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			deriveSourceInput, err = s.constructionDeriveSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile derive source choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			unpivotSourceInput, err = s.constructionUnpivotSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile unpivot source choices: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			var err error
			expandSourceInput, err = s.constructionExpandSourceCapability(ctx, base, request.OutputID, selected.ID)
			if err != nil {
				return fmt.Errorf("compile expand source choices: %w", err)
			}
			return nil
		},
	}
	for _, buildErr := range runConstructionCapabilityBuilders(ctx, 4, builders) {
		if buildErr != nil {
			return ConstructionCapabilitiesResponse{}, buildErr
		}
	}
	sourceProjectionAvailability := constructionSourceProjectionAvailability(base.Construction, selected.ID)
	workspaceInputs, err := constructionWorkspaceInputs(base, request.OutputID)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, err
	}
	return ConstructionCapabilitiesResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
		OutputID: request.OutputID, StageID: request.StageID, BaseConstruction: base.Construction,
		Stages: stages, SelectedStage: *selected, SourceInput: sourceInput, PivotSourceInput: pivotSourceInput,
		FilterSourceInput:              filterSourceInput,
		DeriveSourceInput:              deriveSourceInput,
		UnpivotSourceInput:             unpivotSourceInput,
		ExpandSourceInput:              expandSourceInput,
		GroupFieldChoices:              groupFieldChoices,
		GroupFieldChoicesIncomplete:    groupFieldChoicesIncomplete,
		GroupRowValueChoices:           groupRowValueChoices,
		SemanticGroupChoices:           semanticGroupChoices,
		SemanticGroupChoicesIncomplete: semanticGroupChoicesIncomplete,
		SourceProjectionAvailability:   &sourceProjectionAvailability,
		NodeOccurrences:                constructionNodeOccurrences(base.Document.Route, base.Snapshot),
		WorkspaceInputs:                workspaceInputs,
	}, nil
}

func (s *Service) ProposeConstruction(ctx context.Context, request ConstructionProposalRequest) (ConstructionProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionProposalResponse{}, malformed("construction-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err := s.constructionSources().constructionCandidateWithGroupSource(ctx, base, request)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.resolveConstructionGroupFieldSources(ctx, base, request.OutputID, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.resolveConstructionGroupSemanticCategories(ctx, base, request.OutputID, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	var authorizedPivotSourceHelpers map[string]bool
	candidateConstruction, authorizedPivotSourceHelpers, err = s.constructionSources().constructionCandidateWithPivotSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.constructionCandidateWithFilterSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.constructionCandidateWithDeriveSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.constructionCandidateWithUnpivotSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err = s.constructionCandidateWithExpandSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	var authorizedCodedPivotSteps map[string]bool
	candidateConstruction, authorizedCodedPivotSteps, err = s.resolveConstructionCodedPivotChoices(ctx, base, request.OutputID, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	var resolvedDiscoverySteps map[string]bool
	candidateConstruction, resolvedDiscoverySteps, err = s.resolveConstructionPivotDiscoveries(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	diagnostics, err := s.verifyConstructionProposalPivotCategoriesExcept(ctx, request, candidateConstruction, &base.Construction, resolvedDiscoverySteps)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateDocument, impact, err := analyzeConstructionCandidateWithCascade(
		base.Document, candidateConstruction, request.ChangedStepID, request.RemoveStepIDs, base.Stages,
	)
	if err != nil {
		return ConstructionProposalResponse{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	baseRelatedSources := make(map[string]ws.ConstructionRelatedSource)
	baseRelatedExpands := make(map[string]ws.ConstructionRelatedExpand)
	baseRelatedEligibility := make(map[string]ws.ConstructionRelatedEligibility)
	baseRelatedFields := make(map[string]ws.ConstructionRelatedField)
	baseCodedGroups := make(map[string]ws.ConstructionCodedGroup)
	baseCodedPivots := make(map[string]ws.ConstructionCodedPivot)
	for _, step := range base.Construction.Steps {
		if step.Operation.Kind == ws.ConstructionOperationRelatedSource && step.Operation.RelatedSource != nil {
			baseRelatedSources[step.ID] = *step.Operation.RelatedSource
		}
		if step.Operation.Kind == ws.ConstructionOperationRelatedExpand && step.Operation.RelatedExpand != nil {
			baseRelatedExpands[step.ID] = *step.Operation.RelatedExpand
		}
		if step.Operation.Kind == ws.ConstructionOperationRelatedEligibility && step.Operation.RelatedEligibility != nil {
			baseRelatedEligibility[step.ID] = *step.Operation.RelatedEligibility
		}
		if step.Operation.Kind == ws.ConstructionOperationRelatedField && step.Operation.RelatedField != nil {
			baseRelatedFields[step.ID] = *step.Operation.RelatedField
		}
		if step.Operation.Kind == ws.ConstructionOperationCodedGroup && step.Operation.CodedGroup != nil {
			baseCodedGroups[step.ID] = *step.Operation.CodedGroup
		}
		if step.Operation.Kind == ws.ConstructionOperationCodedPivot && step.Operation.CodedPivot != nil {
			baseCodedPivots[step.ID] = *step.Operation.CodedPivot
		}
	}
	for _, step := range candidateDocument.Construction.Steps {
		switch step.Operation.Kind {
		case ws.ConstructionOperationRelatedSource:
			if step.Operation.RelatedSource == nil {
				continue
			}
			if prior, exists := baseRelatedSources[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedSource) {
				continue
			}
			if err := reauthorizeConstructionRelatedSource(ctx, base, candidateDocument.RootResourceType, *step.Operation.RelatedSource); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case ws.ConstructionOperationRelatedExpand:
			if step.Operation.RelatedExpand == nil {
				continue
			}
			if prior, exists := baseRelatedExpands[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedExpand) {
				continue
			}
			if err := reauthorizeConstructionRelatedExpand(ctx, base, candidateDocument.RootResourceType, step, *step.Operation.RelatedExpand); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case ws.ConstructionOperationRelatedEligibility:
			if step.Operation.RelatedEligibility == nil {
				continue
			}
			if prior, exists := baseRelatedEligibility[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedEligibility) {
				continue
			}
			if err := reauthorizeConstructionRelatedEligibility(ctx, base, candidateDocument.RootResourceType, step, *step.Operation.RelatedEligibility); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case ws.ConstructionOperationRelatedField:
			if step.Operation.RelatedField == nil {
				continue
			}
			if prior, exists := baseRelatedFields[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedField) {
				continue
			}
			if authorizedPivotSourceHelpers[step.ID] {
				continue
			}
			if err := reauthorizeConstructionRelatedField(ctx, base, step, *step.Operation.RelatedField); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case ws.ConstructionOperationCodedGroup:
			if step.Operation.CodedGroup == nil {
				continue
			}
			if prior, exists := baseCodedGroups[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.CodedGroup) {
				continue
			}
			if err := reauthorizeConstructionCodedGroup(ctx, base, request.OutputID, step, *step.Operation.CodedGroup); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case ws.ConstructionOperationCodedPivot:
			if step.Operation.CodedPivot == nil {
				continue
			}
			if authorizedCodedPivotSteps[step.ID] {
				continue
			}
			if prior, exists := baseCodedPivots[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.CodedPivot) {
				continue
			}
			return ConstructionProposalResponse{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "a changed coded Pivot requires current source and category choices", nil)
		}
	}
	candidateWorkspace := base.Workspace
	candidateWorkspace.Documents = append([]ws.Document(nil), base.Workspace.Documents...)
	documentIndex := candidateWorkspace.DocumentIndex(request.OutputID)
	if documentIndex < 0 {
		return ConstructionProposalResponse{}, conflict("construction-proposal", "OUTPUT_NOT_FOUND", "the proposal output is missing from the candidate workspace", nil, nil)
	}
	candidateWorkspace.Documents[documentIndex] = candidateDocument
	preparedCandidate, err := ws.PrepareWorkspaceForCompilation(candidateWorkspace, s.catalog(base.Snapshot, request.ExplorerID))
	if err != nil {
		return ConstructionProposalResponse{}, fmt.Errorf("prepare construction candidate workspace: %w", err)
	}
	candidateDigest, err := preparedCandidate.Digest()
	if err != nil && impact.HasMissingInputs() {
		candidateDigest, err = constructionWorkspaceCandidateDigest(preparedCandidate)
	}
	if err != nil {
		return ConstructionProposalResponse{}, fmt.Errorf("digest construction candidate workspace: %w", err)
	}
	baseResponse := ConstructionProposalResponse{
		OutputID: request.OutputID, SnapshotToken: request.SnapshotToken,
		DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
		BaseDocumentDigest: base.BaseDocumentSHA, CandidateWorkspaceDigest: candidateDigest,
		ChangedStepID: request.ChangedStepID, CandidateConstruction: *candidateDocument.Construction,
		DependencyImpact: impact, Stages: base.Stages,
		PreviewStatus: "NEEDS_REPAIR",
		Diagnostics:   diagnostics,
	}
	if impact.HasMissingInputs() {
		return baseResponse, nil
	}
	binding := &receiptmodel.ConstructionProposalBinding{
		DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID, RemoveStepIDs: append([]string(nil), impact.RemovedStepIDs...),
		BaseDocumentDigest: base.BaseDocumentSHA, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken, PreviewLimit: request.Limit,
	}
	candidateReceipt, err := s.compile(ctx, authoringcore.CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-proposal-candidate", ConstructionProposal: binding,
	})
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-proposal", candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, base.Snapshot, &candidateWorkspace); err != nil {
		return ConstructionProposalResponse{}, err
	}
	if len(candidateReceipt.ConstructionStages[request.OutputID]) == 0 {
		return ConstructionProposalResponse{}, conflict("construction-proposal", "INVALID_COMPILATION_RECEIPT", "the candidate receipt has no stage descriptors for this output", nil, nil)
	}
	baseResponse.ProposalID = candidateReceipt.ID
	if base.Receipt != nil {
		baseResponse.BaseReceiptID = base.Receipt.ID
	}
	baseResponse.Stages = candidateReceipt.ConstructionStages[request.OutputID]
	baseResponse.PreviewStatus = "PREVIEW_PENDING"
	return baseResponse, nil
}

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command, sinkFactory PreviewSinkFactory) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, *AcceptedConstruction, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyConstructionProposal {
		return nil, nil, nil, malformed("commands", "APPLY_CONSTRUCTION_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID())
	if err != nil {
		return nil, nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID() {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.ConstructionProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseIndex := current.DocumentIndex(command.OutputID)
	if baseIndex < 0 {
		return nil, nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the proposal output is missing or duplicated in the saved draft", nil, nil)
	}
	baseDocument := current.Documents[baseIndex]
	baseDocumentDigest, err := support.DocumentDigest(baseDocument)
	if err != nil {
		return nil, nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyProposalReceipt(ctx, "construction-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	candidateIndex := candidate.DocumentIndex(command.OutputID)
	if candidateIndex < 0 || candidateIndex != baseIndex {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt does not contain the requested output in its saved position", nil, nil)
	}
	candidateDocument := candidate.Documents[candidateIndex]
	if candidateDocument.Construction == nil {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt has no staged construction", nil, nil)
	}
	if constructionHasGroupFieldSource(*candidateDocument.Construction) {
		liveBase, liveErr := s.loadConstructionBase(ctx, project, explorerID, request.SnapshotToken,
			request.ExpectedDraftVersion, request.ExpectedDraftDigest, command.OutputID)
		if liveErr != nil {
			return nil, nil, nil, liveErr
		}
		liveBase.Document = candidateDocument
		liveBase.Construction = *candidateDocument.Construction
		liveBase.Stages = receipt.ConstructionStages[command.OutputID]
		reauthorized, reauthErr := s.resolveConstructionGroupFieldSources(ctx, liveBase, command.OutputID, *candidateDocument.Construction)
		if reauthErr != nil || !reflect.DeepEqual(reauthorized, *candidateDocument.Construction) {
			return nil, nil, nil, conflict("commands", "STALE_GROUP_FIELD_BINDING", "the Group field binding no longer matches the live compiler-resolved source stage", nil, reauthErr)
		}
	}
	if s.config.SourceLanes.ConstructionHasFilterSourceInput(*candidateDocument.Construction) {
		liveBase := Base{
			Document: candidateDocument, Construction: *candidateDocument.Construction,
			Snapshot: snapshot.Clone(), Stages: receipt.ConstructionStages[command.OutputID],
		}
		if reauthErr := s.reauthorizeConstructionFilterSources(ctx, liveBase, command.OutputID, *candidateDocument.Construction); reauthErr != nil {
			return nil, nil, nil, conflict("commands", "STALE_FILTER_SOURCE_BINDING", "the filter source binding no longer matches the live authorized root scalar field", nil, reauthErr)
		}
	}
	if s.config.SourceLanes.ConstructionHasDeriveSourceInput(*candidateDocument.Construction) {
		liveBase := Base{
			Document: candidateDocument, Construction: *candidateDocument.Construction,
			Snapshot: snapshot.Clone(), Stages: receipt.ConstructionStages[command.OutputID],
		}
		if reauthErr := s.reauthorizeConstructionDeriveSources(ctx, liveBase, command.OutputID, *candidateDocument.Construction); reauthErr != nil {
			return nil, nil, nil, conflict("commands", "STALE_DERIVE_SOURCE_BINDING", "the derive source binding no longer matches the live authorized root scalar field", nil, reauthErr)
		}
	}
	if s.config.SourceLanes.ConstructionHasUnpivotSourceInput(*candidateDocument.Construction) {
		liveBase := Base{
			Document: candidateDocument, Construction: *candidateDocument.Construction,
			Snapshot: snapshot.Clone(), Stages: receipt.ConstructionStages[command.OutputID],
		}
		if reauthErr := s.reauthorizeConstructionUnpivotSources(ctx, liveBase, command.OutputID, *candidateDocument.Construction); reauthErr != nil {
			return nil, nil, nil, conflict("commands", "STALE_UNPIVOT_SOURCE_BINDING", "the unpivot source binding no longer matches the live authorized root scalar field", nil, reauthErr)
		}
	}
	if s.config.SourceLanes.ConstructionHasExpandSourceInput(*candidateDocument.Construction) {
		liveBase := Base{
			Document: candidateDocument, Construction: *candidateDocument.Construction,
			Snapshot: snapshot.Clone(), Stages: receipt.ConstructionStages[command.OutputID],
		}
		if reauthErr := s.reauthorizeConstructionExpandSources(ctx, liveBase, command.OutputID, *candidateDocument.Construction); reauthErr != nil {
			return nil, nil, nil, conflict("commands", "STALE_EXPAND_SOURCE_BINDING", "the expand source binding no longer matches the live authorized root array field", nil, reauthErr)
		}
	}
	expectedDocument, impact, err := baseDocument.AnalyzeConstructionCandidate(*candidateDocument.Construction, binding.ChangedStepID, binding.RemoveStepIDs)
	if err != nil || impact.HasMissingInputs() {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate construction no longer resolves against the saved base", nil, err)
	}
	if !sameConstructionDocument(expectedDocument, candidateDocument) {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt changes authoring fields outside the construction proposal", nil, nil)
	}
	expectedWorkspace := current
	expectedWorkspace.Documents = append([]ws.Document(nil), current.Documents...)
	expectedWorkspace.Documents[baseIndex] = expectedDocument
	preparedExpected, err := ws.PrepareWorkspaceForCompilation(expectedWorkspace, s.catalog(snapshot, explorerID))
	if err != nil {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the expected candidate workspace cannot be normalized for compilation", nil, err)
	}
	expectedDigest, err := preparedExpected.Digest()
	if err != nil || expectedDigest != binding.CandidateWorkspaceDigest || expectedDigest != receipt.IntentDigest {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate workspace digest does not match the exact proposal", nil, err)
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, &expectedWorkspace); err != nil {
		return nil, nil, nil, err
	}
	if err := command.ResolveConstructionProposal(&candidateDocument); err != nil {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate construction is invalid", nil, err)
	}
	captureAvailable := sinkFactory != nil
	preview, err := s.Preview(ctx, authoringcore.PreviewRequest{
		Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: command.OutputID,
		Limit: binding.PreviewLimit,
		SinkFactory: func(previewReceipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
			if sinkFactory == nil {
				return func(map[string]any) error { return nil }, nil
			}
			sink, sinkErr := sinkFactory(previewReceipt, columns)
			if sinkErr != nil || sink == nil {
				captureAvailable = false
				return func(map[string]any) error { return nil }, nil
			}
			return func(row map[string]any) error {
				if !captureAvailable {
					return nil
				}
				if visitErr := sink(row); visitErr != nil {
					captureAvailable = false
				}
				return nil
			}, nil
		},
	})
	if err != nil {
		return nil, nil, nil, conflict("commands", "CONSTRUCTION_PREVIEW_FAILED", "the exact candidate preview failed; the accepted draft remains unchanged", nil, err)
	}
	if preview.Receipt == nil || preview.Receipt.ID != receipt.ID || preview.Receipt.IntentDigest != binding.CandidateWorkspaceDigest {
		return nil, nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the Apply-time preview did not return the exact candidate receipt", nil, nil)
	}
	var accepted *AcceptedConstruction
	if captureAvailable {
		accepted = &AcceptedConstruction{Preview: preview, PreviewLimit: binding.PreviewLimit}
	}
	return commands, receipt, accepted, nil
}

func (s *Service) ProposeConstructionChoice(ctx context.Context, request ConstructionChoiceProposalRequest) (ConstructionChoiceProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return ConstructionChoiceProposalResponse{}, unavailable("construction-choice-proposal", "CAPABILITY_UNAVAILABLE", "authorized construction choice compilation is not configured", nil)
	}
	if s.config.CompileReceipt == nil {
		return ConstructionChoiceProposalResponse{}, unavailable("construction-choice-proposal", "PREVIEW_UNAVAILABLE", "candidate compilation is not configured", nil)
	}

	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	authorized.Snapshot = snapshot.Clone()

	identities := make([]capability.ConstructionChoiceIdentity, len(request.ConstructionChoices))
	commands := make([]authoringv2.Command, len(request.ConstructionChoices))
	for index, selection := range request.ConstructionChoices {
		identity, decodeErr := capability.DecodeConstructionChoiceID(selection.ChoiceID)
		if decodeErr != nil {
			return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", fmt.Sprintf("constructionChoices[%d].choiceId is invalid", index), decodeErr)
		}
		if identity.SnapshotToken != request.SnapshotToken {
			return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CONSTRUCTION_CHOICE", "a construction choice belongs to a different catalog snapshot", nil, nil)
		}
		identities[index] = identity
		title := ""
		if selection.Title != nil {
			title = *selection.Title
		}
		commands[index] = authoringv2.Command{Type: authoringv2.CommandApplyConstructionChoice, OutputID: request.OutputID, Title: title, Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: selection.ChoiceID, Form: selection.Form, FrameID: selection.FrameID, RowValuePolicy: selection.RowValuePolicy}}}
	}
	applyRequest := authoringv2.ApplyCommandsRequest{
		CommandID: request.CommandID, SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: request.SnapshotToken, ExpectedDraftVersion: request.ExpectedDraftVersion,
		ExpectedDraftDigest: request.ExpectedDraftDigest, Commands: commands,
	}
	if err := applyRequest.Validate(); err != nil {
		return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", err.Error(), err)
	}

	owner, err := s.getOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return ConstructionChoiceProposalResponse{}, notFound("construction-choice-proposal", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting a construction choice preview", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be previewed", nil, err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil || baseDigest != owner.DraftDigest {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	if workspace.DocumentIndex(request.OutputID) < 0 {
		return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify one saved table", nil)
	}

	catalogSnapshot := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	prepared, err := s.prepareConstructionChoice(ctx, request.Project, request.ExplorerID, authorized.Clone(), identities, workspace, catalogSnapshot, commands)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	candidateWorkspace, results, err := authoringv2.ApplyCommands(workspace, catalogSnapshot, request.CommandID, prepared)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "INVALID_CONSTRUCTION_CHOICE", err.Error(), err)
	}
	if len(results) != len(request.ConstructionChoices) {
		return ConstructionChoiceProposalResponse{}, fmt.Errorf("construction choice reducer returned %d results for %d choices", len(results), len(request.ConstructionChoices))
	}
	candidateColumnIDs := make([]string, len(results))
	for index, result := range results {
		if result.Type != authoringv2.CommandResultColumnAdded || result.OutputID != request.OutputID || strings.TrimSpace(result.Column) == "" {
			return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "INVALID_CONSTRUCTION_CHOICE", "the candidate choice did not produce its expected output column", nil)
		}
		candidateColumnIDs[index] = result.Column
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return ConstructionChoiceProposalResponse{}, fmt.Errorf("digest construction choice candidate workspace: %w", err)
	}
	candidateReceipt, err := s.compile(ctx, authoringcore.CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-choice-proposal-candidate",
	})
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	if err := verifyPreviewReceiptIntent(candidateReceipt, candidateWorkspace); err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the proposed workspace", nil, err)
	}

	choices := append([]ConstructionChoiceProposalSelection(nil), request.ConstructionChoices...)
	return ConstructionChoiceProposalResponse{
		CommandID: request.CommandID, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest,
		OutputID: request.OutputID, ConstructionChoices: choices, CandidateWorkspaceDigest: candidateDigest,
		CandidateColumnIDs: candidateColumnIDs, PreviewStatus: "PREVIEW_PENDING", PreviewReceiptID: candidateReceipt.ID,
	}, nil
}
