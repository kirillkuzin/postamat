package db

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
	"github.com/kirillkuzin/postamat/internal/audit"
)

type auditDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type AuditRepository struct {
	db auditDB
}

func NewAuditRepository(db auditDB) *AuditRepository {
	return &AuditRepository{db: db}
}

func (r *AuditRepository) Save(ctx context.Context, event audit.TransferEvent) (audit.TransferEvent, error) {
	if err := r.db.QueryRow(ctx, insertAuditEventSQL, event.TransferID, event.EventType, nullIfEmpty(event.ActorAgentID), event.Role, []byte(event.RedactedPayload), event.CreatedAt).Scan(&event.ID); err != nil {
		return audit.TransferEvent{}, err
	}
	return event, nil
}

func (r *AuditRepository) ListByTransfer(ctx context.Context, transferID string) ([]audit.TransferEvent, error) {
	rows, err := r.db.Query(ctx, selectAuditEventSQL+" WHERE transfer_id = $1 ORDER BY created_at ASC", transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []audit.TransferEvent
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

const auditEventColumns = "id, transfer_id, event_type, actor_agent_id, role, redacted_payload, created_at"
const selectAuditEventSQL = "SELECT " + auditEventColumns + " FROM transfer_events"
const insertAuditEventSQL = `INSERT INTO transfer_events (transfer_id, event_type, actor_agent_id, role, redacted_payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id`

func scanAuditEvent(row pgx.Row) (audit.TransferEvent, error) {
	var event audit.TransferEvent
	var actorAgentID sql.NullString
	if err := row.Scan(&event.ID, &event.TransferID, &event.EventType, &actorAgentID, &event.Role, &event.RedactedPayload, &event.CreatedAt); err != nil {
		return audit.TransferEvent{}, err
	}
	event.ActorAgentID = stringFromNull(actorAgentID)
	return event, nil
}
