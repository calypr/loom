package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres/control"
)

// Live probes: the report's backend versions must be observed from the
// services themselves, never written by hand. The PostgreSQL driver stays
// confined to internal/backends (ownership contract): every probe here
// reaches the database through the backend's own public factory surface,
// never through a driver import.

// openProbeRuntime opens the backend factory's single readiness-gated pool
// with no schema assignment (the empty placement is the valid probe shape)
// and SetupNone, then hands back the runtime whose ConnSource is the
// factory's public single-connection port.
func openProbeRuntime(ctx context.Context, dsn string) (*backendspostgres.Runtime, error) {
	return backendspostgres.Open(ctx, backendspostgres.Config{
		DSN:            dsn,
		MaxConns:       2,
		MinConns:       1,
		AcquireTimeout: 15 * time.Second,
		Setup:          backendspostgres.SetupNone,
	})
}

func probePostgresVersion(ctx context.Context, dsn string) (string, error) {
	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	runtime, err := openProbeRuntime(connCtx, dsn)
	if err != nil {
		return "", fmt.Errorf("postgres probe open: %w", scrub(err, dsn))
	}
	defer func() { _ = runtime.Close() }()
	conn, err := runtime.ConnSource().Acquire(connCtx)
	if err != nil {
		return "", fmt.Errorf("postgres probe acquire: %w", scrub(err, dsn))
	}
	defer conn.Release()
	var version string
	if err := conn.QueryRow(connCtx, "SELECT version()").Scan(&version); err != nil {
		return "", fmt.Errorf("postgres probe query: %w", scrub(err, dsn))
	}
	return strings.TrimSpace(version), nil
}

func probeArangoVersion(ctx context.Context, rawDSN string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", fmt.Errorf("arango dsn: %w", err)
	}
	endpoint := parsed.Scheme + "://" + parsed.Host + "/_api/version"
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		req.SetBasicAuth(parsed.User.Username(), password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("arango probe: %w", scrub(err, rawDSN))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("arango probe: /_api/version returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Server  string `json:"server"`
		Version string `json:"version"`
		License string `json:"license"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("arango probe decode: %w", err)
	}
	if body.Server != "arango" || strings.TrimSpace(body.Version) == "" {
		return "", fmt.Errorf("arango probe: unexpected /_api/version body %+v", body)
	}
	return fmt.Sprintf("ArangoDB %s (%s)", body.Version, body.License), nil
}

func scrub(err error, secret string) error {
	if secret == "" {
		return err
	}
	text := err.Error()
	if redacted, changed := redact(text, secret); changed {
		return fmt.Errorf("%s", redacted)
	}
	return err
}

func redact(text, secret string) (string, bool) {
	if !strings.Contains(text, secret) {
		return text, false
	}
	return strings.ReplaceAll(text, secret, "<redacted>"), true
}

// benchmarkNamespacePrefix: benchmark arms may only touch databases named
// under this prefix. A shared PostgreSQL instance must never be writable by
// a benchmark run outside its namespace.
const benchmarkNamespacePrefix = "loom_bench_"

// parsePostgresURL enforces the URL-form law this lane accepts everywhere
// (the same shape provision, arms.json evidence, and the admin DSN use) and
// returns the parsed URL with a non-empty database named.
func parsePostgresURL(dsn string) (*url.URL, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return nil, "", fmt.Errorf("postgres dsn unparsable: URL form postgres://… is required (credentials redacted)")
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		return nil, "", fmt.Errorf("postgres dsn unparsable: no database named (credentials redacted)")
	}
	return parsed, database, nil
}

func guardPostgresNamespace(dsn string) error {
	_, database, err := parsePostgresURL(dsn)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(database, benchmarkNamespacePrefix) {
		return fmt.Errorf("postgres database %q is outside the benchmark namespace %s*; refusing to run against it",
			redactSecretText(database, dsn), benchmarkNamespacePrefix)
	}
	return nil
}

func redactSecretText(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "<redacted>")
}

func randomRunIDSuffix() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func parsePostgresDSN(dsn string) (host string, port int, database, user, password, sslmode string, err error) {
	parsed, database, err := parsePostgresURL(dsn)
	if err != nil {
		return "", 0, "", "", "", "", fmt.Errorf("unparsable postgres dsn")
	}
	host = parsed.Hostname()
	if host == "" {
		return "", 0, "", "", "", "", fmt.Errorf("unparsable postgres dsn")
	}
	port = 5432
	if rawPort := parsed.Port(); rawPort != "" {
		p, perr := strconv.Atoi(rawPort)
		if perr != nil || p <= 0 || p > 65535 {
			return "", 0, "", "", "", "", fmt.Errorf("unparsable postgres dsn")
		}
		port = p
	}
	if parsed.User != nil {
		user = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	if user == "" {
		return "", 0, "", "", "", "", fmt.Errorf("unparsable postgres dsn")
	}
	// sslmode rides the query parameter; the lane's DSNs state it
	// explicitly, and disable is the honest local default.
	sslmode = parsed.Query().Get("sslmode")
	if sslmode == "" {
		sslmode = "disable"
	}
	return host, port, database, user, password, sslmode, nil
}

func parseArangoDSN(dsn string) (rawURL, database string, err error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", "", fmt.Errorf("unparsable arango dsn")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("arango dsn scheme must be http or https")
	}
	database = strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		return "", "", fmt.Errorf("arango dsn must name a database")
	}
	if !strings.HasPrefix(database, benchmarkNamespacePrefix) {
		return "", "", fmt.Errorf("arango database %q is outside the benchmark namespace %s*", database, benchmarkNamespacePrefix)
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	return parsed.String(), database, nil
}
