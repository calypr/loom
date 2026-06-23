package graphqlapi

import (
	"fmt"
	"regexp"
	"strings"

	"arangodb-proto/internal/dataframe"
	"arangodb-proto/internal/proto"
)

type FieldHintResponse struct {
	ResourceType      string
	FieldRef          string
	Label             string
	Path              string
	Selector          FieldSelectorResponse
	Kind              string
	DocCount          int64
	SampleCount       int
	DistinctValues    []string
	DistinctTruncated bool
	PivotCandidate    bool
	PivotKind         string
	PivotColumns      []string
}

type FieldSelectorResponse struct {
	SourcePath string
	Where      *FieldPredicateResponse
	ValuePath  string
}

type FieldPredicateResponse struct {
	Path  string
	Op    string
	Value string
}

type fieldAlias struct {
	ResourceType string
	FieldRef     string
	Label        string
	Selector     string
}

var fieldRefSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func discoveredFieldHints(resourceType string, fields []proto.PopulatedField) []FieldHintResponse {
	if len(fields) == 0 {
		return []FieldHintResponse{}
	}
	out := make([]FieldHintResponse, 0, len(fields)+8)
	seen := map[string]struct{}{}
	for _, alias := range aliasesForResource(resourceType) {
		sel, err := dataframe.ParseSelector(alias.Selector)
		if err != nil {
			continue
		}
		base := findFieldByPath(fields, sel.CanonicalPath())
		if base == nil {
			continue
		}
		out = append(out, fieldHintFromDiscovered(*base, alias.FieldRef, alias.Label, alias.Selector))
		seen[alias.FieldRef] = struct{}{}
	}
	for _, field := range fields {
		ref := defaultFieldRef(resourceType, field.Path)
		if _, ok := seen[ref]; ok {
			continue
		}
		out = append(out, fieldHintFromDiscovered(field, ref, defaultFieldLabel(field.Path), field.Path))
	}
	return out
}

func fieldHintFromDiscovered(field proto.PopulatedField, fieldRef string, label string, selector string) FieldHintResponse {
	selectorParts := decomposeSelector(selector)
	return FieldHintResponse{
		ResourceType:      field.ResourceType,
		FieldRef:          fieldRef,
		Label:             label,
		Path:              field.Path,
		Selector:          selectorParts,
		Kind:              field.Kind,
		DocCount:          field.DocCount,
		SampleCount:       field.SampleCount,
		DistinctValues:    cloneStrings(field.DistinctValues),
		DistinctTruncated: field.DistinctTruncated,
		PivotCandidate:    field.PivotCandidate,
		PivotKind:         field.PivotKind,
		PivotColumns:      cloneStrings(field.PivotColumns),
	}
}

func resolveFieldRef(resourceType string, discovered []proto.PopulatedField, fieldRef string) (string, error) {
	fieldRef = strings.TrimSpace(fieldRef)
	if fieldRef == "" {
		return "", fmt.Errorf("fieldRef is required")
	}
	for _, alias := range aliasesForResource(resourceType) {
		if alias.FieldRef == fieldRef {
			return alias.Selector, nil
		}
	}
	for _, field := range discovered {
		if defaultFieldRef(resourceType, field.Path) == fieldRef {
			return field.Path, nil
		}
	}
	return "", fmt.Errorf("unknown fieldRef %q for resourceType %q", fieldRef, resourceType)
}

func findFieldByPath(fields []proto.PopulatedField, path string) *proto.PopulatedField {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}

func aliasesForResource(resourceType string) []fieldAlias {
	switch resourceType {
	case "Patient":
		return []fieldAlias{
			{ResourceType: resourceType, FieldRef: "Patient.case_id", Label: "Case ID", Selector: `identifier[].value where system contains "case_id"`},
			{ResourceType: resourceType, FieldRef: "Patient.case_submitter_id", Label: "Case Submitter ID", Selector: `identifier[].value where system contains "case_submitter_id"`},
			{ResourceType: resourceType, FieldRef: "Patient.gender", Label: "Gender", Selector: `gender`},
			{ResourceType: resourceType, FieldRef: "Patient.deceased", Label: "Deceased", Selector: `deceasedBoolean`},
			{ResourceType: resourceType, FieldRef: "Patient.race", Label: "Race", Selector: `extension[].valueString where url contains "us-core-race"`},
			{ResourceType: resourceType, FieldRef: "Patient.ethnicity", Label: "Ethnicity", Selector: `extension[].valueString where url contains "us-core-ethnicity"`},
			{ResourceType: resourceType, FieldRef: "Patient.birth_sex", Label: "Birth Sex", Selector: `extension[].valueCode where url contains "us-core-birthsex"`},
			{ResourceType: resourceType, FieldRef: "Patient.patient_age", Label: "Patient Age", Selector: `extension[].valueQuantity.value where url contains "Patient-age"`},
			{ResourceType: resourceType, FieldRef: "Patient.part_of_study", Label: "Part Of Study", Selector: `extension[].valueReference.reference where url contains "part-of-study"`},
		}
	case "Condition":
		return []fieldAlias{
			{ResourceType: resourceType, FieldRef: "Condition.id", Label: "Condition ID", Selector: `id`},
			{ResourceType: resourceType, FieldRef: "Condition.diagnosis", Label: "Diagnosis", Selector: `code.coding[].display`},
			{ResourceType: resourceType, FieldRef: "Condition.body_site", Label: "Body Site", Selector: `bodySite[].coding[].display`},
		}
	case "Specimen":
		return []fieldAlias{
			{ResourceType: resourceType, FieldRef: "Specimen.id", Label: "Specimen ID", Selector: `id`},
			{ResourceType: resourceType, FieldRef: "Specimen.type_display", Label: "Specimen Type", Selector: `type.coding[].display`},
			{ResourceType: resourceType, FieldRef: "Specimen.preservation_method", Label: "Preservation Method", Selector: `processing[].method.coding[].display where system contains "preservation_method"`},
		}
	case "ResearchSubject":
		return []fieldAlias{
			{ResourceType: resourceType, FieldRef: "ResearchSubject.id", Label: "Research Subject ID", Selector: `id`},
			{ResourceType: resourceType, FieldRef: "ResearchSubject.status", Label: "Status", Selector: `status`},
			{ResourceType: resourceType, FieldRef: "ResearchSubject.study_ref", Label: "Study Reference", Selector: `study.reference`},
		}
	case "DocumentReference":
		return []fieldAlias{
			{ResourceType: resourceType, FieldRef: "DocumentReference.file_id", Label: "File ID", Selector: `identifier[].value where system contains "file_id"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.file_name", Label: "File Name", Selector: `content[].attachment.title`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.file_url", Label: "File URL", Selector: `content[].attachment.url`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.file_size", Label: "File Size", Selector: `content[].attachment.size`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.data_category", Label: "Data Category", Selector: `category[].coding[].display where system contains "data_category"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.data_type", Label: "Data Type", Selector: `category[].coding[].display where system contains "data_type"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.experimental_strategy", Label: "Experimental Strategy", Selector: `category[].coding[].display where system contains "experimental_strategy"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.workflow_type", Label: "Workflow Type", Selector: `category[].coding[].display where system contains "workflow_type"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.platform", Label: "Platform", Selector: `category[].coding[].display where system contains "platform"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.access", Label: "Access", Selector: `category[].coding[].display where system contains "access"`},
			{ResourceType: resourceType, FieldRef: "DocumentReference.data_format", Label: "Data Format", Selector: `type.coding[].display`},
		}
	}
	return nil
}

func defaultFieldRef(resourceType string, path string) string {
	key := strings.ToLower(path)
	key = strings.ReplaceAll(key, "[]", "_")
	key = strings.ReplaceAll(key, ".", "_")
	key = fieldRefSanitizer.ReplaceAllString(key, "_")
	key = strings.Trim(key, "_")
	return resourceType + "." + key
}

func defaultFieldLabel(path string) string {
	path = strings.ReplaceAll(path, "[]", "")
	parts := strings.Split(path, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func decomposeSelector(expression string) FieldSelectorResponse {
	sel, err := dataframe.ParseSelector(expression)
	if err != nil {
		return FieldSelectorResponse{
			ValuePath: strings.TrimSpace(expression),
		}
	}
	sourcePath := ""
	valuePath := ""
	if len(sel.Steps) > 0 {
		last := len(sel.Steps) - 1
		valuePath = selectorStepText(sel.Steps[last])
		if last > 0 {
			sourceParts := make([]string, 0, last)
			for _, step := range sel.Steps[:last] {
				sourceParts = append(sourceParts, selectorStepText(step))
			}
			sourcePath = strings.Join(sourceParts, ".")
		}
	}
	var where *FieldPredicateResponse
	if sel.Filter != nil {
		where = &FieldPredicateResponse{
			Path:  sel.Filter.Field,
			Op:    "CONTAINS",
			Value: sel.Filter.Needle,
		}
	}
	return FieldSelectorResponse{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  valuePath,
	}
}

func composeSelector(sourcePath string, wherePath string, whereOp string, whereValue string, valuePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	valuePath = strings.TrimSpace(valuePath)
	if valuePath == "" {
		return ""
	}
	path := valuePath
	if sourcePath != "" {
		path = sourcePath + "." + valuePath
	}
	wherePath = strings.TrimSpace(wherePath)
	whereOp = strings.TrimSpace(strings.ToUpper(whereOp))
	whereValue = strings.TrimSpace(whereValue)
	if wherePath == "" || whereOp == "" || whereValue == "" {
		return path
	}
	if whereOp == "CONTAINS" {
		return fmt.Sprintf(`%s where %s contains %q`, path, wherePath, whereValue)
	}
	return path
}

func selectorStepText(step dataframe.SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}
