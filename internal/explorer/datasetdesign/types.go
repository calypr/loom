// Package datasetdesign contains the product-level dataset design contract.
//
// A design is deliberately smaller than an authoring workspace. It names
// catalog identities and user intent; the adapter is the only code that turns
// those identities into the paths understood by the existing V2 compiler.
package datasetdesign

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

const Version1 = 1

// GrainKind identifies a named product row grain. F1 intentionally exposes
// only Patient; adding a new grain is a versioned domain change.
type GrainKind string

const (
	GrainPatient GrainKind = "patient"
)

// RowGrain is a catalog-backed row identity. NodeID is the immutable catalog
// identity; resource type and schema details are resolved from the snapshot.
type RowGrain struct {
	Kind   GrainKind `json:"kind"`
	NodeID string    `json:"nodeId"`
}

type FeatureRole string

const (
	RoleIdentifier  FeatureRole = "identifier"
	RoleFeature     FeatureRole = "feature"
	RoleOutcome     FeatureRole = "outcome"
	RoleTimestamp   FeatureRole = "timestamp"
	RoleIgnore      FeatureRole = "ignore"
	RoleUnspecified FeatureRole = "unspecified"
)

type MissingMeaning string

const (
	MissingUnknown       MissingMeaning = "unknown"
	MissingNotObserved   MissingMeaning = "not_observed"
	MissingNotApplicable MissingMeaning = "not_applicable"
	MissingZero          MissingMeaning = "zero"
	MissingFalse         MissingMeaning = "false"
)

type FeatureSourceKind string

const SourceRootValue FeatureSourceKind = "root-value"

// RootProjection is deliberately not an arbitrary compiler projection. F1
// has one scalar root-value operation; later projection choices belong to a
// versioned design variant rather than becoming implicit compiler behavior.
type RootProjection string

const ProjectionValue RootProjection = "VALUE"

// FieldRef names a catalog candidate. It has no path or expression field by
// design: physical paths are catalog facts and are introduced only by Lower.
type FieldRef struct {
	CandidateID string `json:"candidateId"`
}

// FeatureSource is the closed F1 source union. Its custom decoder rejects
// unknown variants, raw paths, raw expressions, and additional fields at the
// JSON boundary.
type FeatureSource struct {
	Kind       FeatureSourceKind `json:"kind"`
	Field      FieldRef          `json:"field"`
	Projection RootProjection    `json:"projection"`
}

type FeatureDefinition struct {
	Key            string         `json:"key"`
	Label          string         `json:"label"`
	Role           FeatureRole    `json:"role"`
	MissingMeaning MissingMeaning `json:"missingMeaning"`
	Source         FeatureSource  `json:"source"`
}

type DatasetDesignV1 struct {
	Version  int                 `json:"version"`
	Title    string              `json:"title"`
	Grain    RowGrain            `json:"grain"`
	Features []FeatureDefinition `json:"features"`
}

var featureKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validRole(role FeatureRole) bool {
	switch role {
	case RoleIdentifier, RoleFeature, RoleOutcome, RoleTimestamp, RoleIgnore, RoleUnspecified:
		return true
	default:
		return false
	}
}

func validMissingMeaning(value MissingMeaning) bool {
	switch value {
	case MissingUnknown, MissingNotObserved, MissingNotApplicable, MissingZero, MissingFalse:
		return true
	default:
		return false
	}
}

func (s FeatureSource) validateShape(path string) error {
	if s.Kind != SourceRootValue {
		return fmt.Errorf("%s.kind %q is unsupported", path, s.Kind)
	}
	if strings.TrimSpace(s.Field.CandidateID) == "" {
		return fmt.Errorf("%s.field.candidateId is required", path)
	}
	if s.Projection != ProjectionValue {
		return fmt.Errorf("%s.projection %q is unsupported", path, s.Projection)
	}
	return nil
}

func (g RowGrain) validateShape(path string) error {
	if g.Kind != GrainPatient {
		return fmt.Errorf("%s.kind %q is unsupported", path, g.Kind)
	}
	if strings.TrimSpace(g.NodeID) == "" {
		return fmt.Errorf("%s.nodeId is required", path)
	}
	return nil
}

func (d DatasetDesignV1) validateShape() error {
	if d.Version != Version1 {
		return fmt.Errorf("version %d is unsupported", d.Version)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if err := d.Grain.validateShape("grain"); err != nil {
		return err
	}
	if len(d.Features) == 0 {
		return fmt.Errorf("features must not be empty")
	}
	keys := make(map[string]struct{}, len(d.Features))
	for index, feature := range d.Features {
		path := fmt.Sprintf("features[%d]", index)
		if !featureKeyPattern.MatchString(feature.Key) {
			return fmt.Errorf("%s.key must be a stable identifier", path)
		}
		if _, exists := keys[feature.Key]; exists {
			return fmt.Errorf("%s.key %q is duplicated", path, feature.Key)
		}
		keys[feature.Key] = struct{}{}
		if strings.TrimSpace(feature.Label) == "" {
			return fmt.Errorf("%s.label is required", path)
		}
		if !validRole(feature.Role) {
			return fmt.Errorf("%s.role %q is unsupported", path, feature.Role)
		}
		if !validMissingMeaning(feature.MissingMeaning) {
			return fmt.Errorf("%s.missingMeaning %q is unsupported", path, feature.MissingMeaning)
		}
		if err := feature.Source.validateShape(path + ".source"); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks intrinsic design invariants. Catalog-dependent checks use
// ValidateAgainst because a design must never trust a path supplied by a
// caller or an unrelated capability snapshot.
func (d DatasetDesignV1) Validate() error { return d.validateShape() }

// ValidateAgainst checks all catalog-backed identities and F1's scalar root
// projection against the exact immutable capability snapshot.
func (d DatasetDesignV1) ValidateAgainst(snapshot authoringv2.CatalogSnapshot) error {
	if err := d.validateShape(); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("catalog snapshot: %w", err)
	}
	grain, ok := catalogNode(snapshot, d.Grain.NodeID)
	if !ok {
		return fmt.Errorf("grain.nodeId %q is not present in the catalog snapshot", d.Grain.NodeID)
	}
	if grain.ResourceType != "Patient" || !grain.RowRootEligible {
		return fmt.Errorf("grain.nodeId %q is not an eligible Patient root", d.Grain.NodeID)
	}
	if grain.RowGrain != "" && !strings.EqualFold(grain.RowGrain, string(GrainPatient)) {
		return fmt.Errorf("grain.nodeId %q advertises row grain %q, want %q", d.Grain.NodeID, grain.RowGrain, GrainPatient)
	}
	for index, feature := range d.Features {
		candidate, ok := catalogCandidate(snapshot, feature.Source.Field.CandidateID)
		if !ok {
			return fmt.Errorf("features[%d].source.field.candidateId %q is not present in the catalog snapshot", index, feature.Source.Field.CandidateID)
		}
		if candidate.NodeID != grain.ID {
			return fmt.Errorf("features[%d].source.field.candidateId %q does not belong to grain %q", index, candidate.ID, grain.ID)
		}
		if strings.TrimSpace(candidate.FieldPath) == "" {
			return fmt.Errorf("catalog candidate %q has no field path", candidate.ID)
		}
		if !containsScalarProjection(candidate.ProjectionModes) {
			return fmt.Errorf("catalog candidate %q does not advertise scalar root-value projection", candidate.ID)
		}
		if candidate.Repeated || len(candidate.RepeatedBoundaries) != 0 {
			return fmt.Errorf("catalog candidate %q is repeated and cannot be an F1 root value", candidate.ID)
		}
	}
	return nil
}

func containsScalarProjection(values []string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), "VALUE") || strings.EqualFold(strings.TrimSpace(value), "SCALAR") {
			return true
		}
	}
	return false
}

// Normalized returns an independent design with the authored feature order
// intact. Feature order is part of the product contract because it determines
// descriptor and lowered column order.
func (d DatasetDesignV1) Normalized() (DatasetDesignV1, error) {
	if err := d.validateShape(); err != nil {
		return DatasetDesignV1{}, err
	}
	n := d
	n.Features = append([]FeatureDefinition(nil), d.Features...)
	return n, nil
}

func (d DatasetDesignV1) CanonicalJSON() ([]byte, error) {
	n, err := d.Normalized()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	return canonicalJSONBytes(raw)
}

func (d DatasetDesignV1) Digest() (string, error) {
	raw, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// UnmarshalJSON is strict so the durable design cannot silently grow a path,
// expression, or an unrecognized source variant.
func (d *DatasetDesignV1) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Version  int                 `json:"version"`
		Title    string              `json:"title"`
		Grain    RowGrain            `json:"grain"`
		Features []FeatureDefinition `json:"features"`
	}
	if err := authoringv2.DecodeStrictJSON(raw, &wire); err != nil {
		return err
	}
	value := DatasetDesignV1{Version: wire.Version, Title: wire.Title, Grain: wire.Grain, Features: wire.Features}
	if err := value.validateShape(); err != nil {
		return err
	}
	*d = value
	return nil
}

func (s *FeatureSource) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind       FeatureSourceKind `json:"kind"`
		Field      *FieldRef         `json:"field"`
		Projection RootProjection    `json:"projection"`
	}
	if err := authoringv2.DecodeStrictJSON(raw, &wire); err != nil {
		return err
	}
	if wire.Field == nil {
		return fmt.Errorf("source.field is required")
	}
	value := FeatureSource{Kind: wire.Kind, Field: *wire.Field, Projection: wire.Projection}
	if err := value.validateShape("source"); err != nil {
		return err
	}
	*s = value
	return nil
}

func canonicalJSONBytes(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing JSON: %w", err)
	}
	return json.Marshal(value)
}

// LowerOptions controls only the V2 identity needed by the existing compiler.
// It does not permit callers to override catalog-derived paths or resource
// types.
type LowerOptions struct {
	OutputID string
}

// LowerToWorkspace is the one-way F1 adapter. It validates the design against
// the supplied snapshot, resolves candidate paths from that snapshot, and
// produces an ordinary V2 workspace. Existing V2 workspaces are never
// inspected or inferred into a design.
func (d DatasetDesignV1) LowerToWorkspace(snapshot authoringv2.CatalogSnapshot, options LowerOptions) (authoringv2.Workspace, error) {
	if err := d.ValidateAgainst(snapshot); err != nil {
		return authoringv2.Workspace{}, err
	}
	outputID := strings.TrimSpace(options.OutputID)
	if !featureKeyPattern.MatchString(outputID) {
		return authoringv2.Workspace{}, fmt.Errorf("outputId must be a stable identifier")
	}
	normalized, err := d.Normalized()
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	designJSON, err := normalized.CanonicalJSON()
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	digest, err := normalized.Digest()
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	visible := true
	columns := make([]authoringv2.Column, 0, len(normalized.Features))
	for _, feature := range normalized.Features {
		candidate, _ := catalogCandidate(snapshot, feature.Source.Field.CandidateID)
		path := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
		columns = append(columns, authoringv2.Column{
			Column: feature.Key, Label: feature.Label, LogicalType: candidate.LogicalType,
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: path, ProjectionMode: string(ProjectionValue)}},
			Table:        &authoringv2.TablePresentation{Visible: &visible},
		})
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer:      authoringv2.ExplorerMetadata{Title: normalized.Title},
		DatasetDesign: designJSON, DatasetDesignDigest: digest,
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: outputID, Title: normalized.Title},
			RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: columns,
		}},
		Tabs: []authoringv2.Tab{{ID: outputID + "_tab", Title: normalized.Title, OutputID: outputID, Order: 0, Visible: true}},
	}
	if err := workspace.Validate(); err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("lowered workspace: %w", err)
	}
	return workspace, nil
}

func catalogNode(catalog authoringv2.CatalogSnapshot, id string) (authoringv2.CatalogNode, bool) {
	for _, node := range catalog.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return authoringv2.CatalogNode{}, false
}

func catalogCandidate(catalog authoringv2.CatalogSnapshot, id string) (authoringv2.CatalogCandidate, bool) {
	for _, candidate := range catalog.Candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return authoringv2.CatalogCandidate{}, false
}
