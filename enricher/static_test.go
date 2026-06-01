// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package enricher

import (
	"context"
	"testing"
	"time"

	typesv1 "buf.build/gen/go/agntcy/oasf/protocolbuffers/go/agntcy/oasf/types/v1"
	"github.com/agntcy/dir-importer/types"
	corev1 "github.com/agntcy/dir/api/core/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// newStaticEnricherFixture builds a StaticEnricher with arbitrary but
// representative OASF taxonomy values. Behavior tests below don't depend on
// the specific values — they just need *some* skill/domain on every output
// record to verify the enricher's pipeline mechanics (forwarding, context
// cancellation, error handling). Concrete-value semantics are covered
// separately in the TestNewStaticEnricher_* assignment tests.
func newStaticEnricherFixture() *StaticEnricher {
	return NewStaticEnricher(
		[]*typesv1.Skill{{
			Name: "natural_language_processing/natural_language_understanding/contextual_comprehension",
			Id:   10101, //nolint:mnd
		}},
		[]*typesv1.Domain{{
			Name: "technology/software_engineering",
			Id:   102, //nolint:mnd
		}},
	)
}

func TestStaticEnricher_Enrich_HappyPath(t *testing.T) {
	t.Parallel()

	se := newStaticEnricherFixture()

	rec := &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{
		fieldName: structpb.NewStringValue("agent-1"),
	}}}

	in := make(chan *corev1.Record, 1)
	in <- rec

	close(in)

	result := &types.Result{}
	out, errCh := se.Enrich(context.Background(), in, result)

	got, ok := <-out
	if !ok {
		t.Fatal("output channel closed before producing record")
	}

	if got != rec {
		t.Errorf("output record should be the same pointer as input")
	}

	skills := got.GetData().GetFields()["skills"].GetListValue()
	if skills == nil || len(skills.GetValues()) != 1 {
		t.Errorf("skills field not populated: %+v", skills)
	}

	domains := got.GetData().GetFields()["domains"].GetListValue()
	if domains == nil || len(domains.GetValues()) != 1 {
		t.Errorf("domains field not populated: %+v", domains)
	}

	if _, more := <-out; more {
		t.Error("output channel should be closed after single record")
	}

	for err := range errCh {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStaticEnricher_Enrich_PreservesExistingFields(t *testing.T) {
	t.Parallel()

	se := newStaticEnricherFixture()

	rec := &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{
		fieldName: structpb.NewStringValue("preserve-me"),
		"version": structpb.NewStringValue("1.0.0"),
	}}}

	in := make(chan *corev1.Record, 1)
	in <- rec

	close(in)

	result := &types.Result{}
	out, errCh := se.Enrich(context.Background(), in, result)
	drainErrCh(errCh)

	got := <-out
	if got.GetData().GetFields()[fieldName].GetStringValue() != "preserve-me" {
		t.Error("name field was clobbered")
	}

	if got.GetData().GetFields()["version"].GetStringValue() != "1.0.0" {
		t.Error("version field was clobbered")
	}
}

func TestStaticEnricher_Enrich_NilData_Errors(t *testing.T) {
	t.Parallel()

	se := newStaticEnricherFixture()

	in := make(chan *corev1.Record, 1)
	in <- &corev1.Record{Data: nil}

	close(in)

	result := &types.Result{}
	out, errCh := se.Enrich(context.Background(), in, result)

	records, errs := drainBoth(t, out, errCh)
	if len(records) != 0 {
		t.Errorf("expected no records, got %d", len(records))
	}

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}

	if result.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestStaticEnricher_Enrich_NilFields_Errors(t *testing.T) {
	t.Parallel()

	// A non-nil Data with a nil Fields map should be rejected too — without
	// a Fields map the static skills/domains have nowhere to land.
	se := newStaticEnricherFixture()

	in := make(chan *corev1.Record, 1)
	in <- &corev1.Record{Data: &structpb.Struct{Fields: nil}}

	close(in)

	result := &types.Result{}
	out, errCh := se.Enrich(context.Background(), in, result)

	records, errs := drainBoth(t, out, errCh)
	if len(records) != 0 {
		t.Errorf("expected no records, got %d", len(records))
	}

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestStaticEnricher_Enrich_MultipleRecords(t *testing.T) {
	t.Parallel()

	se := newStaticEnricherFixture()

	const n = 5

	in := make(chan *corev1.Record, n)
	for range n {
		in <- &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{}}}
	}

	close(in)

	result := &types.Result{}
	out, errCh := se.Enrich(context.Background(), in, result)
	drainErrCh(errCh)

	var got int

	for rec := range out {
		got++

		if rec.GetData().GetFields()["skills"].GetListValue() == nil {
			t.Errorf("record %d missing skills", got)
		}
	}

	if got != n {
		t.Errorf("got %d records, want %d", got, n)
	}
}

func TestStaticEnricher_Enrich_ContextCancellation(t *testing.T) {
	t.Parallel()

	se := newStaticEnricherFixture()

	in := make(chan *corev1.Record)

	ctx, cancel := context.WithCancel(context.Background())

	out, errCh := se.Enrich(ctx, in, &types.Result{})

	cancel()

	// Both channels must close after cancellation; if the goroutine leaks
	// the test will hang.
	deadline := time.After(time.Second)

	for out != nil || errCh != nil {
		select {
		case _, ok := <-out:
			if !ok {
				out = nil
			}
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
			}
		case <-deadline:
			t.Fatal("Enrich did not return after context cancellation")
		}
	}

	close(in)
}

func TestStaticEnricher_Enrich_StopsOnFirstError(t *testing.T) {
	t.Parallel()

	// Enrich closes its output after the first nil-data error so downstream
	// stages get no further records from a degraded enricher.
	se := newStaticEnricherFixture()

	in := make(chan *corev1.Record, 2)
	in <- &corev1.Record{Data: nil} // bad first

	in <- &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{}}}

	close(in)

	out, errCh := se.Enrich(context.Background(), in, &types.Result{})

	records, errs := drainBoth(t, out, errCh)
	if len(records) != 0 {
		t.Errorf("got %d records, want 0 (Enrich should stop after first error)", len(records))
	}

	if len(errs) != 1 {
		t.Errorf("got %d errors, want 1", len(errs))
	}
}

func TestNewStaticEnricher_AssignsCallerSuppliedEntries(t *testing.T) {
	t.Parallel()

	se := NewStaticEnricher(
		[]*typesv1.Skill{
			{Name: "skill-a", Id: 1},
			{Name: "skill-b", Id: 2},
		},
		[]*typesv1.Domain{
			{Name: testDomainA, Id: 10},
		},
	)

	rec := &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{
		fieldName: structpb.NewStringValue("agent-1"),
	}}}

	in := make(chan *corev1.Record, 1)
	in <- rec

	close(in)

	out, errCh := se.Enrich(context.Background(), in, &types.Result{})
	drainErrCh(errCh)

	got := <-out

	skills := got.GetData().GetFields()["skills"].GetListValue().GetValues()
	if len(skills) != 2 {
		t.Fatalf("skills: got %d entries, want 2", len(skills))
	}

	first := skills[0].GetStructValue().GetFields()
	if first["name"].GetStringValue() != "skill-a" || first["id"].GetNumberValue() != 1 {
		t.Errorf("skills[0] = %+v", first)
	}

	domains := got.GetData().GetFields()["domains"].GetListValue().GetValues()
	if len(domains) != 1 {
		t.Fatalf("domains: got %d entries, want 1", len(domains))
	}

	if domains[0].GetStructValue().GetFields()["name"].GetStringValue() != testDomainA {
		t.Errorf("domains[0] = %+v", domains[0])
	}
}

// Empty input slices must produce an explicit empty list on every record —
// not nil and not "leave the field alone". Downstream consumers rely on
// the field always being present after the enricher stage runs.
func TestNewStaticEnricher_NilInputs_ProduceEmptyLists(t *testing.T) {
	t.Parallel()

	se := NewStaticEnricher(nil, nil)

	rec := &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{
		fieldName: structpb.NewStringValue("agent-1"),
		"skills":  structpb.NewStringValue("stale"),
		"domains": structpb.NewStringValue("stale"),
	}}}

	in := make(chan *corev1.Record, 1)
	in <- rec

	close(in)

	out, errCh := se.Enrich(context.Background(), in, &types.Result{})
	drainErrCh(errCh)

	got := <-out

	skills := got.GetData().GetFields()["skills"].GetListValue()
	if skills == nil {
		t.Fatal("skills field should be a list value, got nil")
	}

	if len(skills.GetValues()) != 0 {
		t.Errorf("skills should be empty, got %d entries", len(skills.GetValues()))
	}

	domains := got.GetData().GetFields()["domains"].GetListValue()
	if domains == nil {
		t.Fatal("domains field should be a list value, got nil")
	}

	if len(domains.GetValues()) != 0 {
		t.Errorf("domains should be empty, got %d entries", len(domains.GetValues()))
	}
}

// Mirrors setStructSkills' omission rules: empty Name and zero Id must not
// be written as struct keys. Keeping the rules identical to the LLM path
// means a record enriched by the static path is indistinguishable on the
// wire from one enriched by the LLM path — both paths route through
// skillsToListValue/domainsToListValue.
func TestNewStaticEnricher_OmitsEmptyNameAndZeroID(t *testing.T) {
	t.Parallel()

	se := NewStaticEnricher(
		[]*typesv1.Skill{
			{Name: "named-only"},
			{Id: 99},
		},
		nil,
	)

	rec := &corev1.Record{Data: &structpb.Struct{Fields: map[string]*structpb.Value{}}}

	in := make(chan *corev1.Record, 1)
	in <- rec

	close(in)

	out, errCh := se.Enrich(context.Background(), in, &types.Result{})
	drainErrCh(errCh)

	got := <-out
	skills := got.GetData().GetFields()["skills"].GetListValue().GetValues()

	first := skills[0].GetStructValue().GetFields()
	if _, ok := first["id"]; ok {
		t.Errorf("name-only entry should omit id key, got %+v", first)
	}

	second := skills[1].GetStructValue().GetFields()
	if _, ok := second["name"]; ok {
		t.Errorf("id-only entry should omit name key, got %+v", second)
	}
}

// drainErrCh consumes any errors emitted on errCh in the background, allowing
// tests to focus assertions on the output channel.
func drainErrCh(errCh <-chan error) {
	go func() {
		for range errCh {
		}
	}()
}

// drainBoth reads concurrently from both channels until they close. Both must
// be drained in parallel: the StaticEnricher uses unbuffered channels and a
// serialized drain deadlocks if the producer has a record and an error to send.
func drainBoth(t *testing.T, out <-chan *corev1.Record, errCh <-chan error) ([]*corev1.Record, []error) {
	t.Helper()

	type pair struct {
		rec *corev1.Record
		err error
	}

	results := make(chan pair, 32)

	go func() {
		for r := range out {
			results <- pair{rec: r}
		}
	}()

	go func() {
		for e := range errCh {
			results <- pair{err: e}
		}
	}()

	// Wait for both producers by polling on a short ticker; this is simpler
	// than building an explicit waitgroup channel and adequate for tests.
	deadline := time.After(5 * time.Second)

	var (
		records []*corev1.Record
		errs    []error
		quiet   int // counts consecutive empty polls
	)

	for {
		select {
		case p := <-results:
			if p.rec != nil {
				records = append(records, p.rec)
			}

			if p.err != nil {
				errs = append(errs, p.err)
			}

			quiet = 0
		case <-time.After(50 * time.Millisecond):
			quiet++
			// Both producers should have exited well within ~150ms after
			// drainBoth observes silence; reading from a closed input
			// channel is essentially instantaneous for these tests.
			if quiet >= 3 {
				return records, errs
			}
		case <-deadline:
			t.Fatal("drainBoth timed out")

			return records, errs
		}
	}
}
