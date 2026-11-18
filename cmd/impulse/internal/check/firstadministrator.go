package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// firstAdministrator verifies that an auth that brings in a first administrator invites
// one. The first person into a deployed application comes in as everyone after them
// does: the migrate command has an invitation mailed to the address the placement
// names, while the auth has no account, and the person accepts it by signing in through
// a method of the auth (InviteAdministrator in the package's foothold.go, over
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
// invitation, which is what stops once the auth has an account, withdraws an invitation
// the placement no longer names and refuses to write the link to a log.
type firstAdministrator struct{}

// firstAdministratorName is the check's name.
const firstAdministratorName = "first-administrator"

func (firstAdministrator) Name() string {
	return firstAdministratorName
}

func (firstAdministrator) Describe() string {
	return "an auth that brings in a first administrator invites one: its package asks the session library for the invitation (InviteAdministrator over session.AuthAPI.InviteFirstAdministrator), mailed to the address the placement names, and creates no account and no password ahead of the person"
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
		if foothold.seeds == "" && foothold.invites == "" && foothold.reinvites == "" {
			continue // the package brings in no first administrator
		}
		if findings := foothold.findings(p.Name); len(findings) > 0 {
			details = append(details, findings...)

			continue
		}
		held = append(held, p.Name)
	}

	switch {
	case len(details) > 0:
		return fail(c.Name(), fmt.Sprintf("%d finding(s): a first administrator is brought in another way than by an invitation", len(details)), details...)
	case len(held) == 0:
		return skip(c.Name(), "no auth brings in a first administrator")
	default:
		return pass(c.Name(), "the first administrator is invited: "+strings.Join(held, ", "))
	}
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
}

// readFoothold reads the hand-written files of an auth package for the first
// administrator it brings in: where the seed and the invitation are declared, and what
// the invitation's two functions do.
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
		findings = append(findings, fmt.Sprintf("%s: the %s auth's %s does not ask the session library for the invitation (session.AuthAPI.%s), which is what stops once the auth has an account, withdraws an invitation the placement no longer names, and refuses to write the link to a log; invite through it, as impulse add auth writes foothold.go",
			f.invites, auth, inviteAdministratorFunc, inviteFirstFunc))
	}
	if f.reinvites != "" && !f.reasks {
		findings = append(findings, fmt.Sprintf("%s: the %s auth's %s does not ask the session library for the fresh invitation (session.AuthAPI.%s), which is what refuses once the auth has an account, withdraws the standing invitation so its link no longer opens, and refuses to write the new link to a log; invite again through it, as impulse add auth writes foothold.go",
			f.reinvites, auth, reinviteAdministratorFunc, reinviteFirstFunc))
	}

	return findings
}

// Meaning explains the invariant for the brief.
func (firstAdministrator) Meaning() string {
	return "The first administrator of a deployed application comes in by an invitation mailed to the address the placement names, for each environment, in `APP_<AUTH>_ADMINISTRATOR`: while the auth has no account, the migrate command asks for it (`InviteAdministrator` in `pkg/auth/<name>/foothold.go`, over `session.AuthAPI.InviteFirstAdministrator`), and the person opens the link and signs in by a method of the auth, with a password they choose there or through a directory, however many organizations the directory method admits. The invitation gives the Super Administrator role and the roles `FirstAdministratorRoles` names. No account is created ahead of the person and no password is generated: nobody can read a generated password, and an account that has never signed in is tied to whoever signs in first by its name alone. A release leaves an invitation that stands as it is, replaces one that ran out, and withdraws one for an address the placement no longer names, which it does even once the auth has an account, when nobody is invited any more; `ReinviteAdministrator` (the migrate command's `-reinvite-administrator <auth>`) withdraws the standing one and mails a fresh one while the auth has no account; and both refuse while the application has no mailbox to send as, since an invitation's link is never written to a log. The check refuses an auth package that still seeds an account (`SeedAdministrator`), one that creates a user where it invites, and one whose `InviteAdministrator` or `ReinviteAdministrator` does not go through the session library. An auth whose directory writes the roles brings in nobody: the directory decides who its administrators are."
}
