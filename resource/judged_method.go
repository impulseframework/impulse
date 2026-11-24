package resource

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// judgedDecisions answers the caller's decisions on resources for perm, as
// perms.Check answers them, where an RPC method no role is given is judged by another
// method's Execute grant (CollectionResource.JudgedBy). The engine is asked once, with
// each judged method replaced by the method that judges it, and never about the judged
// method itself, so a grant or a mask that names it changes nothing.
//
// A judged method is answered under its own name: Granted or Denied as the judging
// method is, Denied where the engine answered nothing for it, and, where the judging
// method's grant is conditional, a condition of the judged method alone in which every
// term that reads the judging method's request (new.<name>) reads the attribute <name>
// of the row the judged method names. Every other resource gets the engine's own
// decision. The request's own check (gateFor) and the row's Execute answer
// (checkCapabilityPermissions) both ask it, so the two cannot disagree.
func judgedDecisions(ctx context.Context, perms UserPermissions, collection *GeneratedCollection, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if perm != accesstypes.Execute || collection == nil {
		return perms.Check(ctx, env, scope, perm, resources...)
	}

	judging := make(map[accesstypes.Resource]accesstypes.Resource)
	asked := make([]accesstypes.Resource, 0, len(resources))
	for _, res := range resources {
		ask := res
		if by, judged := collection.JudgedBy(res); judged {
			judging[res] = by
			ask = by
		}
		if !slices.Contains(asked, ask) {
			asked = append(asked, ask)
		}
	}
	if len(judging) == 0 {
		return perms.Check(ctx, env, scope, perm, resources...)
	}

	engine, err := perms.Check(ctx, env, scope, perm, asked...)
	if err != nil {
		return nil, err
	}

	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		by, judged := judging[res]
		if !judged {
			if decision, ok := engine[res]; ok {
				decisions[res] = decision
			}

			continue
		}

		decision, ok := engine[by]
		switch {
		case !ok:
			decisions[res] = accesstypes.Denied()
		case decision.IsConditional():
			expr, err := conditionalExpr(by, decision)
			if err != nil {
				return nil, err
			}
			if !condition.PostImageAgainstLiteralsOnly(expr) {
				return nil, errors.Newf("method %s is judged by the grant of %s, whose condition compares a request field to something other than a literal", res, by)
			}
			decisions[res] = accesstypes.Conditional(accesstypes.ConditionGroup{
				Resources: []accesstypes.Resource{res},
				Condition: accesstypes.ConditionFromExpr(condition.PostImageAsRow(expr)),
			})
		default:
			decisions[res] = decision
		}
	}

	return decisions, nil
}
