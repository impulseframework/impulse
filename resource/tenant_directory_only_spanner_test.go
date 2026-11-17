package resource

import (
	"context"
	"encoding/json"
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

// switchMethod is the method that turns a station's directory-only switch, as grants
// name it, and stationPeople the station's list of its people with the fields that
// name a person.
const (
	switchMethod  accesstypes.Resource = "SetStationDirectoryOnly"
	stationPeople accesstypes.Resource = "StationUsers"
)

// switchPermissions scripts the grants the reads of the directory-only switch ask: Execute
// on the method that turns it, and List on the station's list of its people, each in
// the station's own scope.
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
// request cannot turn it on and remove the directory it is only of; and the read a page
// calls first answers the consequence in words, with the people it names, or the same
// refusal.
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
				Count:    3,
				People:   []DirectoryOutsider{amy, bo, zed},
				Confirm:  "Admit only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001",
			},
		},
		{
			name: "one person is one person", ctx: through(provider), station: "delta", outsiders: []DirectoryOutsiders{reader(bo)},
			want: &DirectoryOnlyConsequence{
				Directory: "OpenID Connect provider id.delta.example", Connection: "id.delta.example",
				Sentence: "Only people who sign in through id.delta.example will be able to enter this station. 1 person seated here signs in another way and will lose access.",
				Count:    1,
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

	t.Run("the served reads are the station's own, asked under the grant of the method that turns the switch", func(t *testing.T) {
		t.Parallel()

		declared := DirectoryPeople{List: stationPeople, Fields: []accesstypes.Tag{"username", "name"}, Outsiders: reader(zed)}
		serveFor := func(ctx context.Context, perms *switchPermissions, path string, people DirectoryPeople) *httptest.ResponseRecorder {
			reads := NewDirectoryOnlyReads(client, rules, func(*http.Request) UserPermissions { return perms }, switchMethod, "stationID", people)
			r := chi.NewRouter()
			r.Use(httpio.WithParams)
			r.Get("/api/stations/{stationID}/"+DirectoryOnlyRoute, reads.State())
			r.Get("/api/stations/{stationID}/"+DirectoryOnlyConsequenceRoute, reads.Consequence())
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/stations/"+path, http.NoBody))

			return rr
		}
		serve := func(ctx context.Context, perms *switchPermissions, path string) *httptest.ResponseRecorder {
			return serveFor(ctx, perms, path, declared)
		}
		const consequence = "beta/" + DirectoryOnlyConsequenceRoute

		granted := &switchPermissions{}
		rr := serve(through(entra), granted, consequence)
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
			Count:    1,
			People:   []DirectoryOutsider{zed},
			Confirm:  "Admit only people who sign in through 6f2c1b7e-0000-4000-8000-000000000001",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the served consequence mismatch (-want +got):\n%s", diff)
		}
		wantAsked := []string{
			"Execute SetStationDirectoryOnly in beta",
			"List StationUsers in beta", "List StationUsers.username in beta", "List StationUsers.name in beta",
		}
		if diff := cmp.Diff(wantAsked, granted.asked); diff != "" {
			t.Errorf("the grants the read asked (-want +got):\n%s", diff)
		}

		// The list held on a condition beside the fields that name a person held outright
		// is the list's own to judge row by row, and names them.
		rr = serve(through(entra), &switchPermissions{conditional: []accesstypes.Resource{stationPeople}}, consequence)
		if !strings.Contains(rr.Body.String(), zed.Username) {
			t.Errorf("with the list on a condition and its naming fields outright: the answer does not name %s: %s", zed.Username, rr.Body.String())
		}

		// The people are named only to someone who may list them: a caller without the
		// station's list of its people, or without a field of it that names a person, or
		// who holds such a field on a condition, is told how many and nobody's name, and
		// so is one whose answer does not hold what was asked.
		counted := want
		counted.People = []DirectoryOutsider{}
		for _, without := range []*switchPermissions{
			{denied: []accesstypes.Resource{stationPeople}},
			{omitted: []accesstypes.Resource{stationPeople}},
			{denied: []accesstypes.Resource{stationPeople.ResourceWithTag("username")}},
			{denied: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
			{conditional: []accesstypes.Resource{stationPeople.ResourceWithTag("username")}},
			{conditional: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
			{omitted: []accesstypes.Resource{stationPeople.ResourceWithTag("name")}},
		} {
			rr := serve(through(entra), without, consequence)
			var got DirectoryOnlyConsequence
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v (body %q)", err, rr.Body.String())
			}
			if diff := cmp.Diff(counted, got); diff != "" {
				t.Errorf("without the list of people (denied %v, on a condition %v): the consequence mismatch (-want +got):\n%s", without.denied, without.conditional, diff)
			}
			if strings.Contains(rr.Body.String(), zed.Username) {
				t.Errorf("without the list of people (denied %v, on a condition %v): the answer names %s: %s", without.denied, without.conditional, zed.Username, rr.Body.String())
			}
		}
		// A declaration that names no field of the list names nobody, whatever the
		// caller holds.
		for _, people := range []DirectoryPeople{{List: stationPeople, Outsiders: reader(zed)}, {Fields: []accesstypes.Tag{"username"}, Outsiders: reader(zed)}} {
			if rr := serveFor(through(entra), &switchPermissions{}, consequence, people); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), zed.Username) || !strings.Contains(rr.Body.String(), `"count":1`) {
				t.Errorf("with the people declared as %+v: status = %d, body %q, want 200 with the count and nobody's name", people, rr.Code, rr.Body.String())
			}
		}

		// The switch as it stands, for the person who may turn it.
		for station, want := range map[string]string{"alpha": `{"directoryOnly":true}`, "beta": `{"directoryOnly":false}`, "gamma": `{"directoryOnly":false}`} {
			if rr := serve(password, &switchPermissions{}, station+"/"+DirectoryOnlyRoute); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != want {
				t.Errorf("the switch of %s: status = %d, body %q, want 200 %s", station, rr.Code, rr.Body.String(), want)
			}
		}

		// A caller without the method's grant is Forbidden on both reads and learns
		// nothing of the station, and a grant that holds on a condition is no grant to
		// a read: it has no request for the condition to judge.
		for _, path := range []string{consequence, "alpha/" + DirectoryOnlyRoute} {
			for name, perms := range map[string]*switchPermissions{
				"without the method":            {denied: []accesstypes.Resource{switchMethod}},
				"the method on a condition":     {conditional: []accesstypes.Resource{switchMethod}},
				"an answer that holds no grant": {omitted: []accesstypes.Resource{switchMethod}},
			} {
				rr := serve(through(entra), perms, path)
				if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), "6f2c1b7e") || strings.Contains(rr.Body.String(), "directoryOnly\"") {
					t.Errorf("%s, %s: status = %d, body %q, want 403 that says nothing of the station", path, name, rr.Code, rr.Body.String())
				}
			}
		}
		// The refusal is the method's, in the same words.
		if rr := serve(password, &switchPermissions{}, consequence); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "You signed in with a password.") {
			t.Errorf("with a password: status = %d, body %q, want the 409 that says how the caller signed in", rr.Code, rr.Body.String())
		}
		for _, path := range []string{"nowhere/" + DirectoryOnlyConsequenceRoute, "nowhere/" + DirectoryOnlyRoute} {
			if rr := serve(through(entra), &switchPermissions{}, path); rr.Code != http.StatusNotFound {
				t.Errorf("%s, for a station that is not one: status = %d, want 404 (body %q)", path, rr.Code, rr.Body.String())
			}
		}
	})

	t.Run("the method's body says which way it turns the switch", func(t *testing.T) {
		t.Parallel()

		for body, want := range map[string]bool{`{"directoryOnly":true}`: true, `{"directoryOnly":false}`: false} {
			var request SetDirectoryOnlyRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
			}
			if on, err := request.Turn(); err != nil || on != want {
				t.Errorf("Turn() of %s = %t, %v, want %t", body, on, err, want)
			}
		}
		var silent SetDirectoryOnlyRequest
		if err := json.Unmarshal([]byte(`{}`), &silent); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if _, err := silent.Turn(); !httpio.HasBadRequest(err) {
			t.Errorf("Turn() of a body that does not say error = %v, want a BadRequest: a silent body must not turn the switch off", err)
		}
	})
}
