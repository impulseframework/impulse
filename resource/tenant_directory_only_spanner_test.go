package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// namedFlag is a type named over a boolean, as an application may type its
// directory-only field.
type namedFlag bool

// makeMethod and endMethod are the methods that turn a station's directory-only switch
// on and off, as grants name them, stationPeople the station's list of its people with
// the fields that name a person, and stationInvitations its list of the invitations into
// it, with the fields that name the person invited.
const (
	makeMethod         accesstypes.Resource = "MakeStationDirectoryOnly"
	endMethod          accesstypes.Resource = "EndStationDirectoryOnly"
	stationPeople      accesstypes.Resource = "StationUsers"
	stationInvitations accesstypes.Resource = "StationInvitations"
)

// switchPermissions scripts the grants the directory-only switch asks: Execute on the
// methods that turn it, and List on the station's list of its people, each in the
// station's own scope.
type switchPermissions struct {
	stubPermissions
	denied []accesstypes.Resource
	// conditional are the resources the grant holds on a condition, and omitted the
	// ones the answer does not hold at all.
	conditional []accesstypes.Resource
	omitted     []accesstypes.Resource
	asked       []string
}

func (s *switchPermissions) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if scope.IsGlobal() {
		return nil, errors.Newf("the read asked %s in the global scope, want the station's own", perm)
	}
	domain, _ := scope.Domain()
	decisions := accesstypes.Decisions{}
	for _, res := range resources {
		s.asked = append(s.asked, string(perm)+" "+string(res)+" in "+string(domain))
		if slices.Contains(s.omitted, res) {
			continue
		}
		decisions[res] = accesstypes.Granted()
		if slices.Contains(s.conditional, res) {
			decisions[res] = accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{res}})
		}
		if slices.Contains(s.denied, res) {
			decisions[res] = accesstypes.Denied()
		}
	}

	return decisions, nil
}

// TestTenantDirectoryRules_directoryOnly proves the rules of a tenant's directory-only
// switch over the emulator: turning it on is refused until the tenant has its
// directory and unless the caller is signed in through that directory, each with a
// sentence the person can act on; turning it off and leaving it on are plain; one
// request cannot turn it on and remove the directory it is only of; and the warning the
// method that turns it on answers first says the consequence in words, with the people
// it names for a caller who may list them, or the same refusal.
func TestTenantDirectoryRules_directoryOnly(t *testing.T) {
	t.Parallel()

	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "frontier.example"}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000001"}
	provider := signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "id.delta.example"}
	db, client := tenantDatabase(t, "directory-only-rules", "alpha", "beta", "gamma", "delta")
	// alpha has the Workspace and is directory only; beta has the Entra tenant; gamma
	// has no directory; delta has an organization's own OpenID Connect provider.
	declareStation(t, db, "alpha", true, workspace)
	declareStation(t, db, "beta", false, entra)
	declareStation(t, db, "delta", false, provider)
	rules := stationRules()

	through := func(directory signin.Directory) context.Context {
		return establishedBy(sessioninfo.AuthEvent{Method: directory.Method, Connection: directory.Connection})
	}
	password := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
	impersonation := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"})
	personal := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection})

	const (
		noDirectory = "This station has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this station's directory, then turn directory only on."
		onAlready   = "This station is directory only already: only people who sign in through its directory enter it. There is nothing to turn on."
		notThrough  = "Directory only is turned on by a person who is signed in through this station's directory, your organization's Microsoft directory: that shows the directory works, and that you will not shut yourself out. %s Sign in through your organization's Microsoft directory and try again."
	)
	writeTests := []struct {
		name    string
		ctx     context.Context
		station accesstypes.Domain
		value   any
		// wantRefusal is the Conflict's sentence; empty where the write may be made.
		wantRefusal string
	}{
		{name: "turning it on through the station's own directory", ctx: through(entra), station: "beta", value: true},
		{name: "turning it on through an organization's own OpenID Connect provider that is the station's directory", ctx: through(provider), station: "delta", value: true},
		{name: "turning it on with no directory is refused, saying to set one first", ctx: through(entra), station: "gamma", value: true, wantRefusal: noDirectory},
		{
			name: "turning it on with a password is refused, saying how the caller signed in", ctx: password, station: "beta", value: true,
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
		},
		{
			name: "turning it on through another station's directory is refused, naming both", ctx: through(workspace), station: "beta", value: true,
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in through Google Workspace frontier.example.", 1),
		},
		{
			name: "turning it on with a personal account is refused", ctx: personal, station: "beta", value: true,
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a personal Google account.", 1),
		},
		{
			name: "turning it on in a session that views as someone else is refused", ctx: impersonation, station: "beta", value: true,
			wantRefusal: strings.Replace(notThrough, "%s", "This session is viewing the application as someone else, which is no sign-in through its directory.", 1),
		},
		{
			name: "turning it on in a session that records no sign-in is refused", ctx: establishedBy(), station: "beta", value: true,
			wantRefusal: strings.Replace(notThrough, "%s", "This session was not signed in through it.", 1),
		},
		{name: "turning it off is plain, whoever asks", ctx: password, station: "alpha", value: false},
		{name: "a write that leaves it on turns nothing on", ctx: password, station: "alpha", value: true},
		{name: "a pointer to true is the flag turned on", ctx: password, station: "gamma", value: new(true), wantRefusal: noDirectory},
		{name: "a write that carries no flag turns nothing on", ctx: password, station: "gamma", value: nil},
		{name: "a flag of a type named over a boolean is the flag all the same", ctx: password, station: "gamma", value: namedFlag(true), wantRefusal: noDirectory},
		{name: "a value that is no boolean is held to the rule, never waved through", ctx: password, station: "gamma", value: "true", wantRefusal: noDirectory},
	}
	for _, tt := range writeTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				return rules.DirectoryOnly(ctx, txn, tt.station, tt.value)
			})
			if tt.wantRefusal == "" {
				if err != nil {
					t.Fatalf("DirectoryOnly() error = %v, want none", err)
				}

				return
			}
			if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
				t.Fatalf("DirectoryOnly() error = %v, want the Conflict %q", err, tt.wantRefusal)
			}
		})
	}

	// The people each auth lists for a station who have no sign-in through its directory.
	zed := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000a")), Username: "zed"}
	amy := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000b")), Username: "Amy", Name: new("Amy Okafor")}
	bo := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000c")), Username: "bo"}
	reader := func(people ...DirectoryOutsider) DirectoryOutsiders {
		return func(_ context.Context, _ ReadWriteTransaction, _ accesstypes.Domain) ([]DirectoryOutsider, error) {
			return people, nil
		}
	}
	// asks is the request's context with its caller stamped, as the generated frame
	// stamps it once the method's grant has answered.
	asks := func(ctx context.Context, perms *switchPermissions) context.Context {
		return WithCaller(ctx, &Caller{Permissions: perms, Scope: accesstypes.DomainScope("beta"), Env: RequestEnvironment()})
	}
	people := func(readers ...DirectoryOutsiders) []DirectoryPeople {
		out := make([]DirectoryPeople, 0, len(readers))
		for _, read := range readers {
			out = append(out, DirectoryPeople{List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: read})
		}

		return out
	}
	// The station's own administrators who have a password, and the invitations into it
	// not yet accepted that give its administrator role, as an auth's readers answer them.
	ines := DirectoryAdministrator{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-0000000000d1")), Username: "ines", Name: new("Ines Okoro")}
	wren := DirectoryAdministrator{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-0000000000d2")), Username: "Wren"}
	abe := DirectoryAdministrator{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-0000000000d3")), Username: "abe"}
	kit := DirectoryInvitation{ID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-0000000000e1")), Email: "kit@example.com", Name: new("Kit Marlow")}
	lee := DirectoryInvitation{ID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-0000000000e2")), Email: "Lee@example.com"}
	administrators := func(people ...DirectoryAdministrator) DirectoryAdministrators {
		return func(_ context.Context, _ ReadWriteTransaction, _ accesstypes.Domain) ([]DirectoryAdministrator, error) {
			return people, nil
		}
	}
	invitations := func(sent ...DirectoryInvitation) DirectoryInvitations {
		return func(_ context.Context, _ ReadWriteTransaction, _ accesstypes.Domain) ([]DirectoryInvitation, error) {
			return sent, nil
		}
	}
	// The readers that fail say so in the call's error, wherever they are called.
	failingAdministrators := func(_ context.Context, _ ReadWriteTransaction, _ accesstypes.Domain) ([]DirectoryAdministrator, error) {
		return nil, errors.New("the administrators could not be read")
	}
	failingInvitations := func(_ context.Context, _ ReadWriteTransaction, _ accesstypes.Domain) ([]DirectoryInvitation, error) {
		return nil, errors.New("the invitations could not be read")
	}
	// enters and staysOut are an auth's answers to whether its people keep the
	// administrators' way in, as a generated identity client answers them.
	enters := func() bool {
		return true
	}
	staysOut := func() bool {
		return false
	}
	// keeps is an auth's entry whose people keep the administrators' way in, with the
	// station's lists of its people and its invitations, and nobody the switch keeps out.
	keeps := func(admins DirectoryAdministrators, invited DirectoryInvitations) DirectoryPeople {
		return DirectoryPeople{
			List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(), AdministratorsEnter: enters,
			Administrators: admins, InvitationList: stationInvitations, InvitationFields: []accesstypes.Tag{"email", "name"}, Invitations: invited,
		}
	}
	const (
		entraOnly    = "Only people who sign in through your organization's Microsoft directory will be able to enter this station."
		entraConfirm = "Admit only people who sign in through your organization's Microsoft directory"
		deltaOnly    = "Only people who sign in through id.delta.example will be able to enter this station."
		deltaConfirm = "Admit only people who sign in through id.delta.example"
		// entraKept is the consequence where the administrators keep their way in and the
		// switch keeps nobody out.
		entraKept           = entraOnly + " An administrator of this station who has a password can still sign in with it and a code sent to their email. Nobody seated here will lose access."
		oneAdministrator    = "1 administrator of this station has a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."
		twoAdministrators   = "2 administrators of this station have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."
		threeAdministrators = "3 administrators of this station have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."
		oneInvitation       = "1 invitation that has not been accepted gives a role that administers this station. Whoever accepts it can choose a password and enter without the directory. Withdraw it first if it should not stand."
		twoInvitations      = "2 invitations that have not been accepted give a role that administers this station. Whoever accepts one can choose a password and enter without the directory. Withdraw first any that should not stand."
	)
	warningTests := []struct {
		name    string
		ctx     context.Context
		station accesstypes.Domain
		people  []DirectoryPeople
		want    *Warning
		// wantRefusal is the Conflict's sentence, wantNotFound a station that is not one,
		// and wantErr what the call's error says where it is none of those.
		wantRefusal  string
		wantNotFound bool
		wantErr      string
	}{
		{
			name: "the sentence counts the people of every auth, and lists them by name and username", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: people(reader(zed, amy), nil, reader(bo)),
			want: &Warning{
				Consequence: entraOnly + " 3 people seated here sign in another way and will lose access.",
				Names:       []string{"Amy Okafor (Amy)", "bo", "zed"},
				Confirm:     entraConfirm,
			},
		},
		{
			name: "one person is one person", ctx: asks(through(provider), &switchPermissions{}), station: "delta", people: people(reader(bo)),
			want: &Warning{Consequence: deltaOnly + " 1 person seated here signs in another way and will lose access.", Names: []string{"bo"}, Confirm: deltaConfirm},
		},
		{
			name: "nobody outside the directory is said so, and no list is drawn", ctx: asks(through(provider), &switchPermissions{}), station: "delta", people: people(reader()),
			want: &Warning{Consequence: deltaOnly + " Everyone seated here has signed in through it.", Confirm: deltaConfirm},
		},
		{
			name: "people nobody reads are not counted, and the sentence does not say that nobody loses access", ctx: asks(through(provider), &switchPermissions{}), station: "delta",
			want: &Warning{Consequence: deltaOnly + " Anyone seated here who signs in another way will lose access.", Confirm: deltaConfirm},
		},
		{
			name: "nor where the one entry has no reader", ctx: asks(through(provider), &switchPermissions{}), station: "delta", people: people(nil),
			want: &Warning{Consequence: deltaOnly + " Anyone seated here who signs in another way will lose access.", Confirm: deltaConfirm},
		},
		{
			name: "a caller who may not list the people is told how many and nobody's name", ctx: asks(through(entra), &switchPermissions{denied: []accesstypes.Resource{stationPeople}}), station: "beta",
			people: people(reader(zed, amy), reader(bo)),
			want:   &Warning{Consequence: entraOnly + " 3 people seated here sign in another way and will lose access.", Confirm: entraConfirm},
		},
		{
			name: "a call outside a request names nobody", ctx: through(entra), station: "beta", people: people(reader(zed, amy)),
			want: &Warning{Consequence: entraOnly + " 2 people seated here sign in another way and will lose access.", Confirm: entraConfirm},
		},
		{
			name: "a list that names fewer people than it counts says the list is the ones the caller may list", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{
				{List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(zed, amy)},
				{List: "", Fields: nil, Outsiders: reader(bo)},
			},
			want: &Warning{
				Consequence: entraOnly + " 3 people seated here sign in another way and will lose access. The 2 of them you may list are named here.",
				Names:       []string{"Amy Okafor (Amy)", "zed"},
				Confirm:     entraConfirm,
			},
		},
		{
			name: "and says so of one", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{
				{List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(zed)},
				{List: "", Fields: nil, Outsiders: reader(bo)},
			},
			want: &Warning{
				Consequence: entraOnly + " 2 people seated here sign in another way and will lose access. The one of them you may list is named here.",
				Names:       []string{"zed"},
				Confirm:     entraConfirm,
			},
		},
		{
			name: "a station that is directory only already has nothing to turn on, and the refusal names neither its directory nor its people", ctx: asks(password, &switchPermissions{}), station: "alpha", people: people(reader(zed, bo)),
			wantRefusal: onAlready,
		},
		{
			name: "nor to a caller who is signed in through its directory", ctx: asks(through(workspace), &switchPermissions{}), station: "alpha", people: people(reader(zed, bo)),
			wantRefusal: onAlready,
		},
		{name: "a station with no directory is refused as the write is", ctx: asks(through(entra), &switchPermissions{}), station: "gamma", wantRefusal: noDirectory},
		{
			name: "a caller who is not signed in through the directory is refused as the write is, and reads nobody's name", ctx: asks(password, &switchPermissions{}), station: "beta", people: people(reader(zed)),
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
		},
		{name: "a station the record does not hold is not found", ctx: asks(through(entra), &switchPermissions{}), station: "nowhere", wantNotFound: true},
		{
			name: "the administrators who have a password are counted in a note, and named by name and username, sorted without case", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(wren, ines), nil)},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{{Says: twoAdministrators, Names: []string{"Ines Okoro (ines)", "Wren"}}},
			},
		},
		{
			name: "one administrator is one administrator", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(ines), nil)},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: oneAdministrator, Names: []string{"Ines Okoro (ines)"}}}},
		},
		{
			name: "a caller who may not list the people is told how many administrators and nobody's name", ctx: asks(through(entra), &switchPermissions{denied: []accesstypes.Resource{stationPeople}}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(wren, ines), nil)},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: twoAdministrators}}},
		},
		{
			name: "administrators named fewer than counted are the one the caller may list", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{
				keeps(administrators(ines), nil),
				{Outsiders: reader(), AdministratorsEnter: enters, Administrators: administrators(wren, abe)},
			},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{{Says: threeAdministrators + " The one of them you may list is named here.", Names: []string{"Ines Okoro (ines)"}}},
			},
		},
		{
			name: "and the several the caller may list", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{
				{Outsiders: reader(), AdministratorsEnter: enters, Administrators: administrators(abe)},
				keeps(administrators(wren, ines), nil),
			},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{{Says: threeAdministrators + " The 2 of them you may list are named here.", Names: []string{"Ines Okoro (ines)", "Wren"}}},
			},
		},
		{
			name: "an auth whose people keep no administrators' way in has neither reader called, and no note", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{{
				List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(), AdministratorsEnter: staysOut,
				Administrators: failingAdministrators, InvitationList: stationInvitations, InvitationFields: []accesstypes.Tag{"email", "name"}, Invitations: failingInvitations,
			}},
			want: &Warning{Consequence: entraOnly + " Everyone seated here has signed in through it.", Confirm: entraConfirm},
		},
		{
			name: "nor one that was never asked whether they do", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{{
				List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(),
				Administrators: failingAdministrators, InvitationList: stationInvitations, InvitationFields: []accesstypes.Tag{"email", "name"}, Invitations: failingInvitations,
			}},
			want: &Warning{Consequence: entraOnly + " Everyone seated here has signed in through it.", Confirm: entraConfirm},
		},
		{
			name: "readers that answer nobody add no note", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(), invitations())},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm},
		},
		{
			name: "an invitation that would make another administrator is a note of its own, named by its address", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(), invitations(lee))},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: oneInvitation, Names: []string{"Lee@example.com"}}}},
		},
		{
			name: "several invitations are named by name and address or by address, sorted by address without case", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(nil, invitations(lee, kit))},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{{Says: twoInvitations, Names: []string{"Kit Marlow (kit@example.com)", "Lee@example.com"}}},
			},
		},
		{
			name: "a caller denied the invitations list is told how many and no address", ctx: asks(through(entra), &switchPermissions{denied: []accesstypes.Resource{stationInvitations}}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(ines), invitations(lee, kit))},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{{Says: oneAdministrator, Names: []string{"Ines Okoro (ines)"}}, {Says: twoInvitations}},
			},
		},
		{
			name: "nor one who holds a field that names the person invited on a condition", ctx: asks(through(entra), &switchPermissions{conditional: []accesstypes.Resource{stationInvitations.ResourceWithTag("email")}}), station: "beta",
			people: []DirectoryPeople{keeps(nil, invitations(lee, kit))},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: twoInvitations}}},
		},
		{
			name: "nor one whose answer does not hold such a field", ctx: asks(through(entra), &switchPermissions{omitted: []accesstypes.Resource{stationInvitations.ResourceWithTag("name")}}), station: "beta",
			people: []DirectoryPeople{keeps(nil, invitations(lee, kit))},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: twoInvitations}}},
		},
		{
			name: "a call outside a request names nobody in either note", ctx: through(entra), station: "beta",
			people: []DirectoryPeople{keeps(administrators(ines), invitations(kit))},
			want:   &Warning{Consequence: entraKept, Confirm: entraConfirm, Also: []WarningNote{{Says: oneAdministrator}, {Says: oneInvitation}}},
		},
		{
			name: "the administrators' note comes first, then the invitations'", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(wren, ines), invitations(kit))},
			want: &Warning{
				Consequence: entraKept, Confirm: entraConfirm,
				Also: []WarningNote{
					{Says: twoAdministrators, Names: []string{"Ines Okoro (ines)", "Wren"}},
					{Says: oneInvitation, Names: []string{"Kit Marlow (kit@example.com)"}},
				},
			},
		},
		{
			name: "the administrators' reader's error is the call's error", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(failingAdministrators, invitations(kit))}, wantErr: "the administrators could not be read",
		},
		{
			name: "and so is the invitations' reader's", ctx: asks(through(entra), &switchPermissions{}), station: "beta",
			people: []DirectoryPeople{keeps(administrators(ines), failingInvitations)}, wantErr: "the invitations could not be read",
		},
	}
	for _, tt := range warningTests {
		t.Run("the warning: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var got *Warning
			err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = rules.CountedBy(tt.people...).DirectoryOnlyWarning(ctx, txn, tt.station)

				return err
			})
			switch {
			case tt.wantNotFound:
				if !httpio.HasNotFound(err) {
					t.Fatalf("DirectoryOnlyWarning() error = %v, want NotFound", err)
				}
			case tt.wantRefusal != "":
				if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
					t.Fatalf("DirectoryOnlyWarning() error = %v, want the Conflict %q", err, tt.wantRefusal)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DirectoryOnlyWarning() error = %v, want containing %q", err, tt.wantErr)
				}
			default:
				if err != nil {
					t.Fatalf("DirectoryOnlyWarning() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("DirectoryOnlyWarning() mismatch (-want +got):\n%s", diff)
				}
				if err := CheckWarning("MakeStationDirectoryOnly", got); err != nil {
					t.Errorf("CheckWarning() error = %v, want a warning a dialog can show", err)
				}
			}
		})
	}

	// The switch is turned on by a person who has a password, where their auth has a
	// password method: the write and the warning refuse alike, so a page that asks
	// first shows the refusal before the person types the word; an auth that asks
	// nothing of the caller stands the rule down.
	t.Run("turning it on is refused until the caller has a password, where their auth has passwords", func(t *testing.T) {
		t.Parallel()

		const noPassword = "Directory only is turned on by a person who has a password: an administrator of this station enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem. Choose a password on Your account first, then try again."
		answers := func(has, applies bool) CallerPassword {
			return func(context.Context, ReadWriteTransaction) (bool, bool, error) {
				return has, applies, nil
			}
		}
		fails := func(context.Context, ReadWriteTransaction) (bool, bool, error) {
			return false, false, errors.New("users unavailable")
		}
		tests := []struct {
			name    string
			readers []CallerPassword
			ctx     context.Context
			// wantRefusal is the Conflict's sentence; empty where the switch may be turned.
			wantRefusal string
			wantErr     bool
		}{
			{name: "a caller with no password is refused, and told to choose one", readers: []CallerPassword{answers(false, true)}, ctx: through(entra), wantRefusal: noPassword},
			{name: "a caller with a password turns it on", readers: []CallerPassword{answers(true, true)}, ctx: through(entra)},
			{name: "an auth with no password method asks nothing, and the switch is turned on without one", readers: []CallerPassword{answers(false, false)}, ctx: through(entra)},
			{name: "the auth the caller is a person of answers, whatever the other says of them", readers: []CallerPassword{answers(false, false), answers(false, true)}, ctx: through(entra), wantRefusal: noPassword},
			{name: "a reader that was never set asks nothing", readers: []CallerPassword{nil}, ctx: through(entra)},
			{name: "rules built without readers ask nothing", ctx: through(entra)},
			{
				name: "a caller who is not signed in through the directory is told that first", readers: []CallerPassword{answers(false, true)}, ctx: password,
				wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
			},
			{name: "a failure to read the caller's account fails the call", readers: []CallerPassword{fails}, ctx: through(entra), wantErr: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				asked := rules.PasswordsBy(tt.readers...)
				writeErr := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
					return asked.DirectoryOnly(ctx, txn, "beta", true)
				})
				warningErr := client.ExecuteFunc(asks(tt.ctx, &switchPermissions{}), func(ctx context.Context, txn ReadWriteTransaction) error {
					_, err := asked.CountedBy(people(reader(zed))...).DirectoryOnlyWarning(ctx, txn, "beta")

					return err
				})
				for call, err := range map[string]error{"DirectoryOnly()": writeErr, "DirectoryOnlyWarning()": warningErr} {
					switch {
					case tt.wantErr:
						if err == nil || httpio.HasConflict(err) {
							t.Errorf("%s error = %v, want the reader's failure", call, err)
						}
					case tt.wantRefusal != "":
						if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
							t.Errorf("%s error = %v, want the Conflict %q", call, err, tt.wantRefusal)
						}
					case err != nil:
						t.Errorf("%s error = %v, want none", call, err)
					}
				}
			})
		}
	})

	// The warning tells the person about to close the tenant of the one other way into
	// it, where an auth's people keep it.
	t.Run("the warning says the administrators' way in where an auth keeps it", func(t *testing.T) {
		t.Parallel()

		entries := people(reader(zed), reader())
		entries[1].AdministratorsEnter = enters
		var warning *Warning
		err := client.ExecuteFunc(asks(through(entra), &switchPermissions{}), func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			warning, err = rules.CountedBy(entries...).DirectoryOnlyWarning(ctx, txn, "beta")

			return err
		})
		if err != nil {
			t.Fatalf("DirectoryOnlyWarning() error = %v", err)
		}
		want := &Warning{
			Consequence: entraOnly + " An administrator of this station who has a password can still sign in with it and a code sent to their email. 1 person seated here signs in another way and will lose access.",
			Names:       []string{"zed"},
			Confirm:     entraConfirm,
		}
		if diff := cmp.Diff(want, warning); diff != "" {
			t.Errorf("DirectoryOnlyWarning() mismatch (-want +got):\n%s", diff)
		}
	})

	// Rules the application's registrations name say a directory by its name, and by
	// the label of its button where none names it; no sentence says the tenant's id.
	t.Run("rules named by the sign-in registrations say a directory in words a person knows", func(t *testing.T) {
		t.Parallel()

		named := rules.NamedBy(func(directory signin.Directory) (string, string) {
			if directory == entra {
				return "Lakeside", "Microsoft"
			}

			return "", "Staff sign-in"
		})
		var warning *Warning
		err := client.ExecuteFunc(asks(through(entra), &switchPermissions{}), func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			warning, err = named.CountedBy(people(reader(zed))...).DirectoryOnlyWarning(ctx, txn, "beta")

			return err
		})
		if err != nil {
			t.Fatalf("DirectoryOnlyWarning() error = %v", err)
		}
		wantWarning := &Warning{
			Consequence: "Only people who sign in through Lakeside will be able to enter this station. 1 person seated here signs in another way and will lose access.",
			Names:       []string{"zed"},
			Confirm:     "Admit only people who sign in through Lakeside",
		}
		if diff := cmp.Diff(wantWarning, warning); diff != "" {
			t.Errorf("DirectoryOnlyWarning() of named rules mismatch (-want +got):\n%s", diff)
		}

		// The refusal names the station's directory with its kind, and the directory the
		// caller came through, which no registration names, by the label of its button.
		err = client.ExecuteFunc(through(workspace), func(ctx context.Context, txn ReadWriteTransaction) error {
			return named.DirectoryOnly(ctx, txn, "beta", true)
		})
		const wantRefusal = "Directory only is turned on by a person who is signed in through this station's directory, Microsoft Entra tenant Lakeside: that shows the directory works, and that you will not shut yourself out. You signed in through your organization's Staff sign-in directory. Sign in through Microsoft Entra tenant Lakeside and try again."
		if !httpio.HasConflict(err) || httpio.Message(err) != wantRefusal {
			t.Errorf("DirectoryOnly() of named rules error = %v, want the Conflict %q", err, wantRefusal)
		}

		wantOpen := &Warning{
			Consequence: "Anyone who signs in through Lakeside will be able to enter this station without an invitation and gets the role Dockhand.",
			Confirm:     "Open to people who sign in through Lakeside",
		}
		if diff := cmp.Diff(wantOpen, named.OpenToDirectoryWarning(&TenantDirectory{Key: "beta-0", Directory: entra}, "Dockhand")); diff != "" {
			t.Errorf("OpenToDirectoryWarning() of named rules mismatch (-want +got):\n%s", diff)
		}
		for _, said := range []string{warning.Consequence, warning.Confirm, httpio.Message(err)} {
			if strings.Contains(said, entra.Connection) {
				t.Errorf("%q says the tenant's id, want words a person knows", said)
			}
		}
		// Naming the rules leaves the rules they were made from as they were.
		if len(rules.namers) != 0 {
			t.Errorf("NamedBy() changed the rules it was called on: %d namers", len(rules.namers))
		}
	})

	// One request's writes are held to each other. The rules read what is committed and
	// a transaction's reads do not see its own writes, so the transaction notes the
	// writes the rules pass: the switch turned on beside the removal of the directory it
	// is only of is refused, whichever comes first. Nothing here commits a change: the
	// rules write nothing.
	const (
		removesToo = "This same request removes this station's directory, so it cannot also turn directory only on: nobody could enter the station. Keep the directory, or leave directory only off."
		turnsOnToo = "This same request turns this station directory only, so it cannot also remove its directory: nobody could enter the station. Keep the directory, or leave directory only off."
	)
	type write func(ctx context.Context, txn ReadWriteTransaction) error
	set := func(station accesstypes.Domain, on bool) write {
		return func(ctx context.Context, txn ReadWriteTransaction) error {
			return rules.DirectoryOnly(ctx, txn, station, on)
		}
	}
	remove := func(station accesstypes.Domain) write {
		return func(ctx context.Context, txn ReadWriteTransaction) error {
			return rules.Remove(ctx, txn, station)
		}
	}
	requestTests := []struct {
		name string
		// requests are run one after another, each in a transaction of its own.
		requests [][]write
		// wantRefusal is the Conflict the last write of the last request answers; empty
		// where every write passes.
		wantRefusal string
	}{
		{name: "turning it on and removing the directory in one request is refused at the removal", requests: [][]write{{set("beta", true), remove("beta")}}, wantRefusal: turnsOnToo},
		{name: "removing the directory and turning it on in one request is refused at the switch", requests: [][]write{{remove("beta"), set("beta", true)}}, wantRefusal: removesToo},
		{name: "a request that turns it on and off again may remove the directory", requests: [][]write{{set("beta", true), set("beta", false), remove("beta")}}},
		{name: "another station's switch does not hold this station's directory", requests: [][]write{{set("beta", true), remove("delta")}}},
		{name: "the note is one transaction's: a removal the rules passed in one request does not refuse the switch in the next", requests: [][]write{{remove("beta")}, {set("beta", true)}}},
		{name: "nor does the switch in one request refuse the removal in the next, while nothing was committed", requests: [][]write{{set("beta", true)}, {remove("beta")}}},
	}
	for _, tt := range requestTests {
		t.Run("one request: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var err error
			// stopped is the write the last attempt of the last request stopped at: the
			// emulator aborts a transaction that meets another, and the client runs the
			// request again, so only the attempt that answered counts.
			stopped := -1
			for i, request := range tt.requests {
				err = client.ExecuteFunc(through(entra), func(ctx context.Context, txn ReadWriteTransaction) error {
					stopped = -1
					for j, write := range request {
						if err := write(ctx, txn); err != nil {
							stopped = j

							return err
						}
					}

					return nil
				})
				if err != nil && i != len(tt.requests)-1 {
					t.Fatalf("request %d error = %v, want it made", i+1, err)
				}
			}
			if tt.wantRefusal == "" {
				if err != nil {
					t.Fatalf("the last request error = %v, want none", err)
				}

				return
			}
			if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
				t.Fatalf("the last request error = %v, want the Conflict %q", err, tt.wantRefusal)
			}
			if last := len(tt.requests[len(tt.requests)-1]) - 1; stopped != last {
				t.Errorf("the last request stopped at write %d, want its last write, %d, refused", stopped+1, last+1)
			}
		})
	}

	t.Run("the warning names people only to someone who may list them", func(t *testing.T) {
		t.Parallel()

		declared := DirectoryPeople{List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(zed)}
		warn := func(perms *switchPermissions, declared DirectoryPeople) *Warning {
			var got *Warning
			if err := client.ExecuteFunc(asks(through(entra), perms), func(ctx context.Context, txn ReadWriteTransaction) error {
				// The emulator aborts a transaction that meets another and the client runs
				// it again, so the grants asked are those of the attempt that answered.
				perms.asked = nil
				var err error
				got, err = rules.CountedBy(declared).DirectoryOnlyWarning(ctx, txn, "beta")

				return err
			}); err != nil {
				t.Fatalf("DirectoryOnlyWarning() error = %v", err)
			}

			return got
		}
		counted := &Warning{Consequence: entraOnly + " 1 person seated here signs in another way and will lose access.", Confirm: entraConfirm}
		named := &Warning{Consequence: counted.Consequence, Names: []string{"zed"}, Confirm: entraConfirm}

		granted := &switchPermissions{}
		if diff := cmp.Diff(named, warn(granted, declared)); diff != "" {
			t.Errorf("with the list of people: the warning mismatch (-want +got):\n%s", diff)
		}
		wantAsked := []string{"List StationUsers in beta", "List StationUsers.username in beta", "List StationUsers.name in beta"}
		if diff := cmp.Diff(wantAsked, granted.asked); diff != "" {
			t.Errorf("the grants the warning asked (-want +got):\n%s", diff)
		}

		// The list held on a condition beside the fields that name a person held outright
		// is the list's own to judge row by row, and names them.
		if diff := cmp.Diff(named, warn(&switchPermissions{conditional: []accesstypes.Resource{stationPeople}}, declared)); diff != "" {
			t.Errorf("with the list on a condition and its naming fields outright: the warning mismatch (-want +got):\n%s", diff)
		}

		// The people are named only to someone who may list them: a caller without the
		// station's list of its people, or without a field of it that names a person, or
		// who holds such a field on a condition, is told how many and nobody's name, and
		// so is one whose answer does not hold what was asked.
		for _, without := range []*switchPermissions{
			{denied: []accesstypes.Resource{stationPeople}},
			{omitted: []accesstypes.Resource{stationPeople}},
			{denied: []accesstypes.Resource{stationPeople.ResourceWithTag("username")}},
			{denied: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
			{conditional: []accesstypes.Resource{stationPeople.ResourceWithTag("username")}},
			{conditional: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
			{omitted: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
		} {
			if diff := cmp.Diff(counted, warn(without, declared)); diff != "" {
				t.Errorf("without the list of people (denied %v, on a condition %v, omitted %v): the warning mismatch (-want +got):\n%s", without.denied, without.conditional, without.omitted, diff)
			}
		}
		// A declaration that names no list, or no field of it, names nobody, whatever
		// the caller holds.
		for _, people := range []DirectoryPeople{{List: stationPeople, Outsiders: reader(zed)}, {Fields: []accesstypes.Tag{"username"}, Outsiders: reader(zed)}} {
			if diff := cmp.Diff(counted, warn(&switchPermissions{}, people)); diff != "" {
				t.Errorf("with the people declared as %+v: the warning mismatch (-want +got):\n%s", people, diff)
			}
		}
	})

	t.Run("the switch as it stands is the station's own, read under the grant of either method that turns it", func(t *testing.T) {
		t.Parallel()

		serveBy := func(ctx context.Context, rules *TenantDirectoryRules, perms *switchPermissions, path string) *httptest.ResponseRecorder {
			r := chi.NewRouter()
			r.Use(httpio.WithParams)
			r.Get("/api/stations/{stationID}/"+DirectoryOnlyRoute, DirectoryOnlyStateHandler(client, rules, func(*http.Request) UserPermissions { return perms }, "stationID", makeMethod, endMethod))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/stations/"+path, http.NoBody))

			return rr
		}
		serve := func(ctx context.Context, perms *switchPermissions, path string) *httptest.ResponseRecorder {
			return serveBy(ctx, rules, perms, path)
		}

		granted := &switchPermissions{}
		const alphaOn = `{"directoryOnly":true,"directory":"Google Workspace frontier.example","signedIn":"You signed in with a password."}`
		for station, want := range map[string]string{"alpha": alphaOn, "gamma": `{"directoryOnly":false,"signedIn":"You signed in with a password."}`} {
			if rr := serve(password, granted, station+"/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != want {
				t.Errorf("the switch of %s: status = %d, body %q, want 200 %s", station, rr.Code, rr.Body.String(), want)
			}
		}
		// The read says how the station stands, beside the switch: its directory as
		// people know it, how the caller signed in and through which directory, whether
		// the caller's auth has passwords, and how many people the switch keeps out.
		counted := rules.PasswordsBy(func(context.Context, ReadWriteTransaction) (bool, bool, error) {
			return true, true, nil
		}).CountedBy(DirectoryPeople{
			Seated: func(context.Context, ReadWriteTransaction, accesstypes.Domain) (int, error) {
				return 12, nil
			},
			Outsiders: reader(zed, amy),
		})
		const alphaStands = `{"directoryOnly":true,"directory":"Google Workspace frontier.example","passwords":true,"signedIn":"You signed in through your organization's Microsoft directory.","sessionDirectory":"your organization's Microsoft directory","people":{"seated":12,"outside":2}}`
		if rr := serveBy(through(entra), counted, granted, "alpha/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != alphaStands {
			t.Errorf("the switch of alpha, read with its people counted: status = %d, body %q, want 200 %s", rr.Code, rr.Body.String(), alphaStands)
		}
		// A station that could be turned directory only says what that takes of the
		// caller beside the switch.
		const offWithConditions = `{"directoryOnly":false,"directory":"your organization's Microsoft directory","signedIn":"You signed in with a password.","conditions":[{"sentence":"Directory only is turned on by a person who is signed in through this station`
		if rr := serve(password, granted, "beta/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || !strings.HasPrefix(rr.Body.String(), offWithConditions) {
			t.Errorf("the switch of beta: status = %d, body %q, want 200 with the switch off and what turning it on takes", rr.Code, rr.Body.String())
		}
		if want := "Execute MakeStationDirectoryOnly in alpha"; !slices.Contains(granted.asked, want) {
			t.Errorf("the grants the read asked = %v, want %q among them: the read is asked in the station's own scope", granted.asked, want)
		}

		// The read stamps the caller it checked before it asks the rules, as the frame of
		// the method that turns the switch does. An auth's reader of the caller's password
		// answers nothing for a call that names no caller, so a read that stamped none
		// would never say the password condition the method holds the same person to.
		reader := &switchPermissions{stubPermissions: stubPermissions{user: "reader"}}
		var asked *Caller
		asksWho := rules.PasswordsBy(func(ctx context.Context, _ ReadWriteTransaction) (has, applies bool, err error) {
			asked = CallerFrom(ctx)

			return false, !asked.IsZero(), nil
		})
		const wantPassword = `{"sentence":"Directory only is turned on by a person who has a password: an administrator of this station enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem.","met":false,"toDo":"Choose a password on Your account first, then try again."}]}`
		if rr := serveBy(through(entra), asksWho, reader, "beta/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || !strings.HasSuffix(strings.TrimSpace(rr.Body.String()), wantPassword) {
			t.Errorf("the switch of beta, read by a person with no password: status = %d, body %q, want 200 ending in the password condition %s", rr.Code, rr.Body.String(), wantPassword)
		}
		if asked.IsZero() {
			t.Fatal("the read asked the password reader under no caller, want the caller it checked")
		}
		if domain, _ := asked.Scope.Domain(); asked.Permissions.User() != "reader" || domain != "beta" {
			t.Errorf("the caller the read stamped = %q in %q, want the reader in beta, the station read", asked.Permissions.User(), domain)
		}

		// Either method's grant reads the switch: the person who may only turn it off
		// sees that it is on, and the person who may only turn it on sees that it is off.
		for name, perms := range map[string]*switchPermissions{
			"the method that turns it off alone": {denied: []accesstypes.Resource{makeMethod}},
			"the method that turns it on alone":  {denied: []accesstypes.Resource{endMethod}},
			"one on a condition, the other held": {conditional: []accesstypes.Resource{makeMethod}},
		} {
			if rr := serve(password, perms, "alpha/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != alphaOn {
				t.Errorf("%s: status = %d, body %q, want 200 with the switch", name, rr.Code, rr.Body.String())
			}
		}

		// A caller with neither grant is Forbidden and learns nothing of the station,
		// and a grant that holds on a condition is no grant to a read: it has no
		// request for the condition to judge.
		for name, perms := range map[string]*switchPermissions{
			"without either method":         {denied: []accesstypes.Resource{makeMethod, endMethod}},
			"both methods on a condition":   {conditional: []accesstypes.Resource{makeMethod, endMethod}},
			"an answer that holds no grant": {omitted: []accesstypes.Resource{makeMethod, endMethod}},
		} {
			rr := serve(through(entra), perms, "alpha/"+DirectoryOnlyRoute)
			if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "directoryOnly\"") {
				t.Errorf("%s: status = %d, body %q, want 403 that says nothing of the station", name, rr.Code, rr.Body.String())
			}
		}
		if rr := serve(through(entra), &switchPermissions{}, "nowhere/"+DirectoryOnlyRoute); rr.Code != http.StatusNotFound {
			t.Errorf("for a station that is not one: status = %d, want 404 (body %q)", rr.Code, rr.Body.String())
		}
	})

	// The page that offers the switch says what turning it on takes before the person
	// tries, in the server's words: the read answers each condition for the caller, and
	// the call refuses on the first one not met in the same words.
	t.Run("the switch as it stands says what turning it on takes of the caller, in the words its refusals use", func(t *testing.T) {
		t.Parallel()

		const (
			throughIt   = "Directory only is turned on by a person who is signed in through this station's directory, your organization's Microsoft directory: that shows the directory works, and that you will not shut yourself out."
			signInAgain = "You signed in with a password. Sign in through your organization's Microsoft directory and try again."
			hasPassword = "Directory only is turned on by a person who has a password: an administrator of this station enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem."
			chooseOne   = "Choose a password on Your account first, then try again."
		)
		answers := func(has, applies bool) CallerPassword {
			return func(context.Context, ReadWriteTransaction) (bool, bool, error) {
				return has, applies, nil
			}
		}
		tests := []struct {
			name    string
			station accesstypes.Domain
			ctx     context.Context
			reader  CallerPassword
			want    []DirectoryOnlyCondition
		}{
			{
				name: "a caller who meets both is asked nothing more", station: "beta", ctx: through(entra), reader: answers(true, true),
				want: []DirectoryOnlyCondition{{Sentence: throughIt, Met: true}, {Sentence: hasPassword, Met: true}},
			},
			{
				name: "a caller signed in through the directory who has no password is told to choose one", station: "beta", ctx: through(entra), reader: answers(false, true),
				want: []DirectoryOnlyCondition{{Sentence: throughIt, Met: true}, {Sentence: hasPassword, ToDo: chooseOne}},
			},
			{
				name: "a caller who signed in with their password is told to sign in through the directory", station: "beta", ctx: password, reader: answers(true, true),
				want: []DirectoryOnlyCondition{{Sentence: throughIt, ToDo: signInAgain}, {Sentence: hasPassword, Met: true}},
			},
			{
				name: "a caller who meets neither is told both", station: "beta", ctx: password, reader: answers(false, true),
				want: []DirectoryOnlyCondition{{Sentence: throughIt, ToDo: signInAgain}, {Sentence: hasPassword, ToDo: chooseOne}},
			},
			{
				name: "an auth with no password method asks for none", station: "beta", ctx: through(entra), reader: answers(false, false),
				want: []DirectoryOnlyCondition{{Sentence: throughIt, Met: true}},
			},
			{name: "a station with no directory is refused before anything is asked of the person, so none is said", station: "gamma", ctx: password, reader: answers(false, true)},
			{name: "a station that is directory only already has nothing to turn on, so none is said", station: "alpha", ctx: password, reader: answers(false, true)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				asked := rules.PasswordsBy(tt.reader)
				var state *DirectoryOnlyState
				if err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
					var err error
					state, err = asked.State(ctx, txn, tt.station)

					return err
				}); err != nil {
					t.Fatalf("State() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, state.Conditions); diff != "" {
					t.Errorf("State() conditions mismatch (-want +got):\n%s", diff)
				}
				for _, condition := range tt.want {
					if condition.Met {
						continue
					}
					refusal := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
						return asked.DirectoryOnly(ctx, txn, tt.station, true)
					})
					if want := condition.Sentence + " " + condition.ToDo; !httpio.HasConflict(refusal) || httpio.Message(refusal) != want {
						t.Errorf("DirectoryOnly() error = %v, want the Conflict %q: the call refuses in the words the read says the condition in", refusal, want)
					}

					break
				}
			})
		}
	})

	t.Run("how a station admits its directory's people: the row the two methods write, and the warning's words", func(t *testing.T) {
		t.Parallel()

		read := func(station accesstypes.Domain) (*TenantDirectory, error) {
			var got *TenantDirectory
			err := client.ExecuteFunc(password, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = rules.Directory(ctx, txn, station)

				return err
			})
			if err != nil {
				return nil, errors.Wrap(err, "resource.Client.ExecuteFunc()")
			}

			return got, nil
		}

		// beta has its directory and no default role: it is invite only.
		beta, err := read("beta")
		if err != nil {
			t.Fatalf("Directory() error = %v", err)
		}
		if diff := cmp.Diff(&TenantDirectory{Key: "beta-0", Directory: entra, DefaultRole: ""}, beta); diff != "" {
			t.Errorf("Directory() of a station that is invite only mismatch (-want +got):\n%s", diff)
		}
		// delta's directory gives a role: the row says which.
		seatStationDirectory(t, db, "delta", 0, "Dockhand")
		delta, err := read("delta")
		if err != nil {
			t.Fatalf("Directory() error = %v", err)
		}
		if diff := cmp.Diff(&TenantDirectory{Key: "delta-0", Directory: provider, DefaultRole: "Dockhand"}, delta); diff != "" {
			t.Errorf("Directory() of a station that is open to its directory mismatch (-want +got):\n%s", diff)
		}
		// gamma has no directory: there is nobody to open it to.
		const nobody = "This station has no directory, so there is nobody to open it to. Set its directory first: sign in through your organization's directory and make it this station's directory."
		if _, err := read("gamma"); !httpio.HasConflict(err) || httpio.Message(err) != nobody {
			t.Errorf("Directory() of a station with no directory error = %v, want the Conflict %q", err, nobody)
		}
		// A record that carries no default seat has no role to set.
		noSeat := *stationColumns
		noSeat.DefaultSeatColumn = ""
		err = client.ExecuteFunc(password, func(ctx context.Context, txn ReadWriteTransaction) error {
			_, err := NewTenantDirectoryRules(stationsTable, stationsKey, &noSeat, "station").Directory(ctx, txn, "beta")

			return err
		})
		if err == nil || httpio.HasConflict(err) {
			t.Errorf("Directory() over a record with no default seat column error = %v, want an error that is no refusal", err)
		}

		wantOpen := &Warning{
			Consequence: "Anyone who signs in through your organization's Microsoft directory will be able to enter this station without an invitation and gets the role Dockhand.",
			Confirm:     "Open to people who sign in through your organization's Microsoft directory",
		}
		if diff := cmp.Diff(wantOpen, rules.OpenToDirectoryWarning(beta, "Dockhand")); diff != "" {
			t.Errorf("OpenToDirectoryWarning() mismatch (-want +got):\n%s", diff)
		}
		// A role whose name is several words is said in words.
		wantInWords := &Warning{
			Consequence: "Anyone who signs in through your organization's Microsoft directory will be able to enter this station without an invitation and gets the role Night shift lead.",
			Confirm:     wantOpen.Confirm,
		}
		if diff := cmp.Diff(wantInWords, rules.OpenToDirectoryWarning(beta, "NightShiftLead")); diff != "" {
			t.Errorf("OpenToDirectoryWarning() of a role whose name is several words mismatch (-want +got):\n%s", diff)
		}
		// While no role is picked yet the warning says one is to be chosen.
		wantChoose := &Warning{
			Consequence: "Anyone who signs in through your organization's Microsoft directory will be able to enter this station without an invitation. Choose the role each of them gets.",
			Confirm:     wantOpen.Confirm,
		}
		if diff := cmp.Diff(wantChoose, rules.OpenToDirectoryWarning(beta, "")); diff != "" {
			t.Errorf("OpenToDirectoryWarning() with no role mismatch (-want +got):\n%s", diff)
		}
		for _, warning := range []*Warning{rules.OpenToDirectoryWarning(beta, "Dockhand"), rules.OpenToDirectoryWarning(beta, "")} {
			if err := CheckWarning("OpenStationToDirectory", warning); err != nil {
				t.Errorf("CheckWarning() error = %v, want a warning a dialog can show", err)
			}
		}
	})
}

// TestTenantDirectoryRules_state proves what the read of a tenant's directory-only switch
// says of how the tenant stands, over the emulator: its directory by the name people know
// it, on or off; the role a person gets the first time they sign in through it, in words;
// whether the caller's auth has passwords; how the caller signed in, and through which
// organization's directory; and how many people the tenant seats and how many of them the
// switch keeps out, summed over the entries the rules were given, with each auth's share.
func TestTenantDirectoryRules_state(t *testing.T) {
	t.Parallel()

	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "frontier.example"}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000001"}
	provider := signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "id.delta.example"}
	db, client := tenantDatabase(t, "directory-only-state", "alpha", "beta", "gamma", "delta")
	// alpha has the Workspace and is directory only; beta has the Entra tenant and admits
	// its people by invitation only; gamma has no directory; delta has an organization's
	// own OpenID Connect provider, open to it with a role.
	declareStation(t, db, "alpha", true, workspace)
	declareStation(t, db, "beta", false, entra)
	declareStation(t, db, "delta", false, provider)
	seatStationDirectory(t, db, "delta", 0, "NightShiftLead")

	password := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
	throughEntra := establishedBy(sessioninfo.AuthEvent{Method: entra.Method, Connection: entra.Connection})
	counter := func(seated int, outsiders ...DirectoryOutsider) DirectoryPeople {
		return DirectoryPeople{
			Seated: func(context.Context, ReadWriteTransaction, accesstypes.Domain) (int, error) {
				return seated, nil
			},
			Outsiders: func(context.Context, ReadWriteTransaction, accesstypes.Domain) ([]DirectoryOutsider, error) {
				return outsiders, nil
			},
		}
	}
	// listed is an auth's entry that names the station's list of its people, under what a
	// page calls them.
	listed := func(label string, list accesstypes.Resource, seated int, outsiders ...DirectoryOutsider) DirectoryPeople {
		people := counter(seated, outsiders...)
		people.Label, people.List = label, list

		return people
	}
	const stationStaff accesstypes.Resource = "StationStaffUsers"
	zed := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000a")), Username: "zed"}
	amy := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000b")), Username: "Amy"}
	bo := DirectoryOutsider{UserID: impulse.Must(impulse.UUIDFromString("00000000-0000-4000-8000-00000000000c")), Username: "bo"}
	hasPassword := func(context.Context, ReadWriteTransaction) (bool, bool, error) {
		return true, true, nil
	}
	counted := stationRules().CountedBy(counter(12, zed, amy))
	noSeat := *stationColumns
	noSeat.DefaultSeatColumn = ""

	const (
		byPassword  = "You signed in with a password."
		entraName   = "your organization's Microsoft directory"
		throughName = "You signed in through your organization's Microsoft directory."
	)
	tests := []struct {
		name    string
		rules   *TenantDirectoryRules
		ctx     context.Context
		station accesstypes.Domain
		want    DirectoryOnlyState
		// wantConditions says whether the read says what turning the switch on takes; the
		// conditions' words are pinned where the conditions are.
		wantConditions bool
	}{
		{
			name: "a station with no directory names none and counts nobody", rules: counted, ctx: password, station: "gamma",
			want: DirectoryOnlyState{SignedIn: byPassword},
		},
		{
			name: "a station open to its directory with a role names the directory and the role in words", rules: counted, ctx: password, station: "delta",
			want:           DirectoryOnlyState{Directory: "OpenID Connect provider id.delta.example", DefaultRole: "Night shift lead", SignedIn: byPassword, People: &DirectoryOnlyCount{Seated: 12, Outside: 2}},
			wantConditions: true,
		},
		{
			name: "a station that admits its directory's people by invitation only names no role", rules: counted, ctx: password, station: "beta",
			want:           DirectoryOnlyState{Directory: entraName, SignedIn: byPassword, People: &DirectoryOnlyCount{Seated: 12, Outside: 2}},
			wantConditions: true,
		},
		{
			name: "a station that is directory only names its directory and counts its people, and asks nothing", rules: counted, ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword, People: &DirectoryOnlyCount{Seated: 12, Outside: 2}},
		},
		{
			name: "a caller signed in through the station's directory is told so, with the directory", rules: counted, ctx: throughEntra, station: "beta",
			want:           DirectoryOnlyState{Directory: entraName, SignedIn: throughName, SessionDirectory: entraName, People: &DirectoryOnlyCount{Seated: 12, Outside: 2}},
			wantConditions: true,
		},
		{
			name: "a caller signed in through another station's directory is told which", rules: counted, ctx: throughEntra, station: "gamma",
			want: DirectoryOnlyState{SignedIn: throughName, SessionDirectory: entraName},
		},
		{
			name: "rules that count nobody say no number", rules: stationRules(), ctx: password, station: "beta",
			want:           DirectoryOnlyState{Directory: entraName, SignedIn: byPassword},
			wantConditions: true,
		},
		{
			name: "two counters are summed, and a nil reader counts nobody", rules: stationRules().CountedBy(counter(3, zed), counter(4, amy), DirectoryPeople{}), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword, People: &DirectoryOnlyCount{Seated: 7, Outside: 2}},
		},
		{
			name: "one auth that names its list of people says one share, equal to the totals", rules: stationRules().CountedBy(listed("Crew", stationPeople, 12, zed, amy)), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword, People: &DirectoryOnlyCount{
				Seated: 12, Outside: 2,
				Shares: []DirectoryOnlyShare{{Label: "Crew", People: stationPeople, Seated: 12, Outside: 2}},
			}},
		},
		{
			name: "two auths say their sums and each auth's share, in the order the rules were given them", rules: stationRules().CountedBy(listed("Crew", stationPeople, 12, zed, amy), listed("Staff", stationStaff, 5, bo)), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword, People: &DirectoryOnlyCount{
				Seated: 17, Outside: 3,
				Shares: []DirectoryOnlyShare{{Label: "Crew", People: stationPeople, Seated: 12, Outside: 2}, {Label: "Staff", People: stationStaff, Seated: 5, Outside: 1}},
			}},
		},
		{
			name: "an entry that names no list of people is summed and says no share", rules: stationRules().CountedBy(counter(5, bo), listed("Crew", stationPeople, 12, zed, amy)), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword, People: &DirectoryOnlyCount{
				Seated: 17, Outside: 3,
				Shares: []DirectoryOnlyShare{{Label: "Crew", People: stationPeople, Seated: 12, Outside: 2}},
			}},
		},
		{
			name: "rules given no entries say no count", rules: stationRules().CountedBy(), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", SignedIn: byPassword},
		},
		{
			name: "a caller whose auth has passwords is told so, with the switch on or off", rules: counted.PasswordsBy(hasPassword), ctx: password, station: "alpha",
			want: DirectoryOnlyState{DirectoryOnly: true, Directory: "Google Workspace frontier.example", Passwords: true, SignedIn: byPassword, People: &DirectoryOnlyCount{Seated: 12, Outside: 2}},
		},
		{
			name: "a station with no directory says whether the caller's auth has passwords", rules: counted.PasswordsBy(hasPassword), ctx: password, station: "gamma",
			want: DirectoryOnlyState{Passwords: true, SignedIn: byPassword},
		},
		{
			name: "rules over a record that declares no default seat answer, naming no role", rules: NewTenantDirectoryRules(stationsTable, stationsKey, &noSeat, "station"), ctx: password, station: "delta",
			want:           DirectoryOnlyState{Directory: "OpenID Connect provider id.delta.example", SignedIn: byPassword},
			wantConditions: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var state *DirectoryOnlyState
			if err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				state, err = tt.rules.State(ctx, txn, tt.station)

				return err
			}); err != nil {
				t.Fatalf("State() error = %v", err)
			}
			if got := len(state.Conditions) > 0; got != tt.wantConditions {
				t.Errorf("State() says what turning the switch on takes = %v, want %v (conditions %+v)", got, tt.wantConditions, state.Conditions)
			}
			state.Conditions = nil
			if diff := cmp.Diff(&tt.want, state); diff != "" {
				t.Errorf("State() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A counter's error is the read's, said by the reader that failed.
	for name, failing := range map[string]DirectoryPeople{
		"resource.SeatedCount()": {Seated: func(context.Context, ReadWriteTransaction, accesstypes.Domain) (int, error) {
			return 0, errors.New("the list is unreadable")
		}},
		"resource.DirectoryOutsiders()": {Outsiders: func(context.Context, ReadWriteTransaction, accesstypes.Domain) ([]DirectoryOutsider, error) {
			return nil, errors.New("the list is unreadable")
		}},
	} {
		err := client.ExecuteFunc(password, func(ctx context.Context, txn ReadWriteTransaction) error {
			_, err := stationRules().CountedBy(failing).State(ctx, txn, "beta")

			return err
		})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("State() with a failing counter error = %v, want it wrapped %s", err, name)
		}
	}
}
