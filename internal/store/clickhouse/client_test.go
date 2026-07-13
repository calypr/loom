package clickhouse

import "testing"

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
