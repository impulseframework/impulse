package transition

import (
	"slices"
	"strings"
	"testing"
)

// TestTenancy_MeaningIn holds tenancy's account of what it leaves to the sign-in the
// application has: the general account where an auth has the password method, and
// nothing of a password or of a login made ahead where none has.
func TestTenancy_MeaningIn(t *testing.T) {
	t.Parallel()

	tn := Tenancy{Table: "Tenants", OneTenantPerPerson: true}

	t.Run("every clause it retells is in the general account", func(t *testing.T) {
		t.Parallel()

		general := tn.Meaning()
		for _, clause := range passwordClauses {
			if strings.Count(general, clause.with) != 1 {
				t.Errorf("the general account holds %q %d times, want once", clause.with, strings.Count(general, clause.with))
			}
		}
	})

	t.Run("an application with a password is told the general account", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		if got := tn.MeaningIn(a); got != tn.Meaning() {
			t.Error("MeaningIn() differs from Meaning() in an application whose auth has the password method")
		}
		if !strings.Contains(tn.MeaningIn(a), "The test harnesses. The authorization suite's configurer builds the roster") {
			t.Error("the harnesses are not handed on, and the application's are the base's")
		}
	})

	t.Run("an application with no password is told nothing of one", func(t *testing.T) {
		t.Parallel()

		start := byDirectoryStart("staff")
		start.Tenants = &tn
		a, _ := started(t, &start)
		got := tn.MeaningIn(a)
		for _, never := range []string{"logins", "login's", "a login with", "member login", "signs in with a password", "signed up with a password", "by password", "has a password", "who has none chooses one"} {
			if strings.Contains(got, never) {
				t.Errorf("the account says %q, and no auth of the application has the password method", never)
			}
		}
		for _, clause := range passwordClauses {
			if !strings.Contains(got, clause.without) {
				t.Errorf("the account does not say %q", clause.without)
			}
		}
		if want := "The test harnesses. Both build the roster already (`test/authz/harness_test.go`, `test/integration/harness_test.go`)"; !strings.Contains(got, want) {
			t.Errorf("the account does not say %q, and the start wrote both harnesses", want)
		}
		if strings.Contains(got, "The authorization suite's configurer builds the roster with `NewTenantRoster` over the test client and adds") {
			t.Error("the account hands on the harnesses the start wrote")
		}
	})
}

// TestTenancy_Apply_seedWords holds what tenancy says of the development tenants' seed,
// in the file and in the change, to the sign-in the application has: the logins of the
// bootstrap's identities file where an auth has the password method, and nothing of a
// login made ahead where none has.
func TestTenancy_Apply_seedWords(t *testing.T) {
	t.Parallel()

	const (
		seed = "schema/devseed/000001_dev_tenants.up.sql"
		did  = seed + " and .down.sql: "
	)
	tn := Tenancy{Table: "Tenants", OneTenantPerPerson: true}

	t.Run("an application with a password is told of its logins", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		ch, err := tn.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		if got := read(t, a, seed); !strings.HasPrefix(got, seedCommentLogins+"INSERT INTO Tenants") {
			t.Errorf("%s does not open with the account of the development logins:\n%s", seed, got)
		}
		if got := linesWith(ch.Did, did, []string{"to apply before the logins"}); len(got) != 1 {
			t.Errorf("the change does not say once that the seed is applied before the logins: %q", ch.Did)
		}
	})

	t.Run("an application with no password is told of no login", func(t *testing.T) {
		t.Parallel()

		start := byDirectoryStart("staff")
		start.Tenants = &tn
		a, _ := started(t, &start)
		ch, err := tn.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		got := read(t, a, seed)
		if !strings.HasPrefix(got, seedCommentNoLogins+"INSERT INTO Tenants") {
			t.Errorf("%s does not open with the account for an application with no password method:\n%s", seed, got)
		}
		for _, never := range []string{"development logins", "identities file"} {
			if strings.Contains(got, never) {
				t.Errorf("%s says %q, and no auth of the application has the password method", seed, never)
			}
		}
		if got := linesWith(ch.Did, did, []string{"to apply before anyone is seeded"}); len(got) != 1 {
			t.Errorf("the change does not say once that the seed is applied before anyone is seeded: %q", ch.Did)
		}
		if got := linesWith(ch.Did, did, []string{"logins"}); len(got) != 0 {
			t.Errorf("the change speaks of logins, and no auth of the application has the password method: %q", got)
		}
	})
}

// TestTenancy_Apply_prunesRoles holds tenancy to taking out of the role file the grants
// on what its regeneration stops serving: the engine refuses a file that grants a
// resource the application does not serve, so the application would not start.
func TestTenancy_Apply_prunesRoles(t *testing.T) {
	t.Parallel()

	const roles = "pkg/auth/staff/roles.json"

	t.Run("a grant on a handler the regeneration removed is taken out", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		if !slices.Contains(granted(t, a, roles), "StaffSignUpRoles") {
			t.Fatal("the base's role file grants nothing on StaffSignUpRoles; the case proves nothing")
		}
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &regeneration{a: a, removes: []string{"pkg/identity/staffhandlers/zz_gen_staff_sign_up_roles.go"}})
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		resources := granted(t, a, roles)
		if slices.Contains(resources, "StaffSignUpRoles") {
			t.Error("the role file still grants StaffSignUpRoles, which the application no longer serves")
		}
		if !slices.Contains(resources, "StaffSessionUsers") {
			t.Error("the role file lost its grant on StaffSessionUsers, which the application still serves")
		}
		if got := linesWith(ch.Did, roles+": 1 grant(s) taken out", []string{"StaffSignUpRoles", "now the application has tenants"}); len(got) != 1 {
			t.Errorf("the change does not say once which grant was taken out: %q", ch.Did)
		}
	})

	t.Run("a role file is left as it is where no handler was removed", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		before := read(t, a, roles)
		ch, err := Tenancy{Table: "Tenants"}.Apply(t.Context(), a, &fakeExec{})
		if err != nil {
			t.Fatalf("Tenancy.Apply() error = %v", err)
		}
		if read(t, a, roles) != before {
			t.Error("the role file changed, and the regeneration removed no handler")
		}
		if got := linesWith(ch.Did, roles+": ", []string{"grant(s) taken out"}); len(got) != 0 {
			t.Errorf("the change says a grant was taken out: %q", got)
		}
	})
}
