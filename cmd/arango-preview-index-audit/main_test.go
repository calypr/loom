package main

import "testing"

func TestValidateLocalTarget(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, database string
		ok                       bool
	}{
		{name: "loopback development", endpoint: "http://127.0.0.1:8529", database: "loom_dev", ok: true},
		{name: "compose service test database", endpoint: "http://arangodb:8529", database: "loom_test_wp19", ok: true},
		{name: "owned cleanup database", endpoint: "http://127.0.0.1:64592", database: "loom_cleanup_wp12_7caf7b1a9", ok: true},
		{name: "remote endpoint", endpoint: "https://arangodb.example:8529", database: "loom_dev"},
		{name: "production database", endpoint: "http://localhost:8529", database: "production"},
		{name: "credentials in url", endpoint: "http://user:pass@localhost:8529", database: "loom_dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateLocalTarget(test.endpoint, test.database)
			if (err == nil) != test.ok {
				t.Fatalf("validateLocalTarget(%q, %q) error=%v want success=%v", test.endpoint, test.database, err, test.ok)
			}
		})
	}
}
