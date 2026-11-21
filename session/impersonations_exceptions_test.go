package session

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// exceptionActions is the application the tests of a class's exceptions are held to:
// the grant ActAsUser; the actions GrantSeat and RevokeSeat, of the class access;
// SetPassword, of the classes access and credential; Launch, of no class; the record
// Seats, listed, read, created and updated and never deleted, whose three writes carry
// the class access, with a field that carries Update and one that carries Read alone;
// and the record Ships, whose update carries no class.
func exceptionActions() stubActions {
	access := []accesstypes.ActionClass{accesstypes.ClassAccess}

	return stubActions{
		registered: map[accesstypes.Resource][]accesstypes.Permission{
			"ActAsUser":   {accesstypes.Execute},
			"GrantSeat":   {accesstypes.Execute},
			"RevokeSeat":  {accesstypes.Execute},
			"SetPassword": {accesstypes.Execute},
			"Launch":      {accesstypes.Execute},
			"Seats":       {accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update},
			"Ships":       {accesstypes.List, accesstypes.Read, accesstypes.Update},
		},
		fields: map[accesstypes.Resource]accesstypes.TagPermissions{
			"Seats": {"name": {accesstypes.Read, accesstypes.Update}, "rank": {accesstypes.Read}},
		},
		classes: []accesstypes.ActionClass{accesstypes.ClassAccess, accesstypes.ClassCredential},
		actionClasses: map[accesstypes.Resource][]accesstypes.ActionClass{
			"GrantSeat":   access,
			"RevokeSeat":  access,
			"SetPassword": {accesstypes.ClassAccess, accesstypes.ClassCredential},
		},
		writeClasses: map[accesstypes.Resource][]accesstypes.ActionClass{"Seats": access},
	}
}

// refusingAccessExcept is the mask that keeps every kind and refuses the class access
// except what is named, with the refusals given beside it.
func refusingAccessExcept(exceptions []accesstypes.MaskException, beside ...accesstypes.MaskRefusal) accesstypes.PermissionMask {
	return accesstypes.AllowAll().Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassAccess, exceptions...)).Refusing(beside...)
}

// TestNewImpersonations_exceptions pins what a declaration is refused for, when the
// application starts, over what a mask keeps out of a class it refuses: an exception
// that names what the application does not have, what the class does not cover, or
// what the same mask refuses all the same keeps nothing, and each is refused in a
// sentence that says what is wrong and what to do.
func TestNewImpersonations_exceptions(t *testing.T) {
	t.Parallel()

	one := func(exception accesstypes.MaskException) []accesstypes.MaskException {
		return []accesstypes.MaskException{exception}
	}
	const lead = `the widest mask of the kind "act" `

	tests := []struct {
		name    string
		mask    accesstypes.PermissionMask
		wantErr string
	}{
		{name: "a record's writes, one of which the application never offers", mask: refusingAccessExcept(accesstypes.ExceptWrites("Seats"))},
		{name: "an action", mask: refusingAccessExcept(one(accesstypes.ExceptAction("GrantSeat")))},
		{name: "a field that carries the permission", mask: refusingAccessExcept(one(accesstypes.ExceptPermission(accesstypes.Update, "Seats.name")))},
		{
			name: "an action of two refused classes, kept out of both",
			mask: refusingAccessExcept(one(accesstypes.ExceptAction("SetPassword")),
				accesstypes.RefuseClassExcept(accesstypes.ClassCredential, accesstypes.ExceptAction("SetPassword"))),
		},
		{
			name:    "an action the application does not have",
			mask:    refusingAccessExcept(one(accesstypes.ExceptAction("Nothing"))),
			wantErr: lead + `keeps Execute on "Nothing" out of the refusal of the class "access", and the application registers no Execute on a resource of that name`,
		},
		{
			name: "a field that does not carry the permission",
			mask: refusingAccessExcept(one(accesstypes.ExceptPermission(accesstypes.Update, "Seats.rank"))),
			wantErr: lead + `keeps Update on "Seats.rank" out of the refusal of the class "access", and the application checks no Update on a field of that name: ` +
				`a field is asked about by itself only when it carries the permission, so name such a field, or name the record to keep all of it`,
		},
		{
			name: "an action of another class",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassCredential, accesstypes.ExceptAction("GrantSeat"))),
			wantErr: lead + `keeps Execute on "GrantSeat" out of the refusal of the class "credential", which does not cover it (the classes it carries: access): ` +
				`an exception names something the class refuses, so take it out, or put it on the class that refuses it`,
		},
		{
			name: "an action of no class",
			mask: refusingAccessExcept(one(accesstypes.ExceptAction("Launch"))),
			wantErr: lead + `keeps Execute on "Launch" out of the refusal of the class "access", which does not cover it (the classes it carries: none): ` +
				`an exception names something the class refuses, so take it out, or put it on the class that refuses it`,
		},
		{
			name: "a record's write that carries no class",
			mask: refusingAccessExcept(one(accesstypes.ExceptPermission(accesstypes.Update, "Ships"))),
			wantErr: lead + `keeps Update on "Ships" out of the refusal of the class "access", which does not cover it (the classes it carries: none): ` +
				`an exception names something the class refuses, so take it out, or put it on the class that refuses it`,
		},
		{
			name: "a record's read, which no class covers",
			mask: refusingAccessExcept(one(accesstypes.ExceptPermission(accesstypes.Read, "Seats"))),
			wantErr: lead + `keeps Read on "Seats" out of the refusal of the class "access", which does not cover it (the classes it carries: none): ` +
				`an exception names something the class refuses, so take it out, or put it on the class that refuses it`,
		},
		{
			name: "an action the same mask refuses by name",
			mask: refusingAccessExcept(one(accesstypes.ExceptAction("GrantSeat")), accesstypes.RefuseAction("GrantSeat")),
			wantErr: lead + `keeps Execute on "GrantSeat" out of the refusal of the class "access" and refuses it by name as well: ` +
				`a refusal by name beats an exception, so the exception keeps nothing, and one of the two comes out`,
		},
		{
			name: "a field whose record the same mask refuses by name",
			mask: refusingAccessExcept(one(accesstypes.ExceptPermission(accesstypes.Update, "Seats.name")), accesstypes.RefusePermission(accesstypes.Update, "Seats")),
			wantErr: lead + `keeps Update on "Seats.name" out of the refusal of the class "access" and refuses it by name as well: ` +
				`a refusal by name beats an exception, so the exception keeps nothing, and one of the two comes out`,
		},
		{
			name: "a write of a kind the mask does not keep",
			mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Execute).
				Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptPermission(accesstypes.Update, "Seats"))),
			wantErr: lead + `keeps Update on "Seats" out of the refusal of the class "access", and the mask does not keep the kind Update: ` +
				`the exception keeps nothing, so take it out or keep the kind`,
		},
		{
			name: "an action another refused class carries",
			mask: refusingAccessExcept(one(accesstypes.ExceptAction("SetPassword")), accesstypes.RefuseClass(accesstypes.ClassCredential)),
			wantErr: lead + `keeps Execute on "SetPassword" out of the refusal of the class "access", and it is refused all the same (the mask refuses the class credential): ` +
				`name the exception on that class too, or take it out`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewImpersonations(exceptionActions(), ImpersonatesUser("act", "ActAsUser", tt.mask))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("NewImpersonations() error = %v, want the declaration taken", err)
			case tt.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), "error="+tt.wantErr)):
				// The error opens with where it was made; the sentence is all that follows.
				t.Fatalf("NewImpersonations() error = %v, want the sentence %q", err, tt.wantErr)
			}
		})
	}
}

// TestImpersonations_Request_exceptions pins what a request may do with what its kind
// keeps out of a class: take it all by naming no mask, drop any of it, and never add
// to it. A request that adds is refused whole, with nothing to establish.
func TestImpersonations_Request_exceptions(t *testing.T) {
	t.Parallel()

	kept := append(accesstypes.ExceptWrites("Seats"), accesstypes.ExceptAction("GrantSeat"))
	widest := refusingAccessExcept(kept, accesstypes.RefuseClass(accesstypes.ClassCredential))
	credential := accesstypes.RefuseClass(accesstypes.ClassCredential)

	tests := []struct {
		name        string
		ask         accesstypes.PermissionMask
		wantMask    accesstypes.PermissionMask
		wantStatus  int
		wantMessage string
		// wantCause is what the server's log keeps of a refusal a person reads as one
		// plain sentence; nothing is asked of the cause when it is empty.
		wantCause string
	}{
		{name: "no mask named: all the kind keeps", wantMask: widest},
		{
			name:     "the class refused again with one of the two: the other is dropped",
			ask:      refusingAccessExcept([]accesstypes.MaskException{accesstypes.ExceptAction("GrantSeat")}),
			wantMask: refusingAccessExcept([]accesstypes.MaskException{accesstypes.ExceptAction("GrantSeat")}, credential),
		},
		{
			name:     "the class refused again with nothing kept: both are dropped",
			ask:      accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess)),
			wantMask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess), credential),
		},
		{
			name:     "one of the two refused by name: it is dropped and refused",
			ask:      accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("GrantSeat")),
			wantMask: refusingAccessExcept(accesstypes.ExceptWrites("Seats"), credential, accesstypes.RefuseAction("GrantSeat")),
		},
		{
			name:        "an exception the kind does not carry: refused, not trimmed",
			ask:         refusingAccessExcept([]accesstypes.MaskException{accesstypes.ExceptAction("GrantSeat"), accesstypes.ExceptAction("RevokeSeat")}),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This kind of session gives less than you asked for.",
			wantCause: `the kind "act" gives everything except access actions (still allowed: the action GrantSeat and writing Seats) and credential actions, ` +
				`and the request keeps the action RevokeSeat out of the refusal of access actions as well: ` +
				`a request may drop what the kind keeps and never add to it, so name no more than the kind keeps, or name no mask to take all of it`,
		},
		{
			name:        "an exception that names nothing of the application",
			ask:         refusingAccessExcept([]accesstypes.MaskException{accesstypes.ExceptAction("Nothing")}),
			wantStatus:  http.StatusBadRequest,
			wantMessage: `the mask asked for keeps Execute on "Nothing" out of the refusal of the class "access", and the application registers no Execute on a resource of that name`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declared, err := NewImpersonations(exceptionActions(), ImpersonatesUser("act", "ActAsUser", widest))
			if err != nil {
				t.Fatalf("NewImpersonations() error = %v", err)
			}
			holds := func(context.Context, accesstypes.Resource) (bool, error) {
				return true, nil
			}
			req, err := declared.Request(aliceSignedIn(), &ImpersonationAsk{Kind: "act", Principal: "bob", Mask: tt.ask}, holds)
			if tt.wantStatus != 0 {
				if req != nil {
					t.Errorf("Request() answered a request to establish beside its refusal: %+v", req)
				}
				if got := clientStatus(err); got != tt.wantStatus {
					t.Fatalf("Request() error = %v (status %d), want status %d", err, got, tt.wantStatus)
				}
				if msg := httpio.Message(err); msg != tt.wantMessage {
					t.Errorf("Request() message = %q, want %q", msg, tt.wantMessage)
				}
				if tt.wantCause != "" && !strings.Contains(err.Error(), tt.wantCause) {
					t.Errorf("Request() error = %q, want it to keep %q for the server's log", err.Error(), tt.wantCause)
				}

				return
			}
			if err != nil {
				t.Fatalf("Request() error = %v", err)
			}
			if !req.Mask().Equal(tt.wantMask) {
				t.Errorf("Request().Mask() = %q, want %q", req.Mask().Text(), tt.wantMask.Text())
			}
			if !declared.Kinds()[0].Widest().Equal(widest) {
				t.Errorf("the kind's widest mask is %q after the request, want it unchanged: %q", declared.Kinds()[0].Widest().Text(), widest.Text())
			}
		})
	}
}
