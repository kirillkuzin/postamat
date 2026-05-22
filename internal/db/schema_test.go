package db

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationDefinesCoreTables(t *testing.T) {
	schema := readInitialMigration(t)
	for _, table := range []string{"agents", "agent_devices", "api_tokens", "transfers", "transfer_events"} {
		if !strings.Contains(schema, "CREATE TABLE "+table) {
			t.Fatalf("expected initial migration to create table %s", table)
		}
	}
}

func TestInitialMigrationDefinesImportantConstraintsAndIndexes(t *testing.T) {
	schema := readInitialMigration(t)
	checks := []string{
		"target IN ('agent', 'browser_link')",
		"status IN ('created', 'offered', 'accepted', 'connecting', 'transferring', 'interrupted', 'retryable', 'completed', 'failed', 'cancelled', 'expired')",
		"role IN ('sender_agent', 'receiving_agent', 'browser_recipient')",
		"REFERENCES agents(id)",
		"REFERENCES transfers(id)",
		"CREATE INDEX idx_transfers_status_created_at",
		"CREATE INDEX idx_transfer_events_transfer_id_created_at",
		"CREATE UNIQUE INDEX idx_transfers_public_token_hash_unique",
		"event_type IN ('transfer.created'",
		"agent_ticket_hash TEXT NOT NULL UNIQUE",
		"receiver_ticket_hash TEXT",
		"receiver_ticket_expires_at TIMESTAMPTZ",
		"interrupted_at TIMESTAMPTZ",
		"raw_token",
	}
	for _, check := range checks {
		if strings.Contains(schema, check) == (check == "raw_token") {
			t.Fatalf("schema constraint/index check failed for %q", check)
		}
	}
}

func readInitialMigration(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile("../../migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(content)
}
