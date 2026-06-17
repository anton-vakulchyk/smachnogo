package store

import (
	"context"
	"errors"
	"testing"
)

// TestConsumeZeroCapDeniesWithoutWrite pins the zero-cap guard. A zero cap must
// be rejected with ErrQuotaExceeded BEFORE any UpdateItem — otherwise the DDB
// condition's attribute_not_exists(#k) arm short-circuits TRUE on the day's
// first request and ADD seeds #k=1, leaking one scan/estimate. The Store here
// carries a nil DynamoDB client: the guard returning first proves nothing is
// written, since reaching the UpdateItem would dereference the nil client and
// panic.
func TestConsumeZeroCapDeniesWithoutWrite(t *testing.T) {
	s := &Store{table: "smachnogo-test"} // db is nil on purpose

	for _, kind := range []QuotaKind{QuotaScans, QuotaEstimates} {
		for _, cap := range []int{0, -1} {
			err := s.Consume(context.Background(), "u1", "2026-06-15", kind, cap, 1_700_000_000)
			if !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("Consume(kind=%s, cap=%d) = %v, want ErrQuotaExceeded (and no write)", kind, cap, err)
			}
		}
	}
}
