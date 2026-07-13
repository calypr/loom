package compiler

// This file is deliberately an experiment. It models traversal-time shaping
// entirely in the test package by using the existing typed physical renderer:
// a relationship set returns one identity-plus-selector object and rich
// consumers read those selector fields directly. It does not change the
// production physical IR or renderer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type traversalProjectionExperimentReport struct {
	Sets              int
	EligibleSets      int
	ProjectedFields   int
	PayloadSets       int
	BaselinePayloads  int
	CandidatePayloads int
	BaselineAQLHash   string
	CandidateAQLHash  string
}

// TestTraversalProjectionExperimentBuildsSingleShapedSets proves the
// candidate's structural contract against the real GDC compiler fixture. It
// intentionally does not enable a production policy switch: promotion is a
// coordinator decision after live parity/profile evidence.
func TestTraversalProjectionExperimentBuildsSingleShapedSets(t *testing.T) {
	builder := loadTraversalProjectionGDCBuilder(t)
	baseline, err := compileExperimentPhysicalPlan(builder)
	if err != nil {
		t.Fatal(err)
	}
	candidate, report, err := buildTraversalProjectionExperiment(baseline)
	if err != nil {
		t.Fatal(err)
	}
	renderedBaseline, err := RenderPhysicalPlan(baseline)
	if err != nil {
		t.Fatal(err)
	}
	renderedCandidate, err := RenderPhysicalPlan(candidate)
	if err != nil {
		t.Fatal(err)
	}
	report.BaselineAQLHash = sha256Hex(renderedBaseline.Query)
	report.CandidateAQLHash = sha256Hex(renderedCandidate.Query)
	t.Logf("WP3 traversal projection report: %+v", report)
	t.Logf("baseline AQL:\n%s", renderedBaseline.Query)
	t.Logf("candidate AQL:\n%s", renderedCandidate.Query)

	if report.Sets == 0 || report.EligibleSets == 0 || report.ProjectedFields == 0 {
		t.Fatalf("experiment did not find eligible selector-bearing sets: %+v", report)
	}
	if report.CandidatePayloads >= report.BaselinePayloads {
		t.Fatalf("candidate did not remove payload-bearing set returns: %+v", report)
	}
	if strings.Contains(renderedCandidate.Query, "_prepared = (") || strings.Contains(renderedCandidate.Query, "__loom_prepared_node") {
		t.Fatalf("candidate introduced the rejected second prepared array:\n%s", renderedCandidate.Query)
	}
	if !strings.Contains(renderedCandidate.Query, "__loom_projection_") {
		t.Fatalf("candidate did not render selector projection fields:\n%s", renderedCandidate.Query)
	}
	if strings.Contains(renderedCandidate.Query, "payload: ") {
		t.Fatalf("eligible candidate still retains payload in a set return:\n%s", renderedCandidate.Query)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("candidate physical plan does not validate: %v", err)
	}
}

// TestTraversalProjectionExperimentPreservesFallbackPayload is the required
// mixed-consumer safety case. A fallback selector cannot be represented by a
// single projected field, so the experiment leaves that set on the existing
// payload path and records it as an explicit rejection.
func TestTraversalProjectionExperimentPreservesFallbackPayload(t *testing.T) {
	primary := Selector{Steps: []SelectorStep{{Field: "id"}}}
	fallback := Selector{Steps: []SelectorStep{{Field: "code"}}}
	plan, err := BuildPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p",
		Root: SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Fields: []SemanticField{{Name: "status", Selector: primary, Fallbacks: []Selector{fallback}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, report, err := buildTraversalProjectionExperiment(plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleSets != 0 || report.PayloadSets != 1 {
		t.Fatalf("fallback set was incorrectly projected: report=%+v", report)
	}
	rendered, err := RenderPhysicalPlan(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "payload: ") {
		t.Fatalf("fallback set lost payload retention:\n%s", rendered.Query)
	}
}

// TestTraversalProjectionExperimentProfilesGDC is opt-in because it reads the
// local META Arango fixture. It records parity, Explain/profile statistics, and
// five warm client timings for the exact 1,000-row GDC request. Set
// LOOM_WP3_WRITE_ARTIFACTS=1 to write the raw evidence directory.
func TestTraversalProjectionExperimentProfilesGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run WP3 against Arango")
	}
	builder := loadTraversalProjectionGDCBuilder(t)
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		t.Fatal(err)
	}
	baselinePlan, err := BuildPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	baselinePlan, err = OptimizePhysicalPlan(baselinePlan)
	if err != nil {
		t.Fatal(err)
	}
	candidatePlan, report, err := buildTraversalProjectionExperiment(baselinePlan)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := compilePhysicalExecution(baselinePlan, semantic, 1000)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := compilePhysicalExecution(candidatePlan, semantic, 1000)
	if err != nil {
		t.Fatal(err)
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	baselineProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: baseline.Query, BindVars: baseline.BindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("baseline PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidate.Query, BindVars: candidate.BindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("candidate PROFILE: %v", err)
	}
	baselineHash := hashRawRows(baselineProfile.Result)
	candidateHash := hashRawRows(candidateProfile.Result)
	if baselineHash != candidateHash {
		t.Logf("first result difference: %s", firstRawDifference(baselineProfile.Result, candidateProfile.Result))
		if os.Getenv("LOOM_WP3_WRITE_ARTIFACTS") != "" {
			writeTraversalProjectionArtifacts(t, report, baseline, candidate, baselineHash, baselineProfile, candidateProfile, nil, nil)
		}
		t.Fatalf("result parity mismatch: baseline=%s candidate=%s", baselineHash, candidateHash)
	}
	baselineWarm := profileWarmQuery(ctx, client, baseline)
	candidateWarm := profileWarmQuery(ctx, client, candidate)
	baselineSummary := arangostore.SummarizeProfile(baselineProfile)
	candidateSummary := arangostore.SummarizeProfile(candidateProfile)
	report.BaselineAQLHash = sha256Hex(baseline.Query)
	report.CandidateAQLHash = sha256Hex(candidate.Query)
	t.Logf("WP3 live report: report=%+v baseline_hash=%s candidate_hash=%s baseline_profile=%+v candidate_profile=%+v baseline_warm=%v candidate_warm=%v", report, baselineHash, candidateHash, baselineSummary, candidateSummary, baselineWarm, candidateWarm)
	if os.Getenv("LOOM_WP3_WRITE_ARTIFACTS") != "" {
		writeTraversalProjectionArtifacts(t, report, baseline, candidate, baselineHash, baselineProfile, candidateProfile, baselineWarm, candidateWarm)
	}
}

func profileWarmQuery(ctx context.Context, client *arangostore.Client, compiled CompiledQuery) []float64 {
	times := make([]float64, 0, 5)
	for run := 0; run < 5; run++ {
		started := time.Now()
		_ = client.QueryRows(ctx, compiled.Query, 10000, compiled.BindVars, func(map[string]any) error { return nil })
		times = append(times, time.Since(started).Seconds())
	}
	return times
}

func hashRawRows(rows []json.RawMessage) string {
	hash := sha256.New()
	for _, row := range rows {
		var value any
		if json.Unmarshal(row, &value) != nil {
			continue
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			continue
		}
		_, _ = hash.Write(canonical)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func firstRawDifference(left, right []json.RawMessage) string {
	if len(left) != len(right) {
		return fmt.Sprintf("row count baseline=%d candidate=%d", len(left), len(right))
	}
	for index := range left {
		var leftValue, rightValue any
		if json.Unmarshal(left[index], &leftValue) != nil || json.Unmarshal(right[index], &rightValue) != nil {
			continue
		}
		leftJSON, _ := json.Marshal(leftValue)
		rightJSON, _ := json.Marshal(rightValue)
		if string(leftJSON) != string(rightJSON) {
			return fmt.Sprintf("row=%d baseline=%s candidate=%s", index, leftJSON, rightJSON)
		}
	}
	return "hash-only difference (JSON rows compare equal)"
}

func writeTraversalProjectionArtifacts(t *testing.T, report traversalProjectionExperimentReport, baseline, candidate CompiledQuery, resultHash string, baselineProfile, candidateProfile arangostore.ProfileResult, baselineWarm, candidateWarm []float64) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round3", "wp3")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create WP3 artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "baseline.aql"), []byte(baseline.Query), 0o644); err != nil {
		t.Fatalf("write baseline AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidate.Query), 0o644); err != nil {
		t.Fatalf("write candidate AQL: %v", err)
	}
	payload := map[string]any{
		"report": report, "result_hash": resultHash,
		"baseline_profile": baselineProfile, "candidate_profile": candidateProfile,
		"baseline_warm_seconds": baselineWarm, "candidate_warm_seconds": candidateWarm,
		"decision": "pending coordinator live gate",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode WP3 artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write WP3 evidence: %v", err)
	}
}

func compileExperimentPhysicalPlan(builder Builder) (PhysicalPlan, error) {
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		return PhysicalPlan{}, err
	}
	physical, err := BuildPhysicalPlan(semantic)
	if err != nil {
		return PhysicalPlan{}, err
	}
	return OptimizePhysicalPlan(physical)
}

type experimentSelector struct {
	key      string
	selector Selector
	field    string
}

// buildTraversalProjectionExperiment rewrites only the cloned plan. It uses
// PreparedReference as a test-only field reference; unlike PhysicalPreparedSet
// it points back to the owning set itself, so no second array is rendered.
func buildTraversalProjectionExperiment(plan PhysicalPlan) (PhysicalPlan, traversalProjectionExperimentReport, error) {
	candidate := clonePhysicalPlan(plan)
	// clonePhysicalPlan intentionally predates object-bearing return trees and
	// keeps PhysicalProjection.Expression pointers shared. Detach them here so
	// the experiment cannot mutate the baseline plan while annotating candidate
	// consumers with projected-field references.
	for index := range candidate.Operations {
		operation := &candidate.Operations[index]
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for projectionIndex := range operation.Return.Projections {
			if operation.Return.Projections[projectionIndex].Expression == nil {
				continue
			}
			expression := cloneExperimentExpression(*operation.Return.Projections[projectionIndex].Expression)
			operation.Return.Projections[projectionIndex].Expression = &expression
		}
	}
	report := traversalProjectionExperimentReport{}
	for index := range candidate.Operations {
		op := &candidate.Operations[index]
		if op.Kind != PhysicalSetOp || op.Set == nil {
			continue
		}
		report.Sets++
		set := op.Set
		selectors, fallback := collectSetSelectors(candidate, set.Variable)
		if fallback {
			continue
		}
		if len(selectors) == 0 {
			continue
		}
		report.EligibleSets++
		target := setTargetVariable(*set)
		if target == "" {
			return PhysicalPlan{}, report, fmt.Errorf("set %q has no target variable", set.Variable)
		}
		fields := make([]PhysicalExpressionProjection, 0, len(selectors)+4)
		resourceType := op.Source.ResourceType
		if resourceType == "" {
			resourceType = setResourceType(candidate, *set)
		}
		if resourceType == "" {
			return PhysicalPlan{}, report, fmt.Errorf("set %q has no resource type", set.Variable)
		}
		for _, name := range []string{"_id", "_key", "id", "resourceType"} {
			fields = append(fields, PhysicalExpressionProjection{
				Name:       name,
				Expression: PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: target, Path: []string{name}}},
			})
		}
		for selectorIndex := range selectors {
			selector := &selectors[selectorIndex]
			field := fmt.Sprintf("__loom_projection_%d", report.ProjectedFields)
			selector.field = field
			fields = append(fields, PhysicalExpressionProjection{
				Name: field,
				Expression: PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Extract: &PhysicalExtract{
					Source: PhysicalValue{Variable: target, Path: []string{"payload"}}, ResourceType: resourceType, Selector: selector.selector,
				}},
			})
			report.ProjectedFields++
		}
		set.Subplan.Return = PhysicalExpression{Kind: PhysicalObjectExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull, Object: &PhysicalObject{Fields: fields}}
		set.Output = nil
		set.Prepared = nil
		for projectionIndex := range candidate.Operations {
			operation := &candidate.Operations[projectionIndex]
			if operation.Kind != PhysicalReturnOp || operation.Return == nil {
				continue
			}
			for expressionIndex := range operation.Return.Projections {
				expression := operation.Return.Projections[expressionIndex].Expression
				rewriteProjectionExpression(expression, set.Variable, selectors)
			}
		}
	}
	// The baseline payload count is measured from the original plan's compact
	// output, not the rewritten candidate.
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalSetOp && operation.Set != nil {
			if operation.Set.Output == nil {
				report.BaselinePayloads++
				continue
			}
			for _, field := range operation.Set.Output.Fields {
				if field == PhysicalSetPayloadField {
					report.BaselinePayloads++
					break
				}
			}
		}
	}
	for _, operation := range candidate.Operations {
		if operation.Kind != PhysicalSetOp || operation.Set == nil {
			continue
		}
		if operation.Set.Output != nil {
			for _, field := range operation.Set.Output.Fields {
				if field == PhysicalSetPayloadField {
					report.CandidatePayloads++
					break
				}
			}
		}
		if operation.Set.Subplan.Return.Object != nil {
			for _, field := range operation.Set.Subplan.Return.Object.Fields {
				if field.Name == string(PhysicalSetPayloadField) {
					report.CandidatePayloads++
					break
				}
			}
		}
	}
	report.PayloadSets = report.BaselinePayloads
	return candidate, report, candidate.Validate()
}

// cloneExperimentExpression fills the deep-copy gaps in the production clone
// helper for nested rich expressions. The experiment must not annotate the
// baseline's Slice.Predicate or Slice.Projections while preparing a candidate.
func cloneExperimentExpression(expression PhysicalExpression) PhysicalExpression {
	copy := clonePhysicalExpression(expression)
	if expression.Aggregate != nil {
		aggregate := *copy.Aggregate
		if expression.Aggregate.Value != nil {
			value := cloneExperimentExpression(*expression.Aggregate.Value)
			aggregate.Value = &value
		}
		if expression.Aggregate.Predicate != nil {
			predicate := cloneExperimentPredicateExpression(*expression.Aggregate.Predicate)
			aggregate.Predicate = &predicate
		}
		copy.Aggregate = &aggregate
	}
	if expression.Slice != nil {
		slice := *copy.Slice
		if expression.Slice.Predicate != nil {
			predicate := cloneExperimentPredicateExpression(*expression.Slice.Predicate)
			slice.Predicate = &predicate
		}
		slice.Projections = make([]PhysicalExpressionProjection, len(expression.Slice.Projections))
		for index, projection := range expression.Slice.Projections {
			slice.Projections[index] = projection
			slice.Projections[index].Expression = cloneExperimentExpression(projection.Expression)
		}
		copy.Slice = &slice
	}
	if expression.Object != nil {
		object := *copy.Object
		object.Fields = make([]PhysicalExpressionProjection, len(expression.Object.Fields))
		for index, field := range expression.Object.Fields {
			object.Fields[index] = field
			object.Fields[index].Expression = cloneExperimentExpression(field.Expression)
		}
		copy.Object = &object
	}
	return copy
}

func cloneExperimentPredicateExpression(predicate PhysicalPredicateExpression) PhysicalPredicateExpression {
	copy := predicate
	if predicate.Comparison != nil {
		comparison := clonePhysicalPredicate(*predicate.Comparison)
		if predicate.Comparison.LeftExpression != nil {
			left := cloneExperimentExpression(*predicate.Comparison.LeftExpression)
			comparison.LeftExpression = &left
		}
		copy.Comparison = &comparison
	}
	if predicate.Exists != nil {
		exists := clonePhysicalSubplan(*predicate.Exists)
		copy.Exists = &exists
	}
	copy.Children = make([]PhysicalPredicateExpression, len(predicate.Children))
	for index, child := range predicate.Children {
		copy.Children[index] = cloneExperimentPredicateExpression(child)
	}
	return copy
}

func setTargetVariable(set PhysicalSet) string {
	if set.SourceSetVariable != "" {
		return set.ItemVariable
	}
	if len(set.Subplan.Operations) == 0 || set.Subplan.Operations[0].Traversal == nil {
		return ""
	}
	return set.Subplan.Operations[0].Traversal.TargetVariable
}

func setResourceType(plan PhysicalPlan, set PhysicalSet) string {
	if len(set.Subplan.Operations) > 0 && set.Subplan.Operations[0].Traversal != nil {
		traversal := set.Subplan.Operations[0].Traversal
		if value, ok := plan.BindVars[traversal.TargetTypeBindKey].(string); ok {
			return value
		}
		if values, ok := plan.BindVars[traversal.TargetTypeBindKey].([]string); ok && len(values) == 1 {
			return values[0]
		}
	}
	return ""
}

func collectSetSelectors(plan PhysicalPlan, setVariable string) ([]experimentSelector, bool) {
	byKey := map[string]Selector{}
	fallback := false
	var collect func(*PhysicalExpression)
	collect = func(expression *PhysicalExpression) {
		if expression == nil {
			return
		}
		switch expression.Kind {
		case PhysicalExtractExpression:
			if expression.Extract != nil && expression.Extract.Source.Variable == setVariable {
				if len(expression.Extract.Fallbacks) > 0 {
					fallback = true
				} else {
					byKey[physicalSelectorIdentity(expression.Extract.Selector)] = expression.Extract.Selector
				}
			}
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil {
				collect(expression.Aggregate.Value)
				if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
					collect(expression.Aggregate.Predicate.Comparison.LeftExpression)
				}
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.Source.Variable == setVariable {
				byKey[physicalSelectorIdentity(expression.Pivot.KeySelector)] = expression.Pivot.KeySelector
				byKey[physicalSelectorIdentity(expression.Pivot.ValueSelector)] = expression.Pivot.ValueSelector
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				collect(expression.Slice.Sort)
				if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
					collect(expression.Slice.Predicate.Comparison.LeftExpression)
				}
				for index := range expression.Slice.Projections {
					collect(&expression.Slice.Projections[index].Expression)
				}
			}
		case PhysicalObjectExpression:
			if expression.Object != nil {
				for index := range expression.Object.Fields {
					collect(&expression.Object.Fields[index].Expression)
				}
			}
		}
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for projectionIndex := range operation.Return.Projections {
			collect(operation.Return.Projections[projectionIndex].Expression)
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	selectors := make([]experimentSelector, 0, len(keys))
	for _, key := range keys {
		selectors = append(selectors, experimentSelector{key: key, selector: byKey[key]})
	}
	return selectors, fallback
}

func rewriteProjectionExpression(expression *PhysicalExpression, setVariable string, selectors []experimentSelector) {
	if expression == nil {
		return
	}
	fieldFor := func(selector Selector) string {
		key := physicalSelectorIdentity(selector)
		for _, candidate := range selectors {
			if candidate.key == key {
				return candidate.field
			}
		}
		return ""
	}
	switch expression.Kind {
	case PhysicalExtractExpression:
		if expression.Extract != nil && expression.Extract.Source.Variable == setVariable && len(expression.Extract.Fallbacks) == 0 {
			if field := fieldFor(expression.Extract.Selector); field != "" {
				expression.Extract.Prepared = &PhysicalPreparedReference{SetVariable: setVariable, Field: field}
			}
		}
	case PhysicalAggregateExpression:
		if expression.Aggregate != nil {
			rewriteProjectionExpression(expression.Aggregate.Value, setVariable, selectors)
			if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
				rewriteProjectionExpression(expression.Aggregate.Predicate.Comparison.LeftExpression, setVariable, selectors)
			}
		}
	case PhysicalPivotExpression:
		if expression.Pivot != nil && expression.Pivot.Source.Variable == setVariable {
			if field := fieldFor(expression.Pivot.KeySelector); field != "" {
				expression.Pivot.PreparedKey = &PhysicalPreparedReference{SetVariable: setVariable, Field: field}
			}
			if field := fieldFor(expression.Pivot.ValueSelector); field != "" {
				expression.Pivot.PreparedValue = &PhysicalPreparedReference{SetVariable: setVariable, Field: field}
			}
		}
	case PhysicalSliceExpression:
		if expression.Slice != nil {
			rewriteProjectionExpression(expression.Slice.Sort, setVariable, selectors)
			if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
				rewriteProjectionExpression(expression.Slice.Predicate.Comparison.LeftExpression, setVariable, selectors)
			}
			for index := range expression.Slice.Projections {
				rewriteProjectionExpression(&expression.Slice.Projections[index].Expression, setVariable, selectors)
			}
		}
	case PhysicalObjectExpression:
		if expression.Object != nil {
			for index := range expression.Object.Fields {
				rewriteProjectionExpression(&expression.Object.Fields[index].Expression, setVariable, selectors)
			}
		}
	}
}

func loadTraversalProjectionGDCBuilder(t *testing.T) Builder {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "conformance", "compiler", "fixtures", "gdc-case-matrix.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read GDC fixture %q: %v", path, err)
	}
	var fixture struct {
		Builder Builder `json:"builder"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode GDC fixture: %v", err)
	}
	return fixture.Builder
}

func sha256Hex(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
