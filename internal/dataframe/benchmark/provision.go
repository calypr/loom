package benchmark

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Process provisioning for the two-arm benchmark. The harness owns the
// server processes (we spawn them), which is what makes the process-cold
// cache label honest: nothing else can claim a cold process it does not
// restart.

// ServerSpec is one arm's spawn configuration. Secrets arrive via Env and
// ConfigYAML and are scrubbed from every captured log line.
type ServerSpec struct {
	BinaryPath   string
	Backend      string // arango | postgres
	ConfigYAML   string
	Env          []string
	ReadyURL     string // polled until 2xx
	ReadyHeaders map[string]string
	Timeout      time.Duration // spawn+ready budget; 0 uses 60s
	StderrDir    string        // where arm logs are written (must be a scratch dir)
}

// ServerProcess is a running arm.
type ServerProcess struct {
	spec       ServerSpec
	cmd        *exec.Cmd
	logFile    *os.File
	logPath    string
	secrets    []string
	mu         sync.Mutex
	stopped    bool
	listenAddr string
}

// SpawnServer starts one arm and waits for readiness.
func SpawnServer(ctx context.Context, spec ServerSpec, secrets []string, listenAddr string) (*ServerProcess, error) {
	if !ValidBackend(spec.Backend) {
		return nil, fmt.Errorf("benchmark: cannot spawn backend %q", spec.Backend)
	}
	if spec.BinaryPath == "" || spec.ConfigYAML == "" {
		return nil, fmt.Errorf("benchmark: spawn needs binary path and config")
	}
	if spec.Timeout == 0 {
		spec.Timeout = 60 * time.Second
	}
	if err := os.MkdirAll(spec.StderrDir, 0o755); err != nil {
		return nil, fmt.Errorf("benchmark: arm log dir: %w", err)
	}
	logPath := filepath.Join(spec.StderrDir, fmt.Sprintf("loom-%s-%d.log", spec.Backend, time.Now().UnixNano()))
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("benchmark: arm log: %w", err)
	}
	proc := &ServerProcess{spec: spec, logFile: logFile, logPath: logPath, secrets: append([]string(nil), secrets...), listenAddr: listenAddr}
	if err := proc.start(ctx); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	return proc, nil
}

func (p *ServerProcess) start(ctx context.Context) error {
	// Config is written per-start so restarts reuse the exact same bytes.
	dir := filepath.Dir(p.logPath)
	cfgPath := filepath.Join(dir, fmt.Sprintf("loom-%s-config-%d.yaml", p.spec.Backend, os.Getpid()))
	if err := os.WriteFile(cfgPath, []byte(p.spec.ConfigYAML), 0o600); err != nil {
		return fmt.Errorf("benchmark: write arm config: %w", err)
	}
	args := []string{"--config", cfgPath, "--listen", p.listenAddr}
	cmd := exec.CommandContext(ctx, p.spec.BinaryPath, args...)
	cmd.Env = append(os.Environ(), p.spec.Env...)
	cmd.Stdout = p.scrubWriter()
	cmd.Stderr = p.scrubWriter()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("benchmark: spawn %s arm: %w", p.spec.Backend, err)
	}
	p.cmd = cmd
	if err := p.waitReady(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

// Restart stops and starts the same arm: the only legitimate source of the
// process-cold cache label.
func (p *ServerProcess) Restart(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("benchmark: arm never started")
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	p.stopped = false
	return p.start(ctx)
}

// Stop terminates the arm. Always called via defer by the harness.
func (p *ServerProcess) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.stopped = true
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	_ = p.logFile.Close()
}

// LogPath is the scrubbed arm log for evidence references.
func (p *ServerProcess) LogPath() string { return p.logPath }

func (p *ServerProcess) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(p.spec.Timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr string
	for time.Now().Before(deadline) {
		if p.cmd != nil && p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
			return fmt.Errorf("benchmark: %s arm exited before ready (see %s)", p.spec.Backend, p.scrub(p.logPath))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.spec.ReadyURL, nil)
		if err != nil {
			return fmt.Errorf("benchmark: readiness request: %w", err)
		}
		for k, v := range p.spec.ReadyHeaders {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Sprintf("status %d", resp.StatusCode)
		} else {
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("benchmark: %s arm readiness cancelled: %s", p.spec.Backend, lastErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("benchmark: %s arm not ready within %s (%s); log: %s", p.spec.Backend, p.spec.Timeout, lastErr, p.logPath)
}

// scrubWriter redacts registered secrets before they touch the log file:
// connection strings never enter evidence, mirroring the orchestrator's
// secret-scrub rule.
func (p *ServerProcess) scrubWriter() io.Writer {
	return scrubWriter{inner: p.logFile, secrets: p.secrets}
}

type scrubWriter struct {
	inner   io.Writer
	secrets []string
}

func (w scrubWriter) Write(data []byte) (int, error) {
	out := string(data)
	for _, secret := range w.secrets {
		if secret != "" {
			out = strings.ReplaceAll(out, secret, "[REDACTED]")
		}
	}
	if _, err := io.WriteString(w.inner, out); err != nil {
		return 0, err
	}
	return len(data), nil // report original length: this is an observer, not a pipe
}

func (p *ServerProcess) scrub(s string) string {
	for _, secret := range p.secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return s
}

// FreePort reserves an ephemeral 127.0.0.1 port. The reservation is closed
// immediately; a bind race is handled by SpawnServer's readiness timeout.
func FreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// --- Mock Fence (scoped-authorization negative control) --------------
//
// Contract mirrors internal/authscope: GET <issuer>/user answers the same
// bearer token; token iss must equal the trusted issuer; authz entries are
// resource-path -> [{method:"read", service:"*"}]. Tokens are unsigned JWTs
// because CalyprAuthenticator checks shape only and the fence client checks
// iss + answers /user; the mock is the fence.

// MockFence serves /user grants for benchmark tokens.
type MockFence struct {
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	grants   map[string][]string // sub -> auth resource paths
	requests int
}

// NewMockFence starts the fence on a loopback port.
func NewMockFence() (*MockFence, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("benchmark: mock fence listen: %w", err)
	}
	f := &MockFence{listener: ln, grants: map[string][]string{}}
	f.server = &http.Server{Handler: http.HandlerFunc(f.handle)}
	go func() { _ = f.server.Serve(ln) }()
	return f, nil
}

// URL is the trusted issuer base for this fence.
func (f *MockFence) URL() string { return "http://" + f.listener.Addr().String() }

// Grant gives subject `sub` read access to the given auth resource paths.
func (f *MockFence) Grant(sub string, paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[sub] = append([]string(nil), paths...)
}

// Requests counts /user hits, for the control's evidence line.
func (f *MockFence) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// Close shuts the fence down.
func (f *MockFence) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = f.server.Shutdown(ctx)
}

func (f *MockFence) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/user" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	auth := r.Header.Get("Authorization")
	sub := subjectFromBearer(auth)
	if sub == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	paths := append([]string(nil), f.grants[sub]...)
	f.mu.Unlock()
	authz := map[string]any{}
	for _, path := range paths {
		// A benchmark grant is a real principal grant: reads AND writes on
		// the path. Dataset ingest and explorer publication run as write;
		// the negative-auth control's scoped principal holds no grant on
		// the dataset path at all, in either direction.
		authz[path] = []any{
			map[string]any{"method": "read", "service": "*"},
			map[string]any{"method": "write", "service": "*"},
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sub": sub, "authz": authz})
}

// subjectFromBearer decodes the (unsigned) JWT payload for sub/user_name,
// mirroring internal/authscope's subjectFromAuthorizationHeader shape rules
// without importing its internals.
func subjectFromBearer(auth string) string {
	const prefix = "bearer "
	token := strings.TrimSpace(auth)
	if len(token) < len(prefix) || !strings.EqualFold(token[:len(prefix)], prefix) {
		return ""
	}
	token = strings.TrimSpace(token[len(prefix):])
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if sub, _ := claims["sub"].(string); sub != "" {
		return sub
	}
	if user, _ := claims["user_name"].(string); user != "" {
		return user
	}
	return ""
}

// MakeBearerToken builds an unsigned JWT-shaped bearer token with iss bound
// to the fence URL, satisfying the issuer-match check.
func MakeBearerToken(issuer, sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadRaw, _ := json.Marshal(map[string]any{"iss": issuer, "sub": sub})
	payload := base64.RawURLEncoding.EncodeToString(payloadRaw)
	return "Bearer " + header + "." + payload + "." + base64.RawURLEncoding.EncodeToString([]byte("mock"))
}

// BuildServerConfigYAML renders the arm config: ClickHouse disabled on both
// arms (matched reader path ruling), scoped calypr auth against the mock
// fence, backend wiring passed in verbatim from the caller.
func BuildServerConfigYAML(listen, backend, backendYAML, fenceURL, cursorSecret string) string {
	var b bytes.Buffer
	b.WriteString("server:\n")
	fmt.Fprintf(&b, "  listen: %q\n", listen)
	fmt.Fprintf(&b, "  backend: %q\n", backend)
	b.WriteString("  clickhouse:\n    enabled: false\n")
	// The server refuses to configure without a >=16-char signing secret;
	// callers generate one per run and scrub it from arm logs.
	fmt.Fprintf(&b, "  population_mapping_cursor_secret: %q\n", cursorSecret)
	b.WriteString(backendYAML)
	b.WriteString("auth:\n  mode: calypr\n  calypr:\n")
	fmt.Fprintf(&b, "    trusted_issuer: %q\n", fenceURL)
	b.WriteString("    request_timeout: 10s\n    cache_ttl: 1s\n")
	return b.String()
}

// PostgresBackendYAML renders the server.postgresql block for a spawned
// arm. The six-series schema ladder is mandatory: without it the server
// takes the first-query route and refuses all authoring routes, which would
// make the battery unrunnable — the ladder is spelled out here so a typo
// cannot silently degrade the run. Schemas live under a benchmark-owned
// prefix so arms can never touch production schemas.
func PostgresBackendYAML(host string, port int, database, user, password, sslmode, schemaPrefix string) string {
	if sslmode == "" {
		sslmode = "disable"
	}
	var b strings.Builder
	b.WriteString("  postgresql:\n")
	fmt.Fprintf(&b, "    host: %q\n", host)
	fmt.Fprintf(&b, "    port: %d\n", port)
	fmt.Fprintf(&b, "    database: %q\n", database)
	fmt.Fprintf(&b, "    user: %q\n", user)
	fmt.Fprintf(&b, "    password: %q\n", password)
	fmt.Fprintf(&b, "    sslmode: %q\n", sslmode)
	b.WriteString("    schemas:\n")
	for _, series := range []string{"source", "explorer_control", "publication_control", "dataset_release", "recipe_chains", "recipe_exec_registries"} {
		fmt.Fprintf(&b, "      %s: %q\n", series, schemaPrefix+"_"+series)
	}
	return b.String()
}

// ArangoBackendYAML renders the server-level url/database keys for a
// spawned Arango arm.
func ArangoBackendYAML(rawURL, database string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  url: %q\n", rawURL)
	fmt.Fprintf(&b, "  database: %q\n", database)
	return b.String()
}
