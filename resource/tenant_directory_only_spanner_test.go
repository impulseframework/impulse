package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

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

// switchPermissions scripts the grant the directory-only switch is written under:
// Update on the tenant record and on its directory-only field, in the global scope.
type switchPermissions struct {
	stubPermissions
	denied []accesstypes.Resource
	// conditional are the resources the grant holds on a condition.
	conditional []accesstypes.Resource
	asked       []accesstypes.Resource
}

func (s *switchPermissions) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if !scope.IsGlobal() || perm != accesstypes.Update {
		return nil, errors.Newf("the read asked %s in %s, want Update in the global scope", perm, scope)
	}
	s.asked = append(s.asked, resources...)
	decisions := accesstypes.Decisions{}
	for _, res := range resources {
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
// sentence the person can act on; turning it off and leaving it on are plain; and the
// read a page calls first answers the consequence in words, with the people it names,
// or the same refusal.
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
		notThrough  = "Directory only is turned on by a person who is signed in through this station's directory, Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001: that shows the directory works, and that you will not shut yourself out. %s Sign in through Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001 and try again."
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
	consequenceTests := []struct {
		name      string
		ctx       context.Context
		station   accesstypes.Domain
		outsiders []DirectoryOutsiders
		want      *DirectoryOnlyConsequence
		// wantRefusal is the Conflict's sentence, wantNotFound a station that is not one.
		wantRefusal  string
		wantNotFound bool
	}{
		{
			name: "the sentence counts the people of every auth, and names them by username", ctx: through(entra), station: "beta",
			outsiders: []DirectoryOutsiders{reader(zed, amy), nil, reader(bo)},
			want: &DirectoryOnlyConsequence{
				Directory: "Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001", Connection: "6f2c1b7e-0000-4000-8000-000000000001",
				Sentence: "Only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001 will be able to enter this station. 3 people seated here sign in another way and will lose access.",
				People:   []DirectoryOutsider{amy, bo, zed},
				Confirm:  "Admit only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001",
			},
		},
		{
			name: "one person is one person", ctx: through(provider), station: "delta", outsiders: []DirectoryOutsiders{reader(bo)},
			want: &DirectoryOnlyConsequence{
				Directory: "OpenID Connect provider id.delta.example", Connection: "id.delta.example",
				Sentence: "Only people who sign in through id.delta.example will be able to enter this station. 1 person seated here signs in another way and will lose access.",
				People:   []DirectoryOutsider{bo},
				Confirm:  "Admit only people who sign in through id.delta.example",
			},
		},
		{
			name: "nobody outside the directory is said so, and the people are an empty list", ctx: through(provider), station: "delta",
			want: &DirectoryOnlyConsequence{
				Directory: "OpenID Connect provider id.delta.example", Connection: "id.delta.example",
				Sentence: "Only people who sign in through id.delta.example will be able to enter this station. Everyone seated here has signed in through it.",
				People:   []DirectoryOutsider{},
				Confirm:  "Admit only people who sign in through id.delta.example",
			},
		},
		{
			name: "a station that is directory only already has nothing to turn on, and the refusal names neither its directory nor its people", ctx: password, station: "alpha", outsiders: []DirectoryOutsiders{reader(zed, bo)},
			wantRefusal: onAlready,
		},
		{
			name: "nor to a caller who is signed in through its directory", ctx: through(workspace), station: "alpha", outsiders: []DirectoryOutsiders{reader(zed, bo)},
			wantRefusal: onAlready,
		},
		{name: "a station with no directory is refused as the write is", ctx: through(entra), station: "gamma", wantRefusal: noDirectory},
		{
			name: "a caller who is not signed in through the directory is refused as the write is", ctx: password, station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
		},
		{name: "a station the record does not hold is not found", ctx: through(entra), station: "nowhere", wantNotFound: true},
	}
	for _, tt := range consequenceTests {
		t.Run("the read: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var got *DirectoryOnlyConsequence
			err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = rules.Consequence(ctx, txn, tt.station, tt.outsiders...)

				return err
			})
			switch {
			case tt.wantNotFound:
				if !httpio.HasNotFound(err) {
					t.Fatalf("Consequence() error = %v, want NotFound", err)
				}
			case tt.wantRefusal != "":
				if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
					t.Fatalf("Consequence() error = %v, want the Conflict %q", err, tt.wantRefusal)
				}
			default:
				if err != nil {
					t.Fatalf("Consequence() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("Consequence() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}

	t.Run("the served read is asked under the grant the write is, and answers the consequence or the refusal", func(t *testing.T) {
		t.Parallel()

		serve := func(ctx context.Context, perms *switchPermissions, query string) *httptest.ResponseRecorder {
			handler := DirectoryOnlyHandler(client, rules, func(*http.Request) UserPermissions { return perms }, "Stations", "directoryOnly", reader(zed))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/"+DirectoryOnlyRoute+query, http.NoBody))

			return rr
		}

		granted := &switchPermissions{}
		rr := serve(through(entra), granted, "?domain=beta")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rr.Code, rr.Body.String())
		}
		var got DirectoryOnlyConsequence
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v (body %q)", err, rr.Body.String())
		}
		want := DirectoryOnlyConsequence{
			Directory: "Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001", Connection: "6f2c1b7e-0000-4000-8000-000000000001",
			Sentence: "Only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001 will be able to enter this station. 1 person seated here signs in another way and will lose access.",
			People:   []DirectoryOutsider{zed},
			Confirm:  "Admit only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the served consequence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]accesstypes.Resource{"Stations", "Stations.directoryOnly"}, granted.asked); diff != "" {
			t.Errorf("the grants the read asked (-want +got):\n%s", diff)
		}

		// A caller without the field's grant is Forbidden and learns nothing of the station.
		for _, denied := range []accesstypes.Resource{"Stations", "Stations.directoryOnly"} {
			rr := serve(through(entra), &switchPermissions{denied: []accesstypes.Resource{denied}}, "?domain=beta")
			if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "6f2c1b7e") {
				t.Errorf("without Update on %s: status = %d, body %q, want 403 that names no directory", denied, rr.Code, rr.Body.String())
			}
		}
		// A grant that holds on a condition is no grant to the read: the condition is
		// judged against the row a write changes.
		for _, conditional := range []accesstypes.Resource{"Stations", "Stations.directoryOnly"} {
			rr := serve(through(entra), &switchPermissions{conditional: []accesstypes.Resource{conditional}}, "?domain=beta")
			if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "6f2c1b7e") {
				t.Errorf("with Update on %s held on a condition: status = %d, body %q, want 403 that names no directory", conditional, rr.Code, rr.Body.String())
			}
		}
		// The refusal is the write's, in the same words.
		if rr := serve(password, &switchPermissions{}, "?domain=beta"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "You signed in with a password.") {
			t.Errorf("with a password: status = %d, body %q, want the 409 that says how the caller signed in", rr.Code, rr.Body.String())
		}
		if rr := serve(through(entra), &switchPermissions{}, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("with no domain: status = %d, want 400 (body %q)", rr.Code, rr.Body.String())
		}
		if rr := serve(through(entra), &switchPermissions{}, "?domain=nowhere"); rr.Code != http.StatusNotFound {
			t.Errorf("for a station that is not one: status = %d, want 404 (body %q)", rr.Code, rr.Body.String())
		}
	})
}
