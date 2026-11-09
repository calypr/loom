// Package postgres implements the PostgreSQL 18 source-store connection pool.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package never falls back to another backend on
// its own: Open and Ping return explicit, classifiable errors and callers must
// surface them instead of silently retrying against a different store.
// Schema/migrations are owned elsewhere and are not part of this slice.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RequiredMajorVersion is the only PostgreSQL major version the source store
// accepts. The readiness check rejects every other server.
const RequiredMajorVersion = 18

// Sentinel errors for classifying failures without string matching.
var (
	// ErrInvalidDSN reports a blank or malformed connection string.
	ErrInvalidDSN = errors.New("postgres: invalid dsn")
	// ErrInvalidPoolConfig reports pool bounds outside their valid range.
	ErrInvalidPoolConfig = errors.New("postgres: invalid pool config")
	// ErrConnect reports a failure to reach or talk to the PostgreSQL server.
	ErrConnect = errors.New("postgres: connect failed")
	// ErrUnsupportedVersion reports a server that is not PostgreSQL 18.
	ErrUnsupportedVersion = errors.New("postgres: unsupported server version")
)

// Config is a fully explicit, bounded pool configuration. There are no hidden
// defaults: Validate rejects incomplete settings so a misconfigured pool fails
// before it can open a network connection.
type Config struct {
	// DSN is a PostgreSQL connection string in URL or keyword/value form.
	DSN string
	// MaxConns bounds the number of pooled connections. Must be >= 1.
	MaxConns int32
	// MinConns is the number of idle connections the pool keeps warm.
	// Must satisfy 0 <= MinConns <= MaxConns.
	MinConns int32
	// AcquireTimeout bounds how long Acquire waits for a free connection.
	// Must be positive.
	AcquireTimeout time.Duration
}

// Validate checks the configuration without opening a network connection.
func (c Config) Validate() error {
	if strings.TrimSpace(c.DSN) == "" {
		return fmt.Errorf("%w: dsn is blank", ErrInvalidDSN)
	}
	if c.MaxConns < 1 {
		return fmt.Errorf("%w: MaxConns is %d, want >= 1", ErrInvalidPoolConfig, c.MaxConns)
	}
	if c.MinConns < 0 {
		return fmt.Errorf("%w: MinConns is %d, want >= 0", ErrInvalidPoolConfig, c.MinConns)
	}
	if c.MinConns > c.MaxConns {
		return fmt.Errorf("%w: MinConns is %d, want <= MaxConns (%d)",
			ErrInvalidPoolConfig, c.MinConns, c.MaxConns)
	}
	if c.AcquireTimeout <= 0 {
		return fmt.Errorf("%w: AcquireTimeout is %v, want > 0", ErrInvalidPoolConfig, c.AcquireTimeout)
	}
	return nil
}

// ConnectError wraps any failure to reach or talk to the server. Callers
// classify store-connectivity problems with errors.Is(err, ErrConnect) while
// still inspecting the cause, e.g. errors.Is(err, context.Canceled) for a
// cancelled acquire, errors.Is(err, context.DeadlineExceeded) for an acquire
// timeout, or IsRefusedConnection for a refused TCP connection.
type ConnectError struct {
	// Op is the failing operation: "open", "acquire", or "ping".
	Op string
	// Err is the underlying driver or context error.
	Err error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("postgres: %s: %v", e.Op, e.Err)
}

// Unwrap exposes both the class sentinel and the cause for errors.Is/As.
func (e *ConnectError) Unwrap() []error { return []error{ErrConnect, e.Err} }

// UnsupportedVersionError reports a server whose major version is not
// RequiredMajorVersion. It matches errors.Is(err, ErrUnsupportedVersion).
type UnsupportedVersionError struct {
	// VersionNum is PostgreSQL's server_version_num, e.g. 180006.
	VersionNum int64
	// Version is server_version, e.g. "18.6".
	Version string
	// Major is the derived major version, e.g. 17 for 170014.
	Major int
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("postgres: server version %s (%d) is not supported; this store requires PostgreSQL %d",
		e.Version, e.VersionNum, RequiredMajorVersion)
}

func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }

// CheckServerVersion maps a server_version_num to a readiness verdict. It is
// pure so the version policy is unit-testable without a non-18 server.
func CheckServerVersion(versionNum int64, version string) error {
	major := int(versionNum / 10000)
	if major != RequiredMajorVersion {
		return &UnsupportedVersionError{VersionNum: versionNum, Version: version, Major: major}
	}
	return nil
}

// Client is a bounded, readiness-checked PostgreSQL 18 connection pool.
type Client struct {
	pool *pgxpool.Pool
	cfg  Config
}

// Open validates cfg, builds the pool, and performs one readiness round trip
// (Ping). It fails fast: invalid settings return ErrInvalidDSN or
// ErrInvalidPoolConfig before any connection attempt, and connectivity or
// version failures return a classifiable error (ConnectError or
// UnsupportedVersionError). A failed Open never leaves a half-initialized
// pool behind; callers must treat the error as terminal, not as a cue to fall
// back to another backend.
func Open(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDSN, err)
	}
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, &ConnectError{Op: "open", Err: err}
	}
	c := &Client{pool: pool, cfg: cfg}
	if err := c.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return c, nil
}

// Ping confirms server availability and that the server runs PostgreSQL 18.
// An unreachable server returns a *ConnectError matching ErrConnect (chain
// with errors.Is for syscall.ECONNREFUSED or context errors); a server on the
// wrong major version returns an *UnsupportedVersionError matching
// ErrUnsupportedVersion.
func (c *Client) Ping(ctx context.Context) error {
	conn, err := c.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var (
		versionNum int64
		version    string
	)
	err = conn.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8, current_setting('server_version')",
	).Scan(&versionNum, &version)
	if err != nil {
		return &ConnectError{Op: "ping", Err: err}
	}
	return CheckServerVersion(versionNum, version)
}

// Acquire takes a connection from the pool, waiting at most AcquireTimeout or
// until ctx is done, whichever comes first. On failure the returned error is a
// *ConnectError matching ErrConnect and wraps the cause, so callers can
// classify an acquire timeout with errors.Is(err, context.DeadlineExceeded)
// and cancellation with errors.Is(err, context.Canceled).
//
// The caller must Release the returned connection.
func (c *Client) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.AcquireTimeout)
	defer cancel()
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, &ConnectError{Op: "acquire", Err: err}
	}
	return conn, nil
}

// Close releases every connection held by the pool. The Client must not be
// used afterwards. Close is safe to call more than once.
func (c *Client) Close() {
	if c.pool != nil {
		c.pool.Close()
	}
}

// IsRefusedConnection reports whether err ultimately comes from the operating
// system refusing the TCP connection (nothing listening on the address).
func IsRefusedConnection(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			return sysErr.Err == syscall.ECONNREFUSED
		}
		return strings.Contains(strings.ToLower(opErr.Err.Error()), "connection refused")
	}
	return false
}
