package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// firstAdministratorRoles are the roles the cases' first administrator is invited to.
var firstAdministratorRoles = []string{"SuperAdministrator", "Administrator"}

// firstAdministratorFixture is an Auth with mail and the two tables the first
// administrator's invitation reads: the mail links and the users, which hold nobody
// until a case says so.
type firstAdministratorFixture struct {
	*authFixture
	recorder *mail.Recorder
	links    *mailLinks
	// users is what the store answers for the users table.
	users []*sessionstorage.SessionUser
}

// newFirstAdministratorFixture is the fixture over sender; nil is a recorder, a sender
// that delivers.
func newFirstAdministratorFixture(t *testing.T, sender mail.Sender) *firstAdministratorFixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	recorder := &mail.Recorder{}
	if sender == nil {
		sender = recorder
	}
	f := &firstAdministratorFixture{recorder: recorder}
	f.authFixture = newAuthFixtureWith(t, ctrl, nil, WithMail(sender, testMail))
	f.links = expectMailLinks(f.authFixture)
	f.store.EXPECT().Users(gomock.Any()).DoAndReturn(func(context.Context) ([]*sessionstorage.SessionUser, error) {
		return f.users, nil
	}).AnyTimes()

	return f
}

// standing puts an invitation in the table as an earlier release would have left it:
// into the application, sent by no user.
func (f *firstAdministratorFixture) standing(email string, expiresIn time.Duration, roles ...string) *sessionstorage.MailLink {
	_, hash, err := linkToken()
	if err != nil {
		f.t.Fatalf("linkToken() error = %v", err)
	}
	link := &sessionstorage.MailLink{TokenHash: hash, Kind: sessionstorage.MailLinkInvitation, Email: email, RoleNames: roles, ExpiresAt: time.Now().Add(expiresIn)}
	f.links.insert(link)

	return link
}

// stands reports whether the link is live.
func (f *firstAdministratorFixture) stands(link *sessionstorage.MailLink) bool {
	return f.links.live(link.TokenHash) != nil
}

// mailedTo are the addresses of the messages sent so far.
func (f *firstAdministratorFixture) mailedTo() []string {
	var to []string
	for _, msg := range f.recorder.Messages() {
		to = append(to, msg.To)
	}

	return to
}

func TestAuthAPI_InviteFirstAdministrator(t *testing.T) {
	t.Parallel()

	const ada, grace = "ada@lakeside.edu", "grace@lakeside.edu"
	week := 7 * 24 * time.Hour

	tests := []struct {
		name string
		// sender is the Auth's sender; nil is one that delivers.
		sender mail.Sender
		// arrange sets the tables before the call and answers the links it left, which
		// wantStanding and wantWithdrawn name by position.
		arrange func(f *firstAdministratorFixture) []*sessionstorage.MailLink
		request FirstAdministratorRequest
		want    FirstAdministrator
		// wantErr is the refusal, and wantErrText words the error says.
		wantErr     error
		wantErrText string
		// wantMailed are the addresses mailed by the call.
		wantMailed []string
		// wantStanding and wantDead are the arranged links that stand, and that no
		// longer open, after the call.
		wantStanding []int
		wantDead     []int
	}{
		{
			name:       "no account and nothing standing: the address the deployment names is mailed one invitation",
			request:    FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada},
			wantMailed: []string{ada},
		},
		{
			name: "the Auth has an account: nobody is invited, and the invitation that stands for the address named is left to be accepted",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...)}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorHasAccount},
			wantStanding: []int{0},
		},
		{
			name: "the Auth has an account and the deployment names another address: nobody is invited, and the link mailed to the address taken back no longer opens",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:  FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:     FirstAdministrator{Outcome: FirstAdministratorHasAccount, Withdrawn: []string{grace}},
			wantDead: []int{0},
		},
		{
			name: "the Auth has an account and the deployment names nobody any more: the first administrator's invitation that stood is withdrawn",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:  FirstAdministratorRequest{Email: "", RoleNames: firstAdministratorRoles},
			want:     FirstAdministrator{Outcome: FirstAdministratorHasAccount, Withdrawn: []string{grace}},
			wantDead: []int{0},
		},
		{
			name: "the Auth has an account and the address changed with the roles: the link mailed to the address taken back no longer opens, whatever else it gave beside the Super Administrator role",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(grace, week, "SuperAdministrator", "Retired")}
			},
			request:  FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:     FirstAdministrator{Outcome: FirstAdministratorHasAccount, Withdrawn: []string{grace}},
			wantDead: []int{0},
		},
		{
			name: "the Auth has an account and the roles changed for the address still named: its invitation is left to be accepted, since nobody would be invited in its place",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(ada, week, "SuperAdministrator", "Retired")}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorHasAccount},
			wantStanding: []int{0},
		},
		{
			name: "the Auth has an account: an invitation no user of the Auth sent that does not give the Super Administrator role is another's, as are a user's, a tenant's and one that makes a tenant, and all stand",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}
				sent := f.standing(grace, week, firstAdministratorRoles...)
				sent.InvitedByUserID = impulse.NullUUIDFromUUID(impulse.Must(impulse.NewUUID()))
				tenant := f.standing(grace, week, firstAdministratorRoles...)
				tenant.TenantID = "lakeside"
				makes := f.standing(grace, week, firstAdministratorRoles...)
				makes.NewTenant = &sessioninfo.NewTenant{Name: "Lakeside High"}

				return []*sessionstorage.MailLink{f.standing(grace, week, "Auditor"), sent, tenant, makes}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorHasAccount},
			wantStanding: []int{0, 1, 2, 3},
		},
		{
			name: "an invitation to the address stands: a second release mails nothing, whatever case the address and order the roles are written in",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, "Administrator", "SuperAdministrator")}
			},
			request:      FirstAdministratorRequest{Email: " Ada@Lakeside.edu ", RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorStanding, Email: ada},
			wantStanding: []int{0},
		},
		{
			name: "the invitation ran out: the next release mails a new one",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, -time.Minute, firstAdministratorRoles...)}
			},
			request:    FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada},
			wantMailed: []string{ada},
			wantDead:   []int{0},
		},
		{
			name: "the deployment names another address: the standing invitation is withdrawn and the new address is mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:    FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada, Withdrawn: []string{grace}},
			wantMailed: []string{ada},
			wantDead:   []int{0},
		},
		{
			name: "the release names other roles: the standing invitation, which would give the old ones, is withdrawn and a new one mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, "SuperAdministrator", "Retired")}
			},
			request:    FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada, Withdrawn: []string{ada}},
			wantMailed: []string{ada},
			wantDead:   []int{0},
		},
		{
			name: "two invitations to the address stand: the one that stands longest is kept and the other withdrawn, so one link opens",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...), f.standing(ada, 2*week, firstAdministratorRoles...)}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorStanding, Email: ada, Withdrawn: []string{ada}},
			wantStanding: []int{1},
			wantDead:     []int{0},
		},
		{
			name: "an invitation a user sent, one into a tenant, one that makes a tenant and one no user of the Auth sent that does not give the Super Administrator role are nobody's first administrator: they stand, and the address is mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				sent := f.standing(grace, week, firstAdministratorRoles...)
				sent.InvitedByUserID = impulse.NullUUIDFromUUID(impulse.Must(impulse.NewUUID()))
				tenant := f.standing(grace, week, firstAdministratorRoles...)
				tenant.TenantID = "lakeside"
				makes := f.standing(grace, week, firstAdministratorRoles...)
				makes.NewTenant = &sessioninfo.NewTenant{Name: "Lakeside High"}
				// An administrator of another Auth invited a person to a role of this one:
				// the row names no user of this Auth, and it is not the deployment's.
				another := f.standing(grace, week, "Administrator")

				return []*sessionstorage.MailLink{sent, tenant, makes, another}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada},
			wantMailed:   []string{ada},
			wantStanding: []int{0, 1, 2, 3},
		},
		{
			name:        "no address is named: refused, with nothing mailed",
			request:     FirstAdministratorRequest{Email: "  ", RoleNames: firstAdministratorRoles},
			wantErr:     ErrFirstAdministratorUnnamed,
			wantErrText: "Name the person's email address for this environment, then run this step again",
		},
		{
			name: "no address is named any more: the invitation that stood for the address taken back is withdrawn, and the refusal says so",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:     FirstAdministratorRequest{RoleNames: firstAdministratorRoles},
			wantErr:     ErrFirstAdministratorUnnamed,
			wantErrText: "the invitation mailed to " + grace + " was withdrawn and no new one was sent",
			wantDead:    []int{0},
		},
		{
			name:        "what is named is no address: refused in words, with nothing mailed",
			request:     FirstAdministratorRequest{Email: "ada at lakeside", RoleNames: firstAdministratorRoles},
			wantErrText: "is not an email address",
		},
		{
			name:        "the application writes its mail to the log: refused, and no link is made that a log could hold",
			sender:      mail.Log{},
			request:     FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			wantErr:     ErrFirstAdministratorNoMailbox,
			wantErrText: "an invitation's link is never written to a log",
		},
		{
			name:   "the application writes its mail to the log and an invitation stands: nothing is to send, so nothing is refused",
			sender: mail.Log{},
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...)}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			want:         FirstAdministrator{Outcome: FirstAdministratorStanding, Email: ada},
			wantStanding: []int{0},
		},
		{
			name:   "the application writes its mail to the log and the address changed: the old invitation is withdrawn before the refusal",
			sender: mail.Log{},
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:     FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles},
			wantErr:     ErrFirstAdministratorNoMailbox,
			wantErrText: "the invitation mailed to " + grace + " was withdrawn and no new one was sent",
			wantDead:    []int{0},
		},
		{
			name:        "no role is named: refused before anything is read",
			request:     FirstAdministratorRequest{Email: ada},
			wantErrText: "does not give the SuperAdministrator role",
		},
		{
			name: "the roles named do not give the Super Administrator role: refused before anything is read, since the first administrator is the one who can make another",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			request:      FirstAdministratorRequest{Email: ada, RoleNames: []string{"Administrator"}},
			wantErrText:  "does not give the SuperAdministrator role",
			wantStanding: []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFirstAdministratorFixture(t, tt.sender)
			var arranged []*sessionstorage.MailLink
			if tt.arrange != nil {
				arranged = tt.arrange(f)
			}
			before := len(f.links.rows)

			got, err := f.auth.API().InviteFirstAdministrator(t.Context(), &tt.request)
			checkFirstAdministratorError(t, err, tt.wantErr, tt.wantErrText)
			if err == nil {
				checkFirstAdministrator(t, got, &tt.want)
			}
			if diff := cmp.Diff(tt.wantMailed, f.mailedTo()); diff != "" {
				t.Errorf("addresses mailed mismatch (-want +got):\n%s", diff)
			}
			if made := len(f.links.rows) - before; made != len(tt.wantMailed) {
				t.Errorf("invitations made = %d, want %d: one for each message, and none where nothing is mailed", made, len(tt.wantMailed))
			}
			for _, i := range tt.wantStanding {
				if !f.stands(arranged[i]) {
					t.Errorf("the invitation to %s arranged at %d no longer stands, want it left as it is", arranged[i].Email, i)
				}
			}
			for _, i := range tt.wantDead {
				if f.stands(arranged[i]) {
					t.Errorf("the invitation to %s arranged at %d still stands, want its link dead", arranged[i].Email, i)
				}
			}
			if len(tt.wantMailed) == 0 {
				return
			}
			// What was mailed is an invitation into the application that no user sent,
			// carrying the roles, and its link is in the message alone.
			made := f.links.last(t)
			if made.InvitedByUserID.Valid || made.TenantID != "" || made.NewTenant != nil {
				t.Errorf("the invitation made = %+v, want one into the application that no user sent", made)
			}
			if diff := cmp.Diff(distinctRoleNames(tt.request.RoleNames), made.RoleNames); diff != "" {
				t.Errorf("the invitation's roles mismatch (-want +got):\n%s", diff)
			}
			token := tokenIn(t, f.recorder.Messages()[0].Text, "invitation")
			if hash, ok := linkTokenHash(token); !ok || f.links.live(hash) == nil {
				t.Error("the link mailed does not open the invitation made")
			}
		})
	}
}

// checkFirstAdministratorError holds err to the refusal wanted: is, the sentinel it is,
// and text, words it says; an error is wanted when either is set.
func checkFirstAdministratorError(t *testing.T, err, is error, text string) {
	t.Helper()

	switch {
	case is == nil && text == "":
		if err != nil {
			t.Fatalf("error = %v, want none", err)
		}
	case err == nil:
		t.Fatalf("error = nil, want one saying %q", text)
	case is != nil && !errors.Is(err, is):
		t.Errorf("error = %v, want %v", err, is)
	case !strings.Contains(err.Error(), text):
		t.Errorf("error = %v, want it to say %q", err, text)
	}
}

// checkFirstAdministrator compares what a call answered, the moment the invitation runs
// out apart: it is held to standing for about the seven days an invitation stands for.
func checkFirstAdministrator(t *testing.T, got, want *FirstAdministrator) {
	t.Helper()

	if got == nil {
		t.Fatal("answered nil with no error")
	}
	expires := got.ExpiresAt
	trimmed := *got
	trimmed.ExpiresAt = time.Time{}
	if diff := cmp.Diff(*want, trimmed); diff != "" {
		t.Errorf("FirstAdministrator mismatch (-want +got):\n%s", diff)
	}
	if want.Outcome == FirstAdministratorHasAccount {
		if !expires.IsZero() {
			t.Errorf("ExpiresAt = %s for an Auth with an account, want none", expires)
		}

		return
	}
	if !expires.After(time.Now()) {
		t.Errorf("ExpiresAt = %s, want a moment still to come: the invitation stands", expires)
	}
}

// TestAuthAPI_InviteFirstAdministrator_again pins what the releases after the first
// do, one Auth through them: an address written with a display name is the same
// address at the next release, a message that could not be sent leaves no invitation
// standing and the next release mails again, and a release after the person has an
// account mails nothing.
func TestAuthAPI_InviteFirstAdministrator_again(t *testing.T) {
	t.Parallel()

	f := newFirstAdministratorFixture(t, nil)
	api := f.auth.API()
	req := &FirstAdministratorRequest{Email: "Ada Lovelace <ada@lakeside.edu>", RoleNames: firstAdministratorRoles}

	f.recorder.Fail(errors.New("the relay is down"))
	if _, err := api.InviteFirstAdministrator(t.Context(), req); err == nil || !strings.Contains(err.Error(), "the message could not be sent") {
		t.Fatalf("InviteFirstAdministrator() with the relay down: error = %v, want the message not sent", err)
	}
	if standing, err := api.Invitations(t.Context()); err != nil || len(standing) != 0 {
		t.Fatalf("Invitations() after a message that could not be sent = %v, %v; want none standing: no link stands that nobody holds", standing, err)
	}
	f.recorder.Fail(nil)

	first, err := api.InviteFirstAdministrator(t.Context(), req)
	if err != nil || first.Outcome != FirstAdministratorInvited || first.Email != "ada@lakeside.edu" {
		t.Fatalf("InviteFirstAdministrator() once the relay is back = %+v, %v; want an invitation mailed to the bare address", first, err)
	}
	second, err := api.InviteFirstAdministrator(t.Context(), req)
	if err != nil || second.Outcome != FirstAdministratorStanding || len(second.Withdrawn) != 0 {
		t.Fatalf("InviteFirstAdministrator() at the next release = %+v, %v; want the invitation left standing and nothing mailed", second, err)
	}
	if !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("the invitation standing runs out at %s, want %s: the one the first release mailed", second.ExpiresAt, first.ExpiresAt)
	}

	// The person accepts: they have an account, and the link is spent.
	f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "ada@lakeside.edu"}}
	now := time.Now()
	f.links.last(t).ConsumedAt = &now
	third, err := api.InviteFirstAdministrator(t.Context(), req)
	if err != nil || third.Outcome != FirstAdministratorHasAccount {
		t.Fatalf("InviteFirstAdministrator() after the person accepted = %+v, %v; want nobody invited", third, err)
	}
	// Two attempts were mailed: the one the relay refused and the one that went.
	if got := len(f.recorder.Messages()); got != 2 {
		t.Errorf("messages handed to the sender = %d, want 2: the second and third releases mail nothing", got)
	}
}

func TestAuthAPI_ReinviteFirstAdministrator(t *testing.T) {
	t.Parallel()

	const ada, grace = "ada@lakeside.edu", "grace@lakeside.edu"
	week := 7 * 24 * time.Hour

	tests := []struct {
		name    string
		sender  mail.Sender
		arrange func(f *firstAdministratorFixture) []*sessionstorage.MailLink
		want    FirstAdministrator
		// wantErr is the refusal, and wantErrText words the error says.
		wantErr      error
		wantErrText  string
		wantMailed   []string
		wantStanding []int
		wantDead     []int
	}{
		{
			name: "an invitation stands: it is withdrawn, its link no longer opens, and a fresh one is mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...)}
			},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada, Withdrawn: []string{ada}},
			wantMailed: []string{ada},
			wantDead:   []int{0},
		},
		{
			name:       "none stands: one is mailed",
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada},
			wantMailed: []string{ada},
		},
		{
			name: "one stands for an address the deployment no longer names: it is withdrawn and the address named now is mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(grace, week, firstAdministratorRoles...)}
			},
			want:       FirstAdministrator{Outcome: FirstAdministratorInvited, Email: ada, Withdrawn: []string{grace}},
			wantMailed: []string{ada},
			wantDead:   []int{0},
		},
		{
			name: "the Auth has an account: refused, and what stands is left as it is, for the address named and for another: a refusal changes nothing",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: "someone"}}

				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...), f.standing(grace, week, firstAdministratorRoles...)}
			},
			wantErr:      ErrFirstAdministratorHasAccount,
			wantErrText:  "A person who has one invites the others from the application",
			wantStanding: []int{0, 1},
		},
		{
			name:   "the application writes its mail to the log: refused, and the invitation that stands is not withdrawn for a fresh one that cannot be sent",
			sender: mail.Log{},
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				return []*sessionstorage.MailLink{f.standing(ada, week, firstAdministratorRoles...)}
			},
			wantErr:      ErrFirstAdministratorNoMailbox,
			wantErrText:  "Give this environment its mailbox, then run this step again",
			wantStanding: []int{0},
		},
		{
			name: "the person accepts while the action runs: the withdrawal finds the invitation spent and an account standing, and nothing is mailed",
			arrange: func(f *firstAdministratorFixture) []*sessionstorage.MailLink {
				link := f.standing(ada, week, firstAdministratorRoles...)
				f.links.beforeRevoke = func(context.Context) {
					f.links.mu.Lock()
					defer f.links.mu.Unlock()
					now := time.Now()
					link.ConsumedAt = &now
					f.users = []*sessionstorage.SessionUser{{ID: impulse.Must(impulse.NewUUID()), Username: ada}}
				}

				return []*sessionstorage.MailLink{link}
			},
			wantErr:     ErrFirstAdministratorHasAccount,
			wantErrText: "the application has an account",
			wantDead:    []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFirstAdministratorFixture(t, tt.sender)
			var arranged []*sessionstorage.MailLink
			if tt.arrange != nil {
				arranged = tt.arrange(f)
			}

			got, err := f.auth.API().ReinviteFirstAdministrator(t.Context(), &FirstAdministratorRequest{Email: ada, RoleNames: firstAdministratorRoles})
			checkFirstAdministratorError(t, err, tt.wantErr, tt.wantErrText)
			if err == nil {
				checkFirstAdministrator(t, got, &tt.want)
			}
			if diff := cmp.Diff(tt.wantMailed, f.mailedTo()); diff != "" {
				t.Errorf("addresses mailed mismatch (-want +got):\n%s", diff)
			}
			for _, i := range tt.wantStanding {
				if !f.stands(arranged[i]) {
					t.Errorf("the invitation to %s arranged at %d no longer stands, want it left as it is", arranged[i].Email, i)
				}
			}
			for _, i := range tt.wantDead {
				if f.stands(arranged[i]) {
					t.Errorf("the invitation to %s arranged at %d still stands, want its link dead", arranged[i].Email, i)
				}
			}
		})
	}
}
