// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
)

func TestCheck(t *testing.T) {
	res, err := Check(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Check: %v (partial result %+v)", err, res)
	}
	if !res.FTS5 || !res.BM25 || !res.VacuumInto || res.JournalMode != "wal" {
		t.Errorf("Check result = %+v", res)
	}
	t.Logf("SQLite %s", res.SQLiteVersion)
}
