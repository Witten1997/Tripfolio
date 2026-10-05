package financepg

import (
	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/modules/finance"
)

func SyncCategory(row dbgen.ExpenseCategory) finance.CategoryResource { return toResource(row) }
func SyncLedger(row dbgen.LedgerEntry, currency string, attachments []uuid.UUID, splits []finance.LedgerSplit) (finance.LedgerResource, error) {
	return toLedgerResource(row, currency, attachments, splits)
}
