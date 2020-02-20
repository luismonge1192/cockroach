// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.
package storage

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestSSTSnapshotStorage(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx := context.TODO()
	testRangeID := roachpb.RangeID(1)
	testSnapUUID := uuid.Must(uuid.FromBytes([]byte("foobar1234567890")))
	testLimiter := rate.NewLimiter(rate.Inf, 0)

	cleanup, eng := newEngine(t)
	defer cleanup()
	defer eng.Close()

	sstSnapshotStorage := NewSSTSnapshotStorage(eng, testLimiter)
	scratch := sstSnapshotStorage.NewScratchSpace(testRangeID, testSnapUUID)

	// Check that the storage lazily creates the directories on first write.
	_, err := os.Stat(scratch.snapDir)
	if !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist", scratch.snapDir)
	}

	f, err := scratch.NewFile(ctx, 0)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, f.Close())
	}()

	// Check that even though the files aren't created, they are still recorded in SSTs().
	require.Equal(t, len(scratch.SSTs()), 1)

	// Check that the storage lazily creates the files on write.
	for _, fileName := range scratch.SSTs() {
		_, err := os.Stat(fileName)
		if !os.IsNotExist(err) {
			t.Fatalf("expected %s to not exist", fileName)
		}
	}

	_, err = f.Write([]byte("foo"))
	require.NoError(t, err)

	// After writing to files, check that they have been flushed to disk.
	for _, fileName := range scratch.SSTs() {
		require.FileExists(t, fileName)
		data, err := ioutil.ReadFile(fileName)
		require.NoError(t, err)
		require.Equal(t, data, []byte("foo"))
	}

	// Check that closing is idempotent.
	require.NoError(t, f.Close())
	require.NoError(t, f.Close())

	// Check that writing to a closed file is an error.
	_, err = f.Write([]byte("foo"))
	require.EqualError(t, err, "file has already been closed")

	// Check that closing an empty file is an error.
	f, err = scratch.NewFile(ctx, 0)
	require.NoError(t, err)
	require.EqualError(t, f.Close(), "file is empty")
	_, err = f.Write([]byte("foo"))
	require.NoError(t, err)

	// Check that Clear removes the directory.
	require.NoError(t, scratch.Clear())
	_, err = os.Stat(scratch.snapDir)
	if !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist", scratch.snapDir)
	}
	require.NoError(t, sstSnapshotStorage.Clear())
	_, err = os.Stat(sstSnapshotStorage.dir)
	if !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist", sstSnapshotStorage.dir)
	}
}
