package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Live probes: the report's backend versions must be observed from the
// services themselves, never written by hand.

func probePostgresVersion(ctx context.Context, dsn string) (string, error) {
	connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(connCtx, dsn)
	if err != nil {
		return "", fmt.Errorf("postgres probe connect: %w", scrub(err, dsn))
	}
	defer conn.Close(context.Background())
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

func guardPostgresNamespace(dsn string) error {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("postgres dsn unparsable: %s", redactSecretText(err.Error(), dsn))
	}
	if !strings.HasPrefix(config.Database, benchmarkNamespacePrefix) {
		return fmt.Errorf("postgres database %q is outside the benchmark namespace %s*; refusing to run against it",
			redactSecretText(config.Database, dsn), benchmarkNamespacePrefix)
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
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", 0, "", "", "", "", fmt.Errorf("unparsable postgres dsn")
	}
	host = config.Host
	port = int(config.Port)
	if port == 0 {
		port = 5432
	}
	database, user, password = config.Database, config.User, string(config.Password)
	sslmode = "disable"
	if config.TLSConfig != nil {
		sslmode = config.TLSConfig.ServerName
		if sslmode == "" {
			sslmode = "require"
		}
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
