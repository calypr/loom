package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func validUnitConfig(dsn string) Config {
	return Config{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	}
}

func assertRejectedWithoutConnection(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v (%T), want one matching %v", err, err, want)
	}
	var ce *ConnectError
	if errors.As(err, &ce) {
		t.Fatalf("config rejection must not reach the connection layer, got %T: %v", err, err)
	}
}

func TestConfigValidateAcceptsBoundedConfig(t *testing.T) {
	if err := validUnitConfig("postgres://loom@127.0.0.1:5432/loom").Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestConfigValidateRejectsInvalidPoolBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero max conns", func(c *Config) { c.MaxConns = 0 }},
		{"negative max conns", func(c *Config) { c.MaxConns = -3 }},
		{"negative min conns", func(c *Config) { c.MinConns = -1 }},
		{"min conns above max", func(c *Config) { c.MinConns = c.MaxConns + 1 }},
		{"zero acquire timeout", func(c *Config) { c.AcquireTimeout = 0 }},
		{"negative acquire timeout", func(c *Config) { c.AcquireTimeout = -time.Second }},
	} {
		cfg := validUnitConfig("postgres://loom@127.0.0.1:5432/loom")
		tc.mutate(&cfg)
		if err := cfg.Validate(); !errors.Is(err, ErrInvalidPoolConfig) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidPoolConfig", tc.name, err)
		}
	}
}

func TestOpenRejectsBlankDSNWithoutNetwork(t *testing.T) {
	for _, dsn := range []string{"", "   ", "\t\n "} {
		cfg := validUnitConfig(dsn)
		_, err := Open(context.Background(), cfg)
		assertRejectedWithoutConnection(t, err, ErrInvalidDSN)
	}
}

func TestOpenRejectsMalformedDSNWithoutNetwork(t *testing.T) {
	for _, dsn := range []string{
		"not a dsn at all",
		"postgres://loom@127.0.0.1:notaport/loom?sslmode=disable",
		"postgresql://loom@127.0.0.1:5432/loom?sslmode=not-a-valid-mode",
	} {
		cfg := validUnitConfig(dsn)
		_, err := Open(context.Background(), cfg)
		assertRejectedWithoutConnection(t, err, ErrInvalidDSN)
	}
}

func TestOpenRejectsInvalidPoolBoundsWithoutNetwork(t *testing.T) {
	cfg := validUnitConfig("postgres://loom@127.0.0.1:1/loom")
	cfg.MaxConns = 0
	_, err := Open(context.Background(), cfg)
	assertRejectedWithoutConnection(t, err, ErrInvalidPoolConfig)
}

func TestCheckServerVersion(t *testing.T) {
	for _, num := range []int64{180000, 180006, 189999} {
		if err := CheckServerVersion(num, "18"); err != nil {
			t.Errorf("CheckServerVersion(%d) = %v, want nil", num, err)
		}
	}
	for _, tc := range []struct {
		num     int64
		version string
		major   int
	}{
		{170014, "17.4", 17},
		{179999, "17.99", 17},
		{190000, "19.0", 19},
		{90624, "9.6.24", 9},
		{0, "", 0},
	} {
		err := CheckServerVersion(tc.num, tc.version)
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Errorf("CheckServerVersion(%d) = %v, want ErrUnsupportedVersion", tc.num, err)
			continue
		}
		var uve *UnsupportedVersionError
		if !errors.As(err, &uve) {
			t.Errorf("CheckServerVersion(%d) error type = %T, want *UnsupportedVersionError", tc.num, err)
			continue
		}
		if uve.Major != tc.major || uve.VersionNum != tc.num {
			t.Errorf("CheckServerVersion(%d) = {Major:%d VersionNum:%d}, want {Major:%d VersionNum:%d}",
				tc.num, uve.Major, uve.VersionNum, tc.major, tc.num)
		}
	}
}

func TestRefusedConnectionReturnsClassifiableError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := Config{
		DSN:            fmt.Sprintf("postgres://poolprobe@%s/probe?sslmode=disable", addr),
		MaxConns:       2,
		MinConns:       0,
		AcquireTimeout: 2 * time.Second,
	}
	_, err = Open(ctx, cfg)
	if err == nil {
		t.Fatal("Open against a refused port succeeded, want failure")
	}
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("Open error = %v (%T), want *ConnectError", err, err)
	}
	if !errors.Is(err, ErrConnect) {
		t.Fatalf("Open error does not match ErrConnect: %v", err)
	}
	if !IsRefusedConnection(err) {
		t.Fatalf("Open error not classified as a refused connection: %v", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("refused-connection error chain does not reach syscall.ECONNREFUSED: %v", err)
	}
}

var isolatedDBNameRe = regexp.MustCompile(`^pgstore01_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func TestPostgres18PoolIntegration(t *testing.T) {
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)
	if err := admin.Ping(ctx); err != nil {
		fail("ping test service", err)
	}

	dbName := fmt.Sprintf("pgstore01_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !isolatedDBNameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	testDSN := u.String()
	secrets = append(secrets, testDSN)
	if probe, err := url.Parse(testDSN); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	client, err := Open(ctx, Config{
		DSN:            testDSN,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 5 * time.Second,
	})
	if err != nil {
		fail("open pool", err)
	}
	defer client.Close()

	if err := client.Ping(ctx); err != nil {
		fail("ping pool", err)
	}

	conn, err := client.Acquire(ctx)
	if err != nil {
		fail("acquire connection", err)
	}
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		conn.Release()
		fail("query on acquired connection", err)
	}
	if one != 1 {
		conn.Release()
		t.Fatalf("SELECT 1 returned %d, want 1", one)
	}
	conn.Release()

	versionProbe, err := client.Acquire(ctx)
	if err != nil {
		fail("acquire version probe connection", err)
	}
	var versionNum int64
	if err := versionProbe.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8").Scan(&versionNum); err != nil {
		versionProbe.Release()
		fail("read server_version_num", err)
	}
	versionProbe.Release()
	if versionNum < 180000 || versionNum > 189999 {
		t.Fatalf("service reports server_version_num %d, want a PostgreSQL 18 build", versionNum)
	}

	marker := "pgstore01_marker_" + randomHex(t)
	markerConn, err := client.Acquire(ctx)
	if err != nil {
		fail("acquire marker connection", err)
	}
	_, err = markerConn.Exec(ctx, "CREATE TABLE public."+marker+" (id int)")
	markerConn.Release()
	if err != nil {
		fail("create marker table through pool", err)
	}
	witnessCfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		fail("parse witness config", err)
	}
	witnessCfg.ConnConfig.Database = dbName
	witness, err := pgxpool.NewWithConfig(ctx, witnessCfg)
	if err != nil {
		fail("connect isolated-database witness", err)
	}
	defer witness.Close()
	var markerVisible bool
	if err := witness.QueryRow(ctx,
		"SELECT to_regclass('public."+marker+"') IS NOT NULL").Scan(&markerVisible); err != nil {
		fail("witness query on isolated database", err)
	}
	if !markerVisible {
		t.Fatalf("marker table is absent from isolated database %s: the pool may not target the isolated database", dbName)
	}

	canceledCtx, canceledCancel := context.WithCancel(ctx)
	canceledCancel()
	_, err = client.Acquire(canceledCtx)
	if err == nil {
		t.Fatal("Acquire with a cancelled context succeeded, want failure")
	}
	if !errors.Is(err, ErrConnect) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error = %v, want ConnectError matching context.Canceled", err)
	}

	bounded, err := Open(ctx, Config{
		DSN:            testDSN,
		MaxConns:       1,
		MinConns:       0,
		AcquireTimeout: 5 * time.Second,
	})
	if err != nil {
		fail("open bounded pool", err)
	}
	defer bounded.Close()
	held, err := bounded.Acquire(ctx)
	if err != nil {
		fail("acquire sole bounded connection", err)
	}
	bounded.cfg.AcquireTimeout = 75 * time.Millisecond
	started := time.Now()
	_, err = bounded.Acquire(ctx)
	waited := time.Since(started)
	held.Release()
	if err == nil {
		t.Fatal("second Acquire on an exhausted pool succeeded, want timeout")
	}
	if !errors.Is(err, ErrConnect) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted acquire error = %v, want ConnectError matching context.DeadlineExceeded", err)
	}
	if waited > 5*time.Second {
		t.Fatalf("acquire timeout took %v, far beyond the 75ms bound", waited)
	}

	client.Close()
	bounded.Close()
}
