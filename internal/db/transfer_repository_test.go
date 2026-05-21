package db

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestTransferRepositorySaveInsertsAllPersistedFields(t *testing.T) {
	db := &fakeTransferDB{}
	repo := NewTransferRepository(db)
	session := sampleTransferSession()

	if err := repo.Save(context.Background(), session); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if db.execSQL == "" || !containsAll(db.execSQL, "INSERT INTO transfers", "ON CONFLICT (id) DO UPDATE") {
		t.Fatalf("Save SQL did not upsert transfers: %s", db.execSQL)
	}
	if got, want := db.execArgs[0], session.ID; got != want {
		t.Fatalf("first arg = %v, want transfer id %v", got, want)
	}
	if got, want := db.execArgs[15], session.MaxDownloads; got != want {
		t.Fatalf("max_downloads arg = %v, want %v", got, want)
	}
}

func TestTransferRepositorySaveMapsOptionalEmptyFieldsToNull(t *testing.T) {
	db := &fakeTransferDB{}
	repo := NewTransferRepository(db)
	session := sampleTransferSession()
	session.Target = sessions.TargetBrowserLink
	session.ToAgentID = ""
	session.PublicTokenHash = "public_hash"
	session.ReceiverTicketHash = ""
	session.ReceiverTicketExpiresAt = nil
	session.FileSHA256 = ""
	session.MimeType = ""
	session.FailureReason = ""

	if err := repo.Save(context.Background(), session); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	for _, idx := range []int{5, 8, 9, 12, 13, 23} {
		if db.execArgs[idx] != nil {
			t.Fatalf("arg %d = %#v, want SQL NULL", idx, db.execArgs[idx])
		}
	}
}

func TestTransferRepositoryGetReturnsSessionAndMapsMissingRows(t *testing.T) {
	session := sampleTransferSession()
	db := &fakeTransferDB{row: fakeRow{values: sessionRowValues(session)}}
	repo := NewTransferRepository(db)

	got, err := repo.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !reflect.DeepEqual(got, session) {
		t.Fatalf("Get session mismatch:\n got: %#v\nwant: %#v", got, session)
	}

	db.row.err = pgx.ErrNoRows
	_, err = repo.Get(context.Background(), "missing")
	if err != sessions.ErrSessionNotFound {
		t.Fatalf("missing Get error = %v, want %v", err, sessions.ErrSessionNotFound)
	}
}

func TestTransferRepositoryListActiveOnlyQueriesNonTerminalTransfers(t *testing.T) {
	first := sampleTransferSession()
	second := sampleTransferSession()
	second.ID = "tr_second"
	db := &fakeTransferDB{rows: &fakeRows{rows: [][]any{sessionRowValues(first), sessionRowValues(second)}}}
	repo := NewTransferRepository(db)

	got, err := repo.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if !containsAll(db.querySQL, "WHERE status NOT IN", sessions.StatusCompleted, sessions.StatusFailed, sessions.StatusCancelled, sessions.StatusExpired, "ORDER BY created_at ASC") {
		t.Fatalf("ListActive SQL does not filter active transfers: %s", db.querySQL)
	}
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("unexpected active transfers: %#v", got)
	}
}

func TestTransferRepositoryUpdateLocksRowAndPersistsMutation(t *testing.T) {
	session := sampleTransferSession()
	db := &fakeTransferDB{row: fakeRow{values: sessionRowValues(session)}}
	repo := NewTransferRepository(db)

	updated, err := repo.Update(context.Background(), session.ID, func(s *sessions.TransferSession) error {
		return s.Cancel(session.CreatedAt.Add(time.Minute))
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Status != sessions.StatusCancelled {
		t.Fatalf("updated status = %q", updated.Status)
	}
	if !containsAll(db.tx.querySQL, "SELECT", "FROM transfers", "FOR UPDATE") {
		t.Fatalf("Update did not lock selected row: %s", db.tx.querySQL)
	}
	if !containsAll(db.tx.execSQL, "INSERT INTO transfers", "ON CONFLICT (id) DO UPDATE") {
		t.Fatalf("Update did not persist mutation in transaction: %s", db.tx.execSQL)
	}
	if !db.tx.committed || db.tx.rolledBack {
		t.Fatalf("Update transaction state committed=%v rolledBack=%v", db.tx.committed, db.tx.rolledBack)
	}
}

func sampleTransferSession() sessions.TransferSession {
	completedAt := time.Date(2026, 5, 20, 12, 10, 0, 0, time.UTC)
	failedAt := time.Date(2026, 5, 20, 12, 20, 0, 0, time.UTC)
	cancelledAt := time.Date(2026, 5, 20, 12, 30, 0, 0, time.UTC)
	expiredAt := time.Date(2026, 5, 20, 12, 40, 0, 0, time.UTC)
	receiverTicketExpiresAt := time.Date(2026, 5, 20, 12, 5, 0, 0, time.UTC)
	passwordHash := "pwd_hash"
	return sessions.TransferSession{
		ID:                      "tr_123",
		Transport:               sessions.TransportWebRTCP2P,
		Target:                  sessions.TargetAgent,
		Status:                  sessions.StatusCreated,
		FromAgentID:             "agent-a",
		ToAgentID:               "agent-b",
		PublicTokenHash:         "public_hash",
		AgentTicketHash:         "agent_ticket_hash",
		ReceiverTicketHash:      "receiver_ticket_hash",
		ReceiverTicketExpiresAt: &receiverTicketExpiresAt,
		FileName:                "file.txt",
		FileSizeBytes:           42,
		FileSHA256:              "sha256",
		MimeType:                "text/plain",
		ExpiresAt:               time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC),
		MaxDownloads:            1,
		DownloadCount:           0,
		PasswordHash:            &passwordHash,
		CreatedAt:               time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		CompletedAt:             &completedAt,
		FailedAt:                &failedAt,
		CancelledAt:             &cancelledAt,
		ExpiredAt:               &expiredAt,
		FailureReason:           "reason",
	}
}

func sessionRowValues(s sessions.TransferSession) []any {
	return []any{s.ID, s.Transport, s.Target, s.Status, s.FromAgentID, sqlString(s.ToAgentID), sqlString(s.PublicTokenHash), s.AgentTicketHash, sqlString(s.ReceiverTicketHash), s.ReceiverTicketExpiresAt, s.FileName, s.FileSizeBytes, sqlString(s.FileSHA256), sqlString(s.MimeType), s.ExpiresAt, s.MaxDownloads, s.DownloadCount, s.PasswordHash, s.CreatedAt, s.CompletedAt, s.FailedAt, s.CancelledAt, s.ExpiredAt, sqlString(s.FailureReason)}
}

func sqlString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

type fakeTransferDB struct {
	execSQL     string
	execArgs    []any
	queryRowSQL string
	querySQL    string
	row         fakeRow
	rows        *fakeRows
	tx          *fakeTransferTx
}

func (f *fakeTransferDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = sql
	f.execArgs = args
	return pgconn.CommandTag{}, nil
}
func (f *fakeTransferDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.queryRowSQL = sql
	return f.row
}
func (f *fakeTransferDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.querySQL = sql
	return f.rows, nil
}
func (f *fakeTransferDB) Begin(context.Context) (pgx.Tx, error) {
	if f.tx == nil {
		f.tx = &fakeTransferTx{row: f.row}
	}
	return f.tx, nil
}

type fakeTransferTx struct {
	execSQL    string
	execArgs   []any
	querySQL   string
	row        fakeRow
	committed  bool
	rolledBack bool
}

func (f *fakeTransferTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = sql
	f.execArgs = args
	return pgconn.CommandTag{}, nil
}
func (f *fakeTransferTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.querySQL = sql
	return f.row
}
func (f *fakeTransferTx) Commit(context.Context) error {
	f.committed = true
	return nil
}
func (f *fakeTransferTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}
func (f *fakeTransferTx) Begin(context.Context) (pgx.Tx, error) { return f, nil }
func (f *fakeTransferTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeTransferTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeTransferTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeTransferTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeTransferTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeTransferTx) Conn() *pgx.Conn                                         { return nil }

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		assign(dest[i], r.values[i])
	}
	return nil
}

type fakeRows struct {
	rows [][]any
	idx  int
}

func (r *fakeRows) Next() bool { r.idx++; return r.idx <= len(r.rows) }
func (r *fakeRows) Scan(dest ...any) error {
	for i := range dest {
		assign(dest[i], r.rows[r.idx-1][i])
	}
	return nil
}
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return r.rows[r.idx-1], nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func assign(dest any, value any) { reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(value)) }
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
