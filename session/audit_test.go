package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// recordMatcher matches the record a storage call is handed where the session library
// records the call's act: one, and one a row of the log takes (auditlog.Record.Validate).
// A confirmation's record is handed without the addresses, which the store fills from
// what the account holds in its transaction (addressesLeft): it validates once they are
// filled. What a record holds is pinned by TestAuth_ownActsAreRecorded and its
// neighbors.
type recordMatcher struct {
	addressesLeft bool
	// subjectLeft is a device's redemption: a token made by the device, handed with no
	// subject, since the store names the account that approved the code
	// (sessionstorage.AuthStore.RedeemDeviceCode).
	subjectLeft bool
}

func (m recordMatcher) Matches(x any) bool {
	r, ok := x.(*auditlog.Record)
	if !ok || r == nil {
		return false
	}
	if m.subjectLeft {
		if r.Act != auditlog.ActTokenMade || r.Source.Columns()["Process"] != auditlog.ProcessDevice || r.SubjectID != "" || r.SubjectName != "" {
			return false
		}
		approved := *r
		approved.SubjectID, approved.SubjectName = "approver", "pat"
		r = &approved
	}

	return finished(r, m.addressesLeft).Validate() == nil
}

func (m recordMatcher) String() string {
	switch {
	case m.addressesLeft:
		return "is the record of an address confirmed, which validates once the store fills the addresses"
	case m.subjectLeft:
		return "is the record of a device's redemption, with no subject, which validates once the store names the approving account"
	default:
		return "is the record of an act, which validates"
	}
}

// finished is r as the store writes it: a confirmation's record (addressesLeft) with the
// new address the store fills in.
func finished(r *auditlog.Record, addressesLeft bool) *auditlog.Record {
	if !addressesLeft {
		return r
	}
	filled := *r
	filled.NewValue = "filled@harbor.test"

	return &filled
}

// aRecord matches the record of a recorded storage call (recordMatcher).
func aRecord() gomock.Matcher {
	return recordMatcher{addressesLeft: false, subjectLeft: false}
}

// aConfirmationRecord matches the record of an address confirmed by a link, handed
// without the addresses (recordMatcher).
func aConfirmationRecord() gomock.Matcher {
	return recordMatcher{addressesLeft: true, subjectLeft: false}
}

// aDeviceRecord matches the record of a device's redemption, handed with no subject
// (recordMatcher).
func aDeviceRecord() gomock.Matcher {
	return recordMatcher{addressesLeft: false, subjectLeft: true}
}

// actedBy is who a record says acted: a person, by their account and username, or a
// process, by its name.
type actedBy struct {
	person   impulse.UUID
	username string
	process  string
}

// byPerson is a person who acted, by their account and username.
func byPerson(id impulse.UUID, username string) actedBy {
	return actedBy{person: id, username: username, process: ""}
}

// byProcess is a process that acted with no person behind it.
func byProcess(name string) actedBy {
	return actedBy{person: impulse.NilUUID, username: "", process: name}
}

// checkRecord holds the record call was handed to want, its act, subject and parts, and
// to who as who acted; the record must validate, a confirmation's once the store fills
// its addresses (finished).
func checkRecord(t *testing.T, call string, got, want *auditlog.Record, who actedBy) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s was handed no record, want the record of %s", call, want.Act)
	}
	if err := finished(got, want.Act == auditlog.ActAddressChanged).Validate(); err != nil {
		t.Errorf("%s was handed a record that does not validate: %v", call, err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(auditlog.Record{}, "Source")); diff != "" {
		t.Errorf("the record %s was handed (-want +got):\n%s", call, diff)
	}
	columns := got.Source.Columns()
	if who.process != "" {
		if diff := cmp.Diff(map[string]any{"Process": who.process}, columns); diff != "" {
			t.Errorf("who acted, in the record %s was handed (-want +got):\n%s", call, diff)
		}

		return
	}
	if columns["ActorUserId"] != who.person.String() || columns["ActorUsername"] != who.username || columns["Process"] != nil {
		t.Errorf("who acted, in the record %s was handed = %v, want the person %s (%s)", call, columns, who.username, who.person)
	}
}

// adminSession is the context of an administrator's call of the session API: a session
// of the account id, named ada.
func adminSession(id impulse.UUID) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.NewUUID()), Username: "ada", UpdatedAt: time.Now()},
		UserID:      impulse.NullUUIDFromUUID(id),
	})
}

// wantAccount is the record of act done to the account id, named username, with no part.
func wantAccount(act auditlog.Act, id impulse.UUID, username string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID, r.SubjectName = act, auditlog.SubjectAccount, id.String(), username

	return &r
}

// wantSetting is the record of act done to the setting.
func wantSetting(act auditlog.Act, setting string) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.SubjectKind, r.SubjectID = act, auditlog.SubjectSetting, setting

	return &r
}

// keep is a DoAndReturn function's way to hold the record a call was handed.
func keep(into **auditlog.Record, record *auditlog.Record) {
	*into = record
}

// TestAuth_ownActsAreRecorded pins the record each of the session API's own acts hands
// the one storage call it belongs to: the act, done to the account (or the setting) the
// call names, with its parts, by the person of the call's session; a call with no
// session is the application's. A refused change writes nothing, so its record reaches
// no write.
func TestAuth_ownActsAreRecorded(t *testing.T) {
	t.Parallel()

	admin := impulse.Must(impulse.NewUUID())
	byAdmin := byPerson(admin, "ada")
	userID := impulse.Must(impulse.NewUUID())
	user := func(t *testing.T) *sessionstorage.SessionUser {
		t.Helper()

		return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "the old one")}
	}
	renamed := wantAccount(auditlog.ActUsernameChanged, userID, "robin")
	renamed.OldValue, renamed.NewValue = "pat", "robin"
	days := wantSetting(auditlog.ActRememberedBrowsersSet, auditlog.SettingRememberedBrowsers)
	days.NewValue = "30"

	tests := []struct {
		name string
		// ctx is the call's context: the administrator's session, unless a case says.
		ctx func() context.Context
		// act runs the call, with the store's expectations set, and answers the record
		// the recorded write was handed.
		act  func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error)
		want *auditlog.Record
		who  actedBy
	}{
		{
			name: "a password changed",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, _, _ *securehash.Hash, record *auditlog.Record) error {
						keep(&got, record)

						return nil
					})
				err := f.auth.API().ChangeSessionUserPassword(ctx, nil, userID, &ChangeSessionUserPasswordRequest{OldPassword: "the old one", NewPassword: "a new one, chosen with care"})

				return got, err
			},
			want: wantAccount(auditlog.ActPasswordChanged, userID, "pat"), who: byAdmin,
		},
		{
			name: "a password removed",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().RemoveUserPassword(gomock.Any(), userID, gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, _ []impulse.UUID, record *auditlog.Record) error {
						keep(&got, record)

						return nil
					})

				return got, f.auth.API().RemoveSessionUserPassword(ctx, userID)
			},
			want: wantAccount(auditlog.ActPasswordRemoved, userID, "pat"), who: byAdmin,
		},
		{
			name: "a username changed, by its new name, the old one read before the write",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().SetUserUsername(gomock.Any(), userID, "robin", gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, _ string, record *auditlog.Record) error {
						keep(&got, record)

						return nil
					})

				return got, f.auth.API().ChangeSessionUserUsername(ctx, userID, "robin")
			},
			want: renamed, who: byAdmin,
		},
		{
			name: "an account disabled",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().DeactivateUser(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})
				f.store.EXPECT().DestroyUserSessions(gomock.Any(), userID).Return(nil)

				return got, f.auth.API().DeactivateSessionUser(ctx, userID)
			},
			want: wantAccount(auditlog.ActAccountDisabled, userID, "pat"), who: byAdmin,
		},
		{
			name: "an account enabled, by a job of the application's: no session",
			ctx:  context.Background,
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().ActivateUser(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().ActivateSessionUser(ctx, userID)
			},
			want: wantAccount(auditlog.ActAccountEnabled, userID, "pat"), who: byProcess(auditlog.ProcessApplication),
		},
		{
			name: "an account the store does not hold is named by its id, and the write answers its own NotFound",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(nil, httpio.NewNotFoundMessage("no such user"))
				f.store.EXPECT().ActivateUser(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
					keep(&got, record)

					return httpio.NewNotFoundMessage("no such user")
				})
				if err := f.auth.API().ActivateSessionUser(ctx, userID); !httpio.HasNotFound(err) {
					return got, err
				}

				return got, nil
			},
			want: wantAccount(auditlog.ActAccountEnabled, userID, userID.String()), who: byAdmin,
		},
		{
			name: "an account deleted",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().DestroyUserSessions(gomock.Any(), userID).Return(nil)
				f.store.EXPECT().DeleteUser(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().DeleteSessionUser(ctx, userID)
			},
			want: wantAccount(auditlog.ActAccountDeleted, userID, "pat"), who: byAdmin,
		},
		{
			name: "a token revoked",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				tokenID := impulse.Must(impulse.NewUUID())
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil)
				f.store.EXPECT().RevokeUserToken(gomock.Any(), userID, tokenID, sessionstorage.TokenRevokedByHand, gomock.Any()).
					DoAndReturn(func(_ context.Context, _, _ impulse.UUID, _ sessionstorage.TokenRevocation, record *auditlog.Record) error {
						keep(&got, record)

						return nil
					})

				return got, f.auth.API().RevokeToken(ctx, userID, tokenID)
			},
			want: wantAccount(auditlog.ActTokenRevoked, userID, "pat"), who: byAdmin,
		},
		{
			name: "the remembered browsers forgotten at an administrator's reset of the second factor",
			act: func(ctx context.Context, t *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().User(gomock.Any(), userID).Return(user(t), nil).AnyTimes()
				f.store.EXPECT().RevokeTrustedDevices(gomock.Any(), userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().ResetFactors(ctx, userID)
			},
			want: wantAccount(auditlog.ActBrowsersForgotten, userID, "pat"), who: byAdmin,
		},
		{
			name: "a second factor required of everyone",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().SetRequireSecondFactor(gomock.Any(), true, gomock.Any()).DoAndReturn(func(_ context.Context, _ bool, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().SetSecondFactorRequired(ctx, true)
			},
			want: wantSetting(auditlog.ActSecondFactorRequired, auditlog.SettingSecondFactor), who: byAdmin,
		},
		{
			name: "a second factor no longer required",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().SetRequireSecondFactor(gomock.Any(), false, gomock.Any()).DoAndReturn(func(_ context.Context, _ bool, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().SetSecondFactorRequired(ctx, false)
			},
			want: wantSetting(auditlog.ActSecondFactorNotRequired, auditlog.SettingSecondFactor), who: byAdmin,
		},
		{
			name: "how long a browser is remembered, the days in text",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().SetRememberBrowserDays(gomock.Any(), int64(30), gomock.Any()).DoAndReturn(func(_ context.Context, _ int64, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().SetRememberedBrowserDays(ctx, 30)
			},
			want: days, who: byAdmin,
		},
		{
			name: "a person's own password reset turned on",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().SetSelfServiceReset(gomock.Any(), "password", true, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ bool, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().SetSelfServiceReset(ctx, true)
			},
			want: wantSetting(auditlog.ActSelfServiceResetOn, auditlog.SettingSelfServiceReset), who: byAdmin,
		},
		{
			name: "a person's own password reset turned off",
			act: func(ctx context.Context, _ *testing.T, f *authFixture) (*auditlog.Record, error) {
				var got *auditlog.Record
				f.store.EXPECT().SetSelfServiceReset(gomock.Any(), "password", false, gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ bool, record *auditlog.Record) error {
					keep(&got, record)

					return nil
				})

				return got, f.auth.API().SetSelfServiceReset(ctx, false)
			},
			want: wantSetting(auditlog.ActSelfServiceResetOff, auditlog.SettingSelfServiceReset), who: byAdmin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			ctx := adminSession(admin)
			if tt.ctx != nil {
				ctx = tt.ctx()
			}
			got, err := tt.act(ctx, t, f)
			if err != nil {
				t.Fatalf("the call's error = %v", err)
			}
			checkRecord(t, "the write", got, tt.want, tt.who)
		})
	}
}

// TestAuthAPI_CreateApplicationToken_recorded pins the record of a token an
// administrator makes: done to the person whose token it is, named by the token's name,
// never by its secret or its hash.
func TestAuthAPI_CreateApplicationToken_recorded(t *testing.T) {
	t.Parallel()

	admin := impulse.Must(impulse.NewUUID())
	f := newTokenFixture(t, gomock.NewController(t))
	f.store.EXPECT().User(gomock.Any(), f.user.ID).Return(f.user, nil)
	var got *auditlog.Record
	f.store.EXPECT().InsertToken(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *sessionstorage.Token, record *auditlog.Record) error {
		keep(&got, record)

		return nil
	})

	if _, err := f.auth.API().CreateApplicationToken(adminSession(admin), &ApplicationTokenRequest{UserID: f.user.ID, Outlet: droids, Name: " laptop "}); err != nil {
		t.Fatalf("CreateApplicationToken() error = %v", err)
	}
	want := wantAccount(auditlog.ActTokenMade, f.user.ID, "pat")
	want.NewValue = "laptop"
	checkRecord(t, "InsertToken", got, want, byPerson(admin, "ada"))
}

// TestAuth_refusedChangeRecordsNothing pins a change the session API refuses before it
// writes: a wrong old password is a BadRequest, and the store is asked to write nothing,
// so no record reaches a write.
func TestAuth_refusedChangeRecordsNothing(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	f := newAuthFixture(t, gomock.NewController(t))
	f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "the old one")}, nil)
	f.store.EXPECT().ChangeUserPassword(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	err := f.auth.API().ChangeSessionUserPassword(adminSession(impulse.Must(impulse.NewUUID())), nil, userID,
		&ChangeSessionUserPasswordRequest{OldPassword: "not the old one", NewPassword: "a new one, chosen with care"})
	if !httpio.HasBadRequest(err) {
		t.Fatalf("ChangeSessionUserPassword() with a wrong old password error = %v, want a BadRequest", err)
	}
}

// TestAuth_mailedLinksAreRecorded pins the record of each link the session library
// stores for a person's account, and of each invitation it sends and withdraws: who asked
// (an administrator by their session, a visitor at the sign-in page, the migrate command
// for the first administrator), the account or the invitation, its parts, and the
// tenant whose administrator asked, named as the Auth names it, by its key where it has
// no name.
func TestAuth_mailedLinksAreRecorded(t *testing.T) {
	t.Parallel()

	admin := impulse.Must(impulse.NewUUID())
	byAdmin := byPerson(admin, "ada")

	t.Run("an administrator's reset link, in the tenant whose administrator asked", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()

		if err := f.auth.API().SendPasswordReset(adminSession(admin), userID, AskedByTenant("lakeside")); err != nil {
			t.Fatalf("SendPasswordReset() error = %v", err)
		}
		want := wantAccount(auditlog.ActPasswordResetLinkSent, userID, "pat")
		want.TenantID, want.TenantName = "lakeside", "lakeside"
		checkRecord(t, "InsertMailLink", onlyRecord(t, links, links.last(t).ID), want, byAdmin)
	})

	t.Run("a visitor's own reset link, by the sign-in page, in no tenant", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		on := true
		resetSetting(f, &on)
		links.holds("pat@lakeside.edu", passwordPerson(t, userID))
		links.expectAccounts(f)

		if code := requestReset(f, "pat@lakeside.edu"); code != http.StatusOK {
			t.Fatalf("Request() = %d, want 200", code)
		}
		checkRecord(t, "InsertMailLink", onlyRecord(t, links, links.last(t).ID), wantAccount(auditlog.ActPasswordResetLinkSent, userID, "pat"),
			byProcess(auditlog.ProcessSignInPage))
	})

	t.Run("a password chosen at a reset link, by the link", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		person := passwordPerson(t, userID)
		f.store.EXPECT().User(gomock.Any(), userID).Return(person, nil).AnyTimes()
		token := mailedReset(t, f, recorder, userID)
		var got *auditlog.Record
		f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, person.PasswordHash, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ impulse.UUID, _, _ *securehash.Hash, record *auditlog.Record) error {
				keep(&got, record)

				return nil
			})

		rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("Complete() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "ChangeUserPassword", got, wantAccount(auditlog.ActPasswordChosen, userID, "pat"), byProcess(auditlog.ProcessResetLink))
	})

	t.Run("an administrator's address change asked, the old address and the new", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(passwordPerson(t, userID), nil).AnyTimes()
		links.expectAccounts(f)

		if err := f.auth.API().RequestAddressChange(adminSession(admin), userID, "pat@harbor.test", AskedByApplication()); err != nil {
			t.Fatalf("RequestAddressChange() error = %v", err)
		}
		want := wantAccount(auditlog.ActAddressChangeAsked, userID, "pat")
		want.OldValue, want.NewValue = "pat@lakeside.edu", "pat@harbor.test"
		checkRecord(t, "InsertMailLink", onlyRecord(t, links, links.last(t).ID), want, byAdmin)
	})

	t.Run("an address confirmed at an administrator's link, by the link, the addresses left to the store", func(t *testing.T) {
		t.Parallel()
		userID, _ := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		person := passwordPerson(t, userID)
		f.store.EXPECT().User(gomock.Any(), userID).Return(person, nil).AnyTimes()
		links.expectAccounts(f)
		if err := f.auth.API().RequestAddressChange(adminSession(admin), userID, "pat@harbor.test", AskedByApplication()); err != nil {
			t.Fatalf("RequestAddressChange() error = %v", err)
		}
		f.auth.API().WaitForMail()
		msgs := recorder.Messages()
		token := tokenIn(t, msgs[len(msgs)-1].Text, "address")
		var got *auditlog.Record
		f.store.EXPECT().ConfirmAddressByLink(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, c *sessionstorage.AddressConfirmation, record *auditlog.Record) (*sessionstorage.ConfirmedAddress, error) {
				keep(&got, record)

				return links.confirm(f, c, person)
			})

		rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("Confirm() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "ConfirmAddressByLink", got, wantAccount(auditlog.ActAddressChanged, userID, "pat"), byProcess(auditlog.ProcessAddressLink))
	})

	invite := []struct {
		name string
		req  *InvitationRequest
		// want is the record's parts beyond its subject.
		want func(r *auditlog.Record)
	}{
		{
			name: "an invitation into a tenant, with its roles in order",
			req:  &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside", RoleNames: []string{"Teacher", "Reader"}},
			want: func(r *auditlog.Record) {
				r.RoleNames, r.RoleScope, r.TenantID, r.TenantName = []string{"Teacher", "Reader"}, auditlog.RoleScopeTenant, "lakeside", "lakeside"
			},
		},
		{
			name: "an invitation into every tenant",
			req:  &InvitationRequest{Email: "pat@lakeside.edu", EveryTenant: true, RoleNames: []string{"Auditor"}},
			want: func(r *auditlog.Record) {
				r.RoleNames, r.RoleScope = []string{"Auditor"}, auditlog.RoleScopeEveryTenant
			},
		},
		{
			name: "an invitation into the application",
			req:  &InvitationRequest{Email: "pat@lakeside.edu", RoleNames: []string{"Operator"}},
			want: func(r *auditlog.Record) { r.RoleNames, r.RoleScope = []string{"Operator"}, auditlog.RoleScopeGlobal },
		},
		{
			name: "an invitation that makes a tenant, naming no role and no tenant",
			req:  &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: "Lakeside"}},
			want: func(r *auditlog.Record) { r.Form = auditlog.FormNewTenant },
		},
	}
	for _, tt := range invite {
		t.Run(tt.name+", and its withdrawal", func(t *testing.T) {
			t.Parallel()
			f, _, links := newMailFixture(t, gomock.NewController(t))
			ctx := adminSession(admin)

			inv, err := f.auth.API().Invite(ctx, tt.req)
			if err != nil {
				t.Fatalf("Invite() error = %v", err)
			}
			want := &auditlog.Record{Act: auditlog.ActInvited, SubjectKind: auditlog.SubjectInvitation, SubjectID: inv.ID.String(), SubjectName: "pat@lakeside.edu"}
			tt.want(want)
			checkRecord(t, "InsertMailLink", onlyRecord(t, links, inv.ID), want, byAdmin)

			if err := f.auth.API().RevokeInvitation(ctx, inv.ID); err != nil {
				t.Fatalf("RevokeInvitation() error = %v", err)
			}
			records := links.recordsOf(inv.ID)
			withdrawn := &auditlog.Record{Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: inv.ID.String(), SubjectName: "pat@lakeside.edu"}
			checkRecord(t, "RevokeMailLink", records[len(records)-1], withdrawn, byAdmin)
		})
	}

	t.Run("the first administrator's invitation and the one it replaces, by the migrate command", func(t *testing.T) {
		t.Parallel()
		f := newFirstAdministratorFixture(t, nil)
		stale := f.standing("grace@lakeside.edu", 7*24*time.Hour, firstAdministratorRoles...)

		got, err := f.auth.API().ReinviteFirstAdministrator(t.Context(), f.roles, &FirstAdministratorRequest{Email: "ada@lakeside.edu", RoleNames: firstAdministratorRoles})
		if err != nil || got.Outcome != FirstAdministratorInvited {
			t.Fatalf("ReinviteFirstAdministrator() = %+v, %v, want the address invited", got, err)
		}
		withdrawn := &auditlog.Record{Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: stale.ID.String(), SubjectName: "grace@lakeside.edu"}
		checkRecord(t, "RevokeMailLink", onlyRecord(t, f.links, stale.ID), withdrawn, byProcess(auditlog.ProcessMigrate))
		sent := f.links.last(t)
		invited := &auditlog.Record{
			Act: auditlog.ActInvited, SubjectKind: auditlog.SubjectInvitation, SubjectID: sent.ID.String(), SubjectName: "ada@lakeside.edu",
			RoleNames: sent.RoleNames, RoleScope: auditlog.RoleScopeGlobal,
		}
		checkRecord(t, "InsertMailLink", onlyRecord(t, f.links, sent.ID), invited, byProcess(auditlog.ProcessMigrate))
	})
}

// onlyRecord is the one record the writes of the link id were handed.
func onlyRecord(t *testing.T, links *mailLinks, id impulse.UUID) *auditlog.Record {
	t.Helper()

	records := links.recordsOf(id)
	if len(records) != 1 {
		t.Fatalf("the writes of the link %s were handed %d records, want one", id, len(records))
	}

	return records[0]
}

// TestAuth_personsOwnActsAreRecorded pins the record of each act a signed-in person does
// to their own account through the session library's handlers: by the person, done to
// their account, with the act's parts.
func TestAuth_personsOwnActsAreRecorded(t *testing.T) {
	t.Parallel()

	t.Run("a username changed, by its new name", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		c.signedIn()
		c.f.store.EXPECT().UserByUserName(gomock.Any(), "Robin").Return(nil, httpio.NewNotFoundMessage("no such user"))
		var got *auditlog.Record
		c.f.store.EXPECT().SetUserUsername(gomock.Any(), c.userID, "Robin", gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, _ string, record *auditlog.Record) error {
			keep(&got, record)

			return nil
		})
		c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "Robin"}, nil)

		if rr := c.post(c.f.auth.ChangeUsername(), `{"username":"Robin"}`); rr.Code != http.StatusOK {
			t.Fatalf("ChangeUsername() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		want := wantAccount(auditlog.ActUsernameChanged, c.userID, "Robin")
		want.OldValue, want.NewValue = "pat", "Robin"
		checkRecord(t, "SetUserUsername", got, want, byPerson(c.userID, "pat"))
	})

	t.Run("the remembered browsers forgotten", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		var got *auditlog.Record
		c.f.store.EXPECT().RevokeTrustedDevices(gomock.Any(), c.userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, record *auditlog.Record) error {
			keep(&got, record)

			return nil
		})

		if rr := c.serve(c.f.auth.ForgetBrowsers(), c.ctx(), http.MethodPost, ""); rr.Code != http.StatusOK {
			t.Fatalf("ForgetBrowsers() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "RevokeTrustedDevices", got, wantAccount(auditlog.ActBrowsersForgotten, c.userID, "pat"), byPerson(c.userID, "pat"))
	})

	t.Run("an address added, and confirmed by its link in the person's session", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.expectSession(sessionID, pat(userID))
		links.expectAccounts(f)

		if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "pat@lakeside.edu"}, nil); rr.Code != http.StatusOK {
			t.Fatalf("Add() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		asked := wantAccount(auditlog.ActAddressChangeAsked, userID, "pat")
		asked.NewValue = "pat@lakeside.edu"
		checkRecord(t, "InsertMailLink", onlyRecord(t, links, links.last(t).ID), asked, byPerson(userID, "pat"))

		f.auth.API().WaitForMail()
		msgs := recorder.Messages()
		token := tokenIn(t, msgs[len(msgs)-1].Text, "verify")
		var got *auditlog.Record
		f.store.EXPECT().ConfirmAddressByLink(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, c *sessionstorage.AddressConfirmation, record *auditlog.Record) (*sessionstorage.ConfirmedAddress, error) {
				keep(&got, record)

				return links.confirm(f, c, pat(userID))
			})
		if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil); rr.Code != http.StatusOK {
			t.Fatalf("Verify() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "ConfirmAddressByLink", got, wantAccount(auditlog.ActAddressChanged, userID, "pat"), byPerson(userID, "pat"))
	})

	t.Run("a password removed on its proof", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		kept := hashed(t, "kept")
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
		}, nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept}, nil).Times(2)
		var got *auditlog.Record
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ impulse.UUID, _ *securehash.Hash, _ []impulse.UUID, record *auditlog.Record) error {
				keep(&got, record)

				return nil
			})

		r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/api/user/password/remove",
			strings.NewReader(`{"password":"kept"}`))
		r.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, r)
		if rr.Code != http.StatusOK {
			t.Fatalf("RemoveUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "RemoveProvenUserPassword", got, wantAccount(auditlog.ActPasswordRemoved, userID, "pat"), byPerson(userID, "pat"))
	})

	t.Run("a first password chosen with its code", func(t *testing.T) {
		t.Parallel()
		userID, sessionID := newIDs()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		user := directoryPerson(userID)
		f.expectSession(sessionID, user)
		f.store.EXPECT().AppendAuthEvent(gomock.Any(), sessionID, gomock.Any()).Return(nil).AnyTimes()
		code := sendFirstPasswordCode(t, f, recorder, sessionID)
		var got *auditlog.Record
		f.store.EXPECT().SetFirstUserPassword(gomock.Any(), userID, gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, _ *securehash.Hash, record *auditlog.Record) error {
			keep(&got, record)

			return nil
		})

		if rr := f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil); rr.Code != http.StatusOK {
			t.Fatalf("Set() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		checkRecord(t, "SetFirstUserPassword", got, wantAccount(auditlog.ActPasswordChosen, userID, "pat"), byPerson(userID, "pat"))
	})
}
