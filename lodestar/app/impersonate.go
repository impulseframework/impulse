package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// The kinds of impersonation the console offers, as a request names them.
const (
	// kindView views the console as another person: reading only.
	kindView = "view"
	// kindAct acts as another person: everything they may do, except the credential
	// actions, the actions that change who may do what, and the writes of the records
	// that decide what a person may do.
	kindAct = "act"
	// kindRole acts as a role.
	kindRole = "role"
)

// impersonationKinds declares the impersonation the console offers. Impersonation is
// granted with a mask: each kind names the grant that allows it and the widest mask
// that grant gives, and the person who starts a session may ask for a narrower mask,
// never a wider one. To let someone act as a person with no mask is to give them
// everything that person holds, so the three kinds are three grants, each given by the
// role file on purpose:
//
//   - view: Execute on ViewAsUser. The session reads what the person reads and writes
//     nothing.
//   - act: Execute on ActAsUser. The session does what the person may do, except the
//     actions classed credential (router.ClassCredential), so it cannot set, require or
//     remove a password, send a reset link, unlink a sign-in identity, or change a
//     username or an address, and the actions classed access (router.ClassAccess), so
//     it cannot give or remove a role, change a group or what it holds, invite anyone,
//     or make, disable, enable or delete an account, nor rate a pilot, the one action of
//     Lodestar's own that carries the class. The constants are the generated
//     collection's, so the mask names classes the application's actions carry, or this
//     file does not compile. The mask also names the writes of the records that decide
//     what a person may do (standingRecords), so the session keeps the promise the
//     StandIn role makes: it does what the person may do, except change how they sign
//     in or who may do what.
//   - role: Execute on AssumeRole. The session holds what the role holds; the kind says
//     in so many words that it is unmasked.
//
// Whatever the kind and its mask, nobody is the subject of what they do in such a
// session: the identity library refuses every change to the account of the person who
// started it (identity.RefuseImpersonatorAsSubject), so a person who acts as a role that
// manages the crew cannot give their own account a role and keep it afterward. The
// generated write of a record that decides what a person may do asks the same rule
// about the person the row names (identity.RefuseImpersonatorAsRowSubject), so the same
// person does not raise their own clearance by the registry's ordinary write either.
// The class access refuses what that rule cannot see, a role that reaches them through
// a group or a colleague.
//
// Demonstrates: impersonation.grant-mask, impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role, @class.
func impersonationKinds() []session.ImpersonationKind {
	return []session.ImpersonationKind{
		session.ImpersonatesUser(kindView, resources.ViewAsUser, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
		session.ImpersonatesUser(kindAct, resources.ActAsUser, accesstypes.AllowAll().Refusing(
			accesstypes.RefuseClass(router.ClassCredential), accesstypes.RefuseClass(router.ClassAccess),
		).Refusing(standingRecords()...)),
		session.ImpersonatesRole(kindRole, resources.AssumeRole, accesstypes.AllowAll()),
	}
}

// standingRecords are the writes of Lodestar's own records that decide what a person
// may do, which the kind that acts as a person refuses by name, whoever the row names.
// The rule that nobody is the subject of what they do holds the real person behind the
// session alone; a stand-in is not to change anyone's standing, so the mask says it.
// Each record is one the policy reads about the requester:
//
//   - Pilots: a row carries the clearance and the fee limit (subject.clearance,
//     subject.feeLimit), which say the missions a pilot may claim and book.
//   - PilotCertifications: a row is a certification a pilot holds
//     (subject.certifications), which a mission may require.
//   - ClientContacts: a row says which client a member of the portal speaks for
//     (subject.client), and so whose missions they see.
//   - SquadronMemberships: a row is a person's place in a squadron (subject.squadrons)
//     and, through the squadron, in its wing (subject.wings).
//   - HangarPasses: a row lets a person, or every member of a squadron, into a hangar
//     or lets them run it (subject.hangarsOpen, subject.hangarsRun).
//   - Squadrons, its change alone: a squadron's row says which wing the squadron is in,
//     so writing it moves every member's wing. A mask names a record's permission and
//     not a plain field of it, so the squadron's name and callsigns are refused with
//     its wing; a squadron is still added, having no members, and removed, which the
//     database allows only once it has none.
//
// A test reads the same list off the generated collection, so a record that gains a
// subject value or a subject set fails there until it is named here
// (TestActKindRefusesTheRecordsThatDecideWhatAPersonMayDo).
func standingRecords() []accesstypes.MaskRefusal {
	return slices.Concat(
		accesstypes.RefuseWrites(resources.Pilot{}.Resource()),
		accesstypes.RefuseWrites(resources.PilotCertification{}.Resource()),
		accesstypes.RefuseWrites(resources.ClientContact{}.Resource()),
		accesstypes.RefuseWrites(resources.SquadronMembership{}.Resource()),
		accesstypes.RefuseWrites(resources.HangarPass{}.Resource()),
		[]accesstypes.MaskRefusal{accesstypes.RefusePermission(accesstypes.Update, resources.Squadron{}.Resource())},
	)
}

// impersonateRequest is the mint route's body: the kind of impersonation, whom or what
// to act as, and optionally a narrower mask than the kind gives.
type impersonateRequest struct {
	// Kind is "view", "act" or "role".
	Kind string `json:"kind"`
	// Principal is who or what the session operates as. For the kinds "view" and "act"
	// it is the id of the account of the person, which is how the permission store names
	// a person; a username is not accepted, and the session library refuses a principal
	// that is not an id (400). For the kind "role" it is the role's name.
	Principal string `json:"principal"`
	// Mask lists the permission kinds the session keeps; empty asks for none, and the
	// session takes what the kind gives. A kind the grant does not give is refused,
	// never trimmed.
	Mask []accesstypes.Permission `json:"mask"`
	// RefuseClasses and RefuseActions are what the session refuses beside what the kind
	// refuses: classes of action (irreversible), and actions by name (ScrapShip).
	RefuseClasses []accesstypes.ActionClass `json:"refuseClasses"`
	RefuseActions []accesstypes.Resource    `json:"refuseActions"`
	// Reason is free text recorded on the impersonation record.
	Reason string `json:"reason"`
}

// mask is the mask the body asks for: no kinds named and nothing refused is the zero
// mask, which asks for none.
func (r *impersonateRequest) mask() accesstypes.PermissionMask {
	mask := accesstypes.MaskPermissions(accesstypes.AllowAll(), r.Mask...)
	for _, class := range r.RefuseClasses {
		mask = mask.Refusing(accesstypes.RefuseClass(class))
	}
	for _, action := range r.RefuseActions {
		mask = mask.Refusing(accesstypes.RefuseAction(action))
	}

	return mask
}

// viewAsMaxDuration is the hard cap on every minted session: two hours, however long the
// idle timeout would otherwise let it live.
const viewAsMaxDuration = 2 * time.Hour

// impersonateResponse names the session that was minted and says what it may do.
type impersonateResponse struct {
	SessionID impulse.UUID `json:"sessionId"`
	// Mask is the session's mask in words: the kind's, narrowed by what was asked for.
	Mask string `json:"mask"`
}

// Impersonate mints an impersonated session on behalf of the authenticated actor, of one
// of the kinds the console declares (impersonationKinds): a session that views as
// another person, one that acts as another person, or one that acts as a role. The
// declaration does the deciding, and is the only way to the request a session starts
// from (session.Impersonations.Request): a request written out by hand does not
// compile. It reads the actor off the validated session the call arrives on, asks
// whether the actor holds the kind's grant, checked here in the global scope, and
// computes the session's mask, the kind's widest when the body names none and the
// narrower one when it names one. A request for more than the kind gives is refused
// with nothing started.
// The library refuses chaining (an impersonated session cannot mint another) and writes
// the record atomically with the session, the actor by username and by the id of their
// account, which it reads from the actor's own session. The response cookie replaces the
// actor's session; the actor's own session is linked as the source. Every minted session
// carries a two-hour hard cap (MaxDuration), which the banner counts down.
//
// Demonstrates: impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role, impersonation.mask, impersonation.grant-mask, impersonation.max-duration, @manualAddResource.execute, impersonation.identity-proof, impersonation.session-permissions.
func (a *App) Impersonate() http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		var body impersonateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("decoding request: %s", err))
		}

		req, err := a.impersonations.Request(ctx, &session.ImpersonationAsk{
			Kind:        body.Kind,
			Principal:   body.Principal,
			Mask:        body.mask(),
			Reason:      body.Reason,
			MaxDuration: viewAsMaxDuration,
		}, resource.HoldsAction(a.UserPermissions(r), accesstypes.GlobalScope()))
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.Impersonations.Request()"))
		}

		id, err := a.API().StartImpersonatedSession(ctx, w, req)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.AuthAPI.StartImpersonatedSession()"))
		}

		return httpio.NewEncoder(w).Ok(impersonateResponse{SessionID: id, Mask: req.Mask().Words()})
	})
}
