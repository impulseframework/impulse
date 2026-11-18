package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// askedBy is who a message that a request causes says asked for it: an administrator of
// the tenant the request was checked in, or of the application for a request checked in
// no tenant and for a call outside any request.
func askedBy(ctx context.Context) session.AskedBy {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return session.AskedByApplication()
	}
	if domain, tenant := caller.Scope.Domain(); tenant {
		return session.AskedByTenant(string(domain))
	}

	return session.AskedByApplication()
}

// mailAfterCommit registers a mailing for after the transaction's commit, since a
// message cannot be taken back and one about a change that did not commit must not go,
// and tells sent how it went. A refusal the session library words for a person (an
// account with no confirmed address, an address mailed too often) is passed on as the
// reason. A failure that is no refusal (the mail system's, the database's) is logged
// under what, which names the message and never carries what it holds, and reported in
// the session library's words for it, or as a message that could not be sent.
func (m *Manager) mailAfterCommit(txn resource.ReadWriteTransaction, what string, sent func(sent bool, reason string), mail func(ctx context.Context) error) error {
	if err := resource.AfterCommit(txn, func(ctx context.Context) {
		err := mail(ctx)
		reason := ""
		if err != nil {
			reason = httpio.Message(err)
		}
		if err != nil && reason == "" {
			reason = "The message could not be sent. Try again in a few minutes"
		}
		if err != nil && (!httpio.HasClientMessage(err) || httpio.HasInternalServerError(err)) {
			logger.FromCtx(ctx).Errorf("identity: %s could not be mailed: %v", what, err)
		}
		if sent != nil {
			sent(err == nil, reason)
		}
	}); err != nil {
		return errors.Wrap(err, "resource.AfterCommit()")
	}

	return nil
}

// noticeAfterCommit registers work for after the transaction's commit that nothing
// waits on: a notice that tells a person of something done to their account, or the
// withdrawal of links a change made stale. A failure is logged under what and changes
// nothing.
func (m *Manager) noticeAfterCommit(txn resource.ReadWriteTransaction, what string, notice func(ctx context.Context) error) error {
	if err := resource.AfterCommit(txn, func(ctx context.Context) {
		if err := notice(ctx); err != nil {
			logger.FromCtx(ctx).Errorf("identity: %s failed: %v", what, err)
		}
	}); err != nil {
		return errors.Wrap(err, "resource.AfterCommit()")
	}

	return nil
}
