package resource

import (
	"context"
	"fmt"
	"slices"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

// ownAccountsParamKey is the key the accounts that are the caller's own are bound under
// (paramRegistry.bindShared): one array for the statement, whatever number of booleans
// read it.
const ownAccountsParamKey = "ownAccounts"

// sessionOwnAccounts answers the accounts a row keeps a method off for this request's
// caller (@notOnOwnAccount), each as the text of its id and each once: the account the
// session runs as, then, in a session that views as someone else, the account of the
// real person behind it. A request with no session, or a session with no account, has
// none.
func sessionOwnAccounts(ctx context.Context) []string {
	sess, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || sess == nil {
		return nil
	}

	var accounts []string
	if sess.UserID.Valid {
		accounts = append(accounts, sess.UserID.UUID.String())
	}
	if imp := sess.Impersonation; imp != nil && imp.ActorID.Valid {
		if actor := imp.ActorID.UUID.String(); !slices.Contains(accounts, actor) {
			accounts = append(accounts, actor)
		}
	}

	return accounts
}

// ownAccountTerm renders the boolean a method kept off the row of the caller's own
// account asks beside its own: the row's column holds none of the caller's own accounts,
// and a row whose column is NULL is the account of nobody's. outer is the statement's
// name for the row (loweringContext.outer); the accounts are bound once for the
// statement.
func ownAccountTerm(gen *sqlGenerator, outer, column string, accounts []string, registry *paramRegistry) string {
	ref := gen.renderColumnRef(columnRef{qualifier: outer, column: column})
	param := registry.bindShared(ownAccountsParamKey, accounts)
	if gen.dialect == PostgreSQL {
		return fmt.Sprintf("(%s IS NULL OR %s::text <> ALL(%s))", ref, ref, param)
	}

	return fmt.Sprintf("(%s IS NULL OR CAST(%s AS STRING) NOT IN UNNEST(%s))", ref, ref, param)
}
