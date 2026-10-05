package boot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"google.golang.org/adk/session"
	"google.golang.org/adk/session/database"

	"github.com/apollo-chora/chora-common/secrets"
)

// SecretReader resolves a secret name to its value.
type SecretReader interface {
	GetSecret(ctx context.Context, name string) (string, error)
}

// ResolveSessionsDSN reads the DSN from the env-backed secrets resolver and
// pins the dedicated schema on the connection (search_path), so the ADK tables
// live in companion_chat_sessions and nowhere else. The DSN value is never logged.
func ResolveSessionsDSN(ctx context.Context, reader SecretReader, secretID, schema string) (string, error) {
	raw, err := reader.GetSecret(ctx, secretID)
	if err != nil {
		return "", fmt.Errorf("companion_chat: sessions DSN secret %q: %w", secretID, err)
	}
	return WithSearchPath(strings.TrimSpace(raw), schema)
}

// WithSearchPath appends search_path=<schema> to a postgres:// DSN (pgx passes
// unknown query keys as runtime parameters). A DSN that already pins a
// search_path is refused rather than silently overridden.
func WithSearchPath(dsn, schema string) (string, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return "", fmt.Errorf("companion_chat: sessions schema is empty")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("companion_chat: sessions DSN is not a postgres:// URL")
	}
	q := u.Query()
	if q.Get("search_path") != "" {
		return "", fmt.Errorf("companion_chat: sessions DSN already pins search_path=%s; refusing to override", q.Get("search_path"))
	}
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ---------------------------------------------------------------------------
// Tenant-scoped session store (owner ruling 2026-08-22, ADR-254 D5 note):
// the ADK tables carry tenant_id (DEFAULT from the chora.tenant_id GUC) under
// RLS ENABLED + FORCED; the agent never runs DDL (migration 0058 owns the
// shape) and binds every session call to a dedicated connection on which the
// GUC is set to the turn's tenant, round-tripped, then reset.
// ---------------------------------------------------------------------------

// sessionTables are the library's tables, pre-created by migration 0058.
var sessionTables = []string{"sessions", "events", "app_states", "user_states"}

// tenantGUC is the RLS GUC every policy on this platform filters on.
const tenantGUC = "chora.tenant_id"

// SessionStore is the Postgres-backed, tenant-scoped session.Service.
type SessionStore struct {
	db     *sql.DB
	schema string
}

// OpenSessionStore opens the pool, asserts the migrated shape (four tables in
// the schema, a tenant_id column, RLS enabled AND forced, a policy present)
// and proves the GUC round-trips on a checked-out connection. Anything short
// of that is fatal: an unmigrated or unforced store would silently read as an
// empty, tenant-blind conversation history.
func OpenSessionStore(ctx context.Context, dsn, schema string) (*SessionStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("companion_chat: open sessions pool: %w", err)
	}
	store := &SessionStore{db: db, schema: strings.TrimSpace(schema)}
	if err := store.AssertShape(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.ProbeGUC(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the pool.
func (s *SessionStore) Close() error { return s.db.Close() }

// AssertShape checks migration 0058 landed: every table exists in the schema
// with a tenant_id column, has RLS enabled and forced, and carries at least
// one policy. It is a read of pg_catalog only.
func (s *SessionStore) AssertShape(ctx context.Context) error {
	const q = `SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
	  EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped) AS has_tenant,
	  (SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid) AS policies
	FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1 AND c.relkind = 'r' AND c.relname = ANY(string_to_array($2, ','))`
	rows, err := s.db.QueryContext(ctx, q, s.schema, strings.Join(sessionTables, ","))
	if err != nil {
		return fmt.Errorf("companion_chat: sessions shape check: %w", err)
	}
	defer func() {
		// A Close error can be the only surface a mid-iteration fault reaches;
		// discarding it is how a partial shape check reads as a whole one.
		if err := rows.Close(); err != nil {
			slog.WarnContext(ctx, "companion_chat: sessions shape check: close rows", "err", err)
		}
	}()
	seen := map[string]bool{}
	var problems []string
	for rows.Next() {
		var name string
		var rls, forced, hasTenant bool
		var policies int
		if err := rows.Scan(&name, &rls, &forced, &hasTenant, &policies); err != nil {
			return fmt.Errorf("companion_chat: sessions shape check scan: %w", err)
		}
		seen[name] = true
		switch {
		case !hasTenant:
			problems = append(problems, name+": no tenant_id column")
		case !rls || !forced:
			problems = append(problems, fmt.Sprintf("%s: rls=%v forced=%v (need both)", name, rls, forced))
		case policies == 0:
			problems = append(problems, name+": no RLS policy")
		}
	}
	for _, t := range sessionTables {
		if !seen[t] {
			problems = append(problems, t+": table missing in schema "+s.schema)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("companion_chat: sessions store is not in the migrated shape (apply migration 0058): %s", strings.Join(problems, "; "))
	}
	return nil
}

// ProbeGUC proves the GUC round-trips on a dedicated connection.
func (s *SessionStore) ProbeGUC(ctx context.Context) error {
	probe := uuid.Must(uuid.NewV7())
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("companion_chat: sessions GUC probe: checkout: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.WarnContext(ctx, "companion_chat: sessions GUC probe: release connection", "err", err)
		}
	}()
	if err := setTenantOnConn(ctx, conn, probe.String()); err != nil {
		return fmt.Errorf("companion_chat: sessions GUC probe: %w", err)
	}
	return resetTenantOnConn(ctx, conn)
}

// setTenantOnConn sets the GUC on the connection and reads it back.
func setTenantOnConn(ctx context.Context, conn *sql.Conn, tenant string) error {
	if _, err := conn.ExecContext(ctx, "SELECT set_config($1, $2, false)", tenantGUC, tenant); err != nil {
		return fmt.Errorf("set %s: %w", tenantGUC, err)
	}
	var got string
	if err := conn.QueryRowContext(ctx, "SELECT current_setting($1, true)", tenantGUC).Scan(&got); err != nil {
		return fmt.Errorf("read back %s: %w", tenantGUC, err)
	}
	if got != tenant {
		return fmt.Errorf("%s read back %q, want %q: the store would be tenant-blind", tenantGUC, got, tenant)
	}
	return nil
}

func resetTenantOnConn(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "RESET "+tenantGUC); err != nil {
		return fmt.Errorf("reset %s: %w", tenantGUC, err)
	}
	return nil
}

// TenantFromUserID parses the tenant out of the fleet's "{tenant}:{gcid}"
// user id and insists it is a UUID (the policy casts the GUC ::uuid, so a
// non-UUID would make every statement fail in a less readable place).
func TenantFromUserID(userID string) (string, error) {
	i := strings.Index(userID, ":")
	if i <= 0 {
		return "", fmt.Errorf("companion_chat: user id %q does not carry a tenant prefix ({tenant}:{gcid})", userID)
	}
	tenant := userID[:i]
	if _, err := uuid.Parse(tenant); err != nil {
		return "", fmt.Errorf("companion_chat: tenant %q in user id is not a UUID: %w", tenant, err)
	}
	return tenant, nil
}

// connPool is the single-connection surface the library is bound to per call.
type connPool = gorm.ConnPool

// newBoundSessionService binds the ADK session service to ONE connection (the
// tenant-scoped one). A package variable so the store's sequencing is testable
// against a fake driver without gorm.
var newBoundSessionService = func(conn connPool) (session.Service, error) {
	return database.NewSessionService(postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// withTenantConn checks out a dedicated connection, sets + verifies the GUC,
// builds a session service bound to that single connection, runs fn, then
// resets the GUC and returns the connection to the pool.
func (s *SessionStore) withTenantConn(ctx context.Context, tenant string, fn func(session.Service) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("companion_chat: sessions checkout: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.WarnContext(ctx, "companion_chat: sessions: release connection", "err", err)
		}
	}()
	if err := setTenantOnConn(ctx, conn, tenant); err != nil {
		return fmt.Errorf("companion_chat: sessions tenant scope: %w", err)
	}
	defer func() { _ = resetTenantOnConn(context.WithoutCancel(ctx), conn) }()
	svc, err := newBoundSessionService(conn)
	if err != nil {
		return fmt.Errorf("companion_chat: sessions service on tenant connection: %w", err)
	}
	return fn(svc)
}

// Create implements session.Service.
func (s *SessionStore) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req == nil {
		return nil, errors.New("companion_chat: sessions Create: nil request")
	}
	tenant, err := TenantFromUserID(req.UserID)
	if err != nil {
		return nil, err
	}
	var out *session.CreateResponse
	err = s.withTenantConn(ctx, tenant, func(svc session.Service) error {
		r, err := svc.Create(ctx, req)
		out = r
		return err
	})
	return out, err
}

// Get implements session.Service.
func (s *SessionStore) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req == nil {
		return nil, errors.New("companion_chat: sessions Get: nil request")
	}
	tenant, err := TenantFromUserID(req.UserID)
	if err != nil {
		return nil, err
	}
	var out *session.GetResponse
	err = s.withTenantConn(ctx, tenant, func(svc session.Service) error {
		r, err := svc.Get(ctx, req)
		out = r
		return err
	})
	return out, err
}

// List implements session.Service.
func (s *SessionStore) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	if req == nil {
		return nil, errors.New("companion_chat: sessions List: nil request")
	}
	tenant, err := TenantFromUserID(req.UserID)
	if err != nil {
		return nil, err
	}
	var out *session.ListResponse
	err = s.withTenantConn(ctx, tenant, func(svc session.Service) error {
		r, err := svc.List(ctx, req)
		out = r
		return err
	})
	return out, err
}

// Delete implements session.Service.
func (s *SessionStore) Delete(ctx context.Context, req *session.DeleteRequest) error {
	if req == nil {
		return errors.New("companion_chat: sessions Delete: nil request")
	}
	tenant, err := TenantFromUserID(req.UserID)
	if err != nil {
		return err
	}
	return s.withTenantConn(ctx, tenant, func(svc session.Service) error { return svc.Delete(ctx, req) })
}

// AppendEvent implements session.Service.
func (s *SessionStore) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	if sess == nil {
		return errors.New("companion_chat: sessions AppendEvent: nil session")
	}
	tenant, err := TenantFromUserID(sess.UserID())
	if err != nil {
		return err
	}
	return s.withTenantConn(ctx, tenant, func(svc session.Service) error { return svc.AppendEvent(ctx, sess, ev) })
}

var _ session.Service = (*SessionStore)(nil)

// newSecretReader is the production secrets client (env-backed resolution via
// chora-common/secrets; the project is a label, never a cloud project).
func newSecretReader(ctx context.Context, project string) (SecretReader, func() error, error) {
	c, err := secrets.NewClient(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	return c, c.Close, nil
}
