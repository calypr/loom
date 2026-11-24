package interpretation

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/viewer"
)

type PreviewInterpretationCandidateRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	Column               string
	RevisionID           string
	Limit                int
}

type CandidatePreviewCompleteness string

const (
	CandidatePreviewComplete   CandidatePreviewCompleteness = "COMPLETE"
	CandidatePreviewIncomplete CandidatePreviewCompleteness = "INCOMPLETE"
)

type CandidatePreviewState string

const (
	CandidatePreviewUnchanged  CandidatePreviewState = "UNCHANGED"
	CandidatePreviewChanged    CandidatePreviewState = "CHANGED"
	CandidatePreviewResolved   CandidatePreviewState = "RESOLVED"
	CandidatePreviewUnresolved CandidatePreviewState = "UNRESOLVED"
)

type InterpretationCandidatePreviewSample struct {
	RowID  string                `json:"rowId"`
	Before map[string]any        `json:"before"`
	After  map[string]any        `json:"after"`
	State  CandidatePreviewState `json:"state"`
}

type CandidatePreviewCounts struct {
	Compared   int `json:"compared"`
	Changed    int `json:"changed"`
	Resolved   int `json:"resolved"`
	Unresolved int `json:"unresolved"`
}

type PreviewInterpretationCandidateResult struct {
	BaseReceiptID      string                                 `json:"baseReceiptId"`
	CandidateReceiptID string                                 `json:"candidateReceiptId"`
	OutputID           string                                 `json:"outputId"`
	Column             string                                 `json:"column"`
	RevisionID         string                                 `json:"revisionId"`
	Completeness       CandidatePreviewCompleteness           `json:"completeness"`
	Samples            []InterpretationCandidatePreviewSample `json:"samples"`
	Counts             CandidatePreviewCounts                 `json:"counts"`
}

type PreviewRows struct {
	ReceiptID string
	Rows      map[string]map[string]any
}

func ValidateCandidatePreviewRequest(request PreviewInterpretationCandidateRequest) error {
	values := map[string]string{
		"project": request.Project, "explorerId": request.ExplorerID, "snapshotToken": request.SnapshotToken,
		"expectedDraftDigest": request.ExpectedDraftDigest, "outputId": request.OutputID, "column": request.Column,
		"revisionId": request.RevisionID,
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if request.ExpectedDraftVersion < 0 {
		return fmt.Errorf("expectedDraftVersion must not be negative")
	}
	return nil
}

func AuthoredEmissions(receiptEmissions []viewer.EmittedColumn, outputID, column string) []viewer.EmittedColumn {
	emissions := make([]viewer.EmittedColumn, 0)
	for _, emission := range receiptEmissions {
		if emission.OutputID != outputID || emission.PublicColumn == "" || !slices.Contains(emission.AuthoredColumns, column) {
			continue
		}
		emissions = append(emissions, emission)
	}
	sort.SliceStable(emissions, func(i, j int) bool {
		if emissions[i].EmissionID != emissions[j].EmissionID {
			return emissions[i].EmissionID < emissions[j].EmissionID
		}
		return emissions[i].PublicColumn < emissions[j].PublicColumn
	})
	return emissions
}

func CompareCandidateRows(base, candidate PreviewRows, baseEmissions, candidateEmissions []viewer.EmittedColumn, request PreviewInterpretationCandidateRequest, limit int) PreviewInterpretationCandidateResult {
	emissions := make(map[string]struct{})
	for _, emission := range append(append([]viewer.EmittedColumn(nil), baseEmissions...), candidateEmissions...) {
		emissions[emission.PublicColumn] = struct{}{}
	}
	emissionKeys := make([]string, 0, len(emissions))
	for key := range emissions {
		emissionKeys = append(emissionKeys, key)
	}
	sort.Strings(emissionKeys)
	baseByID := emissionByID(baseEmissions)
	candidateByID := emissionByID(candidateEmissions)
	rowIDs := make(map[string]struct{}, len(base.Rows)+len(candidate.Rows))
	for rowID := range base.Rows {
		rowIDs[rowID] = struct{}{}
	}
	for rowID := range candidate.Rows {
		rowIDs[rowID] = struct{}{}
	}
	orderedRows := make([]string, 0, len(rowIDs))
	for rowID := range rowIDs {
		orderedRows = append(orderedRows, rowID)
	}
	sort.Strings(orderedRows)
	rowsTruncated := len(orderedRows) > limit
	if len(orderedRows) > limit {
		orderedRows = orderedRows[:limit]
	}

	result := PreviewInterpretationCandidateResult{
		BaseReceiptID: base.ReceiptID, CandidateReceiptID: candidate.ReceiptID,
		OutputID: request.OutputID, Column: request.Column, RevisionID: request.RevisionID,
		Completeness: CandidatePreviewComplete, Samples: make([]InterpretationCandidatePreviewSample, 0, len(orderedRows)),
	}
	if rowsTruncated {
		result.Completeness = CandidatePreviewIncomplete
	}
	for _, rowID := range orderedRows {
		beforeRow, beforeOK := base.Rows[rowID]
		afterRow, afterOK := candidate.Rows[rowID]
		before := make(map[string]any, len(emissionKeys))
		after := make(map[string]any, len(emissionKeys))
		state := CandidatePreviewUnchanged
		rowUnresolved, rowResolved, rowChanged, rowDifferent := false, false, false, false
		for _, key := range emissionKeys {
			beforeEmission := baseByID[key]
			afterEmission := candidateByID[key]
			beforeValue, beforeValueOK := previewEmissionValue(beforeRow, beforeOK, beforeEmission)
			afterValue, afterValueOK := previewEmissionValue(afterRow, afterOK, afterEmission)
			if beforeValueOK {
				before[key] = beforeValue
			}
			if afterValueOK {
				after[key] = afterValue
			}
			beforeUnresolved := !beforeValueOK || beforeValue == nil
			afterUnresolved := !afterValueOK || afterValue == nil
			if beforeUnresolved != afterUnresolved || (!beforeUnresolved && !afterUnresolved && !previewValuesEqual(beforeValue, afterValue)) {
				rowDifferent = true
			}
			if afterUnresolved {
				rowUnresolved = true
			} else if beforeUnresolved {
				rowResolved = true
			} else if !previewValuesEqual(beforeValue, afterValue) {
				rowChanged = true
			}
		}
		switch {
		case rowUnresolved:
			state = CandidatePreviewUnresolved
		case rowResolved:
			state = CandidatePreviewResolved
		case rowChanged:
			state = CandidatePreviewChanged
		}
		result.Samples = append(result.Samples, InterpretationCandidatePreviewSample{RowID: rowID, Before: before, After: after, State: state})
		result.Counts.Compared++
		if rowDifferent {
			result.Counts.Changed++
		}
		switch state {
		case CandidatePreviewResolved:
			result.Counts.Resolved++
		case CandidatePreviewUnresolved:
			result.Counts.Unresolved++
		}
	}
	return result
}

func emissionByID(values []viewer.EmittedColumn) map[string]viewer.EmittedColumn {
	result := make(map[string]viewer.EmittedColumn, len(values))
	for _, value := range values {
		if value.PublicColumn != "" {
			result[value.PublicColumn] = value
		}
	}
	return result
}

func previewEmissionValue(row map[string]any, rowOK bool, emission viewer.EmittedColumn) (any, bool) {
	if !rowOK {
		return nil, false
	}
	if value, ok := row[emission.PublicColumn]; ok {
		return value, true
	}
	return nil, false
}

func previewValuesEqual(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func PreviewSummaryComplete(summary dataframeexecution.PreviewSummary) bool {
	return summary.Complete && !summary.Truncated
}
