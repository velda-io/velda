// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	dbtest "velda.io/velda/pkg/db/tests"
)

func TestSqliteDatabase(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:" // Use in-memory database for testing
	db, err := NewSqliteDatabase(dbPath)
	db.db.SetMaxOpenConns(1) // memory db only supports one connection
	assert.NoError(t, err, "Failed to create database")
	defer db.Close()

	ctx := context.Background()
	dbtest.RunTestTaskWithDb(t, db, 1)

	// Test leaser operations
	t.Run("LeaserOperations", func(t *testing.T) {
		now := time.Now()
		err := db.RenewLeaser(ctx, "test-leaser", now)
		assert.NoError(t, err, "RenewLeaser failed")

		// Test release expired leasers
		pastTime := now.Add(-2 * time.Minute)
		err = db.ReleaseExpiredLeaser(ctx, pastTime)
		assert.NoError(t, err, "ReleaseExpiredLeaser failed")
	})
}
