package clickhouse

import (
	"testing"
	"time"
)

func TestNewUsesOfficialDriverDSN(t *testing.T) {
	client, err := New(Options{URL: "clickhouse://127.0.0.1:9000", Database: "loom", Username: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if client.conn == nil {
		t.Fatal("official ClickHouse driver connection was not created")
	}
	_ = client.Close()
}

func TestParseOptionsPreservesAuthAndTimeout(t *testing.T) {
	options, err := parseOptions(Options{URL: "clickhouse://127.0.0.1:9000", Database: "loom", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Auth.Database != "loom" || options.Auth.Username != "u" || options.Auth.Password != "p" {
		t.Fatalf("auth options = %#v", options.Auth)
	}
}

func TestValidateIdentifier(t *testing.T) {
	if err := validateIdentifier("loom_df_123"); err != nil {
		t.Fatal(err)
	}
	if err := validateIdentifier("bad;DROP TABLE"); err == nil {
		t.Fatal("expected invalid identifier")
	}
}

func TestNormalizeInsertValueParsesFHIRTemporalValues(t *testing.T) {
	dateValue, err := normalizeInsertValue(Column{Name: "date", Type: "Nullable(Date)"}, "2026-07-28")
	if err != nil {
		t.Fatal(err)
	}
	if got := dateValue.(time.Time).Format("2006-01-02"); got != "2026-07-28" {
		t.Fatalf("date = %q", got)
	}
	dateTimeValue, err := normalizeInsertValue(Column{Name: "created", Type: "Nullable(DateTime64(3))"}, "2026-01-05T17:15:50+00:00")
	if err != nil {
		t.Fatal(err)
	}
	if got := dateTimeValue.(time.Time).UTC().Format(time.RFC3339); got != "2026-01-05T17:15:50Z" {
		t.Fatalf("date-time = %q", got)
	}
}

func TestNormalizeInsertValueUsesEmptyArrayForMissingRepeatedValue(t *testing.T) {
	value, err := normalizeInsertValue(Column{Name: "tags", Type: "Array(String)"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(value.([]any)); got != 0 {
		t.Fatalf("empty array length = %d", got)
	}
}
