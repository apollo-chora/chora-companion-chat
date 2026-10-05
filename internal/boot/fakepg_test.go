package boot

// A minimal database/sql driver that answers exactly the statements the
// session store issues, so the tenant-scoping sequence (set_config, read-back,
// RESET on release), the shape assertion and the per-call service binding
// are proven without a Postgres. Anything else it is asked is an error, so
// an unexpected statement fails the test instead of passing silently.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

type fakePG struct {
	mu        sync.Mutex
	conns     int
	setCalls  []string
	resets    int
	shapeRows [][]driver.Value // rows for the pg_catalog shape query
	failShape error
}

func (f *fakePG) Open(string) (driver.Conn, error) {
	f.mu.Lock()
	f.conns++
	f.mu.Unlock()
	return &fakeConn{pg: f}, nil
}

type fakeConn struct {
	pg     *fakePG
	tenant string
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fakepg: Prepare unsupported")
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("fakepg: Begin unsupported") }

func (c *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.HasPrefix(q, "SELECT set_config("):
		if len(args) != 2 || args[0].Value != tenantGUC {
			return nil, fmt.Errorf("fakepg: set_config args %v", args)
		}
		c.tenant = args[1].Value.(string)
		c.pg.mu.Lock()
		c.pg.setCalls = append(c.pg.setCalls, c.tenant)
		c.pg.mu.Unlock()
		return driver.RowsAffected(0), nil
	case q == "RESET "+tenantGUC:
		c.tenant = ""
		c.pg.mu.Lock()
		c.pg.resets++
		c.pg.mu.Unlock()
		return driver.RowsAffected(0), nil
	}
	return nil, fmt.Errorf("fakepg: unexpected exec %q", q)
}

func (c *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.HasPrefix(q, "SELECT current_setting("):
		return &fakeRows{cols: []string{"current_setting"}, rows: [][]driver.Value{{c.tenant}}}, nil
	case strings.Contains(q, "relforcerowsecurity"):
		if c.pg.failShape != nil {
			return nil, c.pg.failShape
		}
		return &fakeRows{cols: []string{"relname", "relrowsecurity", "relforcerowsecurity", "has_tenant", "policies"}, rows: c.pg.shapeRows}, nil
	}
	return nil, fmt.Errorf("fakepg: unexpected query %q", q)
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

var (
	_ driver.ExecerContext  = (*fakeConn)(nil)
	_ driver.QueryerContext = (*fakeConn)(nil)
)

// openFakeStore registers a fresh fake driver and opens a SessionStore on it.
func openFakeStore(t interface{ Fatalf(string, ...any) }, name string, pg *fakePG) *SessionStore {
	sql.Register(name, pg)
	db, err := sql.Open(name, "fake")
	if err != nil {
		t.Fatalf("open fake: %v", err)
	}
	return &SessionStore{db: db, schema: "companion_chat_sessions"}
}
