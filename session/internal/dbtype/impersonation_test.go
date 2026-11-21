package dbtype

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

func strPtr(s string) *string { return &s }

func TestMaskColumn_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mask    accesstypes.PermissionMask
		wantCol *string
	}{
		{name: "unrestricted is NULL", mask: accesstypes.PermissionMask{}, wantCol: nil},
		{name: "read-only", mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read, accesstypes.List), wantCol: strPtr("List,Read")},
		{name: "allows nothing is the empty string", mask: accesstypes.DenyAll(), wantCol: strPtr("")},
		{name: "every kind except a class", mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)), wantCol: strPtr("*;-class:credential")},
		{name: "every kind except one action", mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("LaunchMission")), wantCol: strPtr("*;-on:Execute:LaunchMission")},
		{name: "kinds, a class and a record's update", mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Update).Refusing(accesstypes.RefuseClass("sensitive"), accesstypes.RefusePermission(accesstypes.Update, "Ships")), wantCol: strPtr("List,Read,Update;-class:sensitive;-on:Update:Ships")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			col := MaskColumn(tt.mask)
			if (col == nil) != (tt.wantCol == nil) || (col != nil && *col != *tt.wantCol) {
				t.Fatalf("MaskColumn() = %v, want %v", col, tt.wantCol)
			}
			if got := MaskFromColumn(col); !got.Equal(tt.mask) {
				t.Errorf("MaskFromColumn(MaskColumn()) = %v, want %v", got, tt.mask)
			}
		})
	}
}

func TestMaskFromColumn_Tolerates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		col  string
		want string
	}{
		{name: "spaces and empty entries", col: " Read, ,List,", want: "List,Read"},
		{name: "duplicates", col: "Read,Read", want: "Read"},
		{name: "empty", col: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := MaskFromColumn(&tt.col).String(); got != tt.want {
				t.Errorf("MaskFromColumn(%q) = %q, want %q", tt.col, got, tt.want)
			}
		})
	}
}

// TestMaskFromColumn_failsClosed pins what a column that is no mask's text is read as:
// the mask that keeps nothing. A record somebody wrote by hand, or a refusal this
// version cannot read, never leaves the session wider than what was written.
func TestMaskFromColumn_failsClosed(t *testing.T) {
	t.Parallel()

	for _, col := range []string{"*;class:credential", "*;-class:", "*;-on:Execute", "List,Read;", "*;-everything"} {
		t.Run(col, func(t *testing.T) {
			t.Parallel()

			got := MaskFromColumn(&col)
			if !got.Equal(accesstypes.DenyAll()) {
				t.Errorf("MaskFromColumn(%q) = %q, want the mask that keeps nothing", col, got.Text())
			}
			for _, perm := range []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Execute} {
				if got.Allows(perm) {
					t.Errorf("MaskFromColumn(%q) keeps %s", col, perm)
				}
			}
		})
	}
}

// TestImpersonation_MaskFault pins that a row whose mask column does not read says
// why, and that a row whose column reads, or that stores none, says nothing.
func TestImpersonation_MaskFault(t *testing.T) {
	t.Parallel()

	unreadable := "*;class:credential"
	readable := "*;-class:credential"

	if err := (&Impersonation{Mask: &unreadable}).MaskFault(); err == nil || !strings.Contains(err.Error(), `the part "class:credential" is no refusal`) {
		t.Errorf("MaskFault() of %q = %v, want the part that does not read", unreadable, err)
	}
	if err := (&Impersonation{Mask: &readable}).MaskFault(); err != nil {
		t.Errorf("MaskFault() of %q = %v, want none", readable, err)
	}
	if err := (&Impersonation{}).MaskFault(); err != nil {
		t.Errorf("MaskFault() of no mask = %v, want none", err)
	}
}

func TestImpersonation_RowConversions(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"))
	sourceID := impulse.Must(impulse.UUIDFromString("7a1d5f6e-0c2b-4a3e-9d8f-1b2c3d4e5f60"))
	// The accounts of the actor and of the impersonated user.
	actorID := impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	userID := impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	expires := started.Add(time.Hour)
	ended := started.Add(30 * time.Minute)

	tests := []struct {
		name       string
		imp        *sessioninfo.Impersonation
		wantInsert *InsertImpersonation
		row        *Impersonation
		wantInfo   *sessioninfo.Impersonation
	}{
		{
			name: "impersonated user, read-only, minimal",
			imp: &sessioninfo.Impersonation{
				Actor:     "alice@example.com",
				Principal: accesstypes.UserPrincipal("bob@partner.org"),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
				StartedAt: started,
				ExpiresAt: expires,
			},
			wantInsert: &InsertImpersonation{
				ActorUsername:   "alice@example.com",
				PrincipalKind:   PrincipalKindUser,
				PrincipalUserID: strPtr("bob@partner.org"),
				Mask:            strPtr("List,Read"),
				ExpiresAt:       expires,
			},
			row: &Impersonation{
				SessionID:       sessionID,
				ActorUsername:   "alice@example.com",
				PrincipalKind:   PrincipalKindUser,
				PrincipalUserID: strPtr("bob@partner.org"),
				Mask:            strPtr("List,Read"),
				StartedAt:       started,
				ExpiresAt:       expires,
			},
			wantInfo: &sessioninfo.Impersonation{
				SessionID: sessionID,
				Actor:     "alice@example.com",
				Principal: accesstypes.UserPrincipal("bob@partner.org"),
				Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
				StartedAt: started,
				ExpiresAt: expires,
			},
		},
		{
			name: "impersonated role, every optional field, ended",
			imp: &sessioninfo.Impersonation{
				Actor:           "alice@example.com",
				ActorRealm:      "admin-portal",
				SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
				Principal:       accesstypes.RolePrincipal("PartnerViewer"),
				Reason:          "ticket JRN-123",
				StartedAt:       started,
				ExpiresAt:       expires,
			},
			wantInsert: &InsertImpersonation{
				ActorUsername:   "alice@example.com",
				ActorRealm:      strPtr("admin-portal"),
				SourceSessionID: &sourceID,
				PrincipalKind:   PrincipalKindRole,
				PrincipalRole:   strPtr("PartnerViewer"),
				Reason:          strPtr("ticket JRN-123"),
				ExpiresAt:       expires,
			},
			row: &Impersonation{
				SessionID:       sessionID,
				ActorUsername:   "alice@example.com",
				ActorRealm:      strPtr("admin-portal"),
				SourceSessionID: &sourceID,
				PrincipalKind:   PrincipalKindRole,
				PrincipalRole:   strPtr("PartnerViewer"),
				Reason:          strPtr("ticket JRN-123"),
				StartedAt:       started,
				ExpiresAt:       expires,
				EndedAt:         &ended,
				EndReason:       strPtr("Revoked"),
			},
			wantInfo: &sessioninfo.Impersonation{
				SessionID:       sessionID,
				Actor:           "alice@example.com",
				ActorRealm:      "admin-portal",
				SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
				Principal:       accesstypes.RolePrincipal("PartnerViewer"),
				Reason:          "ticket JRN-123",
				StartedAt:       started,
				ExpiresAt:       expires,
				EndedAt:         &ended,
				EndReason:       sessioninfo.ImpersonationEndedByRevocation,
			},
		},
		{
			name: "an account impersonated by a local actor: the actor's account rides beside their username, and the principal is the impersonated account's id",
			imp: &sessioninfo.Impersonation{
				Actor:           "alice@example.com",
				ActorID:         impulse.NullUUIDFromUUID(actorID),
				SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
				Principal:       accesstypes.UserPrincipal(accesstypes.User(userID.String())),
				StartedAt:       started,
				ExpiresAt:       expires,
			},
			wantInsert: &InsertImpersonation{
				ActorUsername:   "alice@example.com",
				ActorUserID:     &actorID,
				SourceSessionID: &sourceID,
				PrincipalKind:   PrincipalKindUser,
				PrincipalUserID: strPtr(userID.String()),
				ExpiresAt:       expires,
			},
			row: &Impersonation{
				SessionID:       sessionID,
				ActorUsername:   "alice@example.com",
				ActorUserID:     &actorID,
				SourceSessionID: &sourceID,
				PrincipalKind:   PrincipalKindUser,
				PrincipalUserID: strPtr(userID.String()),
				StartedAt:       started,
				ExpiresAt:       expires,
			},
			wantInfo: &sessioninfo.Impersonation{
				SessionID:       sessionID,
				Actor:           "alice@example.com",
				ActorID:         impulse.NullUUIDFromUUID(actorID),
				SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
				Principal:       accesstypes.UserPrincipal(accesstypes.User(userID.String())),
				StartedAt:       started,
				ExpiresAt:       expires,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.wantInsert, NewInsertImpersonation(tt.imp)); diff != "" {
				t.Errorf("NewInsertImpersonation() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantInfo, tt.row.ToSessionInfo(), cmp.Comparer(func(a, b accesstypes.Principal) bool { return a == b }), cmp.Comparer(func(a, b accesstypes.PermissionMask) bool { return a.String() == b.String() })); diff != "" {
				t.Errorf("ToSessionInfo() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
