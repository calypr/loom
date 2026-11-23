package lifecycle

import (
	"encoding/json"
	"fmt"
	"strings"

	dataframepublished "github.com/calypr/loom/internal/dataframe/published"

	"github.com/calypr/loom/internal/dataframe/artifactformat"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

func artifactDescriptor(receipt *receiptmodel.CompilationReceipt, materialization dataframepublished.Materialization, outputID string) (artifactformat.ArtifactDescriptor, []artifactformat.ArtifactColumn, error) {
	contracts, err := receiptmodel.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		return artifactformat.ArtifactDescriptor{}, nil, err
	}
	contract, ok := contracts.Output(outputID)
	if !ok {
		return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("output %q is absent from its contract", outputID)
	}
	if spec.RowGrain(contract.RowGrain) == spec.RowGrainGroups {
		return groupArtifactDescriptor(receipt, materialization, outputID, contract)
	}
	contractColumns := make(map[string]struct{}, len(contract.Columns))
	for _, column := range contract.Columns {
		contractColumns[strings.TrimSpace(column.Column)] = struct{}{}
	}
	physical := make(map[string]dataframepublished.Column, len(materialization.Columns))
	for _, column := range materialization.Columns {
		name := strings.TrimSpace(column.Name)
		_, selected := contractColumns[name]
		if name != "" && name != "auth_resource_path" && !strings.HasPrefix(name, "__loom_") && (!column.LoomOwned || selected) {
			physical[name] = column
		}
	}
	emissions := make(map[string]viewer.EmittedColumn, len(receipt.EmittedColumns))
	for _, emission := range receipt.EmittedColumns {
		if emission.OutputID != outputID {
			continue
		}
		if _, duplicate := emissions[emission.PublicColumn]; duplicate {
			return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("duplicate emitted column %q", emission.PublicColumn)
		}
		emissions[emission.PublicColumn] = emission
	}
	columns := make([]artifactformat.ArtifactColumn, 0, len(contract.Columns))
	for _, public := range contract.Columns {
		name := strings.TrimSpace(public.Column)
		column, exists := physical[name]
		if !exists {
			return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("contract column %q is absent from the exact materialization", name)
		}
		emission, exists := emissions[name]
		if !exists {
			return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("contract column %q has no construction identity", name)
		}
		columns = append(columns, artifactformat.ArtifactColumn{
			Name: name, OutputKey: public.Column, Label: public.Label, LogicalType: public.LogicalType, Shape: public.Shape,
			ResultUnit: public.ResultUnit,
			Nullable:   public.Nullable, Repeated: column.Repeated || public.Shape == "array" || public.Shape == "record_list",
			EmissionID: emission.EmissionID, CandidateID: emission.CandidateID,
			OccurrenceID: emission.OccurrenceID, Construction: emission.ProjectionMode,
			ReductionPolicy: emission.ProjectionMode, SourceResourceType: emission.SourceResourceType,
			SourcePath: emission.SourcePath, ChoiceArm: emission.ChoiceArm,
			AuthoredColumns: append([]string(nil), public.AuthoredColumns...),
		})
	}
	if len(columns) != len(physical) {
		return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("materialized column count %d does not match output contract count %d", len(physical), len(columns))
	}
	descriptor := artifactformat.ArtifactDescriptor{
		Version: 1, OutputKey: outputID,
		ReceiptFormatVersion:    receipt.ReceiptFormatVersion,
		CompilerContractVersion: receipt.CompilerContractVersion,
		RecipeSchemaVersion:     receipt.Bundle.RecipeSchemaVersion,
		TranslationVersion:      receipt.Bundle.TranslationVersion,
		SourceGeneration:        receipt.SourceGeneration,
		PublishedSchemaDigest:   materialization.SchemaDigest,
		ResolvedSchemaDigest:    receipt.ResolvedSchemaDigest,
		OutputContractDigest:    receipt.OutputContractDigest,
		RowGrain:                contract.RowGrain, RowMultiplication: contract.RowMultiplication,
		RowIdentity: artifactformat.ArtifactRowIdentity{Key: "__loom_row_id"},
	}
	if materialization.SourceRow != nil {
		descriptor.RowIdentity.SourceResourceType = materialization.SourceRow.ResourceType
		descriptor.RowIdentity.SourceIDColumn = materialization.SourceRow.IDColumn
	}
	for _, interpretation := range receipt.ResolvedInterpretations {
		if interpretation.OutputID != outputID {
			continue
		}
		descriptor.Interpretations = append(descriptor.Interpretations, artifactformat.ArtifactInterpretationIdentity{
			OutputKey: interpretation.Column, OccurrenceID: interpretation.OccurrenceID,
			RevisionID: string(interpretation.Revision.ID), ContentDigest: string(interpretation.Revision.ContentDigest),
			RuleID: string(interpretation.SelectedRuleID),
		})
	}
	return descriptor, columns, nil
}

func groupArtifactDescriptor(receipt *receiptmodel.CompilationReceipt, materialization dataframepublished.Materialization, outputID string, contract receiptmodel.PublicOutputContract) (artifactformat.ArtifactDescriptor, []artifactformat.ArtifactColumn, error) {
	contractColumns := make(map[string]receiptmodel.PublicOutputColumn, len(contract.Columns))
	for _, public := range contract.Columns {
		contractColumns[strings.TrimSpace(public.Column)] = public
	}
	columns := make([]artifactformat.ArtifactColumn, 0, len(materialization.Columns))
	for _, column := range materialization.Columns {
		name := strings.TrimSpace(column.Name)
		semanticPath := strings.TrimSpace(column.SemanticPath)
		if name == "" || name == "auth_resource_path" || strings.HasPrefix(name, "__loom_") || column.LoomOwned || !strings.HasPrefix(semanticPath, "groups.") {
			continue
		}
		shape := "scalar"
		if column.Repeated {
			shape = "array"
			if strings.EqualFold(strings.TrimSpace(column.LogicalType), "object") {
				shape = "record_list"
			}
		}
		public := contractColumns[name]
		columns = append(columns, artifactformat.ArtifactColumn{
			Name: name, OutputKey: name, Label: public.Label, LogicalType: column.LogicalType, Shape: shape,
			ResultUnit: public.ResultUnit,
			Nullable:   column.Nullable, Repeated: column.Repeated,
		})
	}
	if len(columns) == 0 {
		return artifactformat.ArtifactDescriptor{}, nil, fmt.Errorf("group output %q has no public synthetic columns in the exact materialization", outputID)
	}
	descriptor := artifactformat.ArtifactDescriptor{
		Version: 1, OutputKey: outputID,
		ReceiptFormatVersion:    receipt.ReceiptFormatVersion,
		CompilerContractVersion: receipt.CompilerContractVersion,
		RecipeSchemaVersion:     receipt.Bundle.RecipeSchemaVersion,
		TranslationVersion:      receipt.Bundle.TranslationVersion,
		SourceGeneration:        receipt.SourceGeneration,
		PublishedSchemaDigest:   materialization.SchemaDigest,
		ResolvedSchemaDigest:    receipt.ResolvedSchemaDigest,
		OutputContractDigest:    receipt.OutputContractDigest,
		RowGrain:                string(spec.RowGrainGroups), RowMultiplication: contract.RowMultiplication,
		RowIdentity: artifactformat.ArtifactRowIdentity{Key: "__loom_row_id"},
	}
	for _, interpretation := range receipt.ResolvedInterpretations {
		if interpretation.OutputID != outputID {
			continue
		}
		descriptor.Interpretations = append(descriptor.Interpretations, artifactformat.ArtifactInterpretationIdentity{
			OutputKey: interpretation.Column, OccurrenceID: interpretation.OccurrenceID,
			RevisionID: string(interpretation.Revision.ID), ContentDigest: string(interpretation.Revision.ContentDigest),
			RuleID: string(interpretation.SelectedRuleID),
		})
	}
	return descriptor, columns, nil
}

func canonicalGroupArtifactIdentity(value any) (map[string]string, error) {
	var encoded []byte
	switch typed := value.(type) {
	case string:
		encoded = []byte(typed)
	case []byte:
		encoded = append([]byte(nil), typed...)
	case map[string]string:
		return validateGroupArtifactIdentityMap(typed)
	case map[string]any:
		fields := make(map[string]string, len(typed))
		for key, field := range typed {
			text, ok := field.(string)
			if !ok {
				return nil, fmt.Errorf("identity field %q must be a string", key)
			}
			fields[key] = text
		}
		return validateGroupArtifactIdentityMap(fields)
	default:
		return nil, fmt.Errorf("identity has unsupported type %T", value)
	}
	var fields map[string]string
	if err := json.Unmarshal(encoded, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("identity is not a JSON object")
	}
	identity, err := validateGroupArtifactIdentityMap(fields)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(struct {
		GroupRevisionID string `json:"group_revision_id"`
		GroupID         string `json:"group_id"`
	}{GroupRevisionID: identity["group_revision_id"], GroupID: identity["group_id"]})
	if err != nil || string(encoded) != string(canonical) {
		return nil, fmt.Errorf("identity JSON is not the canonical compiler group identity")
	}
	return identity, nil
}

func validateGroupArtifactIdentityMap(fields map[string]string) (map[string]string, error) {
	if len(fields) != 2 || strings.TrimSpace(fields["group_revision_id"]) == "" || strings.TrimSpace(fields["group_id"]) == "" {
		return nil, fmt.Errorf("identity must contain exactly non-empty group_revision_id and group_id fields")
	}
	for key := range fields {
		if key != "group_revision_id" && key != "group_id" {
			return nil, fmt.Errorf("identity contains unknown field %q", key)
		}
	}
	return map[string]string{"group_revision_id": fields["group_revision_id"], "group_id": fields["group_id"]}, nil
}

func artifactColumnNames(columns []artifactformat.ArtifactColumn) []string {
	names := make([]string, len(columns))
	for i := range columns {
		names[i] = columns[i].Name
	}
	return names
}

func artifactColumnsContainRowIdentity(descriptor artifactformat.ArtifactDescriptor, columns []artifactformat.ArtifactColumn) bool {
	identityColumn := strings.TrimSpace(descriptor.RowIdentity.SourceIDColumn)
	if identityColumn == "" {
		return false
	}
	for _, column := range columns {
		if column.Name == identityColumn || column.OutputKey == identityColumn {
			return true
		}
	}
	return false
}
