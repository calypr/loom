package fieldprofile

import (
	"bytes"
	"encoding/json"

	"github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

func (p *Profiler) observeSchemaSemantics(
	index *schema.Index,
	payload map[string]any,
	profile string,
	emit func(semantic.Observation, []any),
) error {
	return p.observeSchemaSemanticsWithEvidence(index, payload, profile, "", func(observation semantic.Observation, examples []any, _ SemanticCoverageEmissionEvidence) {
		emit(observation, examples)
	})
}

func (p *Profiler) observeSchemaSemanticsWithEvidence(
	index *schema.Index,
	payload map[string]any,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) error {
	return p.semanticExtractor.Extract(index, p.resourceType, payload, profile, func(record semantic.Record) {
		p.recordSchemaSemanticObservation(record, sourceID, emit)
	})
}

func (p *Profiler) recordSchemaSemanticObservation(record semantic.Record, sourceID string, emit semanticCoverageObservationEmitter) {
	if stat := p.ensureSemanticStat(record.StoragePath); stat != nil {
		stat.addSemanticObservation(record.Observation, record.Examples)
	}
	var evidence SemanticCoverageEmissionEvidence
	if sourceID != "" {
		evidence = semanticCoverageEmissionEvidence(
			p.resourceType, p.semanticSourceKind, sourceID, record.ScopeOwnerPath,
			record.ConceptOwnerPath, record.KeyOwnerPath, record.ValueOwnerPath, record.Observation,
		)
	}
	emit(record.Observation, record.Examples, evidence)
}

func decodeSemanticValue(raw json.RawMessage) (any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

func semanticStringFromRaw(raw json.RawMessage) string {
	value, ok := decodeSemanticValue(raw)
	if !ok {
		return ""
	}
	return stringValue(value)
}
