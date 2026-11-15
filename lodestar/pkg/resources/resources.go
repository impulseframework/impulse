// Package resources provides the resource types for Lodestar: the rescue and salvage
// service's sectors, clients, ships, crews, missions, refits, and cargo. Every permission
// rule in the application is either structural (an annotation here) or a conditional
// grant in an auth package's role file; no handler carries one. Each struct annotated
// with @resource describes one table of schema/migrations; the generator derives the
// handlers, routes, permission collection, and both TypeScript clients from them.
package resources

import (
	"context"
	"fmt"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

func defaultConfig() resource.Config {
	return resource.Config{
		TrackChanges: false,
	}
}

// currentUser is a FieldDefaultFunc that stamps the requesting user onto rows the server
// attributes to their author (Mission.BookedByUserID, DistressCall.FiledByUserID, Refit.OpenedByUserID,
// Commendation.AwardedByUserID). The fields are output_only, so the wire can never supply them:
// authorship always comes from the session, as CurrentUser reads it, which is also the
// value grant conditions compare subject against (bookedBy = subject, filedBy = subject).
// The droids outlet binds requests to its service account, so the session is present on
// every mutation path.
//
// Demonstrates: default_create_fn, output_only, consolidation.batch, workflow.dot, authz-matrix.
func currentUser(ctx context.Context, _ resource.ReadWriteTransaction) (any, error) {
	return string(CurrentUser(ctx)), nil
}

// currentUserName is the FieldDefaultFunc of the name column beside each of those
// columns (Mission.BookedByName and the others): the author's username as it is when the
// row is written, as CurrentUserName reads it. The column is a snapshot for people to
// read, since the column beside it holds an account id, which no page shows; a username
// that changes later leaves the rows already written as they were.
//
// Demonstrates: default_create_fn, output_only.
func currentUserName(ctx context.Context, _ resource.ReadWriteTransaction) (any, error) {
	return CurrentUserName(ctx), nil
}

// CurrentUser is the person a row is attributed to, named as the permission store names
// them: by the id of their account. It is the identity the request's permission check
// runs as, so a grant such as bookedBy = subject holds for the row's author: the caller
// the generated method frame put on the context (resource.CallerFrom), and otherwise,
// on the resource routes, what resource.SessionPermissions composes from the session,
// which is the session's principal for an ordinary or a view-as session and the actor who
// established it for a session that acts as a role (a role is not anyone).
func CurrentUser(ctx context.Context) accesstypes.User {
	if caller := resource.CallerFrom(ctx); !caller.IsZero() {
		return caller.Permissions.User()
	}
	if user, isUser := sessioninfo.PrincipalFromCtx(ctx).User(); isUser {
		return user
	}

	return sessioninfo.ActorUserFromCtx(ctx)
}

// CurrentUserName is the username of the person CurrentUser names, for people to read:
// the session's username, and for a session that acts as a role the username of the
// actor who established it.
func CurrentUserName(ctx context.Context) string {
	if _, isRole := sessioninfo.PrincipalFromCtx(ctx).Role(); isRole {
		return sessioninfo.ActorFromCtx(ctx)
	}

	return sessioninfo.FromCtx(ctx).Username
}

// defaultCaseNumber issues the server-assigned distress-call case number; the field is
// output_only so a client-supplied value is rejected rather than ignored.
//
// Demonstrates: default_create_fn.
func defaultCaseNumber(_ context.Context, _ resource.ReadWriteTransaction) (any, error) {
	id, err := impulse.NewUUID()
	if err != nil {
		return nil, fmt.Errorf("generating case number: %w", err)
	}

	return fmt.Sprintf("DC-%s", id.String()[:8]), nil
}
