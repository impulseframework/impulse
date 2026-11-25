package generation

import (
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// judgedMethod is one method of the generated management that no role is given: the
// grant of another of its methods judges it.
type judgedMethod struct {
	Method string
	By     string
	Scope  accesstypes.PermissionScope
}

// judgedMethods are the instance's methods no role is given, each with the method whose
// grant judges it, where the instance writes both: sending an invitation again on each
// side, judged by the invitation of the same side, the method that would write the same
// invitation from the same page. Whoever may invite a person into an invitation's roles
// may send it again, so the two acts are judged by one grant and nothing has to be kept
// in step.
func (i *identityInstance) judgedMethods() []judgedMethod {
	var judged []judgedMethod
	// The whole application's invitation and its Send again are written together
	// (writeIdentitySources), under the condition globalGivings names the invitation
	// under.
	if !i.rolesSynced && i.has(identityMemberships) && i.has(identityMailLinks) {
		judged = append(judged, judgedMethod{Method: "Resend" + i.auth + "Invitation", By: "Invite" + i.auth + "User", Scope: accesstypes.GlobalPermissionScope})
	}
	if i.tenant == nil {
		return judged
	}

	// A tenant's methods are written where the tenant's sources hold their files, with
	// every table each needs in the schema, as tenantGivings asks.
	sources := i.tenantMethodSources()
	written := func(method string) bool {
		return slices.ContainsFunc(sources, func(file identitySourceFile) bool {
			return file.tenant && file.name == fileStem(method) &&
				!slices.ContainsFunc(file.needs, func(kind identityTable) bool { return !i.has(kind) })
		})
	}
	subject := i.subject(true)
	method, by := "Resend"+subject+"Invitation", "Invite"+subject+"User"
	if written(method) && written(by) {
		judged = append(judged, judgedMethod{Method: method, By: by, Scope: accesstypes.DomainPermissionScope})
	}

	return judged
}

// collectIdentityJudgedRegistrations marks, for each instance of the generated
// management, its methods no role is given (judgedMethods): each an action no role is
// given, which keeps it out of the role files, judged by the other method's Execute grant
// (resource.CollectionResource.JudgedBy). It runs right after collectRPCRegistrations,
// which registered the methods and their classes; a mark on a name the builder
// registered nothing under is no part of the data, so a run that writes no routes needs
// no guard.
func (r *resourceGenerator) collectIdentityJudgedRegistrations(b *resource.CollectionBuilder) {
	for _, inst := range r.identity {
		for _, judged := range inst.judgedMethods() {
			b.SetActionNoRole(judged.Scope, accesstypes.Resource(judged.Method))
			b.SetMethodJudgedBy(judged.Scope, accesstypes.Resource(judged.Method), accesstypes.Resource(judged.By))
		}
	}
}
