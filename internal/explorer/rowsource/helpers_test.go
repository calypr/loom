package rowsource

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
)

func TestDeriveSourceLogicalTypeIsNumericAdmitsOnlyArithmeticKinds(t *testing.T) {
	for _, want := range []string{"integer", "decimal", " INTEGER", "Decimal "} {
		if !deriveSourceLogicalTypeIsNumeric(want) {
			t.Fatalf("deriveSourceLogicalTypeIsNumeric(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"", "number", "boolean", "string", "code", "date", "uuid", "object", "quantity"} {
		if deriveSourceLogicalTypeIsNumeric(refuse) {
			t.Fatalf("deriveSourceLogicalTypeIsNumeric(%q) = true, want false", refuse)
		}
	}
}

func TestUnpivotSourceLogicalTypeIsTableScalarAdmitsOnlyCompilerKinds(t *testing.T) {
	for _, want := range []string{"string", "integer", "decimal", "boolean", "date", "date_time", "code", "uuid", " STRING", "Decimal ", "BOOLEAN", " Date "} {
		if !unpivotSourceLogicalTypeIsTableScalar(want) {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"", "number", "object", "datetime", "date-time", "quantity", "url", "null"} {
		if unpivotSourceLogicalTypeIsTableScalar(refuse) {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = true, want false", refuse)
		}
	}
}

func TestUnpivotSourceMeltEligibilityExcludesDateFamily(t *testing.T) {
	for _, want := range []string{"string", "integer", "decimal", "boolean", "code", "uuid", " STRING", "Decimal ", "BOOLEAN"} {
		if !unpivotSourceLogicalTypeIsMeltEligible(want) {
			t.Fatalf("unpivotSourceLogicalTypeIsMeltEligible(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"date", "date_time", " Date ", " DATE_TIME ", "date_time ", "", "number", "object", "datetime", "date-time", "quantity", "url", "null"} {
		if unpivotSourceLogicalTypeIsMeltEligible(refuse) {
			t.Fatalf("unpivotSourceLogicalTypeIsMeltEligible(%q) = true, want false", refuse)
		}
	}
}

func TestUnpivotSourceWhitelistIsTheCompilerDecision(t *testing.T) {
	corpus := map[string]struct {
		admitted bool
		class    string
	}{
		"string":    {true, "STRING"},
		"date":      {true, "STRING"},
		"date_time": {true, "STRING"},
		"code":      {true, "STRING"},
		"uuid":      {true, "STRING"},
		"integer":   {true, "INTEGER"},
		"decimal":   {true, "DECIMAL"},
		"boolean":   {true, "BOOLEAN"},
		" DATE ":    {true, "STRING"},
		"Number":    {false, ""},
		"object":    {false, ""},
		"datetime":  {false, ""},
		"date-time": {false, ""},
		"null":      {false, ""},
		"":          {false, ""},
	}
	for kind, want := range corpus {
		class, ok := facade.TableReshapeScalarKind(kind)
		if ok != want.admitted || (ok && class != want.class) {
			t.Fatalf("facade.TableReshapeScalarKind(%q) = (%q,%v), want (%q,%v)", kind, class, ok, want.class, want.admitted)
		}
		if got := unpivotSourceLogicalTypeIsTableScalar(kind); got != want.admitted {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = %v, want %v (channel drifted from the compiler decision)", kind, got, want.admitted)
		}
	}
}

func TestUnpivotMeltKindsSatisfiableFollowsCompilerRule(t *testing.T) {
	for _, ok := range [][]string{nil, {}, {"integer"}, {"decimal"}, {"integer", "decimal"}, {"decimal", "integer"}, {" string "}, {"boolean"}, {"BOOLEAN", "boolean"}, {"code", "uuid", "string"}, {"Code", " STRING"}} {
		if !unpivotMeltKindsSatisfiable(ok) {
			t.Fatalf("unpivotMeltKindsSatisfiable(%v) = false, want true", ok)
		}
	}
	for _, refuse := range [][]string{{"string", "integer"}, {"string", "decimal"}, {"boolean", "string"}, {"boolean", "decimal"}, {"integer", "boolean"}, {"integer", "date"}, {"decimal", "date_time"}, {"boolean", "code"}, {"string", "integer", "uuid"}, {"date"}, {"date_time"}, {"date", "code", "uuid", "date_time", "string"}, {" Date ", "STRING"}, {"date", "date_time"}, {"string", "date"}} {
		if unpivotMeltKindsSatisfiable(refuse) {
			t.Fatalf("unpivotMeltKindsSatisfiable(%v) = true, want false", refuse)
		}
	}
}
