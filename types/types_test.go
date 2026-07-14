// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"testing"
)

func TestResult_IncrementFailedCount(t *testing.T) {
	t.Parallel()

	r := &Result{}
	r.IncrementFailedCount()
	r.IncrementFailedCount()

	if r.FailedCount != 2 {
		t.Errorf("FailedCount = %d, want 2", r.FailedCount)
	}
}

func TestResult_IncrementFailedCount_nil(t *testing.T) {
	t.Parallel()

	var r *Result
	r.IncrementFailedCount() // must not panic
}
