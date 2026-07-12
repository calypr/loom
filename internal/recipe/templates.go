package recipe

import (
	"fmt"
	"strings"
)

// TemplateMetadata describes a bounded product starting point. It deliberately
// contains no FHIR paths, compiler configuration, or destination credentials:
// selected columns and filters remain opaque capability IDs in Recipe.
type TemplateMetadata struct {
	ID                  TemplateID        `json:"id"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	AllowedGrains       []Grain           `json:"allowedGrains"`
	DefaultDestination  DestinationType   `json:"defaultDestination"`
	AllowedDestinations []DestinationType `json:"allowedDestinations"`
}

// ListTemplates returns the six supported product starting points in stable
// product-flow order. The returned metadata and every nested slice are owned by
// the caller and may be changed without affecting subsequent calls.
func ListTemplates() []TemplateMetadata {
	templates := make([]TemplateMetadata, len(templateRegistry))
	for index, template := range templateRegistry {
		templates[index] = cloneTemplateMetadata(template)
	}
	return templates
}

// LookupTemplate returns metadata for one supported product starting point.
// The returned metadata is a defensive copy.
func LookupTemplate(id TemplateID) (TemplateMetadata, bool) {
	template, ok := templateByID[id]
	if !ok {
		return TemplateMetadata{}, false
	}
	return cloneTemplateMetadata(template), true
}

func templateAllowsGrain(template TemplateMetadata, grain Grain) bool {
	for _, allowed := range template.AllowedGrains {
		if grain == allowed {
			return true
		}
	}
	return false
}

func templateAllowsDestination(template TemplateMetadata, destination DestinationType) bool {
	for _, allowed := range template.AllowedDestinations {
		if destination == allowed {
			return true
		}
	}
	return false
}

func cloneTemplateMetadata(template TemplateMetadata) TemplateMetadata {
	template.AllowedGrains = append([]Grain(nil), template.AllowedGrains...)
	template.AllowedDestinations = append([]DestinationType(nil), template.AllowedDestinations...)
	return template
}

var templateRegistry = []TemplateMetadata{
	{
		ID:                  TemplatePatientCohort,
		Name:                "Patient cohort",
		Description:         "Build one row per patient for a cohort.",
		AllowedGrains:       []Grain{GrainPatient},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
	{
		ID:                  TemplateSpecimenInventory,
		Name:                "Specimen inventory",
		Description:         "Build one row per specimen for an inventory.",
		AllowedGrains:       []Grain{GrainSpecimen},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
	{
		ID:                  TemplateFileManifest,
		Name:                "File manifest",
		Description:         "Build one row per file for a manifest.",
		AllowedGrains:       []Grain{GrainFile},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
	{
		ID:                  TemplateDiagnoses,
		Name:                "Diagnoses",
		Description:         "Build one row per diagnosis.",
		AllowedGrains:       []Grain{GrainDiagnosis},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
	{
		ID:                  TemplateLabsObservations,
		Name:                "Labs and observations",
		Description:         "Build one row per lab or observation.",
		AllowedGrains:       []Grain{GrainObservation},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
	{
		ID:                  TemplateStudyEnrollment,
		Name:                "Study enrollment",
		Description:         "Build one row per study enrollment.",
		AllowedGrains:       []Grain{GrainStudyEnrollment},
		DefaultDestination:  DestinationPreview,
		AllowedDestinations: allDestinations(),
	},
}

var templateByID = mustIndexTemplates(templateRegistry)

func mustIndexTemplates(templates []TemplateMetadata) map[TemplateID]TemplateMetadata {
	if err := validateTemplateRegistry(templates); err != nil {
		panic(fmt.Sprintf("invalid recipe template registry: %v", err))
	}
	indexed := make(map[TemplateID]TemplateMetadata, len(templates))
	for _, template := range templates {
		indexed[template.ID] = cloneTemplateMetadata(template)
	}
	return indexed
}

func validateTemplateRegistry(templates []TemplateMetadata) error {
	expected := map[TemplateID]struct{}{
		TemplatePatientCohort: {}, TemplateSpecimenInventory: {},
		TemplateFileManifest: {}, TemplateDiagnoses: {},
		TemplateLabsObservations: {}, TemplateStudyEnrollment: {},
	}
	if len(templates) != len(expected) {
		return fmt.Errorf("expected metadata for %d templates, got %d", len(expected), len(templates))
	}
	seenTemplates := make(map[TemplateID]struct{}, len(templates))
	for index, template := range templates {
		field := fmt.Sprintf("templates[%d]", index)
		if _, known := expected[template.ID]; !known {
			return fmt.Errorf("%s.id %q is not a supported template", field, template.ID)
		}
		if _, duplicate := seenTemplates[template.ID]; duplicate {
			return fmt.Errorf("%s.id %q is duplicated", field, template.ID)
		}
		seenTemplates[template.ID] = struct{}{}
		if strings.TrimSpace(template.Name) == "" || hasControl(template.Name) {
			return fmt.Errorf("%s.name must be non-empty printable text", field)
		}
		if strings.TrimSpace(template.Description) == "" || hasControl(template.Description) {
			return fmt.Errorf("%s.description must be non-empty printable text", field)
		}
		if err := validateTemplateGrains(template.AllowedGrains, field); err != nil {
			return err
		}
		if err := validateTemplateDestinations(template, field); err != nil {
			return err
		}
	}
	for id := range expected {
		if _, ok := seenTemplates[id]; !ok {
			return fmt.Errorf("missing metadata for template %q", id)
		}
	}
	return nil
}

func validateTemplateGrains(grains []Grain, field string) error {
	if len(grains) == 0 {
		return fmt.Errorf("%s.allowedGrains must not be empty", field)
	}
	known := map[Grain]struct{}{
		GrainPatient: {}, GrainSpecimen: {}, GrainFile: {},
		GrainDiagnosis: {}, GrainObservation: {}, GrainStudyEnrollment: {},
	}
	seen := make(map[Grain]struct{}, len(grains))
	for _, grain := range grains {
		if _, ok := known[grain]; !ok {
			return fmt.Errorf("%s.allowedGrains contains unsupported grain %q", field, grain)
		}
		if _, duplicate := seen[grain]; duplicate {
			return fmt.Errorf("%s.allowedGrains contains duplicate grain %q", field, grain)
		}
		seen[grain] = struct{}{}
	}
	return nil
}

func validateTemplateDestinations(template TemplateMetadata, field string) error {
	if len(template.AllowedDestinations) == 0 {
		return fmt.Errorf("%s.allowedDestinations must not be empty", field)
	}
	seen := make(map[DestinationType]struct{}, len(template.AllowedDestinations))
	for _, destination := range template.AllowedDestinations {
		if !validDestination(destination) {
			return fmt.Errorf("%s.allowedDestinations contains unsupported destination %q", field, destination)
		}
		if _, duplicate := seen[destination]; duplicate {
			return fmt.Errorf("%s.allowedDestinations contains duplicate destination %q", field, destination)
		}
		seen[destination] = struct{}{}
	}
	if !validDestination(template.DefaultDestination) {
		return fmt.Errorf("%s.defaultDestination %q is unsupported", field, template.DefaultDestination)
	}
	if _, ok := seen[template.DefaultDestination]; !ok {
		return fmt.Errorf("%s.defaultDestination %q is not allowed", field, template.DefaultDestination)
	}
	return nil
}

func validDestination(destination DestinationType) bool {
	switch destination {
	case DestinationPreview, DestinationNDJSON, DestinationCSV, DestinationElasticsearch:
		return true
	default:
		return false
	}
}

func allDestinations() []DestinationType {
	return []DestinationType{
		DestinationPreview,
		DestinationNDJSON,
		DestinationCSV,
		DestinationElasticsearch,
	}
}
