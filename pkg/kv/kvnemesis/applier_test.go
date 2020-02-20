// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package kvnemesis

import (
	"context"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplier(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx := context.Background()
	tc := testcluster.StartTestCluster(t, 1, base.TestClusterArgs{})
	defer tc.Stopper().Stop(ctx)
	db := tc.Server(0).DB()

	a := MakeApplier(db)
	check := func(t *testing.T, s Step, expected string) {
		t.Helper()
		require.NoError(t, a.Apply(ctx, &s))
		assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(s.String()))
	}
	checkErr := func(t *testing.T, s Step, expected string) {
		t.Helper()
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		require.NoError(t, a.Apply(cancelledCtx, &s))
		assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(s.String()))
	}

	// Basic operations
	check(t, step(get(`a`)), `db.Get(ctx, "a") // (nil, nil)`)

	check(t, step(put(`a`, `1`)), `db.Put(ctx, "a", 1) // nil`)
	check(t, step(get(`a`)), `db.Get(ctx, "a") // ("1", nil)`)

	checkErr(t, step(get(`a`)), `db.Get(ctx, "a") // (nil, aborted in distSender: context canceled)`)
	checkErr(t, step(put(`a`, `1`)), `db.Put(ctx, "a", 1) // aborted in distSender: context canceled`)

	// Batch
	check(t, step(batch(put(`b`, `2`), get(`a`))), `
{
  b := &Batch{}
  b.Put(ctx, "b", 2) // nil
  b.Get(ctx, "a") // ("1", nil)
  db.Run(ctx, b) // nil
}
`)
	checkErr(t, step(batch(put(`b`, `2`), get(`a`))), `
{
  b := &Batch{}
  b.Put(ctx, "b", 2) // aborted in distSender: context canceled
  b.Get(ctx, "a") // (nil, aborted in distSender: context canceled)
  db.Run(ctx, b) // aborted in distSender: context canceled
}
`)

	// Txn commit
	check(t, step(closureTxn(ClosureTxnType_Commit, put(`e`, `5`), batch(put(`f`, `6`)))), `
db.Txn(ctx, func(ctx context.Context, txn *client.Txn) error {
  txn.Put(ctx, "e", 5) // nil
  {
    b := &Batch{}
    b.Put(ctx, "f", 6) // nil
    txn.Run(ctx, b) // nil
  }
  return nil
}) // nil
		`)

	// Txn commit in batch
	check(t, step(closureTxnCommitInBatch(opSlice(get(`a`), put(`f`, `6`)), put(`e`, `5`))), `
db.Txn(ctx, func(ctx context.Context, txn *client.Txn) error {
  txn.Put(ctx, "e", 5) // nil
  b := &Batch{}
  b.Get(ctx, "a") // ("1", nil)
  b.Put(ctx, "f", 6) // nil
  txn.CommitInBatch(ctx, b) // nil
  return nil
}) // nil
		`)

	// Txn rollback
	check(t, step(closureTxn(ClosureTxnType_Rollback, put(`e`, `5`))), `
db.Txn(ctx, func(ctx context.Context, txn *client.Txn) error {
  txn.Put(ctx, "e", 5) // nil
  return errors.New("rollback")
}) // rollback
		`)

	// Txn error
	checkErr(t, step(closureTxn(ClosureTxnType_Rollback, put(`e`, `5`))), `
db.Txn(ctx, func(ctx context.Context, txn *client.Txn) error {
  txn.Put(ctx, "e", 5)
  return errors.New("rollback")
}) // context canceled
		`)

	// Splits and merges
	check(t, step(split(`foo`)), `db.AdminSplit(ctx, "foo") // nil`)
	check(t, step(merge(`foo`)), `db.AdminMerge(ctx, "foo") // nil`)
	checkErr(t, step(split(`foo`)),
		`db.AdminSplit(ctx, "foo") // aborted in distSender: context canceled`)
	checkErr(t, step(merge(`foo`)),
		`db.AdminMerge(ctx, "foo") // aborted in distSender: context canceled`)
}
