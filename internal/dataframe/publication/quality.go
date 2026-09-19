package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const DefaultQualityPolicyVersion = "loom.quality/v1"

var (
	ErrQualityIncomplete = errors.New("QUALITY_INCOMPLETE")
	ErrQualityFailed     = errors.New("QUALITY_FAILED")
)

type QualityCompleteness string

const (
	QualityComplete   QualityCompleteness = "COMPLETE"
	QualityIncomplete QualityCompleteness = "INCOMPLETE"
)

type QualityVerdict string

const (
	QualityPassed QualityVerdict = "PASSED"
	QualityFailed QualityVerdict = "FAILED"
)

// QualityPolicy bounds the state retained while the publication stream is
// checked. An empty Version disables reporting for compatibility callers;
// Explorer publication always supplies an explicit versioned policy.
type QualityPolicy struct {
	Version               string
	MaxRows               int64
	MaxDistinctKeys       int64
	RequireUniqueIdentity bool
	Omissions             []QualityOmission
}

func (p QualityPolicy) enabled() bool { return strings.TrimSpace(p.Version) != "" }

type QualityOmission struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type ColumnQuality struct {
	Column       string `json:"column"`
	Present      int64  `json:"present"`
	Missing      int64  `json:"missing"`
	RecordedNull int64  `json:"recordedNull"`
	EmptyArray   int64  `json:"emptyArray"`
}

// QualityLimits records the exact bounds under which the full-population
// report was produced. A complete report is meaningful only together with
// the limits that could have stopped its scan.
type QualityLimits struct {
	MaxRows         int64 `json:"maxRows"`
	MaxDistinctKeys int64 `json:"maxDistinctKeys"`
}

// QualityIssues counts data-dependent failures enforced by the same stream
// that materializes the candidate publication. Successful reports therefore
// contain explicit zeroes instead of leaving these checks implicit.
type QualityIssues struct {
	Ambiguous        int64 `json:"ambiguous"`
	InvalidType      int64 `json:"invalidType"`
	IncompatibleUnit int64 `json:"incompatibleUnit"`
}

type KeyIntegrity struct {
	Distinct  int64 `json:"distinct"`
	Missing   int64 `json:"missing"`
	Duplicate int64 `json:"duplicate"`
}

// QualityReport is immutable evidence about one fully consumed output stream.
// Its identity is derived only after the receipt already exists, so it can
// never participate in receipt identity.
type QualityReport struct {
	ID                string              `json:"id"`
	ReceiptID         string              `json:"receiptId"`
	Project           string              `json:"project"`
	DatasetGeneration string              `json:"datasetGeneration"`
	ScopeDigest       string              `json:"scopeDigest"`
	Output            string              `json:"output"`
	PolicyVersion     string              `json:"policyVersion"`
	Completeness      QualityCompleteness `json:"completeness"`
	Verdict           QualityVerdict      `json:"verdict"`
	RowCount          int64               `json:"rowCount"`
	Columns           []ColumnQuality     `json:"columns"`
	KeyIntegrity      KeyIntegrity        `json:"keyIntegrity"`
	Limits            QualityLimits       `json:"limits"`
	Issues            QualityIssues       `json:"issues"`
	Omissions         []QualityOmission   `json:"omissions,omitempty"`
}

type QualityIncompleteError struct{ Reports []QualityReport }

func (e *QualityIncompleteError) Error() string { return ErrQualityIncomplete.Error() }
func (e *QualityIncompleteError) Unwrap() error { return ErrQualityIncomplete }

type QualityFailedError struct{ Reports []QualityReport }

func (e *QualityFailedError) Error() string { return ErrQualityFailed.Error() }
func (e *QualityFailedError) Unwrap() error { return ErrQualityFailed }

// CloneQualityReports prevents callers from mutating evidence held by a
// durable publication execution.
func CloneQualityReports(reports []QualityReport) []QualityReport {
	cloned := make([]QualityReport, len(reports))
	for index, report := range reports {
		cloned[index] = report
		cloned[index].Columns = append([]ColumnQuality(nil), report.Columns...)
		cloned[index].Omissions = append([]QualityOmission(nil), report.Omissions...)
	}
	return cloned
}

// ValidateQualityReports enforces the activation-grade relationship between
// evidence and the durable publication it describes.
func ValidateQualityReports(identity BundleIdentity, outputs []BundleOutputRecord, reports []QualityReport) error {
	if err := ValidateQualityReportBindings(identity, outputs, reports); err != nil {
		return err
	}
	if len(reports) == 0 {
		return nil
	}
	if len(reports) != len(outputs) {
		return fmt.Errorf("quality report count %d does not match output count %d", len(reports), len(outputs))
	}
	for _, report := range reports {
		if report.Completeness != QualityComplete || report.Verdict != QualityPassed {
			return fmt.Errorf("output %q quality report is not activation-grade", report.Output)
		}
	}
	return nil
}

// ValidateQualityReportBindings permits partial or failed evidence to be
// retained on a failed candidate execution while still proving that every
// report belongs to that exact publication attempt. Activation uses the
// stricter ValidateQualityReports contract above.
func ValidateQualityReportBindings(identity BundleIdentity, outputs []BundleOutputRecord, reports []QualityReport) error {
	known := make(map[string]struct{}, len(outputs))
	for _, output := range outputs {
		known[output.Name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		if strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.PolicyVersion) == "" {
			return fmt.Errorf("output %q quality report identity and policy are required", report.Output)
		}
		if report.ReceiptID != identity.ReceiptID || report.Project != identity.Project || report.DatasetGeneration != identity.DatasetGeneration || report.ScopeDigest != identity.ScopeDigest {
			return fmt.Errorf("output %q quality report does not match publication identity", report.Output)
		}
		if _, ok := known[report.Output]; !ok {
			return fmt.Errorf("quality report names unknown output %q", report.Output)
		}
		if _, duplicate := seen[report.Output]; duplicate {
			return fmt.Errorf("quality reports contain duplicate output %q", report.Output)
		}
		seen[report.Output] = struct{}{}
	}
	return nil
}

type qualityAccumulator struct {
	identity        PublicationIdentity
	output          OutputStream
	policy          QualityPolicy
	report          QualityReport
	columnIndex     map[string]int
	identityColumns []string
	keys            map[string]struct{}
}

func newQualityAccumulator(identity PublicationIdentity, output OutputStream, policy QualityPolicy) *qualityAccumulator {
	if !policy.enabled() {
		return nil
	}
	report := QualityReport{
		ReceiptID: identity.ReceiptID, Project: identity.Project,
		DatasetGeneration: identity.DatasetGeneration, ScopeDigest: identity.ScopeDigest,
		Output: output.Name, PolicyVersion: policy.Version,
		Completeness: QualityComplete, Verdict: QualityPassed,
		Limits:    QualityLimits{MaxRows: policy.MaxRows, MaxDistinctKeys: policy.MaxDistinctKeys},
		Omissions: append([]QualityOmission(nil), policy.Omissions...),
	}
	accumulator := &qualityAccumulator{
		identity: identity, output: output, policy: policy, report: report,
		columnIndex: make(map[string]int), keys: make(map[string]struct{}),
	}
	for _, column := range output.Columns {
		if column.IsIdentity {
			accumulator.identityColumns = append(accumulator.identityColumns, column.Name)
		}
		if column.LoomOwned || column.IsIdentity {
			continue
		}
		accumulator.columnIndex[column.Name] = len(accumulator.report.Columns)
		accumulator.report.Columns = append(accumulator.report.Columns, ColumnQuality{Column: column.Name})
	}
	if len(accumulator.identityColumns) == 0 {
		accumulator.report.Omissions = append(accumulator.report.Omissions, QualityOmission{Code: "KEY_INTEGRITY_NOT_AVAILABLE", Detail: "The compiled output has no stable identity projection."})
	}
	return accumulator
}

// fail finalizes evidence for a stream that did not reach natural exhaustion.
// Known semantic failures are complete negative findings; cancellation and
// infrastructure failures are incomplete because absence was not proven.
func (a *qualityAccumulator) fail(cause error) (QualityReport, error) {
	if a == nil {
		return QualityReport{}, nil
	}
	if userErr, ok := dataframeerrors.AsUserError(cause); ok {
		switch dataframeerrors.ErrorCode(userErr.Code()) {
		case dataframeerrors.CodeRelationshipCardinalityViolation, dataframeerrors.CodeTemporalTieAmbiguous:
			a.report.Issues.Ambiguous++
			a.report.Verdict = QualityFailed
			a.finish()
			return a.report, &QualityFailedError{Reports: []QualityReport{a.report}}
		case dataframeerrors.CodeUnitIdentityUnknown, dataframeerrors.CodeUnitDimensionIncompatible:
			a.report.Issues.IncompatibleUnit++
			a.report.Verdict = QualityFailed
			a.finish()
			return a.report, &QualityFailedError{Reports: []QualityReport{a.report}}
		case dataframeerrors.CodeInvalidData, dataframeerrors.CodeRecipeContractViolation:
			a.report.Issues.InvalidType++
			a.report.Verdict = QualityFailed
			a.finish()
			return a.report, &QualityFailedError{Reports: []QualityReport{a.report}}
		}
	}
	a.report.Completeness = QualityIncomplete
	code, detail := "STREAM_INTERRUPTED", "The quality scan ended before the publication stream was exhausted."
	if errors.Is(cause, context.DeadlineExceeded) {
		code, detail = "TIME_LIMIT_EXCEEDED", "The quality scan exceeded its execution deadline."
	} else if errors.Is(cause, context.Canceled) {
		code, detail = "SCAN_CANCELED", "The quality scan was canceled before completion."
	}
	a.report.Omissions = append(a.report.Omissions, QualityOmission{Code: code, Detail: detail})
	a.finish()
	return a.report, &QualityIncompleteError{Reports: []QualityReport{a.report}}
}

func (a *qualityAccumulator) observe(row map[string]any) error {
	if a == nil {
		return nil
	}
	if a.policy.MaxRows > 0 && a.report.RowCount >= a.policy.MaxRows {
		a.report.Completeness = QualityIncomplete
		a.report.Omissions = append(a.report.Omissions, QualityOmission{Code: "ROW_LIMIT_EXCEEDED", Detail: fmt.Sprintf("The quality scan exceeded %d rows.", a.policy.MaxRows)})
		a.finish()
		return &QualityIncompleteError{Reports: []QualityReport{a.report}}
	}
	a.report.RowCount++
	for name, index := range a.columnIndex {
		quality := &a.report.Columns[index]
		value, ok := row[name]
		switch {
		case !ok:
			quality.Missing++
		case value == nil:
			quality.RecordedNull++
		case emptyRepeatedValue(value):
			quality.EmptyArray++
		default:
			quality.Present++
		}
	}
	if len(a.identityColumns) == 0 {
		return nil
	}
	key, ok := qualityIdentityKey(row, a.identityColumns)
	if !ok {
		a.report.KeyIntegrity.Missing++
		return nil
	}
	if _, exists := a.keys[key]; exists {
		a.report.KeyIntegrity.Duplicate++
		return nil
	}
	if a.policy.MaxDistinctKeys > 0 && int64(len(a.keys)) >= a.policy.MaxDistinctKeys {
		a.report.Completeness = QualityIncomplete
		a.report.Omissions = append(a.report.Omissions, QualityOmission{Code: "KEY_LIMIT_EXCEEDED", Detail: fmt.Sprintf("The quality scan exceeded %d distinct row keys.", a.policy.MaxDistinctKeys)})
		a.finish()
		return &QualityIncompleteError{Reports: []QualityReport{a.report}}
	}
	a.keys[key] = struct{}{}
	a.report.KeyIntegrity.Distinct++
	return nil
}

func (a *qualityAccumulator) complete() (QualityReport, error) {
	if a == nil {
		return QualityReport{}, nil
	}
	if a.policy.RequireUniqueIdentity && (a.report.KeyIntegrity.Missing > 0 || a.report.KeyIntegrity.Duplicate > 0) {
		a.report.Verdict = QualityFailed
	}
	a.finish()
	if a.report.Verdict == QualityFailed {
		return a.report, &QualityFailedError{Reports: []QualityReport{a.report}}
	}
	return a.report, nil
}

func (a *qualityAccumulator) finish() {
	copy := a.report
	copy.ID = ""
	raw, _ := json.Marshal(copy)
	digest := sha256.Sum256(raw)
	a.report.ID = "quality_" + hex.EncodeToString(digest[:])
}

func emptyRepeatedValue(value any) bool {
	reflected := reflect.ValueOf(value)
	return (reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Slice) && reflected.Len() == 0
}

func qualityIdentityKey(row map[string]any, columns []string) (string, bool) {
	parts := make([]any, len(columns))
	for index, column := range columns {
		value, ok := row[column]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", false
		}
		parts[index] = value
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
