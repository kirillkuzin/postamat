package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

type transferDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

type TransferRepository struct {
	db transferDB
}

func NewTransferRepository(db transferDB) *TransferRepository {
	return &TransferRepository{db: db}
}

func (r *TransferRepository) Save(ctx context.Context, session sessions.TransferSession) error {
	_, err := r.db.Exec(ctx, upsertTransferSQL, transferArgs(session)...)
	return err
}

func (r *TransferRepository) Get(ctx context.Context, id string) (sessions.TransferSession, error) {
	return scanTransfer(r.db.QueryRow(ctx, selectTransferSQL+" WHERE id = $1", id))
}

func (r *TransferRepository) ListActive(ctx context.Context) ([]sessions.TransferSession, error) {
	rows, err := r.db.Query(ctx, selectTransferSQL+" WHERE status NOT IN ('completed', 'failed', 'cancelled', 'expired') ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transfers []sessions.TransferSession
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return transfers, nil
}

func (r *TransferRepository) Update(ctx context.Context, id string, update func(*sessions.TransferSession) error) (sessions.TransferSession, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return sessions.TransferSession{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	transfer, err := scanTransfer(tx.QueryRow(ctx, selectTransferSQL+" WHERE id = $1 FOR UPDATE", id))
	if err != nil {
		return sessions.TransferSession{}, err
	}
	if err := update(&transfer); err != nil {
		return sessions.TransferSession{}, err
	}
	if _, err := tx.Exec(ctx, upsertTransferSQL, transferArgs(transfer)...); err != nil {
		return sessions.TransferSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return sessions.TransferSession{}, err
	}
	committed = true
	return transfer, nil
}

const transferColumns = "id, transport, target, status, from_agent_id, to_agent_id, public_token_hash, agent_ticket_hash, receiver_ticket_hash, receiver_ticket_expires_at, file_name, file_size_bytes, file_sha256, mime_type, expires_at, max_downloads, download_count, password_hash, created_at, completed_at, failed_at, cancelled_at, expired_at, failure_reason"

const selectTransferSQL = "SELECT " + transferColumns + " FROM transfers"

const upsertTransferSQL = `INSERT INTO transfers (` + transferColumns + `)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
ON CONFLICT (id) DO UPDATE SET
    transport = EXCLUDED.transport,
    target = EXCLUDED.target,
    status = EXCLUDED.status,
    from_agent_id = EXCLUDED.from_agent_id,
    to_agent_id = EXCLUDED.to_agent_id,
    public_token_hash = EXCLUDED.public_token_hash,
    agent_ticket_hash = EXCLUDED.agent_ticket_hash,
    receiver_ticket_hash = EXCLUDED.receiver_ticket_hash,
    receiver_ticket_expires_at = EXCLUDED.receiver_ticket_expires_at,
    file_name = EXCLUDED.file_name,
    file_size_bytes = EXCLUDED.file_size_bytes,
    file_sha256 = EXCLUDED.file_sha256,
    mime_type = EXCLUDED.mime_type,
    expires_at = EXCLUDED.expires_at,
    max_downloads = EXCLUDED.max_downloads,
    download_count = EXCLUDED.download_count,
    password_hash = EXCLUDED.password_hash,
    created_at = EXCLUDED.created_at,
    completed_at = EXCLUDED.completed_at,
    failed_at = EXCLUDED.failed_at,
    cancelled_at = EXCLUDED.cancelled_at,
    expired_at = EXCLUDED.expired_at,
    failure_reason = EXCLUDED.failure_reason`

func transferArgs(s sessions.TransferSession) []any {
	return []any{s.ID, s.Transport, s.Target, s.Status, s.FromAgentID, nullIfEmpty(s.ToAgentID), nullIfEmpty(s.PublicTokenHash), s.AgentTicketHash, nullIfEmpty(s.ReceiverTicketHash), timePtrOrNil(s.ReceiverTicketExpiresAt), s.FileName, s.FileSizeBytes, nullIfEmpty(s.FileSHA256), nullIfEmpty(s.MimeType), s.ExpiresAt, s.MaxDownloads, s.DownloadCount, s.PasswordHash, s.CreatedAt, s.CompletedAt, s.FailedAt, s.CancelledAt, s.ExpiredAt, nullIfEmpty(s.FailureReason)}
}

func scanTransfer(row pgx.Row) (sessions.TransferSession, error) {
	var session sessions.TransferSession
	var toAgentID, publicTokenHash, receiverTicketHash, fileSHA256, mimeType, failureReason sql.NullString
	if err := row.Scan(&session.ID, &session.Transport, &session.Target, &session.Status, &session.FromAgentID, &toAgentID, &publicTokenHash, &session.AgentTicketHash, &receiverTicketHash, &session.ReceiverTicketExpiresAt, &session.FileName, &session.FileSizeBytes, &fileSHA256, &mimeType, &session.ExpiresAt, &session.MaxDownloads, &session.DownloadCount, &session.PasswordHash, &session.CreatedAt, &session.CompletedAt, &session.FailedAt, &session.CancelledAt, &session.ExpiredAt, &failureReason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.TransferSession{}, sessions.ErrSessionNotFound
		}
		return sessions.TransferSession{}, err
	}
	session.ToAgentID = stringFromNull(toAgentID)
	session.PublicTokenHash = stringFromNull(publicTokenHash)
	session.ReceiverTicketHash = stringFromNull(receiverTicketHash)
	session.FileSHA256 = stringFromNull(fileSHA256)
	session.MimeType = stringFromNull(mimeType)
	session.FailureReason = stringFromNull(failureReason)
	return session, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timePtrOrNil(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value
}

func stringFromNull(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
