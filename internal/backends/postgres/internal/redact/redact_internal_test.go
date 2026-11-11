package redact

import "strings"

import "testing"

// dsn_redaction tests run white-box because the redaction helpers are an
// internal composition tool; the externally observable half (Open errors
// carrying no credential while errors.Is still reaches the cause) lands with
// Open in the live half of TestPostgresFactoryOwnershipContract.

func TestDSNRedactionKeepsTheRegisteredSecret(t *testing.T) {
	const secret = "Ab06Reg1st3redS3cret!"
	cases := []struct {
		name          string
		dsn           string
		mustKeep      string
		wantExtracted string
	}{
		{
			name:          "url_form",
			dsn:           "postgres://loom:" + secret + "@db.internal:5432/loom_source?sslmode=require",
			mustKeep:      "db.internal",
			wantExtracted: secret,
		},
		{
			name:          "keyword_form",
			dsn:           "host=db.internal port=5432 dbname=loom_source user=loom password=" + secret,
			mustKeep:      "db.internal",
			wantExtracted: secret,
		},
		{
			name:          "keyword_form_quoted",
			dsn:           "host=db.internal password='" + secret + "'",
			mustKeep:      "db.internal",
			wantExtracted: secret,
		},
		{
			name:          "passfile_stays_visible",
			dsn:           "host=db.internal password=" + secret + " passfile=/etc/loom/pgpass",
			mustKeep:      "/etc/loom/pgpass",
			wantExtracted: secret,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redacted := RedactDSN(tc.dsn)
			if strings.Contains(redacted, secret) {
				t.Fatalf("redaction leaked the registered secret: %s", redacted)
			}
			if !strings.Contains(redacted, tc.mustKeep) {
				t.Fatalf("redaction dropped diagnosis material %q: %s", tc.mustKeep, redacted)
			}
			if !strings.Contains(redacted, credentialRedacted) {
				t.Fatalf("redaction marker missing: %s", redacted)
			}
			if got := ExtractDSNPassword(tc.dsn); got != tc.wantExtracted {
				t.Fatalf("extractDSNPassword = %q, want the registered secret", got)
			}
		})
	}

	t.Run("alive_control_credential_free_dsn_unchanged", func(t *testing.T) {
		plain := "postgres://loom@db.internal:5432/loom_source"
		if got := RedactDSN(plain); got != plain {
			t.Fatalf("credential-free DSN was altered: %s", got)
		}
		if got := ExtractDSNPassword(plain); got != "" {
			t.Fatalf("credential-free DSN yielded a secret: %q", got)
		}
	})

	t.Run("scrub_secret_text", func(t *testing.T) {
		const secret = "S3cr3t-Pg-Pass"
		dirty := `pgx: failed to connect to host=db.internal user=loom password=` + secret
		if strings.Contains(ScrubSecretText(dirty, secret), secret) {
			t.Fatalf("scrubSecretText left the secret in: %s", dirty)
		}
		// Alive control: empty secret never erases ordinary text.
		if ScrubSecretText(dirty, "") != dirty {
			t.Fatal("empty secret altered ordinary text")
		}
	})
}
