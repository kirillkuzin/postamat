package db

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kirillkuzin/postamat/internal/audit"
)

func TestAuditRepositorySavePersistsRedactedEvent(t *testing.T) {
	db := &fakeAuditDB{row: fakeAuditRow{values: []any{int64(42)}}}
	repo := NewAuditRepository(db)
	event := sampleAuditEvent()

	saved, err := repo.Save(context.Background(), event)
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if saved.ID != 42 {
		t.Fatalf("saved ID = %d, want 42", saved.ID)
	}
	if !containsAll(db.queryRowSQL, "INSERT INTO transfer_events", "RETURNING id") {
		t.Fatalf("Save SQL did not insert transfer event: %s", db.queryRowSQL)
	}
	if got, want := db.queryRowArgs[0], event.TransferID; got != want {
		t.Fatalf("transfer id arg = %v, want %v", got, want)
	}
	if strings.Contains(string(db.queryRowArgs[4].([]byte)), "raw-secret") {
		t.Fatalf("repository attempted to persist unredacted payload: %s", string(db.queryRowArgs[4].([]byte)))
	}
}

func TestAuditRepositorySaveMapsEmptyActorToNull(t *testing.T) {
	db := &fakeAuditDB{row: fakeAuditRow{values: []any{int64(42)}}}
	repo := NewAuditRepository(db)
	event := sampleAuditEvent()
	event.ActorAgentID = ""

	if _, err := repo.Save(context.Background(), event); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if db.queryRowArgs[2] != nil {
		t.Fatalf("actor arg = %#v, want SQL NULL", db.queryRowArgs[2])
	}
}

func TestAuditRepositoryListByTransferOrdersByCreationTime(t *testing.T) {
	first := sampleAuditEvent()
	first.ID = 1
	second := sampleAuditEvent()
	second.ID = 2
	second.EventType = audit.EventTransferCompleted
	second.ActorAgentID = ""
	second.CreatedAt = first.CreatedAt.Add(time.Minute)
	db := &fakeAuditDB{rows: &fakeAuditRows{rows: [][]any{auditRowValues(first), auditRowValues(second)}}}
	repo := NewAuditRepository(db)

	got, err := repo.ListByTransfer(context.Background(), "tr_123")
	if err != nil {
		t.Fatalf("ListByTransfer returned error: %v", err)
	}
	if !containsAll(db.querySQL, "FROM transfer_events", "WHERE transfer_id = $1", "ORDER BY created_at ASC") {
		t.Fatalf("ListByTransfer SQL should filter by transfer and order ascending: %s", db.querySQL)
	}
	if !reflect.DeepEqual(got, []audit.TransferEvent{first, second}) {
		t.Fatalf("events mismatch:\n got: %#v\nwant: %#v", got, []audit.TransferEvent{first, second})
	}
}

func sampleAuditEvent() audit.TransferEvent {
	event, err := audit.NewTransferEvent(audit.TransferEventInput{
		TransferID:   "tr_123",
		EventType:    audit.EventTransferCreated,
		ActorAgentID: "agent-a",
		Role:         audit.RoleSenderAgent,
		Payload:      map[string]any{"file_name": "report.pdf", "token": "raw-secret"},
		Now:          time.Date(2026, 5, 20, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		panic(err)
	}
	return event
}

func auditRowValues(event audit.TransferEvent) []any {
	return []any{event.ID, event.TransferID, event.EventType, sqlString(event.ActorAgentID), event.Role, event.RedactedPayload, event.CreatedAt}
}

type fakeAuditDB struct {
	queryRowSQL  string
	queryRowArgs []any
	querySQL     string
	row          fakeAuditRow
	rows         *fakeAuditRows
}

func (f *fakeAuditDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.queryRowSQL = sql
	f.queryRowArgs = args
	return f.row
}

func (f *fakeAuditDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.querySQL = sql
	return f.rows, nil
}

type fakeAuditRow struct {
	values []any
	err    error
}

func (r fakeAuditRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		assign(dest[i], r.values[i])
	}
	return nil
}

type fakeAuditRows struct {
	rows [][]any
	idx  int
}

func (r *fakeAuditRows) Next() bool {
	r.idx++
	return r.idx <= len(r.rows)
}
func (r *fakeAuditRows) Scan(dest ...any) error {
	for i := range dest {
		assign(dest[i], r.rows[r.idx-1][i])
	}
	return nil
}
func (r *fakeAuditRows) Close()                                       {}
func (r *fakeAuditRows) Err() error                                   { return nil }
func (r *fakeAuditRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeAuditRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeAuditRows) Values() ([]any, error)                       { return r.rows[r.idx-1], nil }
func (r *fakeAuditRows) RawValues() [][]byte                          { return nil }
func (r *fakeAuditRows) Conn() *pgx.Conn                              { return nil }
