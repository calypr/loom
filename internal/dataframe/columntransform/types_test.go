package columntransform

import (
	"strings"
	"testing"
)

func TestExactCategoryRecodeRequiresExplicitUnknownPolicyAndCompleteMap(t *testing.T) {
	for _, tc := range []struct {
		name           string
		transformation ValueTransformation
		want           string
	}{
		{
			name: "empty mapping",
			transformation: ValueTransformation{
				Kind:                KindExactCategoryRecode,
				ExactCategoryRecode: &ExactCategoryRecode{UnknownPolicy: UnknownError},
			},
			want: "mappings must not be empty",
		},
		{
			name: "missing unknown policy",
			transformation: ValueTransformation{
				Kind:                KindExactCategoryRecode,
				ExactCategoryRecode: &ExactCategoryRecode{Mappings: []CategoryMapping{{From: "recorded", To: "replacement"}}},
			},
			want: "unknownPolicy",
		},
		{
			name: "duplicate exact source",
			transformation: ValueTransformation{
				Kind: KindExactCategoryRecode,
				ExactCategoryRecode: &ExactCategoryRecode{
					Mappings:      []CategoryMapping{{From: "recorded", To: "one"}, {From: "recorded", To: "two"}},
					UnknownPolicy: UnknownKeepOriginal,
				},
			},
			want: "duplicates an earlier exact source value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.transformation.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExactCategoryRecodeAllowsEmptyCategoryValues(t *testing.T) {
	transformation := ValueTransformation{
		Kind: KindExactCategoryRecode,
		ExactCategoryRecode: &ExactCategoryRecode{
			Mappings:      []CategoryMapping{{From: "", To: "unspecified"}},
			UnknownPolicy: UnknownKeepOriginal,
		},
	}
	if err := transformation.Validate(); err != nil {
		t.Fatalf("exact empty-string category was rejected: %v", err)
	}
}
