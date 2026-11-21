package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestRequestReason pins what a request's optional reason reads as: the text of a field
// that is there, and nothing for one left out.
func TestRequestReason(t *testing.T) {
	t.Parallel()

	said := "covering the night desk"
	if got := RequestReason(&said); got != said {
		t.Errorf("RequestReason(%q) = %q, want the text", said, got)
	}
	if got := RequestReason(nil); got != "" {
		t.Errorf("RequestReason(nil) = %q, want nothing", got)
	}
}

// TestManager_AssignRoleFor_reason pins the reason of a membership as the rules give it:
// it is written with the membership, less the space around it; a membership the person
// holds is given the reason handed in place of the one it had, and none where it is
// given again with none; the same period and reason again write nothing; and a reason
// that is not one line of plain text of the length allowed is refused before anything
// is written.
func TestManager_AssignRoleFor_reason(t *testing.T) {
	t.Parallel()

	_, _, current := periodFixtures()
	held := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	text := "domain/alpha " + userID.String() + " Reader"
	tests := []struct {
		name       string
		holds      bool
		had        accesstypes.Period
		hadReason  string
		period     accesstypes.Period
		reason     string
		wantErr    string
		wantWrites []string
	}{
		{
			name: "a new membership with a reason and no period", reason: "covering the night desk",
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": none because covering the night desk"},
		},
		{
			name: "a new membership with a period and a reason", period: current, reason: "covering the night desk",
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + periodText(current) + " because covering the night desk"},
		},
		{
			name: "the space around a reason is not kept", reason: "  covering the night desk \t",
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": none because covering the night desk"},
		},
		{name: "a reason of spaces alone is none", reason: "   ", wantWrites: []string{"insert membership " + text}},
		{
			name: "a new reason takes the old one's place", holds: true, had: current, hadReason: "the audit", period: current, reason: "covering the night desk",
			wantWrites: []string{"period of membership " + text + ": " + periodText(current) + " because covering the night desk"},
		},
		{
			name: "given again with no reason, the membership carries none", holds: true, had: current, hadReason: "the audit", period: current,
			wantWrites: []string{"period of membership " + text + ": " + periodText(current)},
		},
		{
			name: "a membership held with no reason is given one", holds: true, reason: "covering the night desk",
			wantWrites: []string{"period of membership " + text + ": none because covering the night desk"},
		},
		{name: "the period and the reason it has, again", holds: true, had: current, hadReason: "the audit", period: current, reason: "the audit"},
		{name: "a reason of two lines", reason: "covering the night desk\nuntil Friday", wantErr: "one line of plain text"},
		{name: "a reason longer than the length allowed", reason: strings.Repeat("a", access.MaxReason+1), wantErr: "longer than 500 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			if tt.holds {
				store.memberships[held] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{held: tt.had}
				store.membershipReasons = map[access.MembershipKey]string{held: tt.hadReason}
			}
			m, _ := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRoleFor(ctx, txn, alphaScope, userID, "Reader", tt.period, tt.reason)
			})
			if tt.wantErr != "" {
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AssignRoleFor() error = %v, want a bad request containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("AssignRoleFor() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_AssignRole_dropsTheReason pins that a role given again with no period and
// no reason (AssignRole) leaves the membership with neither.
func TestManager_AssignRole_dropsTheReason(t *testing.T) {
	t.Parallel()

	held := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	store := groupStore()
	store.memberships[held] = true
	store.membershipReasons = map[access.MembershipKey]string{held: "the audit"}
	m, _ := newManager(t, store, engineRoles)

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
	})
	if err != nil {
		t.Fatalf("AssignRole() error = %v", err)
	}
	if diff := cmp.Diff([]string{"period of membership " + membershipText(&held) + ": none"}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestTakeLengths pins the lengths a role is taken for and how long each lasts, and that
// anything else is no length.
func TestTakeLengths(t *testing.T) {
	t.Parallel()

	want := map[TakeLength]time.Duration{"1 hour": time.Hour, "4 hours": 4 * time.Hour, "8 hours": 8 * time.Hour, "1 day": 24 * time.Hour}
	lengths := TakeLengths()
	if len(lengths) != len(want) {
		t.Fatalf("TakeLengths() = %v, want %d lengths", lengths, len(want))
	}
	var before time.Duration
	for _, length := range lengths {
		lasts, offered := length.Duration()
		if !offered || lasts != want[length] {
			t.Errorf("TakeLength(%q).Duration() = %v, %v, want %v, true", length, lasts, offered, want[length])
		}
		if lasts <= before {
			t.Errorf("TakeLengths() lists %q after a longer one, want shortest first", length)
		}
		before = lasts
	}
	for _, length := range []TakeLength{"", "2 days", "1 Hour", "forever"} {
		if lasts, offered := length.Duration(); offered || lasts != 0 {
			t.Errorf("TakeLength(%q).Duration() = %v, %v, want 0, false", length, lasts, offered)
		}
	}
}

// TestManager_TakeRole pins the taking of a role for a set time, rule by rule: the role
// goes to the request's caller and nobody else, from now for the length chosen, with the
// reason given; a length that is not offered and a missing reason are refused; the
// caller is asked what giving themselves the role needs, and one who may not give it
// takes nothing; a manager that cannot judge refuses rather than passes; a request with
// no person of this auth behind it, and a session that views as someone else, take
// nothing; taking a role again writes the new time and the new reason over the old ones
// and is not refused as a caller who lengthens their own role is; and the Super
// Administrator role is not taken.
func TestManager_TakeRole(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	text := membershipText(&own)
	erins := access.NewMembershipKey(alphaScope, erin, "Reader")
	steward := access.NewMembershipKey(globalScope, dana, "Steward")
	super := access.NewMembershipKey(globalScope, dana, accesstypes.SuperAdministrator)
	until := func(lasts time.Duration) string {
		return periodText(accesstypes.Period{Start: now, End: now.Add(lasts)})
	}
	asDana := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) }
	asErin := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) }
	why := "covering the night desk"
	notRefusal := func(err error) bool { return err != nil && !httpio.HasForbidden(err) && !httpio.HasBadRequest(err) }

	tests := []struct {
		name   string
		ctx    func(context.Context) context.Context
		scope  accesstypes.PolicyScope
		role   accesstypes.Role
		length TakeLength
		reason string
		// grants judges the taking; noGrants builds the manager with none.
		grants   *fakeGrants
		noGrants bool
		// failing names the store call that fails, none where it is empty.
		failing string
		// had and hadReason are what the caller's own membership carries before the act,
		// where holds says there is one.
		holds      bool
		had        accesstypes.Period
		hadReason  string
		refused    func(error) bool
		wantSays   string
		wantWrites []string
		wantAsked  []string
	}{
		{
			name: "a role for an hour, to the caller", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + until(time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "the role goes to whoever asks: another caller takes it for themselves", ctx: asErin, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			wantWrites: []string{
				"insert membership " + membershipText(&erins),
				"period of membership " + membershipText(&erins) + ": " + until(time.Hour) + " because " + why,
			},
			wantAsked: []string{"give " + membershipText(&erins)},
		},
		{
			name: "for four hours", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeFourHours, reason: why,
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + until(4*time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "for eight hours", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeEightHours, reason: why,
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + until(8*time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "for a day", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneDay, reason: why,
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + until(24*time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "a role of the whole application", ctx: asDana, scope: globalScope, role: "Steward", length: TakeOneHour, reason: why,
			wantWrites: []string{
				"insert membership " + membershipText(&steward),
				"period of membership " + membershipText(&steward) + ": " + until(time.Hour) + " because " + why,
			},
			wantAsked: []string{"give " + membershipText(&steward)},
		},
		{
			name: "a length that is not offered", ctx: asDana, scope: alphaScope, role: "Reader", length: "2 days", reason: why,
			refused: httpio.HasBadRequest, wantSays: "2 days",
		},
		{
			name: "no length", ctx: asDana, scope: alphaScope, role: "Reader", length: "", reason: why,
			refused: httpio.HasBadRequest, wantSays: "is none of them",
		},
		{
			name: "no reason", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: "",
			refused: httpio.HasBadRequest, wantSays: "say why you need the role",
		},
		{
			name: "a reason of spaces alone", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: "  \t ",
			refused: httpio.HasBadRequest, wantSays: "say why you need the role",
		},
		{
			name: "a reason of two lines", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: "covering\nthe night desk",
			refused: httpio.HasBadRequest, wantSays: "one line of plain text",
		},
		{
			name: "a caller who may not give themselves the role", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			grants:  &fakeGrants{noGive: []accesstypes.Role{"Reader"}},
			refused: httpio.HasForbidden, wantSays: "you may not give yourself the role Reader there", wantAsked: []string{"give " + text},
		},
		{
			name: "a manager built with no role grants cannot judge, and does not pass", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			noGrants: true, refused: notRefusal, wantSays: "identity.WithRoleGrants",
		},
		{
			name: "the role grants cannot be read", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			grants: &fakeGrants{err: errStore}, refused: notRefusal, wantSays: errStore.Error(), wantAsked: []string{"give " + text},
		},
		{
			name: "a call outside any request has nobody to give the role to", ctx: func(ctx context.Context) context.Context { return ctx },
			scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			refused: httpio.HasForbidden, wantSays: "A role is taken by a person signed in to this application's own accounts, and this request comes from none.",
		},
		{
			name:  "a caller who came through another auth is nobody of this one",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, dana) },
			scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			refused: httpio.HasForbidden, wantSays: "A role is taken by a person signed in to this application's own accounts, and this request comes from none.",
		},
		{
			name: "a caller with no account", ctx: func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, "night-batch") },
			scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			refused: httpio.HasForbidden, wantSays: "A role is taken for your own account, and this session acts for none.",
		},
		{
			name: "a caller whose account is gone",
			ctx: func(ctx context.Context) context.Context {
				return asCallerThrough(ctx, managedAuth, User(mustUUID("99999999-9999-4999-8999-999999999999")))
			},
			scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			refused: httpio.HasForbidden, wantSays: "A role is taken for your own account, and this session's account is no longer one of this application's. Sign in again.",
		},
		{
			name: "the accounts cannot be read", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			failing: "Username", refused: notRefusal, wantSays: errStore.Error(),
		},
		{
			name: "a session that views as someone else takes nothing",
			ctx: func(ctx context.Context) context.Context {
				viewing := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{Actor: string(erin), ActorRealm: managedAuth}}

				return context.WithValue(asDana(ctx), sessioninfo.CtxSessionInfo, viewing)
			},
			scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			refused: httpio.HasForbidden, wantSays: "A role is taken by the person themselves, and this session views as someone else. End it and take the role as yourself.",
		},
		{
			name: "taken again for longer: the new time and the new reason, and nothing refused", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeFourHours, reason: why,
			holds: true, had: accesstypes.Period{End: now.Add(30 * time.Minute)}, hadReason: "the audit",
			wantWrites: []string{"period of membership " + text + ": " + until(4*time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "taken again for shorter", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			holds: true, had: accesstypes.Period{End: now.Add(6 * time.Hour)}, hadReason: "the audit",
			wantWrites: []string{"period of membership " + text + ": " + until(time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "a role the caller holds with no end is given the time taken: taking it makes it end", ctx: asDana, scope: alphaScope, role: "Reader", length: TakeOneHour, reason: why,
			holds:      true,
			wantWrites: []string{"period of membership " + text + ": " + until(time.Hour) + " because " + why},
			wantAsked:  []string{"give " + text},
		},
		{
			name: "the Super Administrator role is not taken", ctx: asDana, scope: globalScope, role: accesstypes.SuperAdministrator, length: TakeOneHour, reason: why,
			refused: httpio.HasBadRequest, wantSays: "takes no end", wantAsked: []string{"give " + membershipText(&super)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.failOn = tt.failing
			if tt.holds {
				store.memberships[own] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{own: tt.had}
				store.membershipReasons = map[access.MembershipKey]string{own: tt.hadReason}
			}
			grants := tt.grants
			if grants == nil {
				grants = &fakeGrants{}
			}
			var opts []Option
			if !tt.noGrants {
				opts = append(opts, WithRoleGrants(grants))
			}
			m, engine := newManager(t, store, engineRoles, opts...)
			m.now = func() time.Time { return now }

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.TakeRole(tt.ctx(ctx), txn, tt.scope, tt.role, tt.length, tt.reason)
			})
			if tt.refused != nil {
				if !tt.refused(err) || !strings.Contains(err.Error(), tt.wantSays) {
					t.Fatalf("TakeRole() error = %v, want the refusal saying %q", err, tt.wantSays)
				}
				if engine.changed != 0 {
					t.Errorf("the engine was told %d time(s) of a taking refused, want none", engine.changed)
				}
			} else if err != nil {
				t.Fatalf("TakeRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the role grants were asked (-want +got):\n%s", diff)
			}
			if len(grants.askedOutside) != 0 {
				t.Errorf("the role grants were asked outside the transaction: %v", grants.askedOutside)
			}
		})
	}
}

// TestManager_TakeRole_aloneLengthensTheCallersOwn pins that the taking is the one act
// that makes the caller's own role last longer: the same caller, giving themselves the
// same role for the same longer period the ordinary way, is refused.
func TestManager_TakeRole_aloneLengthensTheCallersOwn(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	store := groupStore()
	store.memberships[own] = true
	store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{own: {End: now.Add(30 * time.Minute)}}
	m, _ := newManager(t, store, engineRoles, WithRoleGrants(&fakeGrants{}))
	m.now = func() time.Time { return now }

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		longer := accesstypes.Period{End: now.Add(4 * time.Hour)}

		return m.AssignRoleFor(asCallerThrough(ctx, managedAuth, dana), txn, alphaScope, userID, "Reader", longer, "covering the night desk")
	})
	toldPlainly(t, err, "You cannot make your own role last longer than it was given for.", "someone else who may give it")
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
}

// TestManager_RolesToTake pins the list a role is picked from on the Break glass page:
// the roles that exist in every tenant where the application has tenants, and the whole
// application's where it has none; never the Super Administrator role; only the roles
// the caller could give by hand; every role for a call outside any request; and an
// error, never a list, for a request the manager cannot judge.
func TestManager_RolesToTake(t *testing.T) {
	t.Parallel()

	roles := map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {"Steward", accesstypes.SuperAdministrator},
		alphaScope:  {"Reader", "Warden", "AlphaOnly"},
		everyScope:  {"Reader", "Warden"},
	}
	asDana := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) }
	outside := func(ctx context.Context) context.Context { return ctx }
	tests := []struct {
		name      string
		ctx       func(context.Context) context.Context
		tenants   bool
		grants    *fakeGrants
		noGrants  bool
		want      []accesstypes.Role
		wantAsked []string
		wantErr   string
	}{
		{
			name: "with tenants: the roles that exist in every tenant", ctx: asDana, tenants: true, grants: &fakeGrants{},
			want: []accesstypes.Role{"Reader", "Warden"}, wantAsked: []string{string(assigns) + " could give " + everyScope.String() + " Reader", string(assigns) + " could give " + everyScope.String() + " Warden"},
		},
		{
			name: "without tenants: the whole application's roles, never the Super Administrator role", ctx: asDana, grants: &fakeGrants{},
			want: []accesstypes.Role{"Steward"}, wantAsked: []string{string(assigns) + " could give " + globalScope.String() + " Steward"},
		},
		{
			name: "a role the caller could give nobody is not offered", ctx: asDana, tenants: true, grants: &fakeGrants{noneGives: []accesstypes.Role{"Warden"}},
			want: []accesstypes.Role{"Reader"}, wantAsked: []string{string(assigns) + " could give " + everyScope.String() + " Reader", string(assigns) + " could give " + everyScope.String() + " Warden"},
		},
		{name: "a call outside any request lists every role", ctx: outside, tenants: true, noGrants: true, want: []accesstypes.Role{"Reader", "Warden"}},
		{name: "a request the manager cannot judge is an error", ctx: asDana, tenants: true, noGrants: true, wantErr: "identity.WithRoleGrants"},
		{name: "the role grants cannot be read", ctx: asDana, tenants: true, grants: &fakeGrants{err: errStore}, wantErr: errStore.Error(), wantAsked: []string{string(assigns) + " could give " + everyScope.String() + " Reader"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			grants := tt.grants
			if grants == nil {
				grants = &fakeGrants{}
			}
			var opts []Option
			if !tt.noGrants {
				opts = append(opts, WithRoleGrants(grants))
			}
			m, _ := newManager(t, groupStore(), roles, opts...)

			listed, err := m.RolesToTake(tt.ctx(t.Context()), assigns, tt.tenants)
			if tt.wantErr != "" {
				if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RolesToTake() error = %v, want an error containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("RolesToTake() error = %v", err)
			}
			var got []accesstypes.Role
			for _, role := range listed {
				got = append(got, role.Name)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RolesToTake() roles (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the role grants were asked (-want +got):\n%s", diff)
			}
		})
	}
}
