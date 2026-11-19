package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// subjectRule says how the rule that nobody is the subject of what they do while they
// act as someone else applies to one exported method of the Manager or of a Tenant.
type subjectRule struct {
	// subject says the method names a person by the id of their account, its userID
	// parameter, and asks the guard about that id: itself, or by handing the id to a
	// method that does.
	subject bool
	// by names what a method that reaches a person by another key asks: one of the
	// guards, or the method it hands its request to (Manager.Invite).
	by string
	// none says why the method asks nothing. person says it of a method that takes a
	// person all the same, so that exception is never made by leaving a line out.
	none   string
	person bool
}

// The reasons a method asks nothing, where several share one.
const (
	reads     = "it reads, and the rule is for changes: the person's own row is still listed and read"
	atSignIn  = "it answers a hook the session library runs as a person signs in, which no impersonated session does"
	ofAGroup  = "it names a group and no person; a role that reaches the real person through a group they are in is what the rule cannot see, and what a mask that refuses the class access refuses"
	ofAMethod = "it sets what a sign-in method of the auth offers everyone, and names no person"
	withdrawn = "it takes back what an invitation would have given, and gives the real person nothing"
)

// guardedMethods lists every exported method of the Manager and of a Tenant, each with
// its decision. It is the whole input of Test_everyMethodThatNamesAPersonAsksTheGuard:
// a method added to either type fails the test until it is listed here, a method that
// takes a person (a userID parameter beside a transaction it writes in) fails it until
// it asks the guard about that person, and a line that no longer names a method fails
// it too.
var guardedMethods = map[string]subjectRule{
	// A person named by the id of their account.
	"Manager.RenameUser":            {subject: true},
	"Manager.DeleteUser":            {subject: true},
	"Manager.DisableUser":           {subject: true},
	"Manager.EnableUser":            {subject: true},
	"Manager.SetTemporaryPassword":  {subject: true},
	"Manager.RequirePasswordChange": {subject: true},
	"Manager.RemovePassword":        {subject: true},
	"Manager.SendPasswordResetLink": {subject: true},
	"Manager.SetEmail":              {subject: true},
	"Manager.SetName":               {subject: true},
	"Manager.RevokeTrustedDevices":  {subject: true},
	"Manager.EndUserSessions":       {subject: true},
	"Manager.AssignRole":            {subject: true},
	"Manager.AssignRoleFor":         {subject: true},
	"Manager.RemoveRole":            {subject: true},
	"Manager.AddGroupMember":        {subject: true},
	"Manager.AddGroupMemberFor":     {subject: true},
	"Manager.RemoveGroupMember":     {subject: true},
	"Tenant.AssignRole":             {subject: true},
	"Tenant.AssignRoleFor":          {subject: true},
	"Tenant.RemoveRole":             {subject: true},
	"Tenant.AddGroupMember":         {subject: true},
	"Tenant.AddGroupMemberFor":      {subject: true},
	"Tenant.RemoveGroupMember":      {subject: true},

	// A person reached by another key: the account a sign-in identity or a token
	// belongs to, a session of theirs, the name their failed sign-ins are counted
	// under, and the address an invitation is mailed to.
	"Manager.UnlinkIdentity": {by: "refuseImpersonatorAsSubject"},
	"Manager.RevokeToken":    {by: "refuseImpersonatorAsSubject"},
	"Manager.EndSession":     {by: "refuseImpersonatorsSession"},
	"Manager.UnlockSignIn":   {by: "refuseImpersonatorsName"},
	"Manager.Invite":         {by: "refuseImpersonatorsAddress"},
	"Manager.InviteTenant":   {by: "refuseImpersonatorsAddress"},
	"Tenant.Invite":          {by: "Manager.Invite"},

	// A person named, and nothing asked.
	"Manager.RequireUser": {person: true, none: "it reads whether the account exists, and changes nothing"},
	"Manager.SignInWays":  {person: true, none: reads},
	"Manager.ClearEnded": {
		person: true,
		none:   "it is nobody's act: the removal, at a person's own sign-in, of rows that ended by themselves and count for nothing already",
	},

	// No person named.
	"Manager.EndImpersonation": {
		none: "it ends a session that acts as someone else, which is the session of the person acted as or of no account at all: the person who started one ends it, here and by the session library's own way out",
	},
	"Manager.CreateUser": {
		none: "it makes an account that did not exist, which is nobody's yet; an account made for another address of the real person's is a road the rule cannot see",
	},
	"Tenant.CreateUser":                {none: "it is Manager.CreateUser for one tenant"},
	"Manager.CreateGroup":              {none: ofAGroup},
	"Manager.DescribeGroup":            {none: ofAGroup},
	"Manager.DeleteGroup":              {none: ofAGroup},
	"Manager.GiveGroupRole":            {none: ofAGroup},
	"Manager.GiveGroupRoleFor":         {none: ofAGroup},
	"Manager.TakeGroupRole":            {none: ofAGroup},
	"Tenant.CreateGroup":               {none: ofAGroup},
	"Tenant.DescribeGroup":             {none: ofAGroup},
	"Tenant.DeleteGroup":               {none: ofAGroup},
	"Tenant.GiveGroupRole":             {none: ofAGroup},
	"Tenant.GiveGroupRoleFor":          {none: ofAGroup},
	"Tenant.TakeGroupRole":             {none: ofAGroup},
	"Manager.WithdrawInvitation":       {none: withdrawn},
	"Manager.WithdrawTenantInvitation": {none: withdrawn},
	"Manager.OpenSignUp":               {none: ofAMethod},
	"Manager.OpenSignUpWarning":        {none: ofAMethod},
	"Manager.MakeInviteOnly":           {none: ofAMethod},
	"Manager.SetSelfServiceReset":      {none: ofAMethod},
	"Manager.DirectoryRole":            {none: "it sets the role a tenant's directory gives everyone who arrives through it, and names a tenant and no person"},
	"Manager.ClearTenant":              {none: "it names a tenant: its roles and the people it alone held go with the tenant's record, whose removal is the act"},
	"Manager.SignedIn":                 {none: atSignIn},
	"Manager.SignedUp":                 {none: atSignIn},
	"Manager.InvitationAccepted":       {none: atSignIn},
	"Manager.DirectorySeated":          {none: atSignIn},

	"Manager.Tenant":                          {none: reads},
	"Manager.TenantRoles":                     {none: reads},
	"Manager.TenantAdministratorRoles":        {none: reads},
	"Manager.AdministratorsEnterWithPassword": {none: reads},
	"Manager.CallerPassword":                  {none: reads},
	"Manager.DirectoryRoles":                  {none: reads},
	"Manager.RolesToGive":                     {none: reads},
	"Manager.DirectoryName":                   {none: reads},
	"Manager.CheckInvitedTenant":              {none: reads},
	"Manager.InvitedTenants":                  {none: reads},
	"Manager.SignInMethods":                   {none: reads},
	"Manager.AnyMethodOpen":                   {none: reads},
	"Manager.SignUpRoles":                     {none: reads},
	"Tenant.Domain":                           {none: reads},
}

// subjectGuards are the two forms of the rule a method asks about a person named by the
// id of their account: the one for the person who impersonates, and the one that holds
// in every session and ends in the first (refuseOwnAccount).
var subjectGuards = []string{"refuseImpersonatorAsSubject", "refuseOwnAccount"}

// personParameter is the name the package gives the id of the account a method acts on.
const personParameter = "userID"

// Test_everyMethodThatNamesAPersonAsksTheGuard reads the package's own source and holds
// every exported method of the Manager and of a Tenant to guardedMethods: each is
// listed, the ones that change something of a person ask the guard about that person,
// and no method that takes a person is let off by a line left out.
//
// What a method asks is read from the code. A method listed as naming a person asks
// when its body calls one of the two guards with its userID, or hands its userID to a
// method of the Manager or of the Tenant that asks. A method listed as reaching a
// person by another key asks when its body calls the guard, or the method, its line
// names.
func Test_everyMethodThatNamesAPersonAsksTheGuard(t *testing.T) {
	t.Parallel()

	methods := exportedMethods(t, "Manager", "Tenant")
	asks := askingAboutThePerson(methods)
	for _, want := range []string{"Manager.AssignRoleFor", "Manager.DeleteUser", "Manager.AssignRole", "Tenant.AddGroupMember"} {
		if !asks[want] {
			t.Fatalf("%s was not read as asking the guard about its person: the test no longer reads the code as it is written", want)
		}
	}
	if asks["Manager.RequireUser"] {
		t.Fatal("Manager.RequireUser was read as asking the guard, and it asks nothing")
	}

	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fn := methods[name]
		rule, listed := guardedMethods[name]
		takesPerson := takesAPerson(fn)
		switch {
		case !listed && takesPerson:
			t.Errorf("%s takes a person (%s) and is not listed in guardedMethods: begin it with\n\tif err := m.refuseImpersonatorAsSubject(ctx, %s, \"<what was asked, as the end of 'you cannot'>\"); err != nil {\n\t\treturn err\n\t}\nand list it as {subject: true}", name, personParameter, personParameter)
		case !listed:
			t.Errorf("%s is not listed in guardedMethods: say whether it changes something of a person, and how it reaches them", name)
		case rule.subject && !asks[name]:
			t.Errorf("%s names a person by %s and does not ask the guard about them: call m.refuseImpersonatorAsSubject(ctx, %s, ...) before it changes anything, or hand %s to a method that does", name, personParameter, personParameter, personParameter)
		case rule.subject && !takesPerson:
			t.Errorf("%s is listed as naming a person by %s and has no such parameter beside a transaction it writes in", name, personParameter)
		case rule.by != "" && !calls(fn, rule.by):
			t.Errorf("%s reaches a person by another key and does not call %s", name, rule.by)
		case rule.by != "" && strings.Contains(rule.by, ".") && guardedMethods[rule.by].by == "" && !guardedMethods[rule.by].subject:
			t.Errorf("%s hands its request to %s, which guardedMethods does not list as asking the guard", name, rule.by)
		case rule.none != "" && takesPerson && !rule.person:
			t.Errorf("%s takes a person (%s) and is listed as asking nothing: guard it, or say with person: true why this one needs no guard", name, personParameter)
		case rule.none == "" && rule.by == "" && !rule.subject:
			t.Errorf("guardedMethods lists %s with no decision", name)
		}
	}
	for name := range guardedMethods {
		if _, declared := methods[name]; !declared {
			t.Errorf("guardedMethods lists %s, which is no exported method of the package any more: remove the line", name)
		}
	}
}

// exportedMethods parses the package's own files, its tests apart, and returns the
// exported methods declared on the types named, each under Type.Method.
func exportedMethods(t *testing.T, receivers ...string) map[string]*ast.FuncDecl {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	methods := make(map[string]*ast.FuncDecl)
	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(files, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parser.ParseFile(%s) error = %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !fn.Name.IsExported() || !slices.Contains(receivers, receiverType(fn)) {
				continue
			}
			methods[receiverType(fn)+"."+fn.Name.Name] = fn
		}
	}
	if len(methods) == 0 {
		t.Fatalf("no exported method of %s found in the package's files", strings.Join(receivers, " or "))
	}

	return methods
}

// receiverType names the type a method is declared on, with or without a pointer, and
// is empty for a function.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	ident, ok := typ.(*ast.Ident)
	if !ok {
		return ""
	}

	return ident.Name
}

// receiverName is the name a method gives its receiver, empty where it gives none.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
		return ""
	}

	return fn.Recv.List[0].Names[0].Name
}

// takesAPerson reports whether the method takes a person to act on: a parameter named
// userID, beside a transaction it writes in.
func takesAPerson(fn *ast.FuncDecl) bool {
	var person, writes bool
	for _, param := range fn.Type.Params.List {
		for _, name := range param.Names {
			if name.Name == personParameter {
				person = true
			}
		}
		if selector, ok := param.Type.(*ast.SelectorExpr); ok && selector.Sel.Name == "ReadWriteTransaction" {
			writes = true
		}
	}

	return person && writes
}

// called is one call a method makes on its receiver or on its receiver's manager: the
// method called, as Type.Method where the type is read from the receiver, and whether
// the caller's person is among what it hands over.
type called struct {
	method     string
	name       string
	withPerson bool
}

// callsOf lists the calls a method makes on its receiver (m.name, t.name) and on its
// receiver's manager (t.manager.name).
func callsOf(fn *ast.FuncDecl) []called {
	receiver, on := receiverName(fn), receiverType(fn)
	var calls []called
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		typ := ""
		switch x := selector.X.(type) {
		case *ast.Ident:
			if x.Name == receiver {
				typ = on
			}
		case *ast.SelectorExpr:
			if inner, ok := x.X.(*ast.Ident); ok && inner.Name == receiver && x.Sel.Name == "manager" {
				typ = "Manager"
			}
		}
		if typ == "" {
			return true
		}
		withPerson := slices.ContainsFunc(call.Args, func(arg ast.Expr) bool {
			ident, ok := arg.(*ast.Ident)

			return ok && ident.Name == personParameter
		})
		calls = append(calls, called{method: typ + "." + selector.Sel.Name, name: selector.Sel.Name, withPerson: withPerson})

		return true
	})

	return calls
}

// calls reports whether the method's body calls what is named: a guard by its name, or
// a method as Type.Method.
func calls(fn *ast.FuncDecl, what string) bool {
	return slices.ContainsFunc(callsOf(fn), func(c called) bool {
		return c.name == what || c.method == what
	})
}

// askingAboutThePerson returns the methods that ask the guard about the person they
// name: the ones that call a guard with their userID, and the ones that hand their
// userID to a method that asks, however far down.
func askingAboutThePerson(methods map[string]*ast.FuncDecl) map[string]bool {
	asks := make(map[string]bool)
	for grew := true; grew; {
		grew = false
		for name, fn := range methods {
			if asks[name] || !takesAPerson(fn) {
				continue
			}
			if slices.ContainsFunc(callsOf(fn), func(c called) bool {
				return c.withPerson && (slices.Contains(subjectGuards, c.name) || asks[c.method])
			}) {
				asks[name] = true
				grew = true
			}
		}
	}

	return asks
}
