package app

import (
	"encoding/json"
	"net/http"
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
	// actions and what changes who may do what, which is the actions of that class and
	// the writes of the records that decide what a person may do.
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
//     make an account, change who may sign in (a sign-in method opened or made invite
//     only, a sector opened to its directory, a sector's directory-only switch, the
//     reset link at the sign-in page), or create a sector of the person's own
//     (CreateSector, the action of the generated create-sector route, which no grant
//     admits and the mask alone refuses), nor rate a pilot, the one action of
//     Lodestar's own that carries the class. The generator gives the same class to
//     the writes of the records that decide what a person may do: the create, the
//     change and the delete of each record the policy reads as a subject value or a
//     subject set (Pilots, PilotCertifications, ClientContacts, SquadronMemberships,
//     HangarPasses), the change of the record a value is read through (Squadrons,
//     whose row says which wing its members are in), and the delete of the tenant
//     record (Sectors: a sector that goes takes every role held in it). Nobody lists
//     them here, and a
//     record that gains a subject value is refused once the application is generated
//     again, so the session keeps the promise the StandIn role makes: it does what
//     the person may do, except change how they sign in or who may do what. One
//     thing is kept out of that refusal by name: the writes of HangarPasses
//     (accesstypes.RefuseClassExcept with accesstypes.ExceptWrites). Giving a hangar
//     pass is the marshal's daily work, so a person standing in for the marshal
//     gives, changes and withdraws passes, and everything else the class holds stays
//     refused, whatever joins the class later. An exception that named a record the
//     application lacks, or one the class does not cover, would stop the server at
//     start-up. An
//     account disabled, enabled or deleted carries neither class, so the session does
//     those as the person would, on any account but the real person's own (the rule
//     below). The constants are the generated collection's, so the mask names classes
//     the application carries, or this file does not compile.
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
// A row that reaches a person without naming them is held to the same rule: a hangar
// pass given to a squadron the real person is in
// (identity.RefuseImpersonatorAsSetSubject), and their own squadron moved to another
// wing (identity.RefuseImpersonatorAsReadThroughSubject). The generated writes of
// HangarPasses and Squadrons ask both, under every mask. So the exception the act kind
// keeps lets a person standing in for the marshal give other people and other
// squadrons a pass, and never themselves or a squadron they are in, and the role
// kind, which has no mask, moves other people's squadrons to another wing and never
// the real person's own.
//
// A row removed with its parent, which may name the person, is a limit of its own: a
// hangar pass goes with its hangar, its squadron or its sector, a hangar goes with its
// sector too, and no write of the pass is made for the rule or for the class to
// refuse. Neither kind refuses those deletes here. Every pass gives something and none
// restricts, so such a delete only takes away; an application whose rows restrict (a
// ban, a cap) refuses in the kind's mask each delete that reaches the row, the parent's
// and that of each record above it (accesstypes.RefusePermission(accesstypes.Delete,
// parent)), and by name an action that removes one of them (accesstypes.RefuseAction).
// Lodestar's foreign keys remove a row with its parent or refuse the parent's delete;
// none changes a row where it stands, as PostgreSQL's ON DELETE SET NULL and ON UPDATE
// CASCADE do.
//
// A column that records who made a row is another (a mission's booker,
// Mission.BookedByUserID, which grants compare to the requester): the rule does not
// read it. Nobody writes those columns here. Each is stamped from the session
// (resources.CurrentUser), so no write puts a row in a name the session chose: in a
// session that acts as a person the stamp is that person, and in one that acts as a
// role it is the person who started it, since a role is not anyone. That is the stated
// case, and Lodestar keeps it. A mission booked acting as the Sector Marshal is booked
// by the person who started the session, and what the grants give a mission's booker
// (the Flight Lead reads it, the Booking Agent stands it down) is theirs after the
// session too, for a mission their own role might not have booked
// (TestImpersonation_authorUnderARole pins it). An application that does not want that
// refuses the record's create in the role kind's mask
// (accesstypes.RefusePermission(accesstypes.Create, record)), or stamps the column with
// nobody in a session that acts as a role.
//
// Demonstrates: impersonation.grant-mask, impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role, @class.
func impersonationKinds() []session.ImpersonationKind {
	return []session.ImpersonationKind{
		session.ImpersonatesUser(kindView, resources.ViewAsUser, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
		session.ImpersonatesUser(kindAct, resources.ActAsUser, accesstypes.AllowAll().Refusing(
			accesstypes.RefuseClass(router.ClassCredential),
			accesstypes.RefuseClassExcept(router.ClassAccess, accesstypes.ExceptWrites(resources.HangarPass{}.Resource())...),
		)),
		session.ImpersonatesRole(kindRole, resources.AssumeRole, accesstypes.AllowAll()),
	}
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
	// ExceptWrites are the records whose writes the session keeps out of each class
	// it names in RefuseClasses (HangarPasses). A class the body names is refused
	// whole unless the body names what it keeps, so a body that names the class
	// access and no record drops the exception the act kind carries, and one that
	// names a record the kind does not keep is refused, never trimmed.
	ExceptWrites []accesstypes.Resource `json:"exceptWrites"`
	// Reason is free text recorded on the impersonation record.
	Reason string `json:"reason"`
}

// mask is the mask the body asks for: no kinds named and nothing refused is the zero
// mask, which asks for none.
func (r *impersonateRequest) mask() accesstypes.PermissionMask {
	mask := accesstypes.MaskPermissions(accesstypes.AllowAll(), r.Mask...)
	var kept []accesstypes.MaskException
	for _, record := range r.ExceptWrites {
		kept = append(kept, accesstypes.ExceptWrites(record)...)
	}
	for _, class := range r.RefuseClasses {
		mask = mask.Refusing(accesstypes.RefuseClassExcept(class, kept...))
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
