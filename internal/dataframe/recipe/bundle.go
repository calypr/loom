package recipe

const CurrentSchemaVersion = 1

const (
	maxExpressionDepth = 64
	maxExpressionNodes = 4096
	maxLiteralArray    = 256
)

type Bundle struct {
	RecipeSchemaVersion int              `json:"recipeSchemaVersion"`
	Name                string           `json:"name"`
	TranslationVersion  string           `json:"translationVersion"`
	Fragments           *FragmentLibrary `json:"fragments,omitempty"`
	Outputs             []Output         `json:"outputs"`
}
