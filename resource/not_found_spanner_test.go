package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/httpio"
)

// TestSpannerClient_notFound_saysNoQuery proves, over the emulator, what a caller is told
// where the package finds no record or nothing to change: the read of one row that is not
// there, plain and with what the caller may do with the row beside it, an update of a row
// that is not there, and a low-level insert-or-update that changes nothing. Each says a
// plain sentence with no query, no table's name, no column's and no key, and each keeps
// what the server looked for in the error, for the server's log.
func TestSpannerClient_notFound_saysNoQuery(t *testing.T) {
	t.Parallel()

	db := forkDatabase(t, "not-found-words")
	ctx := context.Background()
	client := NewSpannerClient(db)

	tracked := func(patchType PatchType, id, title string) *PatchSet[trackedForkRow] {
		p := NewPatchSet(NewMetadata[trackedForkRow]()).SetPatchType(patchType)
		p.SetKey("ID", id)
		p.Set("Title", title)

		return p
	}

	t.Run("a read of a row that is not there", func(t *testing.T) {
		t.Parallel()

		q := NewQuerySet(NewMetadata[trackedForkRow]())
		q.SetKey("ID", "row-9")
		q.AddField("ID")
		q.AddField("Title")
		_, err := q.Read(ctx, client)
		plainAnswer(t, err, httpio.HasNotFound, "This tracked fork row does not exist.", "WHERE", "TrackedForkRows", "Id", "row-9")
	})

	t.Run("a read of a row that is not there, with what the caller may do with the row beside it", func(t *testing.T) {
		t.Parallel()

		stmt := missingRowStatement[trackedForkRow](t, SpannerDBType, "row-9")
		// The read that scans a row's envelope: any statement with a capability plan.
		stmt.capabilityPlan = &capabilityPlan{}
		_, err := newReader[trackedForkRow](client).Read(ctx, stmt)
		plainAnswer(t, err, httpio.HasNotFound, "This tracked fork row does not exist.", "WHERE", "TrackedForkRows", "Id", "row-9")
	})

	t.Run("an update of a row that is not there", func(t *testing.T) {
		t.Parallel()

		err := tracked(UpdatePatchType, "row-8", "asked").Apply(ctx, client, ProcessEvent("test"))
		plainAnswer(t, err, httpio.HasNotFound, "This tracked fork row does not exist.", "TrackedForkRows", "row-8")
	})

	t.Run("a low-level insert-or-update that changes nothing", func(t *testing.T) {
		t.Parallel()

		if err := tracked(CreatePatchType, "row-1", "kept").Apply(ctx, client, ProcessEvent("test")); err != nil {
			t.Fatalf("Apply() of the row error = %v", err)
		}
		err := tracked(CreateOrUpdatePatchType, "row-1", "kept").Apply(ctx, client, ProcessEvent("test"))
		plainAnswer(t, err, httpio.HasBadRequest, "There is nothing to change: the record already holds these values.", "TrackedForkRows", "row-1")
	})
}
