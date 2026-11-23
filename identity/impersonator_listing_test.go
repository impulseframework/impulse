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
	ofSignIn  = "it sets what every sign-in of the auth asks for, and names no person"
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
	"Manager.LeaveTenant":           {subject: true},
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
	"Manager.PersonTenants": {
		person: true,
		none:   "it reads the tenants the person holds something in, and what leaving each would do, and changes nothing: leaving one is LeaveTenant, which asks",
	},
	"Manager.HoldsTenant": {
		person: true,
		none:   "it reads whether the person holds a role in some tenant, in the caller's transaction, and changes nothing: the route that asks it has refused the real person behind a session that acts as a role already",
	},
	"Manager.ClearEnded": {
		person: true,
		none:   "it is nobody's act: the removal, at a person's own sign-in, of rows that ended by themselves and count for nothing already",
	},

	// No person named.
	"Manager.EndImpersonation": {
		none: "it ends a session that acts as someone else, which is the session of the person acted as or of no account at all: the person who started one ends it, here and by the session library's own way out",
	},
	"Manager.TakeRole": {
		none: "it names no person: the role goes to the request's caller, and a session that acts as someone else takes nothing, for the person acted as or for the person behind it (taker)",
	},
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
	"Manager.OwnTenants": {
		none: "it builds the session library's hook of a person's own tenants and changes nothing itself: the hook's Leave hands the person to LeaveTenant, which asks, and its List reads (PersonTenants)",
	},
	"Manager.AnnounceWith": {
		none: "it hands the manager the function its sign-in hooks announce what they wrote through, once, where the application is wired, and names no person",
	},

	// What every sign-in asks for (signin_settings.go).
	"Manager.RequireSecondFactor":              {none: ofSignIn},
	"Manager.StopRequiringSecondFactorWarning": {none: ofSignIn},
	"Manager.StopRequiringSecondFactor":        {none: ofSignIn},
	"Manager.SetRememberedBrowsers":            {none: ofSignIn},

	"Manager.Tenant":                          {none: reads},
	"Manager.TenantRoles":                     {none: reads},
	"Manager.TenantAdministratorRoles":        {none: reads},
	"Manager.AdministratorsEnterWithPassword": {none: reads},
	"Manager.CallerPassword":                  {none: reads},
	"Manager.DirectoryRoles":                  {none: reads},
	"Manager.RolesToGive":                     {none: reads},
	"Manager.GivableRoles":                    {none: reads},
	"Manager.GivableGroupRoles":               {none: reads},
	"Manager.RolesToTake":                     {none: reads},
	"Manager.DirectoryName":                   {none: reads},
	"Manager.CheckInvitedTenant":              {none: reads},
	"Manager.InvitedTenants":                  {none: reads},
	"Manager.SignInMethods":                   {none: reads},
	"Manager.AnyMethodOpen":                   {none: reads},
	"Manager.SignInSettings":                  {none: reads},
	"Manager.SignUpRoles":                     {none: reads},
	"Manager.StartingRoles":                   {none: reads},
	"Manager.SkippedStoredGrants":             {none: reads},
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
// listed, the ones that change something of a person ask the guard about that person
// before they change anything, and no method that takes a person is let off by a line
// left out.
//
// What a method asks is read from the code, statement by statement of its body. A
// method asks when one of its body's own statements, not one inside a branch, a loop or
// a function literal, hands the person to the guard and returns what it answers:
// `if err := <guard>(...); err != nil { return ... }`, or `return <call>(...)`. For a
// method listed as naming a person, the guard is one of the two subject guards given
// its userID, or a method of the Manager or the Tenant that asks, given its userID. For
// one listed as reaching a person by another key, it is the guard or the method its
// line names. Either way the statement comes before the first statement that changes
// anything: a write of the store, a message sent after the commit, or one of the steps
// that write (writesSomething).
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
	if !writesSomething(methods["Manager.SetName"].Body, "m") || writesSomething(methods["Manager.RequireUser"].Body, "m") {
		t.Fatal("the test no longer reads which statements write: Manager.SetName writes and Manager.RequireUser does not")
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
			t.Errorf("%s takes a person and is not listed in guardedMethods: begin it with\n\tif err := m.refuseImpersonatorAsSubject(ctx, %s, \"<what was asked, as the end of 'you cannot'>\"); err != nil {\n\t\treturn err\n\t}\nand list it as {subject: true}", name, personParameter)
		case !listed:
			t.Errorf("%s is not listed in guardedMethods: say whether it changes something of a person, and how it reaches them", name)
		case rule.subject && !asks[name]:
			t.Errorf("%s names a person by %s and does not ask the guard about them before it changes anything: begin it with if err := m.refuseImpersonatorAsSubject(ctx, %s, ...); err != nil { return err }, or hand %s to a method that does in a return statement", name, personParameter, personParameter, personParameter)
		case rule.subject && !takesPerson:
			t.Errorf("%s is listed as naming a person by %s and has no such parameter beside a transaction it writes in", name, personParameter)
		case rule.by != "" && !asksFirst(fn, func(c *ast.CallExpr) bool {
			return callNamed(fn, c, rule.by)
		}):
			t.Errorf("%s reaches a person by another key and does not ask %s, in a statement of its own that returns what it answers, before it changes anything", name, rule.by)
		case rule.by != "" && strings.Contains(rule.by, ".") && guardedMethods[rule.by].by == "" && !guardedMethods[rule.by].subject:
			t.Errorf("%s hands its request to %s, which guardedMethods does not list as asking the guard", name, rule.by)
		case rule.none != "" && takesPerson && !rule.person:
			t.Errorf("%s takes a person and is listed as asking nothing: guard it, or say with person: true why this one needs no guard", name)
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

// takesAPerson reports whether the method takes a person to act on, beside a
// transaction it writes in: a parameter named userID, or any parameter that names
// people by their account, an id or ids whose name says user, or a user as the
// permission store names one (accesstypes.User).
func takesAPerson(fn *ast.FuncDecl) bool {
	var person, writes bool
	for _, param := range fn.Type.Params.List {
		typ := typeText(param.Type)
		for _, name := range param.Names {
			lower := strings.ToLower(name.Name)
			byID := strings.Contains(lower, "user") && strings.HasSuffix(typ, "impulse.UUID")
			if name.Name == personParameter || byID || strings.HasSuffix(typ, "accesstypes.User") {
				person = true
			}
		}
		if selector, ok := param.Type.(*ast.SelectorExpr); ok && selector.Sel.Name == "ReadWriteTransaction" {
			writes = true
		}
	}

	return person && writes
}

// typeText writes a parameter's type as the source spells it, for the parts the test
// reads: a name, a qualified name, a pointer or a slice of either.
func typeText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return typeText(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + typeText(e.X)
	case *ast.ArrayType:
		return "[]" + typeText(e.Elt)
	default:
		return ""
	}
}

// managerOf is how a method names the Manager it calls: its receiver for a method of
// the Manager, and the receiver's manager field for a method of a Tenant.
func managerOf(fn *ast.FuncDecl) (receiver, manager string) {
	receiver = receiverName(fn)
	if receiverType(fn) == "Tenant" {
		return receiver, receiver + ".manager"
	}

	return receiver, receiver
}

// callTarget names a call the method makes on its receiver or on the Manager it calls,
// as Type.Method, with the bare name beside it. Any other call names nothing.
func callTarget(fn *ast.FuncDecl, call *ast.CallExpr) (method, name string) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	receiver, manager := managerOf(fn)
	switch on := typeText(selector.X); on {
	case receiver:
		return receiverType(fn) + "." + selector.Sel.Name, selector.Sel.Name
	case manager:
		return "Manager." + selector.Sel.Name, selector.Sel.Name
	default:
		return "", ""
	}
}

// callNamed reports whether the call is the one named: a guard by its bare name, or a
// method as Type.Method.
func callNamed(fn *ast.FuncDecl, call *ast.CallExpr, what string) bool {
	method, name := callTarget(fn, call)

	return method != "" && (name == what || method == what)
}

// handsThePerson reports whether the call hands over the method's person, its userID.
func handsThePerson(call *ast.CallExpr) bool {
	return slices.ContainsFunc(call.Args, func(arg ast.Expr) bool {
		ident, ok := arg.(*ast.Ident)

		return ok && ident.Name == personParameter
	})
}

// askingCall is the call a statement of a method's body makes and answers with: the
// call in `if err := <call>; err != nil { return ... }` and in `return <call>`, nil for
// any other statement. The if's branch ends in a return that carries what the call
// answered, the err the statement assigned, as it is or wrapped: a branch that returns
// nil has asked and thrown the answer away, and is no asking.
func askingCall(statement ast.Stmt) *ast.CallExpr {
	switch st := statement.(type) {
	case *ast.ReturnStmt:
		if len(st.Results) == 1 {
			call, _ := st.Results[0].(*ast.CallExpr)

			return call
		}
	case *ast.IfStmt:
		assign, ok := st.Init.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || len(st.Body.List) == 0 {
			return nil
		}
		answer, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return nil
		}
		last, returns := st.Body.List[len(st.Body.List)-1].(*ast.ReturnStmt)
		if !returns || !names(last, answer.Name) {
			return nil
		}
		call, _ := assign.Rhs[0].(*ast.CallExpr)

		return call
	}

	return nil
}

// names reports whether the node names the identifier anywhere in it.
func names(node ast.Node, identifier string) bool {
	named := false
	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == identifier {
			named = true
		}

		return !named
	})

	return named
}

// asksFirst reports whether one of the statements of the method's body asks a call the
// test accepts, and answers with it, before any statement of the body changes
// anything.
func asksFirst(fn *ast.FuncDecl, accepted func(call *ast.CallExpr) bool) bool {
	receiver, _ := managerOf(fn)
	for _, statement := range fn.Body.List {
		if call := askingCall(statement); call != nil && accepted(call) {
			return true
		}
		if writesSomething(statement, receiver) {
			return false
		}
	}

	return false
}

// writeVerbs open the name of every method of the Store that writes, and writeSteps
// are the Manager's own steps that write or send.
var (
	writeVerbs = []string{"Insert", "Update", "Delete", "Set", "Remove", "Revoke", "End", "Clear", "Consume", "Rename"}
	writeSteps = []string{"mailAfterCommit", "noticeAfterCommit", "resetLinksEndAtCommit", "endUserSessions", "endSession", "endSessionAndRecord", "removeMembership", "deleteAccount", "deleteWithLastRole"}
)

// writesSomething reports whether the node calls a write: a method of the store whose
// name opens with a write verb, or one of the Manager's steps that write or send.
func writesSomething(node ast.Node, receiver string) bool {
	writes := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !writes
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		on, name := typeText(selector.X), selector.Sel.Name
		if strings.HasSuffix(on, ".store") && slices.ContainsFunc(writeVerbs, func(verb string) bool {
			return strings.HasPrefix(name, verb)
		}) {
			writes = true
		}
		if (on == receiver || on == receiver+".manager") && slices.Contains(writeSteps, name) {
			writes = true
		}

		return !writes
	})

	return writes
}

// askingAboutThePerson returns the methods that ask the guard about the person they
// name before they change anything: the ones that hand their userID to a subject
// guard, and the ones that hand it to a method that asks, however far down, each in a
// statement of their body's own that answers with what it asked (asksFirst).
func askingAboutThePerson(methods map[string]*ast.FuncDecl) map[string]bool {
	asks := make(map[string]bool)
	for grew := true; grew; {
		grew = false
		for name, fn := range methods {
			if asks[name] || !takesAPerson(fn) {
				continue
			}
			if asksFirst(fn, func(call *ast.CallExpr) bool {
				method, called := callTarget(fn, call)

				return handsThePerson(call) && (slices.Contains(subjectGuards, called) || asks[method])
			}) {
				asks[name] = true
				grew = true
			}
		}
	}

	return asks
}
