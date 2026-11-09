#!/usr/bin/env node
// Opt-in runner for the isolated PostgreSQL 18 integration fixture.
//
// The default developer stack in experimental/docker-compose.yml stays
// Arango + ClickHouse. This runner is the acceptance path for the opt-in
// postgres-test profile (or any owned PostgreSQL 18 test service exposed
// through LOOM_TEST_POSTGRES_URL). A compose-up command alone is never
// acceptance: readiness must be proven against the live server (PostgreSQL
// 18 only), the run owns exactly one uniquely-named pgfixture_* database,
// the real Go integration case must execute (never skip) with
// LOOM_TEST_POSTGRES_URL propagated to the child process, and cleanup in
// finally removes only the run's own namespace. A missing compose runtime
// is recorded as an explicit blocked capability, never faked healthy.

import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { verificationTimeoutMs } from './lib/verification-budget.mjs';

// Aligned CI budget: the Go test timeout never exceeds the child kill, the
// child kill never exceeds the workflow step timeout, and the step timeout
// (840s) plus the two-minute evidence margin fits the job timeout.
const goTestBudgetMs = Math.min(verificationTimeoutMs, 300_000);

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const composeFile = resolve(root, 'experimental', 'docker-compose.yml');

// Mirrors postgres.RequiredMajorVersion in internal/store/postgres.
export const REQUIRED_MAJOR_VERSION = 18;
// Every database this runner may ever touch starts with this prefix and
// matches the full owned-namespace pattern; the probe refuses anything else.
export const NAMESPACE_PREFIX = 'pgfixture_';
// The merged pg-store-01 real-PG case: creates its own isolated database,
// opens/pings/acquires a bounded pool and drops only that database.
export const REQUIRED_INTEGRATION_TEST = 'TestPostgres18PoolIntegration';
// Positive and negative checks the fixture probe must execute literally.
export const EXPECTED_CHECKS = ['pg18', 'literal-rows', 'aggregate', 'unique-negative', 'numeric-negative'];

const DATABASE_NAME = /^pgfixture_[0-9]+_[0-9a-f]{12}$/;

// Administered through the repository's own pgx dependency: the runner writes
// this program into a fresh private temporary directory and executes it with
// `go run` from the repository root. The admin DSN travels in the
// LOOM_PG_FIXTURE_ADMIN_URL environment variable, never argv, and the probe
// scrubs credentials from every line it prints.
export const PROBE_SOURCE = `// Command pgfixture-probe administers exactly one owned PostgreSQL 18
// fixture database for scripts/run-postgres-integration.mjs. Every mutation
// is restricted to names matching the runner's owned pgfixture namespace
// pattern and uses ordered query parameters or sanitized identifiers. The
// admin DSN is read only from LOOM_PG_FIXTURE_ADMIN_URL so credentials never
// appear in argv or in any printed line.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const requiredMajor = ${REQUIRED_MAJOR_VERSION}

var ownedName = regexp.MustCompile("^pgfixture_[0-9]+_[0-9a-f]{12}$")
var tokenPattern = regexp.MustCompile("^[0-9a-f][0-9a-f-]+$")

func adminSecrets() []string {
	raw := os.Getenv("LOOM_PG_FIXTURE_ADMIN_URL")
	if raw == "" {
		return nil
	}
	parts := []string{raw}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && len(pw) >= 4 {
			parts = append(parts, pw, u.User.Username()+":"+pw+"@")
		}
	}
	return parts
}

func scrub(s string) string {
	for _, secret := range adminSecrets() {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

func emit(op string, fields map[string]any, err error) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ok"] = err == nil
	fields["op"] = op
	if err != nil {
		fields["error"] = err.Error()
	}
	blob, merr := json.Marshal(fields)
	out := "{}"
	if merr == nil {
		out = string(blob)
	}
	fmt.Println(scrub(out))
	if err != nil {
		os.Exit(1)
	}
}

func fail(op string, err error) { emit(op, nil, err) }

// escapeLike neutralizes LIKE wildcards with a custom escape character so the
// owned-name audit matches literally (underscore is a single-char wildcard).
func escapeLike(s string) string {
	return strings.NewReplacer("x", "xx", "%", "x%", "_", "x_").Replace(s)
}

func connect(ctx context.Context, dsn string) *pgx.Conn {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fail("connect", err)
	}
	return conn
}

func serverInfo(ctx context.Context, conn *pgx.Conn) (int64, string) {
	var num int64
	var version string
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int8, current_setting('server_version')").Scan(&num, &version); err != nil {
		fail("version", err)
	}
	return num, version
}

func ping(ctx context.Context, raw string) {
	conn := connect(ctx, raw)
	defer conn.Close(ctx)
	num, version := serverInfo(ctx, conn)
	emit("ping", map[string]any{"versionNum": num, "version": version, "major": int(num / 10000)}, nil)
}

func create(ctx context.Context, raw string, name string) {
	if !ownedName.MatchString(name) {
		fail("create", fmt.Errorf("refusing name %q outside the owned pgfixture namespace", name))
	}
	conn := connect(ctx, raw)
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		fail("create", err)
	}
	if exists {
		fail("create", fmt.Errorf("refusing pre-existing database %s", name))
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		fail("create", err)
	}
	emit("create", map[string]any{"database": name}, nil)
}

func drop(ctx context.Context, raw string, name string) {
	if !ownedName.MatchString(name) {
		fail("drop", fmt.Errorf("refusing name %q outside the owned pgfixture namespace", name))
	}
	conn := connect(ctx, raw)
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		fail("drop", err)
	}
	emit("drop", map[string]any{"database": name}, nil)
}

func list(ctx context.Context, raw string, prefix string) {
	if !ownedName.MatchString(prefix) {
		fail("list", fmt.Errorf("refusing audit prefix %q outside the owned pgfixture namespace", prefix))
	}
	conn := connect(ctx, raw)
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1 ESCAPE 'x' ORDER BY datname", escapeLike(prefix)+"%")
	if err != nil {
		fail("list", err)
	}
	defer rows.Close()
	databases := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			fail("list", err)
		}
		databases = append(databases, name)
	}
	if err := rows.Err(); err != nil {
		fail("list", err)
	}
	emit("list", map[string]any{"prefix": prefix, "databases": databases}, nil)
}

type fixtureRow struct {
	ID     int64
	Label  string
	Amount string
	State  string
}

func sptr(s string) *string { return &s }

func acceptance(ctx context.Context, raw string, name string, token string) {
	if !ownedName.MatchString(name) {
		fail("acceptance", fmt.Errorf("refusing name %q outside the owned pgfixture namespace", name))
	}
	if !tokenPattern.MatchString(token) {
		fail("acceptance", fmt.Errorf("refusing malformed correlation token"))
	}
	u, err := url.Parse(raw)
	if err != nil {
		fail("acceptance", err)
	}
	u.Path = "/" + name
	conn := connect(ctx, u.String())
	defer conn.Close(ctx)
	// Prove the effective target before any write: pgconn lets a dbname query
	// parameter outrank the URL path, so the connection itself is the
	// authority on which database receives the fixture writes.
	var effective string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&effective); err != nil {
		fail("acceptance", err)
	}
	if effective != name {
		fail("acceptance", fmt.Errorf("effective target database %s is not the owned fixture %s", effective, name))
	}
	num, version := serverInfo(ctx, conn)
	if int(num/10000) != requiredMajor {
		fail("acceptance", fmt.Errorf("server version %s is not PostgreSQL %d", version, requiredMajor))
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE loom_fixture_rows (id bigint PRIMARY KEY, label text NOT NULL UNIQUE, amount numeric(10,2) NOT NULL, payload jsonb)"); err != nil {
		fail("acceptance", err)
	}
	// Ordered SQL parameters. The three payload states distinguish a JSON
	// object, JSON null, and SQL NULL exactly as the store contract requires.
	inserts := []struct {
		id      int64
		label   string
		amount  string
		payload *string
	}{
		{1, "alpha", "10.50", sptr("{\\"k\\":\\"v\\"}")},
		{2, "beta", "20.00", sptr("null")},
		{3, "gamma", "0.00", nil},
	}
	const insertSQL = "INSERT INTO loom_fixture_rows (id, label, amount, payload) VALUES ($1, $2, $3::numeric, $4::jsonb)"
	for _, row := range inserts {
		if _, err := conn.Exec(ctx, insertSQL, row.id, row.label, row.amount, row.payload); err != nil {
			fail("acceptance", err)
		}
	}
	got := []fixtureRow{}
	// payload::text keeps SQL NULL (nil) distinct from JSON null (the four
	// bytes n-u-l-l); scanning jsonb directly would conflate them client-side.
	rows, err := conn.Query(ctx, "SELECT id, label, amount::text, payload::text FROM loom_fixture_rows ORDER BY id")
	if err != nil {
		fail("acceptance", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row fixtureRow
		var payload *string
		if err := rows.Scan(&row.ID, &row.Label, &row.Amount, &payload); err != nil {
			fail("acceptance", err)
		}
		switch {
		case payload == nil:
			row.State = "sql-null"
		case *payload == "null":
			row.State = "json-null"
		default:
			row.State = "json-value"
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		fail("acceptance", err)
	}
	want := []fixtureRow{
		{1, "alpha", "10.50", "json-value"},
		{2, "beta", "20.00", "json-null"},
		{3, "gamma", "0.00", "sql-null"},
	}
	if !reflect.DeepEqual(got, want) {
		fail("acceptance", fmt.Errorf("literal row mismatch: got %+v", got))
	}
	var count int64
	var total string
	if err := conn.QueryRow(ctx, "SELECT count(*), sum(amount)::text FROM loom_fixture_rows").Scan(&count, &total); err != nil {
		fail("acceptance", err)
	}
	if count != 3 || total != "30.50" {
		fail("acceptance", fmt.Errorf("literal aggregate mismatch: count=%d total=%s", count, total))
	}
	if _, err := conn.Exec(ctx, insertSQL, int64(4), "alpha", "1.00", nil); err == nil {
		fail("acceptance", fmt.Errorf("unique-negative: duplicate label was accepted"))
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "23505" {
			fail("acceptance", fmt.Errorf("unique-negative: expected unique_violation 23505, got %v", err))
		}
	}
	if _, err := conn.Exec(ctx, insertSQL, int64(5), "delta", "not-a-number", nil); err == nil {
		fail("acceptance", fmt.Errorf("numeric-negative: invalid numeric was accepted"))
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.SQLState() != "22P02" {
			fail("acceptance", fmt.Errorf("numeric-negative: expected invalid_text_representation 22P02, got %v", err))
		}
	}
	emit("acceptance", map[string]any{
		"database": name,
		"token":    token,
		"rows":     len(got),
		"version":  version,
		"checked":  []string{"pg18", "literal-rows", "aggregate", "unique-negative", "numeric-negative"},
	}, nil)
}

func usage() error {
	return fmt.Errorf("usage: pgfixture-probe ping|create NAME|drop NAME|list PREFIX|acceptance NAME TOKEN")
}

func main() {
	if len(os.Args) < 2 {
		fail("usage", usage())
	}
	raw := os.Getenv("LOOM_PG_FIXTURE_ADMIN_URL")
	if raw == "" {
		fail("env", fmt.Errorf("LOOM_PG_FIXTURE_ADMIN_URL is not set"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "ping":
		ping(ctx, raw)
	case "create":
		if len(os.Args) != 3 {
			fail("usage", usage())
		}
		create(ctx, raw, os.Args[2])
	case "drop":
		if len(os.Args) != 3 {
			fail("usage", usage())
		}
		drop(ctx, raw, os.Args[2])
	case "list":
		if len(os.Args) != 3 {
			fail("usage", usage())
		}
		list(ctx, raw, os.Args[2])
	case "acceptance":
		if len(os.Args) != 4 {
			fail("usage", usage())
		}
		acceptance(ctx, raw, os.Args[2], os.Args[3])
	default:
		fail("usage", usage())
	}
}
`;

export function extractSecrets(rawUrl) {
  const secrets = [];
  if (typeof rawUrl === 'string' && rawUrl.length >= 4) secrets.push(rawUrl);
  try {
    const url = new URL(rawUrl);
    const password = url.password ? decodeURIComponent(url.password) : '';
    if (password.length >= 4) {
      secrets.push(password);
      if (url.username) secrets.push(`${decodeURIComponent(url.username)}:${password}@`);
    }
  } catch { /* not a URL: the raw-string scrub above still applies */ }
  return [...new Set(secrets)].filter(secret => secret.length >= 4);
}

// Redacts credential material (raw URL, password, userinfo) from any text
// before it reaches evidence, console output, or child-process logs.
export function scrubSecrets(text, secrets = []) {
  let out = String(text);
  // Material shorter than four characters is treated as ordinary text, not
  // credential material, so redaction cannot mangle the whole log.
  for (const secret of [...secrets].filter(secret => secret.length >= 4).sort((a, b) => b.length - a.length)) out = out.split(secret).join('[redacted]');
  return out;
}

export function parseAdminUrl(raw) {
  assert.ok(typeof raw === 'string' && raw.trim() !== '', 'LOOM_TEST_POSTGRES_URL must name an owned PostgreSQL 18 test service (blocked without one; never faked)');
  let url;
  try { url = new URL(raw.trim()); } catch { assert.fail('LOOM_TEST_POSTGRES_URL must be a postgresql:// connection URL'); }
  assert.ok(url.protocol === 'postgres:' || url.protocol === 'postgresql:', 'LOOM_TEST_POSTGRES_URL must use the postgresql:// scheme');
  assert.ok(url.hostname !== '', 'LOOM_TEST_POSTGRES_URL must name a host');
  assert.ok(url.username !== '', 'LOOM_TEST_POSTGRES_URL must carry the test user');
  assert.ok(url.password !== '', 'LOOM_TEST_POSTGRES_URL must carry the test password');
  const database = decodeURIComponent(url.pathname.replace(/^\//, ''));
  assert.ok(database !== '', 'LOOM_TEST_POSTGRES_URL must name the maintenance database');
  // pgconn lets a dbname query parameter outrank the URL path (see the
  // client_test.go precedent in internal/store/postgres): rejecting it here
  // covers every probe call, so fixture writes can never redirect outside
  // the owned namespace. The probe additionally asserts current_database().
  assert.ok(!url.searchParams.has('dbname') && !url.searchParams.has('database'), 'LOOM_TEST_POSTGRES_URL must not pin a database in the query string; the runner owns the fixture namespace');
  return { raw: raw.trim(), host: url.hostname, port: url.port || '5432', username: decodeURIComponent(url.username), database };
}

export function makeRunId(now = Date.now(), pid = process.pid) {
  return `${now}${String(pid % 100000).padStart(5, '0')}_${randomBytes(6).toString('hex')}`;
}

export function ownedDatabaseName(runId) {
  const name = `${NAMESPACE_PREFIX}${runId}`;
  assert.ok(DATABASE_NAME.test(name), `Refusing fixture database outside the owned namespace pattern: ${name}`);
  return name;
}

// The real Go integration case must run and pass. A skip is a blocker, never
// a substitute for real-PG acceptance.
export function checkGoEvents(stdout, required) {
  const events = stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  assert.ok(!events.some(e => e.Action === 'fail'), 'Go reported failure');
  const passes = events.filter(e => e.Test && e.Action === 'pass').map(e => e.Test);
  for (const name of [required].flat()) {
    assert.ok(!events.some(e => e.Test === name && e.Action === 'skip'), `Required real integration case was skipped, a blocker and never acceptance: ${name}`);
    assert.ok(events.some(e => e.Test === name && e.Action === 'run'), `Required real integration case did not run: ${name}`);
    assert.ok(passes.includes(name), `Required real integration case did not pass: ${name}`);
  }
  assert.ok(events.some(e => !e.Test && e.Action === 'pass'), 'No successful package completion');
  assert.ok(passes.length, 'No test assertions completed');
  const skipped = events.filter(e => e.Test && e.Action === 'skip').map(e => e.Test);
  assert.deepEqual(skipped, [], `Unexpected skipped case (skips cannot establish real-PG acceptance): ${skipped.join(', ')}`);
  return { passes };
}

// Detects a usable compose runtime explicitly. Never assumes a Docker socket.
export function selectComposeRuntime(probe) {
  if (probe('docker', ['compose', 'version']).status === 0) return ['docker', 'compose'];
  if (probe('podman', ['compose', 'version']).status === 0) return ['podman', 'compose'];
  if (probe('podman-compose', ['version']).status === 0) return ['podman-compose'];
  return null;
}

export function composeServices(probe, command, args) {
  const result = probe(command[0], [...command.slice(1), '-f', composeFile, ...args, 'config', '--services']);
  assert.equal(result.status, 0, `Compose config parse failed: ${String(result.stderr).trim()}`);
  return result.stdout.trim().split('\n').filter(Boolean).sort();
}

// Verifies the compose contract offline-by-config: the default stack stays
// Arango + ClickHouse and the PG service stays behind the opt-in profile.
export function inspectComposeContract(probe) {
  const command = selectComposeRuntime(probe);
  if (!command) {
    const error = new Error('No docker/podman compose runtime is available in this environment; the opt-in compose-up path is recorded as blocked and is not counted as acceptance.');
    error.code = 'EComposeUnavailable';
    throw error;
  }
  const defaultServices = composeServices(probe, command, []);
  assert.deepEqual(defaultServices, ['arangodb', 'clickhouse'], `Default experimental stack must stay Arango + ClickHouse, got ${defaultServices.join(', ')}`);
  const profileServices = composeServices(probe, command, ['--profile', 'postgres-test']);
  assert.ok(profileServices.includes('postgres18-test'), 'Opt-in postgres18-test service is missing from the postgres-test profile');
  return { status: 'verified', runtime: command.join(' '), defaultServices, profileServices };
}

function defaultComposeProbe(command, args) {
  const result = spawnSync(command, args, { encoding: 'utf8', timeout: 60_000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error) return { status: 1, stdout: '', stderr: String(result.error.message) };
  return { status: result.status ?? 1, stdout: result.stdout || '', stderr: result.stderr || '' };
}

function createProbeRunner() {
  const directory = mkdtempSync(join(tmpdir(), 'loom-pgfixture-probe-'));
  const file = join(directory, 'main.go');
  writeFileSync(file, PROBE_SOURCE, { mode: 0o600 });
  const run = async (command, args, { adminUrl }) => {
    const result = spawnSync('go', ['run', file, command, ...args], {
      cwd: root,
      env: { ...process.env, GOTOOLCHAIN: 'local', LOOM_PG_FIXTURE_ADMIN_URL: adminUrl },
      encoding: 'utf8',
      timeout: 90_000,
      maxBuffer: 16 * 1024 * 1024,
      detached: true,
    });
    const secrets = extractSecrets(adminUrl);
    if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
    const line = String(result.stdout || '').trim().split('\n').filter(Boolean).pop();
    if (line) {
      const parsed = JSON.parse(line);
      if (typeof parsed.error === 'string') parsed.error = scrubSecrets(parsed.error, secrets);
      return parsed;
    }
    if (result.error) throw new Error(scrubSecrets(`probe ${command} failed to start: ${result.error.message}`, secrets));
    throw new Error(scrubSecrets(`probe ${command} exited ${result.status} without a JSON result: ${String(result.stderr).trim()}`, secrets));
  };
  run.dispose = () => rmSync(directory, { recursive: true, force: true });
  return run;
}

function defaultGoTest(args, env) {
  const result = spawnSync('go', args, {
    cwd: root,
    env,
    encoding: 'utf8',
    // The Go test timeout must fire first; the spawn kill is the backstop.
    timeout: goTestBudgetMs + 30_000,
    maxBuffer: 64 * 1024 * 1024,
    detached: true,
  });
  if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
  return result;
}

export async function runPostgres(output, deps = {}) {
  const env = deps.env ?? process.env;
  const goTest = deps.goTest ?? defaultGoTest;
  const now = deps.now ?? (() => Date.now());
  const uuid = deps.uuid ?? randomUUID;
  const rawUrl = env.LOOM_TEST_POSTGRES_URL;
  const secrets = extractSecrets(rawUrl);
  const directory = resolve(output);
  assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Evidence directory must be empty');
  mkdirSync(directory, { recursive: true });
  const runId = deps.runId ?? makeRunId(now());
  const database = ownedDatabaseName(runId);
  // Created after every pre-flight assertion so a throw below can never
  // strand the private temporary probe directory.
  const probe = deps.probe ?? createProbeRunner();
  const deadline = performance.now() + 8 * 60 * 1000;
  const evidence = {
    schema: 'loom-postgres-fixture/1',
    status: 'running',
    startedAt: new Date(now()).toISOString(),
    tool: 'scripts/run-postgres-integration.mjs',
    requiredMajorVersion: REQUIRED_MAJOR_VERSION,
    runId,
    namespacePrefix: NAMESPACE_PREFIX,
    phases: [],
    cleanup: [],
  };
  const owned = [];
  const budget = () => assert.ok(performance.now() < deadline, 'Eight-minute fixture budget exhausted; reserve remaining CI time for cleanup');
  try {
    const admin = parseAdminUrl(rawUrl);
    evidence.postgres = { host: admin.host, port: admin.port, username: admin.username, maintenanceDatabase: admin.database };
    // Compose capability is recorded, never required: compose-up alone is not
    // acceptance, and a missing runtime is an explicit blocked item.
    const compose = deps.compose ?? (() => inspectComposeContract(deps.composeProbe ?? defaultComposeProbe));
    try {
      evidence.compose = compose();
    } catch (error) {
      if (error.code !== 'EComposeUnavailable') throw error;
      evidence.compose = { status: 'blocked-missing-runtime', note: error.message };
    }
    budget();
    evidence.phases.push('readiness');
    const ping = await probe('ping', [], { adminUrl: rawUrl });
    assert.equal(ping.ok, true, `Readiness ping failed: ${ping.error}`);
    assert.equal(ping.major, REQUIRED_MAJOR_VERSION, `The fixture requires PostgreSQL ${REQUIRED_MAJOR_VERSION}; the service reports ${ping.version}`);
    evidence.readiness = { status: 'ready', versionNum: ping.versionNum, version: ping.version };
    budget();
    evidence.phases.push('namespace');
    const created = await probe('create', [database], { adminUrl: rawUrl });
    assert.equal(created.ok, true, `Refusing unavailable fixture database ${database}: ${created.error}`);
    // Cleanup authority begins only after a verified create of the owned name.
    owned.push(database);
    evidence.namespace = { status: 'owned', database };
    budget();
    evidence.phases.push('acceptance');
    const token = uuid();
    const acceptance = await probe('acceptance', [database, token], { adminUrl: rawUrl });
    assert.equal(acceptance.ok, true, `Real-PG fixture acceptance failed: ${acceptance.error}`);
    assert.equal(acceptance.token, token, 'Fixture acceptance did not observe the run correlation token');
    assert.deepEqual(acceptance.checked, EXPECTED_CHECKS, 'Fixture acceptance checks drifted from the contract');
    evidence.acceptance = { status: 'passed', database, rows: acceptance.rows, checked: acceptance.checked };
    budget();
    evidence.phases.push('integration');
    // Propagation proof: the child sees LOOM_TEST_POSTGRES_URL only because
    // this run passed it; without it the required case skips, which is a
    // failure above, never a pass.
    const childEnv = { ...env, GOTOOLCHAIN: 'local', GOMAXPROCS: '2', LOOM_TEST_POSTGRES_URL: rawUrl };
    const goArgs = ['test', '-json', '-count=1', `-timeout=${goTestBudgetMs / 1000}s`, '-run', `^${REQUIRED_INTEGRATION_TEST}$`, './internal/store/postgres'];
    const go = await Promise.resolve(goTest(goArgs, childEnv));
    // Every evidence file is scrubbed before it lands on disk; checkGoEvents
    // still parses the in-memory stdout, so behavior is unchanged.
    writeFileSync(join(directory, 'integration.jsonl'), scrubSecrets(go.stdout || '', secrets));
    writeFileSync(join(directory, 'integration.stderr'), scrubSecrets(go.stderr || '', secrets));
    assert.equal(go.status, 0, `${REQUIRED_INTEGRATION_TEST} failed: ${scrubSecrets(go.error?.message || go.stderr || '', secrets)}`);
    const { passes } = checkGoEvents(go.stdout, REQUIRED_INTEGRATION_TEST);
    evidence.integration = {
      status: 'passed',
      command: ['go', 'test', '-count=1', '-run', `^${REQUIRED_INTEGRATION_TEST}$`, './internal/store/postgres'],
      propagatedEnv: { LOOM_TEST_POSTGRES_URL: 'set by runner (credential material redacted)' },
      passes,
    };
    evidence.status = 'passed';
  } catch (error) {
    evidence.status = 'failed';
    evidence.error = scrubSecrets(error.message, secrets);
  } finally {
    for (const name of owned.toReversed()) {
      try {
        const dropped = await probe('drop', [name], { adminUrl: rawUrl });
        assert.equal(dropped.ok, true, `drop failed: ${dropped.error}`);
        evidence.cleanup.push({ database: name, status: 'dropped' });
      } catch (cleanupError) {
        evidence.status = 'failed';
        const dropMessage = scrubSecrets(cleanupError.message, secrets);
        evidence.error = evidence.error ? `${evidence.error}; cleanup: ${dropMessage}` : dropMessage;
        evidence.cleanup.push({ database: name, error: dropMessage });
      }
    }
    try {
      // Zero leftovers is proven for this run's owned namespace only; other
      // pgfixture_* databases belong to other owners and are never removed.
      const audit = await probe('list', [database], { adminUrl: rawUrl });
      assert.equal(audit.ok, true, `leftover audit failed: ${audit.error}`);
      evidence.leftovers = audit.databases;
      assert.deepEqual(audit.databases, [], `Owned namespace still present after cleanup: ${audit.databases.join(', ')}`);
    } catch (auditError) {
      evidence.status = 'failed';
      const auditMessage = scrubSecrets(auditError.message, secrets);
      // Keep the primary failure visible; the audit failure is appended.
      evidence.error = evidence.error ? `${evidence.error}; leftover audit: ${auditMessage}` : auditMessage;
    }
    evidence.finishedAt = new Date(now()).toISOString();
    probe.dispose?.();
    writeFileSync(join(directory, 'summary.json'), JSON.stringify(evidence, null, 2) + '\n');
  }
  return evidence;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  assert.ok(args.length === 2 && args[0] === '--out', 'Usage: node scripts/run-postgres-integration.mjs --out EMPTY_DIRECTORY');
  const evidence = await runPostgres(args[1]);
  console.log(JSON.stringify(evidence, null, 2));
  if (evidence.status === 'failed') process.exitCode = 1;
}
