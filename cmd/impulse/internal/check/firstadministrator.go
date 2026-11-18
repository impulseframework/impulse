package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// firstAdministrator verifies that an auth that brings in a first administrator invites
// one, and that an auth whose roles the application gives can invite one. The first
// person into a deployed application comes in as everyone after them does: the migrate
// command has an invitation mailed to the address the placement names, while nobody of
// the auth holds the Super Administrator role, and the person accepts it by signing in
// through a method of the auth (InviteAdministrator in the package's foothold.go, over
// session.AuthAPI.InviteFirstAdministrator). No account is made ahead of the person and
// no password is generated for them: nobody can read a generated password, and an
// account that has never signed in is tied to whoever signs in first by nothing but its
// name.
//
// The check reads what the package shows, in the functions a first administrator is
// brought in by (InviteAdministrator and ReinviteAdministrator, and SeedAdministrator
// and seedFoothold, the two an earlier impulse add auth wrote the seed in) and nowhere
// else, so a user the package creates elsewhere, for some other account, is not taken
// for the first administrator's. It refuses a package that still seeds an account
// (SeedAdministrator), one that creates a user where it invites, and one whose
// InviteAdministrator or ReinviteAdministrator does not ask the session library for the
// invitation, which is what stops once somebody holds the Super Administrator role,
// withdraws an invitation the placement no longer names and refuses to write the link
// to a log.
//
// It refuses, too, an auth whose roles the application gives (a role file, and no method
// whose directory writes the roles) whose package imports no generated identity client:
// the client's acceptance hook is what gives an accepted invitation its roles, so nobody
// can be invited as that auth's first administrator, and the seed that would stand in
// is refused above. Such an auth has no way in until the application generates the
// management of its people. An auth that has the client and invites is held to setting
// the hook (SetInvitationAccepted), which the session library refuses to invite without.
// Both are read in the auth's package, where impulse add auth builds the client and sets
// the hook.
//
// And it refuses a function that seeds a cast of personas (SeedCast, or SeedCastFile,
// the same seed over a cast file handed to it) after it invites: after InviteAdministrators,
// the step that invites for every auth, or after the same auth's own
// InviteAdministrator. The invitation is decided by who holds the Super Administrator
// role, and a persona may hold it, so an auth's cast goes before its invitation. An
// environment whose cast holds the role then invites nobody and needs no mailbox; seeded
// after, the same environment is asked for an address and a mailbox it has no use for.
// The order is read inside one function, where the migrate command runs its steps: a
// cast seeded in a function of its own, or when the application starts, is not put in an
// order with an invitation asked elsewhere.
type firstAdministrator struct{}

// firstAdministratorName is the check's name.
const firstAdministratorName = "first-administrator"

func (firstAdministrator) Name() string {
	return firstAdministratorName
}

func (firstAdministrator) Describe() string {
	return "an auth that brings in a first administrator invites one: its package asks the session library for the invitation (InviteAdministrator over session.AuthAPI.InviteFirstAdministrator), mailed to the address the placement names, and creates no account and no password ahead of the person; an auth whose roles the application gives builds its generated identity client and sets its acceptance hook, without which nobody can be invited as its first administrator; and a cast of personas is seeded before its auth's invitation, since a persona may hold the Super Administrator role"
}

// The functions a first administrator is brought in by, as impulse add auth writes them
// and as it wrote the seed they replace, and what the check reads in them.
const (
	inviteAdministratorFunc   = "InviteAdministrator"
	reinviteAdministratorFunc = "ReinviteAdministrator"
	seedAdministratorFunc     = "SeedAdministrator"
	seedFootholdFunc          = "seedFoothold"
	inviteFirstFunc           = "InviteFirstAdministrator"
	reinviteFirstFunc         = "ReinviteFirstAdministrator"
	createSessionUserFunc     = "CreateSessionUser"
	createUserRequestType     = "CreateUserRequest"
	// The step of a release that invites every auth's first administrator, as the
	// skeletons' deploy package names it, and the two calls that seed an auth's cast:
	// the package's own cast file, and a cast file the caller hands.
	inviteAdministratorsFunc = "InviteAdministrators"
	seedCastFunc             = "SeedCast"
	seedCastFileFunc         = "SeedCastFile"
	// The call that sets what an accepted invitation gives, on the session Auth.
	invitationAcceptedFunc = "SetInvitationAccepted"
)

func (c firstAdministrator) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package (pkg/auth/<name>) constructs a session Auth")
	}

	var details, held []string
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		foothold, err := readFoothold(a, p.Dir)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		findings := foothold.findings(p.Name)
		if givesRoles(a, p) && !foothold.identity {
			findings = append(findings, noIdentityClient(p))
		}
		if foothold.invites != "" && foothold.identity && !foothold.accepts {
			findings = append(findings, noAcceptanceHook(p, foothold.invites))
		}
		switch {
		case len(findings) > 0:
			details = append(details, findings...)
		case foothold.seeds == "" && foothold.invites == "" && foothold.reinvites == "":
			// The package brings in no first administrator.
		default:
			held = append(held, p.Name)
		}
	}
	late, err := castSeededLate(a)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	details = append(details, late...)

	switch {
	case len(details) > 0:
		return fail(c.Name(), fmt.Sprintf("%d finding(s) on how a first administrator is brought in", len(details)), details...)
	case len(held) == 0:
		return skip(c.Name(), "no auth brings in a first administrator")
	default:
		return pass(c.Name(), "the first administrator is invited: "+strings.Join(held, ", "))
	}
}

// castSeededLate reads the application's hand-written Go for a function that seeds a
// cast of personas after it invites that auth's first administrator: a call to SeedCast
// or to SeedCastFile that follows, in the same function, a call to InviteAdministrators,
// which invites for every auth, or a call to InviteAdministrator on the receiver the cast
// is seeded on (data.Staff().InviteAdministrator before data.Staff().SeedCast). Each is a
// finding that says where both are and to seed the cast first.
func castSeededLate(a *app.App) ([]string, error) {
	var findings []string
	for _, rel := range a.GoFiles() {
		if strings.HasPrefix(path.Base(rel), "zz_gen_") {
			continue
		}
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		if !strings.Contains(string(src), seedCastFunc) {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			invited, seeded, by := stepsOf(fn)
			if !seeded.IsValid() {
				continue
			}
			findings = append(findings, fmt.Sprintf("%[1]s:%[2]d: the cast is seeded (%[3]s) after the first administrator is invited (line %[4]d): a release invites while nobody of the auth holds the Super Administrator role, and a persona of the cast may hold it, so an environment whose cast holds the role is asked for an address and a mailbox it has no use for, or mails an invitation nobody wants. Seed the cast first: move the %[3]s call above the invitation, so the invitation is decided with the personas in place",
				rel, fset.Position(seeded).Line, by, fset.Position(invited).Line))
		}
	}

	return findings, nil
}

// stepsOf reads one function for the two steps whose order matters: seeded is where it
// first seeds a cast after the invitation of that cast's auth, by the call it seeds
// with, and invited where that invitation is asked; both token.NoPos for a function that
// seeds none late. The step that invites for every auth (InviteAdministrators) comes
// before any cast seeded after it; an auth's own InviteAdministrator comes before the
// cast seeded on the same receiver, as the source writes it, and says nothing of another
// auth's cast.
func stepsOf(fn *ast.FuncDecl) (invited, seeded token.Pos, by string) {
	var everyAuth token.Pos
	byAuth := map[string]token.Pos{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || seeded.IsValid() {
			return true
		}
		name, receiver := "", ""
		switch called := call.Fun.(type) {
		case *ast.SelectorExpr:
			name, receiver = called.Sel.Name, types.ExprString(called.X)
		case *ast.Ident:
			name = called.Name
		}
		switch name {
		case inviteAdministratorsFunc:
			if !everyAuth.IsValid() {
				everyAuth = call.Pos()
			}
		case inviteAdministratorFunc:
			if _, asked := byAuth[receiver]; !asked {
				byAuth[receiver] = call.Pos()
			}
		case seedCastFunc, seedCastFileFunc:
			before, asked := byAuth[receiver]
			switch {
			case everyAuth.IsValid() && (!asked || everyAuth < before):
				invited, seeded, by = everyAuth, call.Pos(), name
			case asked:
				invited, seeded, by = before, call.Pos(), name
			}
		}

		return true
	})

	return invited, seeded, by
}

// foothold is what an auth package shows of the first administrator it brings in.
type foothold struct {
	// seeds is where the package declares SeedAdministrator, or seedFoothold where it
	// declares that alone (file:line): the seed that creates an account; empty where it
	// declares neither.
	seeds string
	// invites is where the package declares InviteAdministrator (file:line), empty where
	// it declares none.
	invites string
	// asks reports that InviteAdministrator asks the session library for the invitation
	// (a call to InviteFirstAdministrator).
	asks bool
	// reinvites is where the package declares ReinviteAdministrator (file:line), empty
	// where it declares none.
	reinvites string
	// reasks reports that ReinviteAdministrator asks the session library for the fresh
	// invitation (a call to ReinviteFirstAdministrator).
	reasks bool
	// creates is where InviteAdministrator or ReinviteAdministrator creates a user
	// (file:line): a call to CreateSessionUser or a session.CreateUserRequest it builds;
	// empty where neither does.
	creates string
	// identity reports that the package builds the auth's generated identity client: a
	// hand-written file imports the package the application's generator writes for the
	// auth (<tree>/<name>identity), whose acceptance hook gives an invitation its roles.
	identity bool
	// accepts reports that the package sets what an accepted invitation gives: a
	// hand-written file calls SetInvitationAccepted.
	accepts bool
}

// identityPackageSuffix ends the name of the package an application's generator writes
// an auth's identity client in: <name>identity, under the tree GenerateIdentity names.
const identityPackageSuffix = "identity"

// givesRoles reports an auth whose roles the application gives: its package holds a
// role file, and no method of it hands role membership to a directory (assignsRoles).
// Such an auth needs somebody to give the first role, the first administrator.
func givesRoles(a *app.App, p *app.AuthPackage) bool {
	if _, err := os.Stat(a.Abs(rolesFileOf(p))); err != nil {
		return false
	}

	return assignsRoles(a, p)
}

// noAcceptanceHook is the finding for an auth that invites its first administrator from
// a package that never sets the acceptance hook: what is wrong, and what to do. at is
// where the invitation is declared.
func noAcceptanceHook(p *app.AuthPackage, at string) string {
	return fmt.Sprintf("%[1]s: the %[2]s auth invites its first administrator and no file of its package sets the acceptance hook (%[3]s), so an accepted invitation would give its person no role and nobody would come to hold the Super Administrator role; the session library refuses to invite for such an Auth. Set the hook where the Auth is built, in New, with the generated identity client's: %[3]s(identityClient.InvitationAccepted())",
		at, p.Name, invitationAcceptedFunc)
}

// noIdentityClient is the finding for an auth whose roles the application gives and
// whose package imports no generated identity client: what is wrong, and what to do.
func noIdentityClient(p *app.AuthPackage) string {
	return fmt.Sprintf("%[1]s: the %[2]s auth gives its roles itself (it holds a role file, and no method is registered with RoleSync) and no file of its package imports a generated identity client (%[2]s%[3]s), so nothing gives an accepted invitation its roles: nobody can be invited as the auth's first administrator, and an account seeded by hand is refused too, which leaves a deployed auth with no way in. Generate its identity client: add generation.GenerateIdentity with a tree of the program's own to the generator program of the outlet that declares the auth and regenerate, build the client from that tree in the auth's New (%[2]s%[3]s.NewClient) and set its acceptance hook (SetInvitationAccepted(identityClient.InvitationAccepted())), then invite the first administrator from foothold.go (%[4]s and %[5]s, as impulse add auth writes them for an auth whose management the application generates)",
		p.Dir, p.Name, identityPackageSuffix, inviteAdministratorFunc, reinviteAdministratorFunc)
}

// readFoothold reads the hand-written files of an auth package for the first
// administrator it brings in: where the seed and the invitation are declared, what the
// invitation's two functions do, and whether the package builds the auth's generated
// identity client and sets the acceptance hook.
func readFoothold(a *app.App, dir string) (*foothold, error) {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadDir()")
	}
	found := &foothold{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || isTestFile(name) || strings.HasPrefix(name, "zz_gen_") {
			continue
		}
		rel := path.Join(dir, name)
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
		}
		found.readWiring(f, path.Base(dir))
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			at := func(pos token.Pos) string {
				return fmt.Sprintf("%s:%d", rel, fset.Position(pos).Line)
			}
			switch fn.Name.Name {
			case seedAdministratorFunc, seedFootholdFunc:
				if found.seeds == "" {
					found.seeds = at(fn.Pos())
				}
			case inviteAdministratorFunc:
				if found.invites == "" {
					found.invites = at(fn.Pos())
				}
				asks, creates := readInvitation(fn, inviteFirstFunc)
				found.asks = found.asks || asks
				if creates.IsValid() && found.creates == "" {
					found.creates = at(creates)
				}
			case reinviteAdministratorFunc:
				if found.reinvites == "" {
					found.reinvites = at(fn.Pos())
				}
				asks, creates := readInvitation(fn, reinviteFirstFunc)
				found.reasks = found.reasks || asks
				if creates.IsValid() && found.creates == "" {
					found.creates = at(creates)
				}
			}
		}
	}

	return found, nil
}

// readWiring reads one file of the package of the auth named for what gives an accepted
// invitation its roles: an import of the auth's generated identity client
// (<name>identity), and a call that sets the acceptance hook.
func (f *foothold) readWiring(file *ast.File, auth string) {
	for _, imported := range file.Imports {
		if path.Base(strings.Trim(imported.Path.Value, `"`)) == auth+identityPackageSuffix {
			f.identity = true
		}
	}
	if callsMethod(file, invitationAcceptedFunc) {
		f.accepts = true
	}
}

// readInvitation reads one of the invitation's two functions: whether it asks the
// session library for the first administrator's invitation, by a call to the method
// named, and where it creates a user, by a call to CreateSessionUser or a
// CreateUserRequest it builds (token.NoPos where it creates none).
func readInvitation(fn *ast.FuncDecl, method string) (asks bool, creates token.Pos) {
	if fn.Body == nil {
		return false, token.NoPos
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case method:
				asks = true
			case createSessionUserFunc:
				if !creates.IsValid() {
					creates = node.Pos()
				}
			}
		case *ast.CompositeLit:
			if namesType(node.Type, createUserRequestType) && !creates.IsValid() {
				creates = node.Pos()
			}
		}

		return true
	})

	return asks, creates
}

// namesType reports a composite literal's type written as the named type, qualified by
// its package or not.
func namesType(expr ast.Expr, name string) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == name
	case *ast.Ident:
		return t.Name == name
	default:
		return false
	}
}

// findings says what is wrong with the first administrator the auth named brings in,
// and what to do; none where it invites one and nothing else.
func (f *foothold) findings(auth string) []string {
	variable := authpkg.AdministratorVariable(auth)
	var findings []string
	if f.seeds != "" {
		findings = append(findings, fmt.Sprintf("%s: the %s auth seeds its first administrator as an account (%s), under a password the deployment generates or a sign-in name the placement names. Nobody can read a generated password, and an account made ahead of its person is tied to whoever signs in first by its name alone, so the first administrator is invited instead: replace %s and %s with %s and %s as impulse add auth writes foothold.go (session.AuthAPI.InviteFirstAdministrator and ReinviteFirstAdministrator, mailed to the address the placement names in %s), read that address in the data level in place of the password (the %s_PASSWORD declaration and its secret:\"generated\" tag go), and call the two from pkg/deploy and the migrate command",
			f.seeds, auth, seedAdministratorFunc, seedAdministratorFunc, seedFootholdFunc, inviteAdministratorFunc, reinviteAdministratorFunc, variable, variable))
	}
	if f.creates != "" {
		findings = append(findings, fmt.Sprintf("%s: the %s auth creates a user where it invites its first administrator (session.AuthAPI.%s or a session.%s): the person's account is made by their own first sign-in from the mailed link, and none is created ahead of them, with a password or without; remove the user it creates",
			f.creates, auth, createSessionUserFunc, createUserRequestType))
	}
	if f.invites != "" && !f.asks {
		findings = append(findings, fmt.Sprintf("%s: the %s auth's %s does not ask the session library for the invitation (session.AuthAPI.%s), which is what stops once somebody holds the Super Administrator role, withdraws an invitation the placement no longer names, and refuses to write the link to a log; invite through it, as impulse add auth writes foothold.go",
			f.invites, auth, inviteAdministratorFunc, inviteFirstFunc))
	}
	if f.reinvites != "" && !f.reasks {
		findings = append(findings, fmt.Sprintf("%s: the %s auth's %s does not ask the session library for the fresh invitation (session.AuthAPI.%s), which is what refuses once somebody holds the Super Administrator role, withdraws the standing invitation so its link no longer opens, and refuses to write the new link to a log; invite again through it, as impulse add auth writes foothold.go",
			f.reinvites, auth, reinviteAdministratorFunc, reinviteFirstFunc))
	}

	return findings
}

// Meaning explains the invariant for the brief.
func (firstAdministrator) Meaning() string {
	return "The first administrator of a deployed application comes in by an invitation mailed to the address the placement names, for each environment, in `APP_<AUTH>_ADMINISTRATOR`: while nobody of the auth holds the Super Administrator role, the migrate command asks for it (`InviteAdministrator` in `pkg/auth/<name>/foothold.go`, over `session.AuthAPI.InviteFirstAdministrator`), and the person opens the link and signs in by a method of the auth, with a password they choose there or through a directory, however many organizations the directory method admits. The invitation gives the Super Administrator role and the roles `FirstAdministratorRoles` names. No account is created ahead of the person and no password is generated: nobody can read a generated password, and an account that has never signed in is tied to whoever signs in first by its name alone. A release leaves an invitation that stands as it is, replaces one that ran out, and withdraws one for an address the placement no longer names, which it does even once somebody holds the Super Administrator role, when nobody is invited any more; an account that holds no such role stops no invitation; `ReinviteAdministrator` (the migrate command's `-reinvite-administrator <auth>`) withdraws the standing one and mails a fresh one while nobody holds the role; and both refuse while nobody holds it and the application has no mailbox to send as, since an invitation's link is never written to a log. The check refuses an auth package that still seeds an account (`SeedAdministrator`), one that creates a user where it invites, and one whose `InviteAdministrator` or `ReinviteAdministrator` does not go through the session library. It refuses, too, an auth whose roles the application gives whose package imports no generated identity client (`<name>identity`, from the tree `generation.GenerateIdentity` names): the client's acceptance hook gives an accepted invitation its roles, so without it nobody can be invited as the auth's first administrator, and the auth has no way in until the application generates the management of its people; and an auth that invites from a package that never sets the hook (`SetInvitationAccepted`), which the session library refuses to invite without. And it refuses a function that seeds a cast of personas (`SeedCast`, or `SeedCastFile`, the same seed over a cast file handed to it) after it invites, for every auth (`InviteAdministrators`) or for the cast's own auth (`InviteAdministrator` on the same receiver): a persona may hold the Super Administrator role, so an auth's cast is seeded before its invitation, and an environment whose cast holds the role invites nobody and needs no mailbox. An auth whose directory writes the roles brings in nobody: the directory decides who its administrators are."
}
