//go:build integration

package integration

import (
	"testing"
)

// Legacy concurrency test against live MSSQL removed: me book/cancel return FEATURE_DISABLED until LK-2b.
// Future write integration tests must use approved pl_AddVisitContext path on allowlisted test patient/line only.
func TestBookMe_LegacyConcurrencyRemoved(t *testing.T) {
	t.Skip("me appointment write routes disabled until LK-2b; see handler golden FEATURE_DISABLED tests")
}
