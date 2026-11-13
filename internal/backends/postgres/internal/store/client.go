package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const RequiredMajorVersion = 18

var (
	ErrInvalidDSN         = errors.New("postgres: invalid dsn")
	ErrInvalidPoolConfig  = errors.New("postgres: invalid pool config")
	ErrConnect            = errors.New("postgres: connect failed")
	ErrUnsupportedVersion = errors.New("postgres: unsupported server version")
)

type Config struct {
	DSN            string
	MaxConns       int32
	MinConns       int32
	AcquireTimeout time.Duration
}

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

type ConnectError struct {
	Op  string
	Err error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("postgres: %s: %v", e.Op, e.Err)
}

func (e *ConnectError) Unwrap() []error { return []error{ErrConnect, e.Err} }

type UnsupportedVersionError struct {
	VersionNum int64
	Version    string
	Major      int
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("postgres: server version %s (%d) is not supported; this store requires PostgreSQL %d",
		e.Version, e.VersionNum, RequiredMajorVersion)
}

func (e *UnsupportedVersionError) Unwrap() error { return ErrUnsupportedVersion }

func CheckServerVersion(versionNum int64, version string) error {
	major := int(versionNum / 10000)
	if major != RequiredMajorVersion {
		return &UnsupportedVersionError{VersionNum: versionNum, Version: version, Major: major}
	}
	return nil
}

type Client struct {
	pool *pgxpool.Pool
	cfg  Config
}

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

func (c *Client) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.AcquireTimeout)
	defer cancel()
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, &ConnectError{Op: "acquire", Err: err}
	}
	return conn, nil
}

func (c *Client) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := c.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		conn.Release()
		return nil, &ConnectError{Op: "query", Err: err}
	}
	return &leasedRows{Rows: rows, conn: conn}, nil
}

func (c *Client) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := c.Acquire(ctx)
	if err != nil {
		return &queryRowError{err: &ConnectError{Op: "query_row", Err: err}}
	}
	return &leasedRow{Row: conn.QueryRow(ctx, sql, args...), conn: conn}
}

type leasedRows struct {
	pgx.Rows
	conn   *pgxpool.Conn
	closed sync.Once
}

func (l *leasedRows) Close() {
	l.closed.Do(func() {
		l.Rows.Close()
		l.conn.Release()
	})
}

type leasedRow struct {
	pgx.Row
	conn    *pgxpool.Conn
	scanned sync.Once
}

func (r *leasedRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	r.scanned.Do(func() { r.conn.Release() })
	return err
}

type queryRowError struct{ err error }

func (r *queryRowError) Scan(_ ...any) error { return r.err }

func (c *Client) Close() {
	if c.pool != nil {
		c.pool.Close()
	}
}

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
