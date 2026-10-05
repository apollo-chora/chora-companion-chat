package boot

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"google.golang.org/adk/session"
)

func goodShape() [][]driver.Value {
	var rows [][]driver.Value
	for _, t := range sessionTables {
		rows = append(rows, []driver.Value{t, true, true, true, int64(1)})
	}
	return rows
}

func TestSessionStore_assertShapeAndProbe(t *testing.T) {
	pg := &fakePG{shapeRows: goodShape()}
	store := openFakeStore(t, "fakepg-shape-ok", pg)
	if err := store.AssertShape(context.Background()); err != nil {
		t.Fatalf("migrated shape must pass: %v", err)
	}
	if err := store.ProbeGUC(context.Background()); err != nil {
		t.Fatalf("GUC probe must round-trip: %v", err)
	}
	if len(pg.setCalls) != 1 || pg.resets != 1 {
		t.Errorf("probe must set once and reset once: sets=%v resets=%d", pg.setCalls, pg.resets)
	}

	// Every defect is named: missing table, missing tenant column, RLS not forced, no policy.
	cases := map[string][][]driver.Value{
		"table missing":       goodShape()[:3],
		"no tenant_id column": {{"sessions", true, true, false, int64(1)}, {"events", true, true, true, int64(1)}, {"app_states", true, true, true, int64(1)}, {"user_states", true, true, true, int64(1)}},
		"need both":           {{"sessions", true, false, true, int64(1)}, {"events", true, true, true, int64(1)}, {"app_states", true, true, true, int64(1)}, {"user_states", true, true, true, int64(1)}},
		"no RLS policy":       {{"sessions", true, true, true, int64(0)}, {"events", true, true, true, int64(1)}, {"app_states", true, true, true, int64(1)}, {"user_states", true, true, true, int64(1)}},
	}
	i := 0
	for want, rows := range cases {
		i++
		bad := openFakeStore(t, "fakepg-shape-bad-"+strings.ReplaceAll(want, " ", "-")+string(rune('a'+i)), &fakePG{shapeRows: rows})
		err := bad.AssertShape(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "0058") {
			t.Errorf("%s: want a named defect mentioning migration 0058, got %v", want, err)
		}
	}
	broken := openFakeStore(t, "fakepg-shape-err", &fakePG{failShape: errors.New("permission denied")})
	if err := broken.AssertShape(context.Background()); err == nil {
		t.Errorf("a failing catalog read must fail the assertion")
	}
}

func TestSessionStore_everyCallIsTenantScopedAndReleased(t *testing.T) {
	pg := &fakePG{shapeRows: goodShape()}
	store := openFakeStore(t, "fakepg-calls", pg)
	// Bind the library to an in-memory service for the test so the tenant
	// connection sequence is what is under test, not gorm.
	old := newBoundSessionService
	mem := session.InMemoryService()
	newBoundSessionService = func(connPool) (session.Service, error) { return mem, nil }
	t.Cleanup(func() { newBoundSessionService = old })

	ctx := context.Background()
	const user = "11111111-1111-7111-8111-111111111111:g1"
	if _, err := store.Create(ctx, &session.CreateRequest{AppName: "companion_chat", UserID: user, SessionID: "c1", State: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, &session.GetRequest{AppName: "companion_chat", UserID: user, SessionID: "c1"})
	if err != nil || got == nil || got.Session == nil {
		t.Fatalf("get: %v", err)
	}
	ev := session.NewEvent("inv-1")
	ev.Author = "companion_chat"
	if err := store.AppendEvent(ctx, got.Session, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(ctx, &session.ListRequest{AppName: "companion_chat", UserID: user}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, &session.DeleteRequest{AppName: "companion_chat", UserID: user, SessionID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if len(pg.setCalls) != 5 || pg.resets != 5 {
		t.Errorf("five calls must set and reset the GUC five times: sets=%d resets=%d", len(pg.setCalls), pg.resets)
	}
	for _, s := range pg.setCalls {
		if s != "11111111-1111-7111-8111-111111111111" {
			t.Errorf("GUC set to %q, want the tenant parsed from the user id", s)
		}
	}
	// A user id without a UUID tenant never reaches the database.
	if _, err := store.Create(ctx, &session.CreateRequest{AppName: "companion_chat", UserID: "nope:g1", SessionID: "c2"}); err == nil {
		t.Errorf("non-UUID tenant must be refused")
	}
	if len(pg.setCalls) != 5 {
		t.Errorf("a refused call must not touch the connection")
	}
	if _, err := store.Get(ctx, nil); err == nil {
		t.Errorf("nil request must be refused")
	}
	if err := store.AppendEvent(ctx, nil, ev); err == nil {
		t.Errorf("nil session must be refused")
	}
}
