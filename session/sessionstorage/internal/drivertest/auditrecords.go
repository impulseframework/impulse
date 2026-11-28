package drivertest

// The audit records a storage write is handed. Each write of the list takes the record of
// its act as its last parameter and writes it in its own transaction, after its own work:
// handed nil it writes no row and does what it always did; handed a record it writes
// exactly that one row; and a write that is refused, or finds nothing to change, writes
// none. The suite reads the rows back by their columns' names (AuthHarness.AuditRows).

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// auditRun is one run of a recorded write over an instance: the write, the record it is
// handed, and the row it is to leave.
type auditRun struct {
	// write runs the write, handed record.
	write func(record *auditlog.Record) error
	// record is the row the write is handed; want is the row it leaves where it differs
	// (the flow writes the addresses of a confirmation on its copy), nil for record.
	record *auditlog.Record
	want   *auditlog.Record
	// fails says a run that leaves no row is refused; one that is not finds nothing to
	// change and answers nil.
	fails bool
	// held, where set, fails the test unless what the run was to change is as it was.
	held func(t *testing.T)
	// secrets are what the run wrote that no row of the audit table holds: a password's
	// hash, a token's, a link's, a remembered browser's, and a device's code and its
	// hash.
	secrets []writtenSecret
}

// writtenSecret is a secret a run wrote, and what it is.
type writtenSecret struct {
	what  string
	value []byte
}

// hashSecret is a password hash as its column keeps it.
func hashSecret(t *testing.T, hash *securehash.Hash) writtenSecret {
	t.Helper()

	text, err := hash.MarshalText()
	if err != nil {
		t.Fatalf("securehash.Hash.MarshalText() error = %v", err)
	}

	return writtenSecret{what: "a password hash", value: text}
}

// auditedWrite is one write of the list: run readies a run of it on in that takes effect
// (the n-th of the case, which keeps its rows apart from the others'), and none one that
// takes no effect.
type auditedWrite struct {
	name string
	run  func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun
	none func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun
}

// auditSource is who acts in the suite's records: the application's own code.
func auditSource() eventsource.EventSource {
	return eventsource.ProcessEvent(auditlog.ProcessApplication)
}

// accountRow is the record of act done to user's account.
func accountRow(act auditlog.Act, user *dbtype.SessionUser) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditSource(), auditlog.SubjectAccount, user.ID.String(), user.Username

	return &r
}

// settingRow is the record of act done to a setting of the Auth's sign-in.
func settingRow(act auditlog.Act, setting, name string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditSource(), auditlog.SubjectSetting, setting, name

	return &r
}

// unrecordable is a record no write takes: it names no act, so it does not validate,
// and the write it is handed fails and writes nothing.
func unrecordable() *auditlog.Record {
	var r auditlog.Record
	r.Source, r.SubjectKind, r.SubjectID = auditSource(), auditlog.SubjectSetting, auditlog.SettingSecondFactor

	return &r
}

// auditUser creates a user named for the case's run, holding hash where it is not nil.
func auditUser(ctx context.Context, t *testing.T, d AuthDriver, name string, n byte, hash []byte) *dbtype.SessionUser {
	t.Helper()

	insert := &dbtype.InsertSessionUser{Username: fmt.Sprintf("%s-%d@lakeside.edu", name, n)}
	if hash != nil {
		insert.PasswordHash = passwordHashes(t, string(hash))[0]
	}
	user, err := d.CreateUser(ctx, insert, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", insert.Username, err)
	}

	return user
}

// unknownUser is an account no row holds.
func unknownUser() *dbtype.SessionUser {
	return &dbtype.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "nobody@lakeside.edu"}
}

// auditedWrites is the list of the writes that take a record.
func auditedWrites() []auditedWrite {
	return slices.Concat(auditedPasswordWrites(), auditedAccountWrites(), auditedLinkAndTokenWrites(), auditedSettingWrites())
}

func auditedPasswordWrites() []auditedWrite {
	return []auditedWrite{
		{
			name: "ChangeUserPassword",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "change", n, []byte("old"))
				chosen := passwordHashes(t, "new")[0]
				secrets := []writtenSecret{hashSecret(t, user.PasswordHash), hashSecret(t, chosen)}

				return auditRun{record: accountRow(auditlog.ActPasswordChanged, user), secrets: secrets, write: func(record *auditlog.Record) error {
					return in.Driver.ChangeUserPassword(ctx, user.ID, user.PasswordHash, chosen, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "change", n, []byte("old"))
				hashes := passwordHashes(t, "not the one held", "new")
				secrets := []writtenSecret{hashSecret(t, user.PasswordHash), hashSecret(t, hashes[1])}

				return auditRun{record: accountRow(auditlog.ActPasswordChanged, user), secrets: secrets, fails: true, held: passwordHeld(ctx, in, user), write: func(record *auditlog.Record) error {
					return in.Driver.ChangeUserPassword(ctx, user.ID, hashes[0], hashes[1], record)
				}}
			},
		},
		{
			name: "SetFirstUserPassword",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "first", n, nil)
				chosen := passwordHashes(t, "chosen")[0]

				return auditRun{record: accountRow(auditlog.ActPasswordChosen, user), secrets: []writtenSecret{hashSecret(t, chosen)}, write: func(record *auditlog.Record) error {
					return in.Driver.SetFirstUserPassword(ctx, user.ID, chosen, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "first", n, []byte("held"))
				chosen := passwordHashes(t, "chosen")[0]

				return auditRun{record: accountRow(auditlog.ActPasswordChosen, user), secrets: []writtenSecret{hashSecret(t, user.PasswordHash)}, fails: true, held: passwordHeld(ctx, in, user), write: func(record *auditlog.Record) error {
					return in.Driver.SetFirstUserPassword(ctx, user.ID, chosen, record)
				}}
			},
		},
		{
			name: "RemoveUserPassword",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "remove", n, []byte("held"))
				link := mustLink(ctx, t, in.Driver, user.ID, lakeside(fmt.Sprintf("idp_remove_%d", n)))

				return auditRun{record: accountRow(auditlog.ActPasswordRemoved, user), secrets: []writtenSecret{hashSecret(t, user.PasswordHash)}, write: func(record *auditlog.Record) error {
					return in.Driver.RemoveUserPassword(ctx, user.ID, []impulse.UUID{link.ID}, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A user who holds no password has none to remove: a BadRequest.
				user := auditUser(ctx, t, in.Driver, "remove", n, nil)
				link := mustLink(ctx, t, in.Driver, user.ID, lakeside(fmt.Sprintf("idp_remove_%d", n)))

				return auditRun{record: accountRow(auditlog.ActPasswordRemoved, user), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.RemoveUserPassword(ctx, user.ID, []impulse.UUID{link.ID}, record)
				}}
			},
		},
		{
			name: "RemoveProvenUserPassword",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "proven", n, []byte("held"))
				link := mustLink(ctx, t, in.Driver, user.ID, lakeside(fmt.Sprintf("idp_proven_%d", n)))

				return auditRun{record: accountRow(auditlog.ActPasswordRemoved, user), secrets: []writtenSecret{hashSecret(t, user.PasswordHash)}, write: func(record *auditlog.Record) error {
					return in.Driver.RemoveProvenUserPassword(ctx, user.ID, user.PasswordHash, []impulse.UUID{link.ID}, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "proven", n, []byte("held"))
				link := mustLink(ctx, t, in.Driver, user.ID, lakeside(fmt.Sprintf("idp_proven_%d", n)))
				other := passwordHashes(t, "not the one held")[0]

				return auditRun{record: accountRow(auditlog.ActPasswordRemoved, user), secrets: []writtenSecret{hashSecret(t, user.PasswordHash)}, fails: true, held: passwordHeld(ctx, in, user), write: func(record *auditlog.Record) error {
					return in.Driver.RemoveProvenUserPassword(ctx, user.ID, other, []impulse.UUID{link.ID}, record)
				}}
			},
		},
	}
}

// passwordHeld fails the test unless user still holds the password hash it held.
func passwordHeld(ctx context.Context, in *AuthInstance, user *dbtype.SessionUser) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		if got := mustUser(ctx, t, in.Driver, user.ID); !sameHash(got.PasswordHash, user.PasswordHash) {
			t.Errorf("the password of %s after the run that wrote nothing changed, want it as it was", user.Username)
		}
	}
}

func auditedAccountWrites() []auditedWrite {
	// accountWrite is a write done to an account that is there, which an account no
	// row holds refuses as NotFound.
	accountWrite := func(name string, act auditlog.Act, write func(ctx context.Context, d AuthDriver, id impulse.UUID, record *auditlog.Record) error) auditedWrite {
		return auditedWrite{
			name: name,
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "account", n, nil)

				return auditRun{record: accountRow(act, user), write: func(record *auditlog.Record) error {
					return write(ctx, in.Driver, user.ID, record)
				}}
			},
			none: func(ctx context.Context, _ *testing.T, in *AuthInstance, _ byte) auditRun {
				nobody := unknownUser()

				return auditRun{record: accountRow(act, nobody), fails: true, write: func(record *auditlog.Record) error {
					return write(ctx, in.Driver, nobody.ID, record)
				}}
			},
		}
	}

	return []auditedWrite{
		accountWrite("DeactivateUser", auditlog.ActAccountDisabled, func(ctx context.Context, d AuthDriver, id impulse.UUID, record *auditlog.Record) error {
			return d.DeactivateUser(ctx, id, record)
		}),
		accountWrite("ActivateUser", auditlog.ActAccountEnabled, func(ctx context.Context, d AuthDriver, id impulse.UUID, record *auditlog.Record) error {
			return d.ActivateUser(ctx, id, record)
		}),
		accountWrite("DeleteUser", auditlog.ActAccountDeleted, func(ctx context.Context, d AuthDriver, id impulse.UUID, record *auditlog.Record) error {
			return d.DeleteUser(ctx, id, record)
		}),
		{
			name: "SetUserUsername",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "rename", n, nil)
				renamed := fmt.Sprintf("renamed-%d@lakeside.edu", n)
				record := accountRow(auditlog.ActUsernameChanged, user)
				record.OldValue, record.NewValue, record.SubjectName = user.Username, renamed, renamed

				return auditRun{record: record, write: func(record *auditlog.Record) error {
					return in.Driver.SetUserUsername(ctx, user.ID, renamed, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A name another account holds is a Conflict.
				user := auditUser(ctx, t, in.Driver, "rename", n, nil)
				holder := auditUser(ctx, t, in.Driver, "holder", n, nil)
				record := accountRow(auditlog.ActUsernameChanged, user)
				record.OldValue, record.NewValue = user.Username, holder.Username

				return auditRun{record: record, fails: true, held: func(t *testing.T) {
					t.Helper()

					if got := mustUser(ctx, t, in.Driver, user.ID); got.Username != user.Username {
						t.Errorf("the username after the refused rename = %q, want %q", got.Username, user.Username)
					}
				}, write: func(record *auditlog.Record) error {
					return in.Driver.SetUserUsername(ctx, user.ID, holder.Username, record)
				}}
			},
		},
		{
			name: "SetUserEmail",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "address", n, nil)
				address := fmt.Sprintf("address-%d@lakeside.edu", n)
				record := accountRow(auditlog.ActAddressChanged, user)
				record.NewValue = address

				return auditRun{record: record, write: func(record *auditlog.Record) error {
					return in.Driver.SetUserEmail(ctx, user.ID, &address, record)
				}}
			},
			none: func(ctx context.Context, _ *testing.T, in *AuthInstance, n byte) auditRun {
				nobody := unknownUser()
				address := fmt.Sprintf("address-%d@lakeside.edu", n)
				record := accountRow(auditlog.ActAddressChanged, nobody)
				record.NewValue = address

				return auditRun{record: record, fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.SetUserEmail(ctx, nobody.ID, &address, record)
				}}
			},
		},
		{
			// Taking the address off is a write of its own, which ends the codes mailed
			// to the address with it. The store writes the record it is handed whatever
			// its act, and the log gives taking an address off no act of its own, so the
			// row here is an account's of another act.
			name: "SetUserEmail, taking the address off",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := createUserHolding(ctx, t, in.Driver, fmt.Sprintf("off-%d", n), fmt.Sprintf("off-%d@lakeside.edu", n))

				return auditRun{record: accountRow(auditlog.ActAccountDisabled, user), write: func(record *auditlog.Record) error {
					return in.Driver.SetUserEmail(ctx, user.ID, nil, record)
				}}
			},
			none: func(ctx context.Context, _ *testing.T, in *AuthInstance, _ byte) auditRun {
				nobody := unknownUser()

				return auditRun{record: accountRow(auditlog.ActAccountDisabled, nobody), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.SetUserEmail(ctx, nobody.ID, nil, record)
				}}
			},
		},
		{
			name: "UnlinkIdentity",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "unlink", n, []byte("held"))
				link := mustLink(ctx, t, in.Driver, user.ID, lakeside(fmt.Sprintf("idp_unlink_%d", n)))
				record := accountRow(auditlog.ActIdentityUnlinked, user)
				record.OldValue = "Lakeside"

				return auditRun{record: record, secrets: []writtenSecret{hashSecret(t, user.PasswordHash)}, write: func(record *auditlog.Record) error {
					return in.Driver.UnlinkIdentity(ctx, link.ID, lakesideSignsIn, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "unlink", n, []byte("held"))
				record := accountRow(auditlog.ActIdentityUnlinked, user)
				record.OldValue = "Lakeside"

				return auditRun{record: record, fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.UnlinkIdentity(ctx, impulse.Must(impulse.NewUUID()), lakesideSignsIn, record)
				}}
			},
		},
	}
}

// invitationRow is the record of an invitation into the tenant lakeside of two roles,
// mailed by link: a row that holds two role names.
func invitationRow(link *dbtype.MailLink) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = auditlog.ActInvited, auditSource(), auditlog.SubjectInvitation, link.ID.String(), link.Email
	r.RoleNames, r.RoleScope, r.TenantID, r.TenantName = []string{"Teacher", "Counselor"}, auditlog.RoleScopeTenant, "lakeside", "Lakeside High"

	return &r
}

func auditedLinkAndTokenWrites() []auditedWrite {
	return []auditedWrite{
		{
			name: "InsertMailLink",
			run: func(ctx context.Context, _ *testing.T, in *AuthInstance, n byte) auditRun {
				link := newMailLink(dbtype.MailLinkInvitation, fmt.Sprintf("invited-%d@lakeside.edu", n), tokenHash(n))
				link.TenantID, link.RoleNames = "lakeside", []string{"Teacher", "Counselor"}

				return auditRun{record: invitationRow(link), secrets: linkSecrets(link), write: func(record *auditlog.Record) error {
					return in.Driver.InsertMailLink(ctx, link, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A link under an id another link holds is refused by the table's key.
				link := insertInvitation(ctx, t, in.Driver, fmt.Sprintf("invited-%d@lakeside.edu", n), "lakeside", []string{"Teacher", "Counselor"}, tokenHash(n))
				again := *link
				again.TokenHash = tokenHash(n + 100)

				return auditRun{record: invitationRow(link), secrets: linkSecrets(link), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.InsertMailLink(ctx, &again, record)
				}}
			},
		},
		{
			name: "RevokeMailLink",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				link := insertInvitation(ctx, t, in.Driver, fmt.Sprintf("withdrawn-%d@lakeside.edu", n), "lakeside", []string{"Teacher"}, tokenHash(n))
				record := invitationRow(link)
				record.Act, record.RoleNames, record.RoleScope = auditlog.ActInvitationWithdrawn, nil, ""

				return auditRun{record: record, secrets: linkSecrets(link), write: func(record *auditlog.Record) error {
					return in.Driver.RevokeMailLink(ctx, link.ID, time.Now(), record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A link taken back already is not live: NotFound.
				link := insertInvitation(ctx, t, in.Driver, fmt.Sprintf("withdrawn-%d@lakeside.edu", n), "lakeside", []string{"Teacher"}, tokenHash(n))
				if err := in.Driver.RevokeMailLink(ctx, link.ID, time.Now(), nil); err != nil {
					t.Fatalf("RevokeMailLink() error = %v", err)
				}
				record := invitationRow(link)
				record.Act, record.RoleNames, record.RoleScope = auditlog.ActInvitationWithdrawn, nil, ""

				return auditRun{record: record, secrets: linkSecrets(link), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.RevokeMailLink(ctx, link.ID, time.Now(), record)
				}}
			},
		},
		{
			name: "InsertToken",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "token", n, nil)
				token := newToken(dbtype.TokenAccess, user.ID, tokenHash(n))
				record := accountRow(auditlog.ActTokenMade, user)
				record.NewValue = "deploy"

				return auditRun{record: record, secrets: tokenSecrets(token), write: func(record *auditlog.Record) error {
					return in.Driver.InsertToken(ctx, token, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A token under an id another token holds is refused by the table's key.
				user := auditUser(ctx, t, in.Driver, "token", n, nil)
				token := newToken(dbtype.TokenAccess, user.ID, tokenHash(n))
				insertTokens(ctx, t, in.Driver, token)
				again := *token
				again.TokenHash = tokenHash(n + 100)

				return auditRun{record: accountRow(auditlog.ActTokenMade, user), secrets: tokenSecrets(token), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.InsertToken(ctx, &again, record)
				}}
			},
		},
		{
			name: "RevokeUserToken",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "revoke", n, nil)
				token := newToken(dbtype.TokenAccess, user.ID, tokenHash(n))
				insertTokens(ctx, t, in.Driver, token)

				return auditRun{record: accountRow(auditlog.ActTokenRevoked, user), secrets: tokenSecrets(token), write: func(record *auditlog.Record) error {
					return in.Driver.RevokeUserToken(ctx, user.ID, token.ID, time.Now(), dbtype.TokenRevokedByHand, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// Another user's token is not this user's: NotFound.
				user := auditUser(ctx, t, in.Driver, "revoke", n, nil)
				other := auditUser(ctx, t, in.Driver, "other", n, nil)
				token := newToken(dbtype.TokenAccess, other.ID, tokenHash(n))
				insertTokens(ctx, t, in.Driver, token)

				return auditRun{record: accountRow(auditlog.ActTokenRevoked, user), secrets: tokenSecrets(token), fails: true, write: func(record *auditlog.Record) error {
					return in.Driver.RevokeUserToken(ctx, user.ID, token.ID, time.Now(), dbtype.TokenRevokedByHand, record)
				}}
			},
		},
		{
			name: "RevokeUserTokens",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// Both live tokens of the person go, and the act is one row.
				user := auditUser(ctx, t, in.Driver, "revoke-all", n, nil)
				app, other := newToken(dbtype.TokenAccess, user.ID, tokenHash(n)), newToken(dbtype.TokenAccess, user.ID, tokenHash(n+100))
				insertTokens(ctx, t, in.Driver, app, other)

				return auditRun{record: accountRow(auditlog.ActTokenRevoked, user), secrets: slices.Concat(tokenSecrets(app), tokenSecrets(other)), write: func(record *auditlog.Record) error {
					if err := in.Driver.RevokeUserTokens(ctx, user.ID, dbtype.TokenRevokedWithUser, record); err != nil {
						return errors.Wrap(err, "AuthDriver.RevokeUserTokens()")
					}

					return noLiveToken(ctx, in.Driver, user.ID)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A person whose one token was revoked already holds no live token:
				// nothing changes.
				user := auditUser(ctx, t, in.Driver, "revoke-all", n, nil)
				token := newToken(dbtype.TokenAccess, user.ID, tokenHash(n))
				insertTokens(ctx, t, in.Driver, token)
				if err := in.Driver.RevokeUserToken(ctx, user.ID, token.ID, time.Now(), dbtype.TokenRevokedByHand, nil); err != nil {
					t.Fatalf("RevokeUserToken() error = %v", err)
				}

				return auditRun{record: accountRow(auditlog.ActTokenRevoked, user), secrets: tokenSecrets(token), write: func(record *auditlog.Record) error {
					return in.Driver.RevokeUserTokens(ctx, user.ID, dbtype.TokenRevokedWithUser, record)
				}}
			},
		},
		{
			name: "RedeemDeviceCode",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "device", n, nil)
				code := insertDeviceCode(ctx, t, in, n)
				if err := in.Driver.ApproveDeviceCode(ctx, code.ID, user.ID, time.Now()); err != nil {
					t.Fatalf("ApproveDeviceCode() error = %v", err)
				}
				access, refresh := newFamily(user.ID, tokenHash(n+100), tokenHash(n+200))
				// The record is handed with no subject, and written done to the account that
				// approved the code.
				want := accountRow(auditlog.ActTokenMade, user)
				want.Source = eventsource.ProcessEvent(auditlog.ProcessDevice)
				handed := *want
				handed.SubjectID, handed.SubjectName = "", ""
				secrets := slices.Concat(deviceSecrets(code), tokenSecrets(access), tokenSecrets(refresh))

				return auditRun{record: &handed, want: want, secrets: secrets, write: func(record *auditlog.Record) error {
					return redeemed(in.Driver.RedeemDeviceCode(ctx, code.DeviceCodeHash, time.Now(), devicePollInterval, access, refresh, record))
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A poll of a code nobody approved yet issues no token: pending, and only
				// the poll is written.
				user := auditUser(ctx, t, in.Driver, "device", n, nil)
				code := insertDeviceCode(ctx, t, in, n)
				access, refresh := newFamily(user.ID, tokenHash(n+100), tokenHash(n+200))

				return auditRun{record: accountRow(auditlog.ActTokenMade, user), secrets: deviceSecrets(code), write: func(record *auditlog.Record) error {
					_, status, err := in.Driver.RedeemDeviceCode(ctx, code.DeviceCodeHash, time.Now(), devicePollInterval, access, refresh, record)
					if err != nil {
						return errors.Wrap(err, "AuthDriver.RedeemDeviceCode()")
					}
					if status != dbtype.DevicePending {
						return errors.Newf("RedeemDeviceCode() of a code nobody approved = %q, want pending", status)
					}

					return nil
				}}
			},
		},
		{
			name: "RevokeTrustedDevices",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				user := auditUser(ctx, t, in.Driver, "browsers", n, nil)
				if err := in.Driver.InsertTrustedDevice(ctx, newTrustedDevice(user.ID, tokenHash(n))); err != nil {
					t.Fatalf("InsertTrustedDevice() error = %v", err)
				}
				secrets := []writtenSecret{{what: "a remembered browser's token hash", value: tokenHash(n)}}

				return auditRun{record: accountRow(auditlog.ActBrowsersForgotten, user), secrets: secrets, write: func(record *auditlog.Record) error {
					return in.Driver.RevokeTrustedDevices(ctx, user.ID, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A user with no remembered browser has none to forget: nothing changes.
				user := auditUser(ctx, t, in.Driver, "browsers", n, nil)

				return auditRun{record: accountRow(auditlog.ActBrowsersForgotten, user), write: func(record *auditlog.Record) error {
					return in.Driver.RevokeTrustedDevices(ctx, user.ID, record)
				}}
			},
		},
		{
			name: "ClaimVerifiedEmail",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A user with no confirmed address takes the one a sign-in's method vouches
				// for; the row keeps the address the account held before, which the driver
				// reads in the claim's transaction, on its copy.
				user := createUserHolding(ctx, t, in.Driver, fmt.Sprintf("vouched-%d", n), fmt.Sprintf("vouched-%d@work.example", n))
				address := fmt.Sprintf("vouched-%d@gmail.example", n)
				record := accountRow(auditlog.ActAddressChanged, user)
				record.NewValue = address
				want := *record
				want.OldValue = fmt.Sprintf("vouched-%d@work.example", n)

				return auditRun{record: record, want: &want, write: func(record *auditlog.Record) error {
					return claimed(in.Driver.ClaimVerifiedEmail(ctx, user.ID, address, record))
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A user who holds a confirmed address keeps it: nothing is claimed.
				user := createUserConfirmed(ctx, t, in.Driver, fmt.Sprintf("vouched-%d", n), fmt.Sprintf("vouched-%d@lakeside.edu", n))
				address := fmt.Sprintf("vouched-%d@gmail.example", n)
				record := accountRow(auditlog.ActAddressChanged, user)
				record.NewValue = address

				return auditRun{record: record, write: func(record *auditlog.Record) error {
					if took, err := in.Driver.ClaimVerifiedEmail(ctx, user.ID, address, record); err != nil || took {
						return errors.Newf("ClaimVerifiedEmail() over a confirmed address = %t, %v, want nothing claimed", took, err)
					}

					return nil
				}}
			},
		},
		{
			name: "ConfirmAddressByLink",
			run: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// The record is handed with who acted and the account; the flow writes the
				// address held before and the one confirmed on its copy.
				user := createUserConfirmed(ctx, t, in.Driver, fmt.Sprintf("confirm-%d", n), fmt.Sprintf("confirm-%d@lakeside.edu", n))
				link := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, user.ID, fmt.Sprintf("moved-%d@lakeside.edu", n), tokenHash(n))
				record := accountRow(auditlog.ActAddressChanged, user)
				record.Source = eventsource.ProcessEvent(auditlog.ProcessAddressLink)
				want := *record
				want.OldValue, want.NewValue = fmt.Sprintf("confirm-%d@lakeside.edu", n), link.Email

				return auditRun{record: record, want: &want, secrets: linkSecrets(link), write: func(record *auditlog.Record) error {
					_, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, nil), record)

					return err
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				// A link followed already is no longer live: NotFound.
				user := createUserConfirmed(ctx, t, in.Driver, fmt.Sprintf("confirm-%d", n), fmt.Sprintf("confirm-%d@lakeside.edu", n))
				link := insertLinkFor(ctx, t, in.Driver, dbtype.MailLinkAddressChange, user.ID, fmt.Sprintf("moved-%d@lakeside.edu", n), tokenHash(n))
				if _, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, nil), nil); err != nil {
					t.Fatalf("ConfirmAddressByLink() error = %v", err)
				}
				record := accountRow(auditlog.ActAddressChanged, user)

				return auditRun{record: record, secrets: linkSecrets(link), fails: true, write: func(record *auditlog.Record) error {
					_, err := in.ConfirmAddressByLink(ctx, confirmationOf(link, nil), record)

					return err
				}}
			},
		},
	}
}

// linkSecrets are the secrets of a mail link: its token's hash.
func linkSecrets(link *dbtype.MailLink) []writtenSecret {
	return []writtenSecret{{what: "a mail link's token hash", value: link.TokenHash}}
}

// tokenSecrets are the secrets of a token: its hash.
func tokenSecrets(token *dbtype.Token) []writtenSecret {
	return []writtenSecret{{what: "a token's hash", value: token.TokenHash}}
}

// claimed answers the error of a claim, and an error where it claimed nothing.
func claimed(took bool, err error) error {
	if err == nil && !took {
		return errors.New("ClaimVerifiedEmail() of a user with no confirmed address claimed nothing, want the address")
	}

	return err
}

// noLiveToken answers an error where userID still holds a live token.
func noLiveToken(ctx context.Context, d AuthDriver, userID impulse.UUID) error {
	tokens, err := d.TokensByUser(ctx, userID, time.Now())
	if err != nil {
		return errors.Wrap(err, "AuthDriver.TokensByUser()")
	}
	if len(tokens) != 0 {
		return errors.Newf("%d token(s) of the person still live, want none", len(tokens))
	}

	return nil
}

// deviceSecrets are the secrets of a device code: its hash and the code a person types.
func deviceSecrets(code *dbtype.DeviceCode) []writtenSecret {
	return []writtenSecret{{what: "a device code's hash", value: code.DeviceCodeHash}, {what: "a device's user code", value: []byte(code.UserCode)}}
}

// insertDeviceCode inserts a pending device code, the case's n-th.
func insertDeviceCode(ctx context.Context, t *testing.T, in *AuthInstance, n byte) *dbtype.DeviceCode {
	t.Helper()

	code := newDeviceCode(fmt.Sprintf("CODE-%03d", n), tokenHash(n+50))
	if err := in.Driver.InsertDeviceCode(ctx, code); err != nil {
		t.Fatalf("InsertDeviceCode() error = %v", err)
	}

	return code
}

// redeemed answers the error of a redemption, and an error where it issued no tokens.
func redeemed(_ *dbtype.DeviceCode, status dbtype.DeviceCodeStatus, err error) error {
	if err == nil && status != dbtype.DeviceApproved {
		return errors.Newf("RedeemDeviceCode() of an approved code = %q, want approved", status)
	}

	return err
}

func auditedSettingWrites() []auditedWrite {
	// settingWrite is a write of the Auth's sign-in settings, which nothing refuses: a
	// run that takes no effect is handed a record that does not validate, and the
	// setting stands as it was (read).
	settingWrite := func(name string, record func(n byte) *auditlog.Record, write func(ctx context.Context, d AuthDriver, n byte, record *auditlog.Record) error,
		read func(ctx context.Context, t *testing.T, d AuthDriver, n byte) string,
	) auditedWrite {
		return auditedWrite{
			name: name,
			run: func(ctx context.Context, _ *testing.T, in *AuthInstance, n byte) auditRun {
				return auditRun{record: record(n), write: func(record *auditlog.Record) error {
					return write(ctx, in.Driver, n, record)
				}}
			},
			none: func(ctx context.Context, t *testing.T, in *AuthInstance, n byte) auditRun {
				before := read(ctx, t, in.Driver, n)

				return auditRun{record: unrecordable(), fails: true, held: func(t *testing.T) {
					t.Helper()

					if after := read(ctx, t, in.Driver, n); after != before {
						t.Errorf("the setting after the write whose record failed = %s, want it as it was, %s", after, before)
					}
				}, write: func(record *auditlog.Record) error {
					return write(ctx, in.Driver, n, record)
				}}
			},
		}
	}
	methodName := func(n byte) string {
		return fmt.Sprintf("method-%d", n)
	}
	methodRow := func(ctx context.Context, t *testing.T, d AuthDriver, n byte) string {
		t.Helper()

		methods, err := d.SignInMethods(ctx)
		if err != nil {
			t.Fatalf("SignInMethods() error = %v", err)
		}
		for _, m := range methods {
			if m.Name == methodName(n) {
				return fmt.Sprintf("open %t, own reset %t", m.OpenSignUp, m.SelfServiceReset)
			}
		}

		return "no row"
	}
	settingsRow := func(ctx context.Context, t *testing.T, d AuthDriver, _ byte) string {
		t.Helper()

		settings, err := d.SignInSettings(ctx)
		if err != nil {
			t.Fatalf("SignInSettings() error = %v", err)
		}
		if settings == nil {
			return "no row"
		}

		return fmt.Sprintf("second factor %t, %d days", settings.RequireSecondFactor, settings.RememberBrowserDays)
	}

	return []auditedWrite{
		settingWrite("SetSignInMethod", func(n byte) *auditlog.Record {
			record := settingRow(auditlog.ActSignUpOpened, auditlog.SignUpSetting(methodName(n)), "Lakeside Google")
			record.RoleNames = []string{"Reader"}

			return record
		}, func(ctx context.Context, d AuthDriver, n byte, record *auditlog.Record) error {
			role := "Reader"

			return d.SetSignInMethod(ctx, &dbtype.SignInMethod{Name: methodName(n), OpenSignUp: true, DefaultRole: &role}, record)
		}, methodRow),
		settingWrite("SetSelfServiceReset", func(_ byte) *auditlog.Record {
			return settingRow(auditlog.ActSelfServiceResetOn, auditlog.SettingSelfServiceReset, "")
		}, func(ctx context.Context, d AuthDriver, n byte, record *auditlog.Record) error {
			return d.SetSelfServiceReset(ctx, methodName(n), true, record)
		}, methodRow),
		settingWrite("SetRequireSecondFactor", func(_ byte) *auditlog.Record {
			return settingRow(auditlog.ActSecondFactorRequired, auditlog.SettingSecondFactor, "")
		}, func(ctx context.Context, d AuthDriver, _ byte, record *auditlog.Record) error {
			return d.SetRequireSecondFactor(ctx, true, record)
		}, settingsRow),
		settingWrite("SetRememberBrowserDays", func(n byte) *auditlog.Record {
			record := settingRow(auditlog.ActRememberedBrowsersSet, auditlog.SettingRememberedBrowsers, "")
			record.NewValue = strconv.Itoa(int(n) + 6)

			return record
		}, func(ctx context.Context, d AuthDriver, n byte, record *auditlog.Record) error {
			return d.SetRememberBrowserDays(ctx, int64(n)+6, record)
		}, settingsRow),
	}
}

// testAuditRecords runs each write of the list on an instance of its own three times:
// handed nil, it takes effect and writes no row; handed a record where it is refused or
// finds nothing to change, it writes none and what it was to change stands; handed a
// record, it takes effect and writes exactly one row, whose columns are the record's.
// No column of the audit table then holds a secret the three runs wrote, in any form
// (wantNoSecret).
func testAuditRecords(_ context.Context, t *testing.T, h *AuthHarness) {
	for _, tt := range auditedWrites() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			in := h.New(ctx, t, Auth, AuthConfig{Auth: true})

			plain := tt.run(ctx, t, in, 1)
			if err := plain.write(nil); err != nil {
				t.Fatalf("%s with no record error = %v", tt.name, err)
			}
			wantAuditRows(ctx, t, h, in, tt.name+" with no record")

			none := tt.none(ctx, t, in, 2)
			err := none.write(none.record)
			switch {
			case none.fails && err == nil:
				t.Errorf("%s that is to be refused answered nil", tt.name)
			case !none.fails && err != nil:
				t.Errorf("%s that finds nothing to change error = %v, want nil", tt.name, err)
			}
			if none.held != nil {
				none.held(t)
			}
			wantAuditRows(ctx, t, h, in, tt.name+" that takes no effect")

			recorded := tt.run(ctx, t, in, 3)
			if err := recorded.write(recorded.record); err != nil {
				t.Fatalf("%s with a record error = %v", tt.name, err)
			}
			want := recorded.record
			if recorded.want != nil {
				want = recorded.want
			}
			wantAuditRows(ctx, t, h, in, tt.name+" with a record", want)
			wantNoSecret(ctx, t, h, in, tt.name, slices.Concat(plain.secrets, none.secrets, recorded.secrets))
		})
	}
}

// wantNoSecret fails the test where a column of a row of the audit records table holds
// one of the secrets, as it is, in hex or in base64.
func wantNoSecret(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, what string, secrets []writtenSecret) {
	t.Helper()

	rows := h.AuditRows(ctx, t, in.Raw)
	for i, row := range rows {
		for column, value := range row {
			text := fmt.Sprint(value)
			for _, secret := range secrets {
				if len(secret.value) == 0 {
					t.Fatalf("%s: %s is empty, which every column holds", what, secret.what)
				}
				forms := []string{
					string(secret.value), hex.EncodeToString(secret.value),
					base64.StdEncoding.EncodeToString(secret.value), base64.RawURLEncoding.EncodeToString(secret.value),
				}
				if slices.ContainsFunc(forms, func(form string) bool { return strings.Contains(text, form) }) {
					t.Errorf("%s: row %d's %s holds %s that the write wrote", what, i, column, secret.what)
				}
			}
		}
	}
}

// testAuditRecordParts holds the parts a row keeps to their values when they are read
// back: a role given with a period and a reason, its two times as they were handed, and
// an invitation of two roles, both names (OtherRoleNames holds the second). No act takes
// two roles and a period at once, so they are two rows. The row is the record's whatever
// the write that carries it.
func testAuditRecordParts(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})

	user := auditUser(ctx, t, in.Driver, "parts", 1, nil)
	given := accountRow(auditlog.ActRoleGiven, user)
	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	given.RoleNames, given.RoleScope, given.TenantID, given.TenantName = []string{"Teacher"}, auditlog.RoleScopeTenant, "lakeside", "Lakeside High"
	given.Period = accesstypes.Period{Start: start, End: start.Add(7 * 24 * time.Hour)}
	given.Reason = "covering the night desk"
	if err := in.Driver.DeactivateUser(ctx, user.ID, given); err != nil {
		t.Fatalf("DeactivateUser() error = %v", err)
	}

	link := newMailLink(dbtype.MailLinkInvitation, "invited@lakeside.edu", tokenHash(2))
	link.TenantID, link.RoleNames = "lakeside", []string{"Teacher", "Counselor"}
	invited := invitationRow(link)
	if err := in.Driver.InsertMailLink(ctx, link, invited); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}

	wantAuditRows(ctx, t, h, in, "a role given with a period, then an invitation of two roles", given, invited)
}

// wantAuditRows fails the test unless the audit records table holds the rows of want, in
// order, and no other: each with the record's columns, by name (auditlog.Record.Columns),
// an id of its own and the time it was written.
func wantAuditRows(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, what string, want ...*auditlog.Record) {
	t.Helper()

	got := h.AuditRows(ctx, t, in.Raw)
	if len(got) != len(want) {
		t.Fatalf("%s: audit rows = %d %v, want %d", what, len(got), got, len(want))
	}
	for i, record := range want {
		columns, err := record.Columns()
		if err != nil {
			t.Fatalf("%s: Record.Columns() error = %v", what, err)
		}
		row := got[i]
		if _, err := impulse.UUIDFromString(fmt.Sprint(row["Id"])); err != nil {
			t.Errorf("%s: row %d's Id = %v, want an id", what, i, row["Id"])
		}
		if at, ok := row["CreatedAt"].(time.Time); !ok || time.Since(at) > time.Hour || time.Until(at) > time.Hour {
			t.Errorf("%s: row %d's CreatedAt = %v, want the time it was written", what, i, row["CreatedAt"])
		}
		delete(columns, "Id")
		delete(row, "Id")
		delete(row, "CreatedAt")
		if diff := cmp.Diff(columns, row); diff != "" {
			t.Errorf("%s: row %d (-the record's columns +read back):\n%s", what, i, diff)
		}
	}
}
