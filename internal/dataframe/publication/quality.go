package publication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	if len(reports) == 0 {
		return nil
	}
	if len(reports) != len(outputs) {
		return fmt.Errorf("quality report count %d does not match output count %d", len(reports), len(outputs))
	}
	expected := make(map[string]struct{}, len(outputs))
	for _, output := range outputs {
		expected[output.Name] = struct{}{}
	}
	for _, report := range reports {
		if strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.PolicyVersion) == "" {
			return fmt.Errorf("output %q quality report identity and policy are required", report.Output)
		}
		if report.ReceiptID != identity.ReceiptID || report.Project != identity.Project || report.DatasetGeneration != identity.DatasetGeneration || report.ScopeDigest != identity.ScopeDigest {
			return fmt.Errorf("output %q quality report does not match publication identity", report.Output)
		}
		if report.Completeness != QualityComplete || report.Verdict != QualityPassed {
			return fmt.Errorf("output %q quality report is not activation-grade", report.Output)
		}
		if _, ok := expected[report.Output]; !ok {
			return fmt.Errorf("quality report names unknown output %q", report.Output)
		}
		delete(expected, report.Output)
	}
	if len(expected) != 0 {
		return fmt.Errorf("quality reports do not cover every output")
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
