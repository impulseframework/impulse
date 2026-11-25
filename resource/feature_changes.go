package resource

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/tracer"
)

// The history of one flag, as the Feature flags page reads it.
const (
	// FeatureFlagChangesRoute is the segment under one flag's route that its history is
	// read at: GET <prefix>/feature-flags/{featureFlagName}/changes.
	FeatureFlagChangesRoute = "changes"
	// FeatureFlagChangesLimit is the most flips one read of a flag's history answers: the
	// newest ones. A flag flipped more often says so (FeatureFlagChangesResponse.More).
	FeatureFlagChangesLimit = 100
)

// FeatureWords is a flag's name in words, as a page names the flag: cargo_manifest is
// "Cargo manifest". Nothing is declared beside the flag for it.
func FeatureWords(name Feature) string {
	words := strings.Join(strings.FieldsFunc(string(name), func(r rune) bool {
		return r == '_'
	}), " ")
	first, size := utf8.DecodeRuneInString(words)
	if size == 0 {
		return ""
	}

	return string(unicode.ToUpper(first)) + words[size:]
}

// Words is the declaration's description as a person reads it on the Feature flags
// page. The description is the comment above the constant, which by Go's convention
// opens with the constant's name ("CargoManifest shows a ship's cargo bays."): that name
// is swapped for the flag's name in words ("Cargo manifest shows a ship's cargo bays."),
// the name the page lists the flag by. A description that opens any other way stands as
// written.
func (d FeatureDeclaration) Words() string {
	rest, opens := strings.CutPrefix(d.Description, d.Constant)
	if d.Constant == "" || !opens {
		return d.Description
	}
	// The constant is the description's first word only where the word ends with it.
	if next, size := utf8.DecodeRuneInString(rest); size > 0 && (unicode.IsLetter(next) || unicode.IsDigit(next) || next == '_') {
		return d.Description
	}

	return FeatureWords(d.Name) + rest
}

// FeatureFlagChange is one flip of a flag, a row of the FeatureFlagChanges table as the
// flag's history serves it to a caller who may see every fact of the flag. Each fact is
// served under the grant on the flag's field that holds the same fact today (ChangedAt
// under updatedAt, Enabled under enabled, ChangedBy under updatedBy), and is left out of
// the answer where the caller's grant leaves that field out.
type FeatureFlagChange struct {
	// ChangedAt is when the flip was committed.
	ChangedAt time.Time `json:"changedAt"`
	// Enabled is the value written: true for a flag turned on, false for one turned off.
	Enabled bool `json:"enabled"`
	// ChangedBy is who flipped it, as one sentence (eventsource.EventSource.String): the
	// name the person signs in under, or the process's where no person made the flip.
	ChangedBy string `json:"changedBy"`
}

// FeatureFlagChangesResponse is the answer of a flag's history: its flips, newest first.
type FeatureFlagChangesResponse struct {
	// Changes are the newest flips, at most FeatureFlagChangesLimit of them. Never nil: a
	// flag nobody has flipped answers an empty list.
	Changes []FeatureFlagChange `json:"changes"`
	// More says the flag was flipped more often than the answer lists, so older flips
	// are left out.
	More bool `json:"more"`
}

// featureFlagChangesAnswer is the history as the handler writes it: each flip with the
// facts the caller may see, which FeatureFlagChangesResponse reads back.
type featureFlagChangesAnswer struct {
	Changes []map[string]any `json:"changes"`
	More    bool             `json:"more"`
}

// featureFlagChangeMap renders one flip with the facts the caller may see of the flag:
// shown answers for a field of the flag, and a flip's fact rides the field that holds
// the same fact of the flag as it stands (when under updatedAt, the value under enabled,
// who under updatedBy).
func featureFlagChangeMap(change FeatureFlagChange, shown func(field accesstypes.Field, wire string) bool) map[string]any {
	rmap := make(map[string]any, 3)
	if shown(flagUpdatedAtColumn, "updatedAt") {
		rmap["changedAt"] = change.ChangedAt
	}
	if shown(flagEnabledColumn, "enabled") {
		rmap["enabled"] = change.Enabled
	}
	if shown(flagUpdatedByColumn, "updatedBy") {
		rmap["changedBy"] = change.ChangedBy
	}

	return rmap
}

// featureFlagChangeRow is a row of the FeatureFlagChanges table as the history reads it:
// when, the value written, and who made the flip in the columns the history keeps it in.
type featureFlagChangeRow struct {
	ChangedAt time.Time `spanner:"ChangedAt"`
	Enabled   bool      `spanner:"Enabled"`
	eventSourceColumns
}

// readFeatureFlagChanges reads one flag's newest flips, at most limit of them, and
// whether the table holds more.
func readFeatureFlagChanges(ctx context.Context, txn ReadOnlyTransaction, dbType DBType, name Feature, limit int) (changes []FeatureFlagChange, more bool, err error) {
	if dbType != SpannerDBType {
		return nil, false, errFeaturesUnsupportedDatabase
	}
	var rows []featureFlagChangeRow
	stmt := spanner.Statement{
		SQL:    "SELECT ChangedAt, Enabled, " + strings.Join(eventsource.ColumnNames(), ", ") + " FROM FeatureFlagChanges WHERE Name = @name ORDER BY ChangedAt DESC LIMIT @limit",
		Params: map[string]any{flagNameField: string(name), "limit": int64(limit) + 1},
	}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, stmt); err != nil {
		return nil, false, errors.Wrap(err, "spxscan.Select()")
	}
	if len(rows) > limit {
		rows, more = rows[:limit], true
	}
	changes = make([]FeatureFlagChange, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		changes = append(changes, FeatureFlagChange{ChangedAt: row.ChangedAt, Enabled: row.Enabled, ChangedBy: whoFlipped(&row.eventSourceColumns)})
	}

	return changes, more, nil
}

// FeatureFlagChangesHandler serves one flag's history, its flips newest first
// (FeatureFlagChangesResponse), to whoever may see the flag in the list: the flag is
// read first as the list reads it, under a List grant on FeatureFlags in the global
// scope with whatever condition the grant carries, so a caller the list would not show
// the flag to is refused as the list refuses them, Forbidden without the grant and
// NotFound for a flag the table does not hold or the grant does not reach. No other
// grant is asked: whoever may see the flags may see a flag's history, and sees of each
// flip what they may see of the flag, so a grant that leaves a field of the flag out
// (who last flipped it, say) leaves the same fact out of every flip, and a caller who may
// see none of the three facts is refused, Forbidden, since the answer would tell them
// only how often the flag was flipped. The request's query
// is not read: the history is the flag's whole, and nothing asked of the list (columns,
// a filter, a page) narrows it. It reads the FeatureFlagChanges table, which every flip
// writes a row to in the transaction that flips the flag, and writes nothing; the
// FeatureSet is not consulted. The generated router registers it at
// GET <prefix>/feature-flags/{featureFlagName}/changes on every session-serving outlet.
func FeatureFlagChangesHandler(a FeatureApp, collection *GeneratedCollection) http.HandlerFunc {
	decoder := MustNewQueryDecoder[FeatureFlag, featureFlagRequest](collection, accesstypes.List).
		WithCursorKey(a.CursorKey()).
		WithPaging(featureFlagsOrder)

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		name := httpio.Param[string](r, FeatureFlagNameParam)
		flag := r.Clone(ctx)
		flag.URL.RawQuery = ""
		querySet, err := decoder.Decode(flag, a.UserPermissions(r), accesstypes.GlobalScope())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		querySet.SetKey(flagNameColumn, name)
		row, err := querySet.Read(ctx, a.ResourceClient())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		// The fields the caller may list of the flag, less the cells the grant masks.
		fields := querySet.Fields()
		shown := func(field accesstypes.Field, wire string) bool {
			return slices.Contains(fields, field) && !row.Masked(wire)
		}
		// A caller who may see none of the three facts would learn from the answer only
		// how often the flag was flipped, which the list never tells them: it is refused.
		if !shown(flagUpdatedAtColumn, "updatedAt") && !shown(flagEnabledColumn, "enabled") && !shown(flagUpdatedByColumn, "updatedBy") {
			return httpio.NewEncoder(w).ClientMessage(ctx, flagHistoryRefused(a.UserPermissions(r).User()))
		}

		client := a.ResourceClient()
		txn := client.ReadOnlyTransaction()
		defer txn.Close()

		changes, more, err := readFeatureFlagChanges(ctx, txn, client.DBType(), Feature(name), FeatureFlagChangesLimit)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		answer := featureFlagChangesAnswer{Changes: make([]map[string]any, 0, len(changes)), More: more}
		for _, change := range changes {
			answer.Changes = append(answer.Changes, featureFlagChangeMap(change, shown))
		}

		return httpio.NewEncoder(w).Ok(answer)
	})
}

// flagHistoryRefused is the answer to a caller who may list none of the three facts a
// flag's history tells: Forbidden, with one sentence a person can read. Who was refused,
// the table and the fields a grant of List would show are the cause's, kept for the
// server's log; no word of it reaches the caller.
func flagHistoryRefused(user accesstypes.User) errors.Chain {
	return httpio.NewForbiddenMessageWithError(
		errors.Newf(
			"a flag's history says when the flag was turned on or off, what to, and by whom, and user (%s) may list none of those of %s for this flag (the fields updatedAt, enabled and updatedBy): a grant of List on any of them shows that much of the history",
			user, FeatureFlagsResource,
		),
		flagHistoryWords,
	)
}
