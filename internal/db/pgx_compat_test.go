package db

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoriesAcceptPGXConnectionTypes(t *testing.T) {
	var _ transferDB = (*pgx.Conn)(nil)
	var _ transferDB = (*pgxpool.Pool)(nil)
	var _ auditDB = (*pgx.Conn)(nil)
	var _ auditDB = (*pgxpool.Pool)(nil)
}
