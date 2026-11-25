package identity

import (
	"context"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// TestManager_notFoundPlainly pins what a person is told by each of the package's
// refusals of something that is not there: one sentence that says what is missing, in
// the word its list uses, and no id, no auth's name and no sign-in method's. The id
// stays in the refusal's cause, for the server's log. An invitation that makes a tenant
// is refused by the withdrawal of an ordinary invitation in the same sentence as one
// that does not exist, so the grant that withdraws an ordinary invitation does not
// learn that one stands.
func TestManager_notFoundPlainly(t *testing.T) {
	t.Parallel()

	ahead := time.Now().Add(time.Hour)
	const (
		account     = "This account does not exist."
		group       = "This group does not exist."
		invitation  = "This invitation does not exist."
		noLonger    = "This invitation no longer stands: it was accepted, withdrawn or has expired."
		signInVia   = "This sign-in identity does not exist."
		session     = "This session does not exist."
		impersonate = "This impersonation does not exist."
		token       = "This token is already revoked or expired, or it does not exist."
		wayToSignIn = "This way to sign in does not exist."
	)
	// withdrawn is a manager whose store holds the invitation's link, and an invitation
	// that makes a tenant where makesTenant says so.
	withdrawn := func(t *testing.T, link *MailLink, makesTenant bool) *Manager {
		t.Helper()
		store := &fakeStore{links: map[impulse.UUID]*MailLink{invitationID: link}}
		var opts []Option
		if makesTenant {
			store.invited = []InvitedTenantRow{{ID: invitationID, Email: "founder@dawn.example", NewTenant: `{"name":"Dawn"}`, ExpiresAt: ahead}}
			opts = append(opts, WithTenantMaker(&fakeMaker{}))
		}
		m, _ := newManager(t, store, nil, opts...)

		return m
	}
	// inTenant is the tenant alpha of a manager over the groups' store, whose one person
	// is dana.
	inTenant := func(t *testing.T) *Tenant {
		t.Helper()
		store := groupStore()
		store.tenantPeople = map[accesstypes.Domain][]impulse.UUID{"alpha": {userID}}
		m, _ := newManager(t, store, engineRoles)

		return &Tenant{manager: m, domain: "alpha"}
	}
	// nobody is a manager over a store that holds no account.
	nobody := func(t *testing.T) *Manager {
		t.Helper()
		m, _ := newManager(t, &fakeStore{}, engineRoles)

		return m
	}

	tests := []struct {
		name string
		// call is the method called, on a manager whose store holds what the row names.
		call     func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error
		sentence string
		kept     []string
	}{
		{
			name: "RenameUser, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).RenameUser(ctx, txn, userID, "dana.r")
			},
		},
		{
			name: "DeleteUser, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := nobody(t).DeleteUser(ctx, txn, userID, false)

				return err
			},
		},
		{
			name: "SendPasswordResetLink, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).SendPasswordResetLink(ctx, txn, userID, nil)
			},
		},
		{
			name: "UnlinkIdentity, an identity that does not exist", sentence: signInVia, kept: []string{identityA.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).UnlinkIdentity(ctx, txn, identityA)
			},
		},
		{
			name: "UnlinkIdentity, an identity whose account is gone", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				m, _ := newManager(t, &fakeStore{identities: map[impulse.UUID]impulse.UUID{identityA: userID}}, nil)

				return m.UnlinkIdentity(ctx, txn, identityA)
			},
		},
		{
			name: "RemovePassword, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).RemovePassword(ctx, txn, userID)
			},
		},
		{
			name: "RemovePassword, an account gone between its two reads", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				m, _ := newManager(t, &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}}, nil)

				return m.RemovePassword(ctx, txn, userID)
			},
		},
		{
			name: "DescribeGroup, a group that does not exist", sentence: group, kept: []string{newGroupID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				m, _ := newManager(t, groupStore(), engineRoles)

				return m.DescribeGroup(ctx, txn, newGroupID, "Who works after dark.")
			},
		},
		{
			name: "Tenant.DescribeGroup, a group kept in another tenant", sentence: group, kept: []string{dayID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return inTenant(t).DescribeGroup(ctx, txn, dayID, "Who works by day.")
			},
		},
		{
			name: "Tenant.AddGroupMember, an account that is none of the tenant's people", sentence: account, kept: []string{otherUser.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return inTenant(t).AddGroupMember(ctx, txn, nightID, otherUser)
			},
		},
		{
			name: "WithdrawInvitation, an invitation that does not exist", sentence: invitation, kept: []string{invitationID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).WithdrawInvitation(ctx, txn, invitationID)
			},
		},
		{
			name: "WithdrawInvitation, an invitation that was accepted or withdrawn", sentence: noLonger, kept: []string{invitationID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return withdrawn(t, &MailLink{Invitation: true, Consumed: true, ExpiresAt: ahead}, false).WithdrawInvitation(ctx, txn, invitationID)
			},
		},
		{
			name: "WithdrawInvitation, an invitation that makes a tenant says what one that does not exist says", sentence: invitation, kept: []string{invitationID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return withdrawn(t, &MailLink{Invitation: true, Consumed: false, ExpiresAt: ahead}, true).WithdrawInvitation(ctx, txn, invitationID)
			},
		},
		{
			name: "EndSession, a session that does not exist", sentence: session, kept: []string{sessionA.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				m, _ := newManager(t, &fakeStore{missing: map[impulse.UUID]bool{sessionA: true}}, nil)

				return m.EndSession(ctx, txn, sessionA)
			},
		},
		{
			name: "EndImpersonation, a session established as nobody else", sentence: impersonate, kept: []string{sessionA.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).EndImpersonation(ctx, txn, sessionA)
			},
		},
		{
			name: "RevokeToken, a token that does not exist", sentence: token, kept: []string{tokenA.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).RevokeToken(ctx, txn, tokenA)
			},
		},
		{
			name: "OpenSignUp, a sign-in method the auth does not register", sentence: wayToSignIn, kept: []string{"github", managedAuth},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return newSignUpManager(t, &fakeStore{}).OpenSignUp(ctx, txn, &SignUp{Method: "github", DefaultRole: "Steward"})
			},
		},
		{
			name: "Tenant.RemoveRole, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				tenant := &Tenant{manager: nobody(t), domain: "alpha"}

				return tenant.RemoveRole(ctx, txn, userID, "Reader")
			},
		},
		{
			name: "WithdrawTenantInvitation, an invitation that makes no tenant", sentence: noLonger, kept: []string{invitationID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return newTenantInviter(t, &fakeStore{}, &fakeMaker{}, &fakeMail{}, &fakeGrants{}).WithdrawTenantInvitation(ctx, txn, invitationID)
			},
		},
		{
			name: "RequirePasswordChange, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).RequirePasswordChange(ctx, txn, userID)
			},
		},
		{
			name: "SetEmail, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).SetEmail(ctx, txn, userID, "dana@example.com", nil)
			},
		},
		{
			name: "SetName, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).SetName(ctx, txn, userID, ptr("Dana Reyes"))
			},
		},
		{
			name: "RequireUser, an account that does not exist", sentence: account, kept: []string{userID.String()},
			call: func(t *testing.T, ctx context.Context, txn resource.ReadWriteTransaction) error {
				return nobody(t).RequireUser(ctx, txn, userID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.call(t, ctx, txn)
			})
			notFoundPlainly(t, err, tt.sentence, tt.kept...)
		})
	}
}
