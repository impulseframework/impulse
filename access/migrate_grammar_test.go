package access

// These tests pin the conditional-grant RoleConfig grammar: the grant object
// (resource + field set + one condition) as the authoring unit, its expansion
// into base and field grant rows sharing the condition (the construction
// invariant), deploy-time condition validation against the Collection's
// vocabulary, and condition-aware reconciliation (a changed condition text is
// remove + re-add).

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// grammarCollection is the fixture vocabulary: one domain-scoped resource
// with typed attributes, and one global execute-only method.
type grammarCollection struct{}

func (grammarCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	return map[accesstypes.Permission][]accesstypes.Resource{
		"Read":    {"Widgets", "Widgets.name", "Widgets.price"},
		"Update":  {"Widgets", "Widgets.price"},
		"Delete":  {"Widgets"},
		"Execute": {"DoThing"},
	}
}

func (grammarCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	switch {
	case res == "DoThing":
		return accesstypes.GlobalPermissionScope
	case strings.HasPrefix(string(res), "Widgets"):
		return accesstypes.DomainPermissionScope
	default:
		return ""
	}
}

func (grammarCollection) IsResourceImmutable(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (grammarCollection) AttributeComparisonType(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	if res != "Widgets" {
		return "", false
	}
	switch name {
	case "owner", "shipClass":
		return accesstypes.AttributeTypeString, true
	case "price":
		return accesstypes.AttributeTypeNumber, true
	case "expires":
		return accesstypes.AttributeTypeTimestamp, true
	case "archived":
		return accesstypes.AttributeTypeBool, true
	case "released":
		return accesstypes.AttributeTypeDate, true
	default:
		return "", false
	}
}

func (grammarCollection) AttributeIsColumn(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	// shipClass is the fixture's join-path attribute; everything else is a
	// column on the row.
	return res == "Widgets" && name != "shipClass"
}

// The fixture's subject vocabulary: a string set and a number set, and a
// number, a timestamp, and a string value — the last standing for a dotted
// value, whose type is its terminal column's.
func (grammarCollection) SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool) {
	switch name {
	case "crews":
		return accesstypes.AttributeTypeString, true
	case "hazardBands":
		return accesstypes.AttributeTypeNumber, true
	default:
		return "", false
	}
}

func (grammarCollection) SubjectValueComparisonType(name string) (accesstypes.AttributeType, bool) {
	switch name {
	case "approvalLimit":
		return accesstypes.AttributeTypeNumber, true
	case "clearedUntil":
		return accesstypes.AttributeTypeTimestamp, true
	case "homeSector":
		return accesstypes.AttributeTypeString, true
	default:
		return "", false
	}
}

func (grammarCollection) IsComputedResource(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (grammarCollection) MethodTarget(accesstypes.PermissionScope, accesstypes.Resource) (accesstypes.Resource, bool) {
	return "", false
}

func (grammarCollection) ConcealingKeys(accesstypes.PermissionScope, accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return nil, nil
}

// computedGrammarCollection is grammarCollection with Widgets reported as a
// computed resource, so the decode-time condition rules can be exercised
// against the same attribute vocabulary.
type computedGrammarCollection struct{ grammarCollection }

func (computedGrammarCollection) IsComputedResource(_ accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return res == "Widgets"
}

// targetedGrammarCollection is grammarCollection with DoThing reporting a
// @target row (Widgets), so the targeted-Execute condition rules can be
// exercised against the target's attribute vocabulary.
type targetedGrammarCollection struct{ grammarCollection }

func (targetedGrammarCollection) MethodTarget(_ accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool) {
	if method == "DoThing" {
		return "Widgets", true
	}

	return "", false
}

func TestExpandRoleGrants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		perm     accesstypes.Permission
		grants   []Grant
		declared accesstypes.PermissionScope
		want     grantSet
		wantErr  string
	}{
		{
			name:   "a grant expands into base and field rows sharing the condition",
			perm:   "Read",
			grants: []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name", "price"}, Condition: "owner = subject"}},
			want: rows(
				"Read", "Widgets", "owner = subject",
				"Read", "Widgets.name", "owner = subject",
				"Read", "Widgets.price", "owner = subject",
			),
		},
		{
			name:   "an unconditional grant expands with empty conditions",
			perm:   "Read",
			grants: []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}},
			want:   rows("Read", "Widgets", "", "Read", "Widgets.name", ""),
		},
		{
			name: "two grants with different conditions share the base row and keep their fields apart",
			perm: "Read",
			grants: []Grant{
				{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}, Condition: "price < 10"},
				{Resource: "Widgets", Fields: []accesstypes.Tag{"price"}, Condition: "owner = subject"},
			},
			want: rows(
				"Read", "Widgets", "owner = subject",
				"Read", "Widgets", "price < 10",
				"Read", "Widgets.name", "price < 10",
				"Read", "Widgets.price", "owner = subject",
			),
		},
		{
			name: "an unconditional grant and a conditional one on the same rows both store",
			perm: "Read",
			grants: []Grant{
				{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}},
				{Resource: "Widgets", Fields: []accesstypes.Tag{"name", "price"}, Condition: "owner = subject"},
			},
			want: rows(
				"Read", "Widgets", "",
				"Read", "Widgets", "owner = subject",
				"Read", "Widgets.name", "",
				"Read", "Widgets.name", "owner = subject",
				"Read", "Widgets.price", "owner = subject",
			),
		},
		{
			name:    "two grants with the same condition are rejected",
			perm:    "Read",
			grants:  []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}, Condition: "owner = subject"}, {Resource: "Widgets", Fields: []accesstypes.Tag{"price"}, Condition: "owner = subject"}},
			wantErr: "carry the same condition",
		},
		{
			name:    "two unconditional grants are rejected",
			perm:    "Read",
			grants:  []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}, {Resource: "Widgets", Fields: []accesstypes.Tag{"price"}}},
			wantErr: "two unconditional",
		},
		{
			name:    "a dotted resource takes no fields or condition",
			perm:    "Read",
			grants:  []Grant{{Resource: "Widgets.name", Condition: "owner = subject"}},
			wantErr: "dotted field resource",
		},
		{
			name:   "a bare dotted resource is a legal mechanical grant",
			perm:   "Read",
			grants: []Grant{{Resource: "Widgets.name"}},
			want:   rows("Read", "Widgets.name", ""),
		},
		{
			name:    "an unregistered field is rejected",
			perm:    "Read",
			grants:  []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"nope"}}},
			wantErr: "does not require permission",
		},
		{
			name:    "a field outside the permission's registrations is rejected",
			perm:    "Update",
			grants:  []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}},
			wantErr: "does not require permission",
		},
		{
			name:     "a global-resource grant in a domain role is rejected",
			perm:     "Execute",
			grants:   []Grant{{Resource: "DoThing"}},
			declared: accesstypes.DomainPermissionScope,
			wantErr:  "a role's grants live at its declared scope",
		},
		{
			name:     "a domain-resource grant in a global role is rejected",
			perm:     "Read",
			grants:   []Grant{{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}}},
			declared: accesstypes.GlobalPermissionScope,
			wantErr:  "a role's grants live at its declared scope",
		},
		{
			name:     "a global-resource grant in a global role expands",
			perm:     "Execute",
			grants:   []Grant{{Resource: "DoThing"}},
			declared: accesstypes.GlobalPermissionScope,
			want:     rows("Execute", "DoThing", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declared := tt.declared
			if declared == "" {
				declared = accesstypes.DomainPermissionScope
			}
			role := &Role{Name: "Tester", Description: "Exercises the grant grammar.", Permissions: map[accesstypes.Permission][]Grant{tt.perm: tt.grants}}
			got, err := expandRoleGrants(grammarCollection{}, role, declared)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expandRoleGrants() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("expandRoleGrants() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("expandRoleGrants() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateGrantCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		perm      accesstypes.Permission
		condition string
		wantErr   string
	}{
		{name: "subject fact against a string attribute", perm: "Read", condition: "owner = subject"},
		{name: "typed literals across the vocabulary", perm: "Read", condition: "price < 10 AND archived = FALSE AND expires > '2026-01-01T00:00:00Z' AND released >= '2026-01-01'"},
		{name: "now against a timestamp attribute", perm: "Read", condition: "expires > now"},
		{name: "now against a timestamp literal", perm: "Read", condition: "now < '2027-01-01T00:00:00Z'"},
		{name: "subject set and subject value", perm: "Read", condition: "owner IN subject.crews AND price <= subject.approvalLimit"},
		{name: "number set against a number attribute", perm: "Read", condition: "price IN subject.hazardBands"},
		{name: "timestamp attribute against a timestamp subject value", perm: "Read", condition: "expires <= subject.clearedUntil"},
		{name: "now against a timestamp subject value", perm: "Read", condition: "now < subject.clearedUntil"},
		{name: "dotted subject value takes its terminal type", perm: "Read", condition: "shipClass = subject.homeSector"},
		{name: "post-image column against a subject set", perm: "Update", condition: "new.owner IN subject.crews"},
		{name: "post-image on update", perm: "Update", condition: "new.price <= 100"},
		{name: "old-vs-new comparison on update", perm: "Update", condition: "new.price <= price"},
		{name: "old-vs-new beside a state guard", perm: "Update", condition: "owner = subject AND new.price <= price"},
		{name: "literal list on a number attribute", perm: "Read", condition: "price IN (1, 2, 3)"},
		{name: "time-of-day window in a named zone", perm: "Read", condition: "timeOfDay(now, 'America/Denver') >= '06:00' AND timeOfDay(now, 'America/Denver') < '13:30'"},
		{name: "time-of-day window in the local zone", perm: "Read", condition: "owner = subject AND timeOfDay(now, local) < '18:00'"},
		{name: "day-of-week membership", perm: "Update", condition: "dayOfWeek(now, 'UTC') IN ('mon', 'tue', 'wed', 'thu', 'fri')"},
		{name: "day-of-week equality in the local zone", perm: "Read", condition: "dayOfWeek(now, local) != 'sun'"},

		{name: "unparseable condition", perm: "Read", condition: "owner = = subject", wantErr: "expected an operand"},
		{name: "unknown temporal zone", perm: "Read", condition: "timeOfDay(now, 'Mars/Olympus_Mons') < '13:30'", wantErr: "not a timezone name"},
		{name: "malformed time-of-day literal", perm: "Read", condition: "timeOfDay(now, 'UTC') < '25:99'", wantErr: "not a 24-hour HH:MM"},
		{name: "time-of-day against a bare number", perm: "Read", condition: "timeOfDay(now, 'UTC') < 630", wantErr: "quoted literal"},
		{name: "day-of-week against a non-day", perm: "Read", condition: "dayOfWeek(now, 'UTC') = 'saturday'", wantErr: "not a day name"},
		{name: "day-of-week has no ordering", perm: "Read", condition: "dayOfWeek(now, 'UTC') < 'fri'", wantErr: "supports =, != and [NOT] IN"},
		{name: "day list with a non-day member", perm: "Read", condition: "dayOfWeek(now, 'UTC') IN ('mon', 'caturday')", wantErr: "not a day name"},
		{name: "unknown zone in a day list", perm: "Read", condition: "dayOfWeek(now, 'Mars/Olympus_Mons') IN ('mon')", wantErr: "not a timezone name"},
		{name: "unknown attribute", perm: "Read", condition: "ghost = subject", wantErr: "not an attribute"},
		{name: "unknown subject set", perm: "Read", condition: "owner IN subject.ghosts", wantErr: "not a declared subject set"},
		{name: "unknown subject value", perm: "Read", condition: "price <= subject.ghostLimit", wantErr: "not a declared subject value"},
		{name: "post-image on read", perm: "Read", condition: "new.price <= 100", wantErr: "post-image"},
		{name: "post-image of a join-path attribute", perm: "Update", condition: "new.shipClass = 'Freighter'", wantErr: "join-path"},
		{name: "old-vs-new is update-only, never create", perm: "Create", condition: "new.price <= price", wantErr: "only update mutations"},
		{name: "old-vs-new is update-only, never read", perm: "Read", condition: "new.price <= price", wantErr: "post-image"},
		{name: "old-vs-new sides must share a type", perm: "Update", condition: "new.price <= owner", wantErr: "cannot compare against owner"},
		{name: "old-vs-new right side must be a column", perm: "Update", condition: "new.owner = shipClass", wantErr: "join-path attribute and cannot stand on the right"},
		{name: "old-vs-new right side must be an attribute", perm: "Update", condition: "new.price <= ghost", wantErr: "not an attribute"},
		{name: "string literal against a number attribute", perm: "Read", condition: "price = 'cheap'", wantErr: "cannot compare against the string"},
		{name: "number literal against a string attribute", perm: "Read", condition: "owner = 3", wantErr: "cannot compare against the number"},
		{name: "malformed timestamp literal", perm: "Read", condition: "expires > 'yesterday'", wantErr: "RFC 3339"},
		{name: "malformed date literal", perm: "Read", condition: "released = '2026-99-99'", wantErr: "YYYY-MM-DD"},
		{name: "boolean literal against a string attribute", perm: "Read", condition: "owner = TRUE", wantErr: "boolean"},
		{name: "subject against a number attribute", perm: "Read", condition: "price = subject", wantErr: "user id"},
		{name: "now against a string attribute", perm: "Read", condition: "owner < now", wantErr: "timestamp"},
		{name: "malformed timestamp against now", perm: "Read", condition: "now < 'soon'", wantErr: "RFC 3339"},
		{name: "subject value of another type", perm: "Read", condition: "owner = subject.approvalLimit", wantErr: "owner is a string attribute and cannot compare against subject.approvalLimit, a number subject value"},
		{name: "subject value of another type on the post-image", perm: "Update", condition: "new.expires > subject.approvalLimit", wantErr: "cannot compare against subject.approvalLimit, a number subject value"},
		{name: "subject set of another type", perm: "Read", condition: "price IN subject.crews", wantErr: "price is a number attribute and cannot test membership in subject.crews, a set of string values"},
		{name: "negated subject set of another type", perm: "Read", condition: "owner NOT IN subject.hazardBands", wantErr: "cannot test membership in subject.hazardBands, a set of number values"},
		{name: "now against a non-timestamp subject value", perm: "Read", condition: "now < subject.approvalLimit", wantErr: "now is a timestamp and cannot compare against subject.approvalLimit, a number subject value"},
		{name: "now against a number is refused at parse", perm: "Read", condition: "now = 5", wantErr: "\"5\" cannot stand against now"},
		{name: "now against a boolean is refused at parse", perm: "Read", condition: "now = true", wantErr: "\"true\" cannot stand against now"},
		{name: "now against bare subject is refused at parse", perm: "Read", condition: "now < subject", wantErr: "\"subject\" cannot stand against now"},
		{name: "post-image of a join-path attribute against a subject set", perm: "Update", condition: "new.shipClass IN subject.crews", wantErr: "join-path"},
		{name: "list literal type mismatch", perm: "Read", condition: "price IN (1, 'two')", wantErr: "cannot compare against the string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grant := Grant{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}, Condition: tt.condition}
			err := validateGrantCondition(grammarCollection{}, "Tester", tt.perm, grant)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateGrantCondition() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("validateGrantCondition() error = %v", err)
			}
		})
	}
}

func TestValidateGrantCondition_executeIsDecodeTime(t *testing.T) {
	t.Parallel()

	// Row-referencing conditions cannot evaluate at decode; row-free
	// conditions fold against the environment facts and are permitted.
	rowBound := Grant{Resource: "DoThing", Condition: "owner = subject"}
	err := validateGrantCondition(grammarCollection{}, "Tester", "Execute", rowBound)
	if err == nil || !strings.Contains(err.Error(), "decode time") {
		t.Fatalf("validateGrantCondition(row-referencing) error = %v, want the decode-time rejection", err)
	}

	rowFree := Grant{Resource: "DoThing", Condition: "now < '2027-01-01T00:00:00Z'"}
	if err := validateGrantCondition(grammarCollection{}, "Tester", "Execute", rowFree); err != nil {
		t.Fatalf("validateGrantCondition(row-free) error = %v, want nil", err)
	}

	// Temporal terms are environment facts — row-free by classification — so
	// a working-hours window is legal even where no row exists.
	temporal := Grant{Resource: "DoThing", Condition: "timeOfDay(now, local) < '18:00' AND dayOfWeek(now, local) NOT IN ('sat', 'sun')"}
	if err := validateGrantCondition(grammarCollection{}, "Tester", "Execute", temporal); err != nil {
		t.Fatalf("validateGrantCondition(temporal) error = %v, want nil", err)
	}
}

func TestValidateGrantCondition_targetedExecute(t *testing.T) {
	t.Parallel()

	// A @target-bearing method's generated handler locates its row inside the
	// transaction (design plan §12), so its Execute grants may reference the
	// row — bindings, subject values, and literal types all validate against
	// the TARGET resource's vocabulary, never the method's.
	tests := []struct {
		name      string
		condition string
		wantErr   string
	}{
		{name: "row-referencing condition is permitted", condition: "owner = subject"},
		{name: "subject-value threshold is permitted", condition: "price <= subject.approvalLimit"},
		{name: "row-free condition still folds at decode", condition: "now < '2027-01-01T00:00:00Z'"},
		{name: "unknown attribute names the target resource", condition: "ghost = subject", wantErr: "not an attribute of Widgets"},
		{name: "literal types validate against the target", condition: "price = 'cheap'", wantErr: "cannot compare against the string"},
		{name: "post-image stays rejected on Execute", condition: "new.price <= 100", wantErr: "post-image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grant := Grant{Resource: "DoThing", Condition: tt.condition}
			err := validateGrantCondition(targetedGrammarCollection{}, "Tester", "Execute", grant)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateGrantCondition() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("validateGrantCondition() error = %v", err)
			}
		})
	}
}

func TestValidateGrantCondition_computedIsDecodeTime(t *testing.T) {
	t.Parallel()

	// A computed resource's checks run at decode, exactly like Execute: only
	// row-free conditions can settle there, whatever the permission.
	tests := []struct {
		name      string
		condition string
		wantErr   bool
	}{
		{name: "row-referencing condition is rejected", condition: "owner = subject", wantErr: true},
		{name: "subject-value condition is rejected", condition: "price <= subject.approvalLimit", wantErr: true},
		{name: "row-free condition is permitted", condition: "now < '2027-01-01T00:00:00Z'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grant := Grant{Resource: "Widgets", Fields: []accesstypes.Tag{"name"}, Condition: tt.condition}
			err := validateGrantCondition(computedGrammarCollection{}, "Tester", "Read", grant)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "decode time") {
					t.Fatalf("validateGrantCondition() error = %v, want the decode-time rejection", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("validateGrantCondition() error = %v", err)
			}
		})
	}
}
