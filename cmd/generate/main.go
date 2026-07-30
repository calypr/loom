package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Schema struct {
	Defs map[string]*Definition `json:"$defs"`
}

type Definition struct {
	ID                   string               `json:"$id"`
	Type                 string               `json:"type"`
	Properties           map[string]*Property `json:"properties"`
	Required             []string             `json:"required"`
	AdditionalProperties bool                 `json:"additionalProperties"`
	Links                []Link               `json:"links"`
}

type Property struct {
	Ref        string               `json:"$ref"`
	Type       any                  `json:"type"` // string or []string
	Items      *Property            `json:"items"`
	Properties map[string]*Property `json:"properties"`
	Required   []string             `json:"required"`
	// Constraints
	Pattern          string   `json:"pattern"`
	MinLength        *int     `json:"minLength"`
	MaxLength        *int     `json:"maxLength"`
	Minimum          *float64 `json:"minimum"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum"`
	Const            any      `json:"const"`
	Format           string   `json:"format"`
}

type Link struct {
	Rel              string           `json:"rel"`
	TargetHints      TargetHints      `json:"targetHints"`
	TargetSchema     TargetSchema     `json:"targetSchema"`
	TemplatePointers TemplatePointers `json:"templatePointers"`
}

type TargetHints struct {
	Backref      []string `json:"backref"`
	Direction    []string `json:"direction"`
	Multiplicity []string `json:"multiplicity"`
	RegexMatch   []string `json:"regex_match"`
}

type TargetSchema struct {
	Ref string `json:"$ref"`
}

type TemplatePointers struct {
	ID string `json:"id"`
}

func main() {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	schemaPath := fs.String("schema", "schemas/graph-fhir.json", "Path to graph-fhir JSON schema")
	structsDir := fs.String("structs-out", "fhirstructs", "Directory for generated FHIR Go structs, validation, and edge extraction")
	metadataOut := fs.String("metadata-out", "fhirschema/generated.go", "Path for generated compiler FHIR schema metadata")
	graphqlOut := fs.String("graphql-out", "graphqlapi/fhir_schema.graphqls", "Path for generated FHIR GraphQL schema")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Reading schema from %s...\n", *schemaPath)
	data, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading schema: %v\n", err)
		os.Exit(1)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing schema JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*structsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*metadataOut), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating compiler metadata directory: %v\n", err)
		os.Exit(1)
	}

	// 1. Generate model.go
	if err := generateModel(&schema, filepath.Join(*structsDir, "model.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating model.go: %v\n", err)
		os.Exit(1)
	}

	// 2. Generate validate.go
	if err := generateValidate(&schema, filepath.Join(*structsDir, "validate.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating validate.go: %v\n", err)
		os.Exit(1)
	}

	// 3. Generate extract.go
	if err := generateExtract(&schema, filepath.Join(*structsDir, "extract.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating extract.go: %v\n", err)
		os.Exit(1)
	}

	// 4. Generate the concrete-resource registry and GraphQL markers.
	if err := generateFHIRResources(&schema, filepath.Join(*structsDir, "resources.go"), filepath.Join(*structsDir, "graphql.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating resource registry: %v\n", err)
		os.Exit(1)
	}

	// 5. Generate fhirschema metadata
	if err := generateFHIRSchema(&schema, *metadataOut); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating fhirschema/generated.go: %v\n", err)
		os.Exit(1)
	}

	// Keep the FHIR GraphQL surface generated from the same parsed schema as
	// the compiler metadata and Go models. The SDL is intentionally emitted as
	// a separate schema document so the handwritten dataframe API remains
	// readable and stable.
	if strings.TrimSpace(*graphqlOut) != "" {
		if err := os.MkdirAll(filepath.Dir(*graphqlOut), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating GraphQL schema directory: %v\n", err)
			os.Exit(1)
		}
		if err := generateFHIRGraphQL(&schema, *graphqlOut); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating FHIR GraphQL schema: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Code generation completed successfully.")
}

func refName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func targetTypeFromLabel(label string) string {
	parts := strings.Split(label, "_")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// generatedForwardTargetType preserves the existing extractor convention
// unless a compiler-owned storage route has a schema-proven exception. This
// is deliberately not a general conversion from link labels to targetSchema:
// unrelated legacy graph edges remain outside the generic compiler's forward
// storage contract.
func generatedForwardTargetType(structName string, link Link) string {
	labelTargetType := targetTypeFromLabel(link.Rel)
	if strings.TrimSpace(structName) != "ResearchSubject" || strings.TrimSpace(link.Rel) != "study" {
		return labelTargetType
	}
	if targetType := refName(link.TargetSchema.Ref); targetType == "ResearchStudy" {
		return targetType
	}
	return labelTargetType
}

func toGoName(s string) string {
	if s == "" {
		return ""
	}
	prefix := ""
	if strings.HasPrefix(s, "_") {
		prefix = "X"
		s = s[1:]
	}
	parts := strings.Split(s, "-")
	for i, part := range parts {
		parts[i] = capitalize(part)
	}
	s = strings.Join(parts, "")
	parts = strings.Split(s, "_")
	for i, part := range parts {
		parts[i] = capitalize(part)
	}
	s = strings.Join(parts, "")
	s = strings.ReplaceAll(s, "Id", "ID")
	s = strings.ReplaceAll(s, "Uri", "URI")
	s = strings.ReplaceAll(s, "Url", "URL")
	s = strings.ReplaceAll(s, "Uuid", "UUID")
	return prefix + s
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

func isRequired(def *Definition, propName string) bool {
	for _, req := range def.Required {
		if req == propName {
			return true
		}
	}
	return false
}

func getGoType(prop *Property, isField bool) string {
	if prop.Ref != "" {
		name := refName(prop.Ref)
		if name == "links" {
			return "[]any"
		}
		return "*" + name
	}

	// Check for fhir_comments which is anyOf [string, array of string]
	if prop.Type == nil && prop.Items == nil && prop.Ref == "" {
		return "FHIRComments"
	}

	typeStr, ok := prop.Type.(string)
	if !ok {
		return "any"
	}

	switch typeStr {
	case "string":
		if isField {
			return "*string"
		}
		return "string"
	case "boolean":
		if isField {
			return "*bool"
		}
		return "bool"
	case "number":
		if isField {
			return "*float64"
		}
		return "float64"
	case "integer":
		if isField {
			return "*int64"
		}
		return "int64"
	case "array":
		if prop.Items != nil {
			itemType := getGoType(prop.Items, false)
			return "[]" + itemType
		}
		return "[]any"
	case "object":
		return "map[string]any"
	default:
		return "any"
	}
}

func generateModel(schema *Schema, path string) error {
	var sb strings.Builder
	sb.WriteString("// Code generated by cmd/generate/main.go. DO NOT EDIT.\n")
	sb.WriteString("package fhirstructs\n\n")

	var keys []string
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		def := schema.Defs[k]
		sb.WriteString(fmt.Sprintf("// %s\n", k))
		sb.WriteString(fmt.Sprintf("type %s struct {\n", k))

		var propKeys []string
		for pk := range def.Properties {
			propKeys = append(propKeys, pk)
		}
		sort.Strings(propKeys)

		for _, pk := range propKeys {
			prop := def.Properties[pk]
			required := isRequired(def, pk)
			goType := getGoType(prop, true)
			goName := toGoName(pk)

			jsonTag := pk
			if !required {
				jsonTag += ",omitempty"
			}

			// gqlgen uses this explicit tag to preserve FHIR primitive-extension
			// names such as _birthDate, whose Go field is XBirthDate. Keeping
			// the tag on every field makes autobinding deterministic while the
			// JSON tag remains the storage contract.
			sb.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\" gqlgen:\"%s\"`\n", goName, goType, jsonTag, pk))
		}
		sb.WriteString("}\n\n")
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func generateValidate(schema *Schema, path string) error {
	var sb strings.Builder
	sb.WriteString("// Code generated by cmd/generate/main.go. DO NOT EDIT.\n")
	sb.WriteString("package fhirstructs\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\t\"fmt\"\n")
	sb.WriteString("\t\"regexp\"\n")
	sb.WriteString("\t\"unicode/utf8\"\n")
	sb.WriteString(")\n\n")

	var keys []string
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Collect and write regexes
	sb.WriteString("// Pre-compiled regular expressions for pattern validation\n")
	sb.WriteString("var (\n")
	regexMap := make(map[string]string)
	for _, k := range keys {
		def := schema.Defs[k]
		var propKeys []string
		for pk := range def.Properties {
			propKeys = append(propKeys, pk)
		}
		sort.Strings(propKeys)

		for _, pk := range propKeys {
			prop := def.Properties[pk]
			if prop.Pattern != "" && prop.Pattern != "[ \\r\\n\\t\\S]+" && prop.Pattern != "\\S*" {
				varName := fmt.Sprintf("rx_%s_%s", k, toGoName(pk))
				regexMap[varName] = prop.Pattern
				sb.WriteString(fmt.Sprintf("\t%s = regexp.MustCompile(%s)\n", varName, strconv.Quote(prop.Pattern)))
			}
			// check array items pattern
			if prop.Items != nil && prop.Items.Pattern != "" && prop.Items.Pattern != "[ \\r\\n\\t\\S]+" && prop.Items.Pattern != "\\S*" {
				varName := fmt.Sprintf("rx_%s_%s_items", k, toGoName(pk))
				regexMap[varName] = prop.Items.Pattern
				sb.WriteString(fmt.Sprintf("\t%s = regexp.MustCompile(%s)\n", varName, strconv.Quote(prop.Items.Pattern)))
			}
		}
	}
	sb.WriteString(")\n\n")

	for _, k := range keys {
		def := schema.Defs[k]
		sb.WriteString(fmt.Sprintf("// Validate validates a %s struct against its schema constraints.\n", k))
		sb.WriteString(fmt.Sprintf("func (x *%s) Validate() error {\n", k))
		sb.WriteString("\tif x == nil {\n\t\treturn nil\n\t}\n")

		var propKeys []string
		for pk := range def.Properties {
			propKeys = append(propKeys, pk)
		}
		sort.Strings(propKeys)

		for _, pk := range propKeys {
			prop := def.Properties[pk]
			required := isRequired(def, pk)
			goName := "x." + toGoName(pk)

			// 1. Required field check
			if required {
				sb.WriteString(fmt.Sprintf("\tif %s == nil {\n", goName))
				sb.WriteString(fmt.Sprintf("\t\treturn fmt.Errorf(\"required field '%s' is missing\")\n", pk))
				sb.WriteString("\t}\n")
			}

			// Under what condition do we validate constraints?
			// For optional fields: if not nil.
			// For required fields: since they are pointers (or strings/bools if required), we check if they are not nil.
			// Actually, if we made a required field non-pointer (like string), we can check it directly.
			// Let's determine if goType is pointer.
			goType := getGoType(prop, true)
			isPointer := strings.HasPrefix(goType, "*")
			isSlice := strings.HasPrefix(goType, "[]")
			isCustomComments := goType == "FHIRComments"

			if isCustomComments {
				// No validation needed for FHIRComments
				continue
			}

			indent := "\t"
			if isPointer {
				sb.WriteString(fmt.Sprintf("\tif %s != nil {\n", goName))
				indent = "\t\t"
			} else if isSlice {
				sb.WriteString(fmt.Sprintf("\tif %s != nil {\n", goName))
				indent = "\t\t"
			}

			// Validate constraints on the field value
			valExpr := goName
			if isPointer {
				valExpr = "*" + goName
			}

			// 2. Const check
			if prop.Const != nil {
				constStr, ok := prop.Const.(string)
				if ok {
					sb.WriteString(fmt.Sprintf("%sif %s != %s {\n", indent, valExpr, strconv.Quote(constStr)))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' must be exactly '%s'\")\n", indent, pk, constStr))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				}
			}

			// 3. Pattern check
			if prop.Pattern != "" {
				if prop.Pattern == "[ \\r\\n\\t\\S]+" {
					sb.WriteString(fmt.Sprintf("%sif %s == \"\" {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' does not match pattern '%s'\")\n", indent, pk, escapePattern(prop.Pattern)))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				} else if prop.Pattern == "\\S*" {
					// Skip unanchored \S* check since it always matches
				} else {
					varName := fmt.Sprintf("rx_%s_%s", k, toGoName(pk))
					sb.WriteString(fmt.Sprintf("%sif !%s.MatchString(%s) {\n", indent, varName, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' does not match pattern '%s'\")\n", indent, pk, escapePattern(prop.Pattern)))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				}
			}

			// 4. MinLength check
			if prop.MinLength != nil {
				sb.WriteString(fmt.Sprintf("%sif utf8.RuneCountInString(%s) < %d {\n", indent, valExpr, *prop.MinLength))
				sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' is too short (min %d characters)\")\n", indent, pk, *prop.MinLength))
				sb.WriteString(fmt.Sprintf("%s}\n", indent))
			}

			// 5. MaxLength check
			if prop.MaxLength != nil {
				sb.WriteString(fmt.Sprintf("%sif utf8.RuneCountInString(%s) > %d {\n", indent, valExpr, *prop.MaxLength))
				sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' is too long (max %d characters)\")\n", indent, pk, *prop.MaxLength))
				sb.WriteString(fmt.Sprintf("%s}\n", indent))
			}

			// 6. Minimum check
			if prop.Minimum != nil {
				sb.WriteString(fmt.Sprintf("%sif %s < %f {\n", indent, valExpr, *prop.Minimum))
				sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' is below minimum %f\")\n", indent, pk, *prop.Minimum))
				sb.WriteString(fmt.Sprintf("%s}\n", indent))
			}

			// 7. ExclusiveMinimum check
			if prop.ExclusiveMinimum != nil {
				sb.WriteString(fmt.Sprintf("%sif %s <= %f {\n", indent, valExpr, *prop.ExclusiveMinimum))
				sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' is below exclusive minimum %f\")\n", indent, pk, *prop.ExclusiveMinimum))
				sb.WriteString(fmt.Sprintf("%s}\n", indent))
			}

			// 8. Format check
			if prop.Format != "" {
				switch prop.Format {
				case "date-time":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirDateTime(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid date-time format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				case "date":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirDate(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid date format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				case "time":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirTime(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid time format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				case "uri":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirURI(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid URI format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				case "uuid":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirUUID(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid UUID format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				case "binary":
					sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirBinary(%s); err != nil {\n", indent, valExpr))
					sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' has invalid binary format: %%w\", err)\n", indent, pk))
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				}
			}

			// 9. Nested validation (Struct pointer)
			if prop.Ref != "" && refName(prop.Ref) != "links" {
				sb.WriteString(fmt.Sprintf("%sif err := %s.Validate(); err != nil {\n", indent, goName))
				sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s' is invalid: %%w\", err)\n", indent, pk))
				sb.WriteString(fmt.Sprintf("%s}\n", indent))
			}

			// 10. Slice item validation
			if isSlice && prop.Items != nil {
				hasItemValidation := (prop.Items.Pattern != "" && prop.Items.Pattern != "\\S*") || prop.Items.Format != "" || (prop.Items.Ref != "" && refName(prop.Items.Ref) != "links")
				if hasItemValidation {
					itemName := "item"
					sb.WriteString(fmt.Sprintf("%sfor i, %s := range %s {\n", indent, itemName, valExpr))
					loopIndent := indent + "\t"

					itemValExpr := itemName
					itemIsPointer := strings.HasPrefix(getGoType(prop.Items, false), "*")
					if itemIsPointer {
						sb.WriteString(fmt.Sprintf("%sif %s != nil {\n", loopIndent, itemName))
						loopIndent += "\t"
						itemValExpr = "*" + itemName
					}

					// Check items constraints
					if prop.Items.Pattern != "" {
						if prop.Items.Pattern == "[ \\r\\n\\t\\S]+" {
							sb.WriteString(fmt.Sprintf("%sif %s == \"\" {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' does not match pattern '%s'\", i)\n", loopIndent, pk, escapePattern(prop.Items.Pattern)))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						} else if prop.Items.Pattern == "\\S*" {
							// Skip unanchored \S* check
						} else {
							varName := fmt.Sprintf("rx_%s_%s_items", k, toGoName(pk))
							sb.WriteString(fmt.Sprintf("%sif !%s.MatchString(%s) {\n", loopIndent, varName, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' does not match pattern '%s'\", i)\n", loopIndent, pk, escapePattern(prop.Items.Pattern)))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						}
					}
					if prop.Items.Format != "" {
						switch prop.Items.Format {
						case "date-time":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirDateTime(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid date-time format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						case "date":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirDate(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid date format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						case "time":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirTime(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid time format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						case "uri":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirURI(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid URI format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						case "uuid":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirUUID(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid UUID format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						case "binary":
							sb.WriteString(fmt.Sprintf("%sif err := ValidateFhirBinary(%s); err != nil {\n", loopIndent, itemValExpr))
							sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' has invalid binary format: %%w\", i, err)\n", loopIndent, pk))
							sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
						}
					}

					// Check nested object validation for slice item
					if prop.Items.Ref != "" && refName(prop.Items.Ref) != "links" {
						sb.WriteString(fmt.Sprintf("%sif err := %s.Validate(); err != nil {\n", loopIndent, itemName))
						sb.WriteString(fmt.Sprintf("%s\treturn fmt.Errorf(\"field '%s[%%d]' is invalid: %%w\", i, err)\n", loopIndent, pk))
						sb.WriteString(fmt.Sprintf("%s}\n", loopIndent))
					}

					if itemIsPointer {
						sb.WriteString(fmt.Sprintf("%s}\n", indent+"\t"))
					}
					sb.WriteString(fmt.Sprintf("%s}\n", indent))
				}
			}

			if isPointer || isSlice {
				sb.WriteString("\t}\n")
			}
		}

		sb.WriteString("\treturn nil\n")
		sb.WriteString("}\n\n")
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func escapePattern(pat string) string {
	s := strings.ReplaceAll(pat, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func generateExtract(schema *Schema, path string) error {
	var sb strings.Builder
	sb.WriteString("// Code generated by cmd/generate/main.go. DO NOT EDIT.\n")
	sb.WriteString("package fhirstructs\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\t\"bytes\"\n")
	sb.WriteString("\t\"crypto/sha1\"\n")
	sb.WriteString("\t\"encoding/hex\"\n")
	sb.WriteString("\t\"encoding/json\"\n")
	sb.WriteString("\t\"fmt\"\n")
	sb.WriteString("\t\"hash\"\n")
	sb.WriteString("\t\"strconv\"\n")
	sb.WriteString("\t\"strings\"\n")
	sb.WriteString("\t\"sync\"\n")
	sb.WriteString("\t\"github.com/google/uuid\"\n")
	sb.WriteString("\t\"github.com/bytedance/sonic\"\n")
	sb.WriteString(")\n\n")

	// 1. Write EdgeDocument struct definition
	sb.WriteString(`// EdgeDocument represents an edge in ArangoDB.
type EdgeDocument struct {
	Key      string ` + "`" + `json:"_key"` + "`" + `
	From     string ` + "`" + `json:"_from"` + "`" + `
	To       string ` + "`" + `json:"_to"` + "`" + `
	Label    string ` + "`" + `json:"label"` + "`" + `
	Project  string ` + "`" + `json:"project"` + "`" + `
	FromType string ` + "`" + `json:"from_type"` + "`" + `
	ToType   string ` + "`" + `json:"to_type"` + "`" + `
}

// Result of validation and edge extraction.
type Result struct {
	ObjectID     string
	ResourceType string
	Vertex       any
	Edges        []json.RawMessage
}

// Global namespace UUID constant computed from "calypr-public.ohsu.edu"
var namespaceUUID = uuid.NewMD5(uuid.NameSpaceDNS, []byte("calypr-public.ohsu.edu"))

var sha1Pool = sync.Pool{
	New: func() any {
		return sha1.New()
	},
}

var bufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

func fastUUIDSHA1(space uuid.UUID, data []byte) string {
	h := sha1Pool.Get().(hash.Hash)
	h.Reset()
	h.Write(space[:])
	h.Write(data)
	var hashBytes [20]byte
	h.Sum(hashBytes[:0])
	sha1Pool.Put(h)

	// format uuid v5: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// version 5 (SHA-1)
	hashBytes[6] = (hashBytes[6] & 0x0f) | 0x50
	hashBytes[8] = (hashBytes[8] & 0x3f) | 0x80

	var buf [36]byte
	hex.Encode(buf[0:8], hashBytes[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], hashBytes[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], hashBytes[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], hashBytes[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], hashBytes[10:16])

	return string(buf[:])
}

func getEdgeUUID(a, b, c string) string {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteString(a)
	buf.WriteByte('-')
	buf.WriteString(b)
	buf.WriteByte('-')
	buf.WriteString(c)
	uuidStr := fastUUIDSHA1(namespaceUUID, buf.Bytes())
	bufferPool.Put(buf)
	return uuidStr
}

func collectionID(resourceType, id string) string {
	return sanitizeKey(resourceType) + "/" + sanitizeKey(id)
}

func sanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	needsSanitization := false
	for i := 0; i < len(value); i++ {
		r := value[i]
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == ':' || r == '.' || r == '@' || r == '(' || r == ')' ||
			r == '+' || r == ',' || r == '=' || r == ';' || r == '$' || r == '!' || r == '*' ||
			r == '\'' || r == '%' {
			// clean character
		} else {
			needsSanitization = true
			break
		}
	}
	if !needsSanitization {
		return value
	}

	var sb strings.Builder
	sb.Grow(len(value))
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == ':' || r == '.' || r == '@' || r == '(' || r == ')' ||
			r == '+' || r == ',' || r == '=' || r == ';' || r == '$' || r == '!' || r == '*' ||
			r == '\'' || r == '%' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func splitFHIRReference(ref string) (string, string, bool) {
	firstSlash := strings.IndexByte(ref, '/')
	if firstSlash <= 0 || firstSlash == len(ref)-1 {
		return "", "", false
	}
	targetType := ref[:firstSlash]
	targetID := ref[firstSlash+1:]
	if secondSlash := strings.IndexByte(targetID, '/'); secondSlash != -1 {
		targetID = targetID[:secondSlash]
	}
	if targetID == "" {
		return "", "", false
	}
	return targetType, targetID, true
}

func targetTypeFromLabel(label string) string {
	parts := strings.Split(label, "_")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func buildEdgeRawJSON(key, from, to, label, projectJSON, fromType, toType string) json.RawMessage {
	var buf bytes.Buffer
	buf.Grow(256)
	buf.WriteString("{\"_key\":\"")
	buf.WriteString(key)
	buf.WriteString("\",\"_from\":\"")
	buf.WriteString(from)
	buf.WriteString("\",\"_to\":\"")
	buf.WriteString(to)
	buf.WriteString("\",\"label\":\"")
	buf.WriteString(label)
	buf.WriteString("\",\"project\":")
	buf.WriteString(projectJSON)
	buf.WriteString(",\"from_type\":\"")
	buf.WriteString(fromType)
	buf.WriteString("\",\"to_type\":\"")
	buf.WriteString(toType)
	buf.WriteString("\"}")
	return json.RawMessage(buf.Bytes())
}

`)

	var keys []string
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Keep track of which resource types are supported (those having links metadata in the schema)
	supportedResources := []string{}

	for _, k := range keys {
		def := schema.Defs[k]

		// Check if it's a top-level resource (typically has resourceType const)
		var hasResourceType bool
		if prop, ok := def.Properties["resourceType"]; ok && prop.Const != nil {
			hasResourceType = true
		}

		if !hasResourceType {
			continue
		}

		supportedResources = append(supportedResources, k)

		sb.WriteString(fmt.Sprintf("// ExtractEdges extracts graph links from %s.\n", k))
		sb.WriteString(fmt.Sprintf("func (x *%s) ExtractEdges(project string) ([]json.RawMessage, error) {\n", k))
		sb.WriteString("\tvar edges []json.RawMessage\n")
		sb.WriteString("\tif x == nil || x.ID == nil {\n\t\treturn edges, nil\n\t}\n")
		sb.WriteString("\tid := *x.ID\n")
		sb.WriteString("\tsourceType := \"" + k + "\"\n")
		sb.WriteString("\tprojectJSON := strconv.Quote(project)\n")
		sb.WriteString("\t_ = id\n")
		sb.WriteString("\t_ = sourceType\n")
		sb.WriteString("\t_ = projectJSON\n")
		sb.WriteString("\tvar seen map[string]struct{}\n")
		sb.WriteString("\t_ = seen\n")

		// Generate link extraction code statically!
		for _, link := range def.Links {
			ptr := link.TemplatePointers.ID
			if ptr == "" {
				continue
			}

			// We need to generate code to traverse to templatePointers
			pathParts := strings.Split(ptr, "/")[1:]
			traversalCode, err := generateTraversalCode(schema, k, pathParts, "x", k, link, "\t")
			if err != nil {
				return err
			}
			sb.WriteString(traversalCode)
		}

		sb.WriteString("\treturn edges, nil\n")
		sb.WriteString("}\n\n")
	}

	// 2. Write the unified ValidateAndExtract entrypoint
	sb.WriteString("// ValidateAndExtract parses, validates, and extracts edges for a FHIR resource.\n")
	sb.WriteString("func ValidateAndExtract(raw []byte, resourceType string, project string) (Result, error) {\n")
	sb.WriteString("\tswitch resourceType {\n")
	for _, res := range supportedResources {
		sb.WriteString(fmt.Sprintf("\tcase %q:\n", res))
		sb.WriteString(fmt.Sprintf("\t\tvar val %s\n", res))
		sb.WriteString("\t\tif err := sonic.Unmarshal(raw, &val); err != nil {\n")
		sb.WriteString("\t\t\treturn Result{}, fmt.Errorf(\"decode error: %w\", err)\n")
		sb.WriteString("\t\t}\n")
		sb.WriteString("\t\tif err := val.Validate(); err != nil {\n")
		sb.WriteString("\t\t\treturn Result{}, fmt.Errorf(\"validation error: %w\", err)\n")
		sb.WriteString("\t\t}\n")
		sb.WriteString("\t\tedges, err := val.ExtractEdges(project)\n")
		sb.WriteString("\t\tif err != nil {\n")
		sb.WriteString("\t\t\treturn Result{}, fmt.Errorf(\"extraction error: %w\", err)\n")
		sb.WriteString("\t\t}\n")
		sb.WriteString("\t\tvar objectID string\n")
		sb.WriteString("\t\tif val.ID != nil {\n\t\t\tobjectID = *val.ID\n\t\t}\n")
		sb.WriteString("\t\treturn Result{\n")
		sb.WriteString("\t\t\tObjectID:     objectID,\n")
		sb.WriteString("\t\t\tResourceType: resourceType,\n")
		sb.WriteString("\t\t\tVertex:       json.RawMessage(raw),\n")
		sb.WriteString("\t\t\tEdges:        edges,\n")
		sb.WriteString("\t\t}, nil\n")
	}
	sb.WriteString("\tdefault:\n")
	sb.WriteString("\t\treturn Result{}, fmt.Errorf(\"unsupported resource type %s\", resourceType)\n")
	sb.WriteString("\t}\n")
	sb.WriteString("}\n")

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func generateTraversalCode(schema *Schema, structName string, path []string, varName string, currentType string, link Link, indent string) (string, error) {
	if len(path) == 0 {
		// We have reached the string value (the reference).
		// We emit the edge building code!
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%srefVal := %s\n", indent, varName))
		sb.WriteString(fmt.Sprintf("%srefType, targetID, ok := splitFHIRReference(refVal)\n", indent))
		sb.WriteString(fmt.Sprintf("%sif ok {\n", indent))

		// Match prefix check
		allowAnyMatch := false
		matchPrefix := ""
		for _, rx := range link.TargetHints.RegexMatch {
			if rx == "Resource/*" {
				allowAnyMatch = true
			} else {
				matchPrefix = strings.TrimSuffix(rx, "/*")
			}
		}

		if !allowAnyMatch && matchPrefix != "" {
			sb.WriteString(fmt.Sprintf("%s\tif refType == %q {\n", indent, matchPrefix))
			indent += "\t"
		}

		// Calculate UUIDs and append EdgeDocuments. Most generated forward edge
		// targets retain the long-standing label-derived convention. The one
		// compiler-owned exception is ResearchSubject.study: its bare label
		// cannot encode a target type, while the checked-in schema proves its
		// concrete ResearchStudy target. Keep this narrow until each other bare
		// label has an equivalent storage-route proof.
		forwardTargetType := generatedForwardTargetType(structName, link)
		sb.WriteString(fmt.Sprintf("%s\tforwardKey := getEdgeUUID(targetID, id, %q)\n", indent, link.Rel))
		sb.WriteString(fmt.Sprintf("%s\tif seen == nil {\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\tseen = make(map[string]struct{}, 4)\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
		sb.WriteString(fmt.Sprintf("%s\tif _, exists := seen[forwardKey]; !exists {\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\tseen[forwardKey] = struct{}{}\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\tedges = append(edges, buildEdgeRawJSON(\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t\tforwardKey,\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t\tcollectionID(sourceType, id),\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t\tcollectionID(%q, targetID),\n", indent, forwardTargetType))
		sb.WriteString(fmt.Sprintf("%s\t\t\t%q,\n", indent, link.Rel))
		sb.WriteString(fmt.Sprintf("%s\t\t\tprojectJSON,\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t\tsourceType,\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t\t\t%q,\n", indent, forwardTargetType))
		sb.WriteString(fmt.Sprintf("%s\t\t))\n", indent))
		sb.WriteString(fmt.Sprintf("%s\t}\n", indent))

		// Check if backref exists
		if len(link.TargetHints.Backref) > 0 && link.TargetHints.Backref[0] != "" {
			backref := link.TargetHints.Backref[0]
			backrefTargetType := targetTypeFromLabel(backref)
			sb.WriteString(fmt.Sprintf("%s\tbackrefKey := getEdgeUUID(id, targetID, %q)\n", indent, backref))
			sb.WriteString(fmt.Sprintf("%s\tif seen == nil {\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\tseen = make(map[string]struct{}, 4)\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
			sb.WriteString(fmt.Sprintf("%s\tif _, exists := seen[backrefKey]; !exists {\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\tseen[backrefKey] = struct{}{}\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\tedges = append(edges, buildEdgeRawJSON(\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\t\tbackrefKey,\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\t\tcollectionID(sourceType, targetID),\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\t\tcollectionID(%q, id),\n", indent, backrefTargetType))
			sb.WriteString(fmt.Sprintf("%s\t\t\t%q,\n", indent, backref))
			sb.WriteString(fmt.Sprintf("%s\t\t\tprojectJSON,\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\t\tsourceType,\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t\t\t%q,\n", indent, backrefTargetType))
			sb.WriteString(fmt.Sprintf("%s\t\t))\n", indent))
			sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
		}

		if !allowAnyMatch && matchPrefix != "" {
			indent = indent[:len(indent)-1]
			sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
		}

		sb.WriteString(fmt.Sprintf("%s}\n", indent))
		return sb.String(), nil
	}

	part := path[0]

	if part == "-" {
		// VarName must be a slice. Loop over the elements.
		// Wait, what is the item type?
		// We need to look up the parent property to see what its items are.
		// E.g. x.PartOf which is a slice. Let's find what the element type is.
		// We can pass currentType down, but since we are iterating, we need to know the type of items.
		// Let's pass the element type in currentType.
		// If part is "-", then the type of item is currentType.
		loopVar := fmt.Sprintf("item_%d_%s", len(path), strings.ReplaceAll(varName, ".", "_"))
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%sfor _, %s := range %s {\n", indent, loopVar, varName))
		// Check for nil if item is a pointer
		sb.WriteString(fmt.Sprintf("%s\tif %s != nil {\n", indent+"\t", loopVar))
		subCode, err := generateTraversalCode(schema, structName, path[1:], loopVar, currentType, link, indent+"\t\t")
		if err != nil {
			return "", err
		}
		sb.WriteString(subCode)
		sb.WriteString(fmt.Sprintf("%s\t}\n", indent))
		sb.WriteString(fmt.Sprintf("%s}\n", indent))
		return sb.String(), nil
	}

	// It's a field name!
	// Look up in schema Defs[currentType] to find property named `part`.
	def, ok := schema.Defs[currentType]
	if !ok {
		return "", fmt.Errorf("definition %s not found in schema while resolving path part %s", currentType, part)
	}

	prop, ok := def.Properties[part]
	if !ok {
		// It could be that part is not a direct property, or it is in some other way.
		// But in graph-fhir.json, they are always properties.
		return "", fmt.Errorf("property %s not found in definition %s", part, currentType)
	}

	fieldName := toGoName(part)
	nextVarName := fmt.Sprintf("%s.%s", varName, fieldName)

	var nextType string
	if prop.Ref != "" {
		nextType = refName(prop.Ref)
	} else if prop.Items != nil && prop.Items.Ref != "" {
		nextType = refName(prop.Items.Ref)
	} else if typeStr, ok := prop.Type.(string); ok && typeStr == "string" {
		nextType = "string"
	}

	var sb strings.Builder
	// If it's a pointer type, check for nil
	goType := getGoType(prop, true) // treat as optional to see if it has pointer prefix
	isPointer := strings.HasPrefix(goType, "*")
	isSlice := strings.HasPrefix(goType, "[]")

	if isPointer {
		sb.WriteString(fmt.Sprintf("%sif %s != nil {\n", indent, nextVarName))
		// If it's a leaf string pointer (like *string), dereference it
		actualVar := nextVarName
		if nextType == "string" {
			actualVar = "*" + nextVarName
		}
		subCode, err := generateTraversalCode(schema, structName, path[1:], actualVar, nextType, link, indent+"\t")
		if err != nil {
			return "", err
		}
		sb.WriteString(subCode)
		sb.WriteString(fmt.Sprintf("%s}\n", indent))
	} else if isSlice {
		sb.WriteString(fmt.Sprintf("%sif %s != nil {\n", indent, nextVarName))
		subCode, err := generateTraversalCode(schema, structName, path[1:], nextVarName, nextType, link, indent+"\t")
		if err != nil {
			return "", err
		}
		sb.WriteString(subCode)
		sb.WriteString(fmt.Sprintf("%s}\n", indent))
	} else {
		subCode, err := generateTraversalCode(schema, structName, path[1:], nextVarName, nextType, link, indent)
		if err != nil {
			return "", err
		}
		sb.WriteString(subCode)
	}

	return sb.String(), nil
}

func generateFHIRSchema(schema *Schema, path string) error {
	var sb strings.Builder
	sb.WriteString("// Code generated by cmd/generate/main.go. DO NOT EDIT.\n")
	sb.WriteString("package fhirschema\n\n")
	sb.WriteString("var generatedResourceTypes = []string{\n")
	for _, k := range schemaFHIRRootResourceTypes(schema) {
		sb.WriteString(fmt.Sprintf("\t%q,\n", k))
	}
	sb.WriteString("}\n\n")
	sb.WriteString("var generatedDefinitions = map[string]generatedDefinition{\n")
	var keys []string
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("\t%q: {\n", k))
		sb.WriteString("\t\tProperties: []generatedProperty{\n")
		writeGeneratedProperties(&sb, schema.Defs[k].Properties, 3)
		sb.WriteString("\t\t},\n")
		sb.WriteString("\t},\n")
	}
	sb.WriteString("}\n\n")
	sb.WriteString("var generatedTraversals = map[string]TraversalSpec{\n")
	seenTraversalKeys := map[string]struct{}{}
	for _, k := range keys {
		def := schema.Defs[k]
		if def == nil || len(def.Links) == 0 {
			continue
		}
		for _, link := range def.Links {
			toType := refName(link.TargetSchema.Ref)
			if strings.TrimSpace(toType) == "" {
				toType = targetTypeFromLabel(link.Rel)
			}
			if strings.TrimSpace(link.Rel) == "" || strings.TrimSpace(toType) == "" {
				continue
			}
			forwardKey := traversalKey(k, link.Rel, toType)
			if _, ok := seenTraversalKeys[forwardKey]; !ok {
				writeGeneratedTraversal(&sb, k, link.Rel, toType, link, forwardKey)
				seenTraversalKeys[forwardKey] = struct{}{}
			}
			reverseKey := traversalKey(toType, link.Rel, k)
			if _, ok := seenTraversalKeys[reverseKey]; !ok {
				writeGeneratedTraversal(&sb, toType, link.Rel, k, link, reverseKey)
				seenTraversalKeys[reverseKey] = struct{}{}
			}
		}
	}
	sb.WriteString("}\n")
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// schemaFHIRRootResourceTypes discovers compiler-visible collections from the
// checked-in graph schema. It deliberately does not carry a copied list of
// FHIR resource names: a schema definition is a concrete root only when its
// own resourceType constant names that definition and it has the FHIR Resource
// root fields (a string id plus an explicit metadata object).
//
// The graph schema also publishes the abstract Resource base definition so
// generated field references can resolve. Its resourceType constant is the
// generic "Resource" placeholder rather than a concrete graph collection, so
// it is excluded by role rather than maintaining a list of concrete types.
func schemaFHIRRootResourceTypes(schema *Schema) []string {
	if schema == nil {
		return nil
	}

	keys := make([]string, 0, len(schema.Defs))
	for name := range schema.Defs {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	roots := make([]string, 0, len(keys))
	for _, name := range keys {
		if isFHIRRootResourceDefinition(name, schema.Defs[name]) {
			roots = append(roots, name)
		}
	}
	return roots
}

func isFHIRRootResourceDefinition(name string, def *Definition) bool {
	name = strings.TrimSpace(name)
	if name == "" || def == nil {
		return false
	}

	resourceType, ok := stringConstant(def.Properties["resourceType"])
	if !ok || resourceType != name {
		return false
	}
	if resourceType == "Resource" {
		return false
	}

	id := def.Properties["id"]
	if id == nil || propertyType(id) != "string" {
		return false
	}
	meta := def.Properties["meta"]
	return meta != nil && strings.TrimSpace(meta.Ref) != ""
}

func stringConstant(prop *Property) (string, bool) {
	if prop == nil {
		return "", false
	}
	value, ok := prop.Const.(string)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func propertyType(prop *Property) string {
	if prop == nil {
		return ""
	}
	if prop.Ref != "" {
		return "object"
	}
	if typeStr, ok := prop.Type.(string); ok {
		return typeStr
	}
	if prop.Items != nil {
		return "array"
	}
	if len(prop.Properties) > 0 {
		return "object"
	}
	return ""
}

func writeGeneratedProperties(sb *strings.Builder, props map[string]*Property, indent int) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		prop := props[k]
		if prop == nil {
			continue
		}
		writeIndent(sb, indent)
		sb.WriteString("{\n")
		writeIndent(sb, indent+1)
		sb.WriteString(fmt.Sprintf("Name: %q,\n", k))
		writeIndent(sb, indent+1)
		sb.WriteString(fmt.Sprintf("Kind: %q,\n", propertyType(prop)))
		if prop.Format != "" {
			writeIndent(sb, indent+1)
			sb.WriteString(fmt.Sprintf("Format: %q,\n", prop.Format))
		}
		if prop.Ref != "" {
			writeIndent(sb, indent+1)
			sb.WriteString(fmt.Sprintf("Ref: %q,\n", refName(prop.Ref)))
		}
		if prop.Items != nil {
			writeIndent(sb, indent+1)
			sb.WriteString(fmt.Sprintf("ItemKind: %q,\n", propertyType(prop.Items)))
			if prop.Items.Format != "" {
				writeIndent(sb, indent+1)
				sb.WriteString(fmt.Sprintf("ItemFormat: %q,\n", prop.Items.Format))
			}
			if prop.Items.Ref != "" {
				writeIndent(sb, indent+1)
				sb.WriteString(fmt.Sprintf("ItemRef: %q,\n", refName(prop.Items.Ref)))
			}
			if len(prop.Items.Properties) > 0 {
				writeIndent(sb, indent+1)
				sb.WriteString("ItemProperties: []generatedProperty{\n")
				writeGeneratedProperties(sb, prop.Items.Properties, indent+2)
				writeIndent(sb, indent+1)
				sb.WriteString("},\n")
			}
		}
		if len(prop.Properties) > 0 {
			writeIndent(sb, indent+1)
			sb.WriteString("Properties: []generatedProperty{\n")
			writeGeneratedProperties(sb, prop.Properties, indent+2)
			writeIndent(sb, indent+1)
			sb.WriteString("},\n")
		}
		writeIndent(sb, indent)
		sb.WriteString("},\n")
	}
}

func writeGeneratedStringSlice(sb *strings.Builder, fieldName string, values []string, indent int) {
	writeIndent(sb, indent)
	sb.WriteString(fieldName)
	sb.WriteString(": []string{")
	if len(values) == 0 {
		sb.WriteString("},\n")
		return
	}
	sb.WriteString("\n")
	for _, value := range values {
		writeIndent(sb, indent+1)
		sb.WriteString(fmt.Sprintf("%q,\n", value))
	}
	writeIndent(sb, indent)
	sb.WriteString("},\n")
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}

func writeGeneratedTraversal(sb *strings.Builder, fromType, edgeLabel, toType string, link Link, key string) {
	sb.WriteString(fmt.Sprintf("\t%q: {\n", key))
	sb.WriteString(fmt.Sprintf("\t\tFromType: %q,\n", fromType))
	sb.WriteString(fmt.Sprintf("\t\tEdgeLabel: %q,\n", edgeLabel))
	sb.WriteString(fmt.Sprintf("\t\tToType: %q,\n", toType))
	writeGeneratedStringSlice(sb, "Direction", link.TargetHints.Direction, 2)
	writeGeneratedStringSlice(sb, "Multiplicity", link.TargetHints.Multiplicity, 2)
	writeGeneratedStringSlice(sb, "Backref", link.TargetHints.Backref, 2)
	writeGeneratedStringSlice(sb, "RegexMatch", link.TargetHints.RegexMatch, 2)
	sb.WriteString("\t},\n")
}

func writeIndent(sb *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		sb.WriteString("\t")
	}
}
