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

	"github.com/impulseframework/impulse/auditlog"
)

// recordRule says how one exported method of the Manager or of a Tenant keeps the audit
// log, by one decision.
type recordRule struct {
	// acts are the acts the method records: it calls record, in its own body or in an
	// unexported function of the package that it calls, and the code it runs names each
	// act listed.
	acts []auditlog.Act
	// by names the method of the table the method hands its write to, which records.
	by string
	// none says why the method writes no row.
	none string
}

// The reasons a method writes no row.
const (
	noRowReads   = "it reads"
	noRowLibrary = "the session library's write records it"
	noRowSession = "a session, which the Sessions page lists"
	noRowNoAct   = "not an act of the log"
)

// recordedMethods lists every exported method of the Manager and of a Tenant, each with
// its decision. It is the whole input of Test_everyMethodIsListedForTheAuditLog: a
// method added to either type fails the test until it is listed here, and a line that
// no longer names a method fails it too. The rows themselves are pinned by the tests of
// each act (audit_*_test.go); this list is what makes a new method fail until it is
// decided.
var recordedMethods = map[string]recordRule{
	// An account.
	"Manager.RenameUser":            {acts: []auditlog.Act{auditlog.ActUsernameChanged}},
	"Manager.DisableUser":           {acts: []auditlog.Act{auditlog.ActAccountDisabled}},
	"Manager.EnableUser":            {acts: []auditlog.Act{auditlog.ActAccountEnabled}},
	"Manager.DeleteUser":            {acts: []auditlog.Act{auditlog.ActAccountDeleted}},
	"Manager.RequirePasswordChange": {acts: []auditlog.Act{auditlog.ActPasswordChangeRequired}},
	"Manager.RemovePassword":        {acts: []auditlog.Act{auditlog.ActPasswordRemoved}},
	"Manager.UnlinkIdentity":        {acts: []auditlog.Act{auditlog.ActIdentityUnlinked}},
	"Manager.RevokeToken":           {acts: []auditlog.Act{auditlog.ActTokenRevoked}},
	"Manager.RevokeTrustedDevices":  {acts: []auditlog.Act{auditlog.ActBrowsersForgotten}},
	"Manager.SendPasswordResetLink": {none: noRowLibrary},
	"Manager.SetEmail":              {none: noRowLibrary},
	"Manager.SetName":               {none: noRowNoAct},
	"Manager.UnlockSignIn":          {none: noRowNoAct},

	// A session.
	"Manager.EndSession":       {none: noRowSession},
	"Manager.EndUserSessions":  {none: noRowSession},
	"Manager.EndImpersonation": {none: noRowSession},

	// Who holds a role.
	"Manager.AssignRoleFor":    {acts: []auditlog.Act{auditlog.ActRoleGiven}},
	"Manager.AssignRole":       {by: "Manager.AssignRoleFor"},
	"Manager.TakeRole":         {acts: []auditlog.Act{auditlog.ActRoleGiven}},
	"Manager.RemoveRole":       {acts: []auditlog.Act{auditlog.ActRoleTaken}},
	"Manager.ClearEnded":       {acts: []auditlog.Act{auditlog.ActRoleEnded, auditlog.ActGroupPlaceEnded}},
	"Manager.LeaveTenant":      {acts: []auditlog.Act{auditlog.ActRoleTaken, auditlog.ActGroupMemberRemoved}},
	"Manager.RemoveFromTenant": {acts: []auditlog.Act{auditlog.ActRoleTaken, auditlog.ActGroupMemberRemoved}},
	"Manager.OwnTenants":       {by: "Manager.LeaveTenant"},
	"Tenant.AssignRoleFor":     {by: "Manager.AssignRoleFor"},
	"Tenant.AssignRole":        {by: "Manager.AssignRoleFor"},
	"Tenant.RemoveRole":        {acts: []auditlog.Act{auditlog.ActRoleTaken}},
	"Tenant.RemovePerson":      {by: "Manager.RemoveFromTenant"},

	// A group.
	"Manager.CreateGroup":       {acts: []auditlog.Act{auditlog.ActGroupMade}},
	"Manager.DescribeGroup":     {acts: []auditlog.Act{auditlog.ActGroupChanged}},
	"Manager.DeleteGroup":       {acts: []auditlog.Act{auditlog.ActGroupRemoved}},
	"Manager.AddGroupMemberFor": {acts: []auditlog.Act{auditlog.ActGroupMemberAdded}},
	"Manager.AddGroupMember":    {by: "Manager.AddGroupMemberFor"},
	"Manager.RemoveGroupMember": {acts: []auditlog.Act{auditlog.ActGroupMemberRemoved}},
	"Manager.GiveGroupRoleFor":  {acts: []auditlog.Act{auditlog.ActGroupRoleGiven}},
	"Manager.GiveGroupRole":     {by: "Manager.GiveGroupRoleFor"},
	"Manager.TakeGroupRole":     {acts: []auditlog.Act{auditlog.ActGroupRoleTaken}},
	"Tenant.CreateGroup":        {by: "Manager.CreateGroup"},
	"Tenant.DescribeGroup":      {by: "Manager.DescribeGroup"},
	"Tenant.DeleteGroup":        {by: "Manager.DeleteGroup"},
	"Tenant.AddGroupMemberFor":  {by: "Manager.AddGroupMemberFor"},
	"Tenant.AddGroupMember":     {by: "Manager.AddGroupMemberFor"},
	"Tenant.RemoveGroupMember":  {by: "Manager.RemoveGroupMember"},
	"Tenant.GiveGroupRoleFor":   {by: "Manager.GiveGroupRoleFor"},
	"Tenant.GiveGroupRole":      {by: "Manager.GiveGroupRoleFor"},
	"Tenant.TakeGroupRole":      {by: "Manager.TakeGroupRole"},

	// An invitation.
	"Manager.Invite":                   {acts: []auditlog.Act{auditlog.ActInvited}},
	"Manager.InviteTenant":             {acts: []auditlog.Act{auditlog.ActInvited}},
	"Manager.ResendInvitation":         {acts: []auditlog.Act{auditlog.ActInvitationSentAgain}},
	"Manager.WithdrawInvitation":       {acts: []auditlog.Act{auditlog.ActInvitationWithdrawn}},
	"Manager.WithdrawTenantInvitation": {acts: []auditlog.Act{auditlog.ActInvitationWithdrawn}},
	"Tenant.Invite":                    {by: "Manager.Invite"},

	// The hooks the session library runs as a person signs in.
	"Manager.SignedIn": {by: "Manager.ClearEnded"},
	"Manager.SignedUp": {by: "Manager.AssignRoleFor"},
	"Manager.InvitationAccepted": {acts: []auditlog.Act{
		auditlog.ActInvitationAccepted, auditlog.ActRoleGiven, auditlog.ActDirectoryTaken, auditlog.ActDirectoryOnlyOn, auditlog.ActDirectoryOpened,
	}},
	"Manager.DirectorySeated": {acts: []auditlog.Act{auditlog.ActRoleGiven, auditlog.ActRoleTaken, auditlog.ActGroupMemberRemoved}},

	// How people sign in: a sign-in method, every sign-in of the auth, and a tenant's
	// directory.
	"Manager.OpenSignUp":                {acts: []auditlog.Act{auditlog.ActSignUpOpened}},
	"Manager.MakeInviteOnly":            {acts: []auditlog.Act{auditlog.ActSignUpClosed}},
	"Manager.SetSelfServiceReset":       {acts: []auditlog.Act{auditlog.ActSelfServiceResetOn, auditlog.ActSelfServiceResetOff}},
	"Manager.RequireSecondFactor":       {acts: []auditlog.Act{auditlog.ActSecondFactorRequired}},
	"Manager.StopRequiringSecondFactor": {acts: []auditlog.Act{auditlog.ActSecondFactorNotRequired}},
	"Manager.SetRememberedBrowsers":     {acts: []auditlog.Act{auditlog.ActRememberedBrowsersSet}},
	"Manager.RecordTenantSignIn": {acts: []auditlog.Act{
		auditlog.ActDirectoryTaken, auditlog.ActDirectoryRemoved, auditlog.ActDirectoryOnlyOn, auditlog.ActDirectoryOnlyOff,
		auditlog.ActDirectoryOpened, auditlog.ActDirectoryInviteOnly, auditlog.ActDirectorySecondFactorVouched, auditlog.ActDirectorySecondFactorUnvouched,
	}},

	// A tenant's delete: the accounts that were its alone.
	"Manager.TenantKinds": {acts: []auditlog.Act{auditlog.ActAccountDeleted}},

	// A record's delete: the roles held on the record go with it and have no row of their
	// own, as a deleted group's members and roles have none.
	"Manager.RecordKinds": {none: noRowNoAct},

	// What is wired once.
	"Manager.AnnounceWith": {none: noRowNoAct},

	// Reads, the warnings before a change among them.
	"Manager.AdministratorsEnterWithPassword":  {none: noRowReads},
	"Manager.AuditRecords":                     {none: noRowReads},
	"Manager.AuthSecondFactor":                 {none: noRowReads},
	"Manager.CallerPassword":                   {none: noRowReads},
	"Manager.CheckInvitedTenant":               {none: noRowReads},
	"Manager.DirectoryName":                    {none: noRowReads},
	"Manager.DirectoryRole":                    {none: noRowReads},
	"Manager.DirectoryRoles":                   {none: noRowReads},
	"Manager.GivableGroupRoles":                {none: noRowReads},
	"Manager.GivableRoles":                     {none: noRowReads},
	"Manager.GivableRecordRoles":               {none: noRowReads},
	"Manager.GroupMemberRemovalWarning":        {none: noRowReads},
	"Manager.HoldsTenant":                      {none: noRowReads},
	"Manager.InvitedTenants":                   {none: noRowReads},
	"Manager.JudgeTenantRemoval":               {none: noRowReads},
	"Manager.OneTenantPerPerson":               {none: noRowReads},
	"Manager.OpenSignUpWarning":                {none: noRowReads},
	"Manager.PersonTenants":                    {none: noRowReads},
	"Manager.RequireUser":                      {none: noRowReads},
	"Manager.RoleRemovalWarning":               {none: noRowReads},
	"Manager.RolesToGive":                      {none: noRowReads},
	"Manager.RolesToTake":                      {none: noRowReads},
	"Manager.SessionMethodOpen":                {none: noRowReads},
	"Manager.SignInMethods":                    {none: noRowReads},
	"Manager.SignInSettings":                   {none: noRowReads},
	"Manager.SignInWays":                       {none: noRowReads},
	"Manager.SignUpRoles":                      {none: noRowReads},
	"Manager.SkippedStoredGrants":              {none: noRowReads},
	"Manager.StartingRoles":                    {none: noRowReads},
	"Manager.StopRequiringSecondFactorWarning": {none: noRowReads},
	"Manager.Tenant":                           {none: noRowReads},
	"Manager.TenantAdministratorRoles":         {none: noRowReads},
	"Manager.TenantRoles":                      {none: noRowReads},
	"Tenant.DeletionWarning":                   {none: noRowReads},
	"Tenant.Domain":                            {none: noRowReads},
	"Tenant.GroupMemberRemovalWarning":         {none: noRowReads},
	"Tenant.RemovalWarning":                    {none: noRowReads},
	"Tenant.RoleRemovalWarning":                {none: noRowReads},
	"Tenant.SeatedAgainNote":                   {none: noRowReads},
}

// What records an act no line of recordedMethods lists, or lists alone.
const (
	bySessionLibrary  = "the session library"
	byPermissionStore = "the permission store, for the roles a directory gives and takes at a sign-in"
)

// recordedElsewhere names what records each act that no method of the package records,
// and the acts the permission store records beside the package's own.
var recordedElsewhere = map[auditlog.Act]string{
	auditlog.ActPasswordResetLinkSent: bySessionLibrary,
	auditlog.ActPasswordChosen:        bySessionLibrary,
	auditlog.ActPasswordChanged:       bySessionLibrary,
	auditlog.ActAddressChangeAsked:    bySessionLibrary,
	auditlog.ActAddressChanged:        bySessionLibrary,
	auditlog.ActAccountDeleted:        bySessionLibrary,
	auditlog.ActTokenMade:             bySessionLibrary,
	auditlog.ActRoleGiven:             byPermissionStore,
	auditlog.ActRoleTaken:             byPermissionStore,
}

// Test_everyMethodIsListedForTheAuditLog reads the package's own source and holds every
// exported method of the Manager and of a Tenant to recordedMethods: each is listed with
// one decision; a method listed with acts calls record, in its body or in an unexported
// function of the package it calls, and the code it runs names each act it lists; a
// method listed as handing its write to another calls it, and that one is listed with
// acts; no line names a method that is gone; and every act of the log is listed on a
// method, or recordedElsewhere names what records it.
//
// The code a method runs is read from the source: its body, function literals within
// it included, and the body of each function or method of the package it names, once
// each. A call on the method's receiver, or on the Manager a Tenant holds, is that
// type's method; any other call of a method is every method of the package of that
// name; and a value made of a type of the package reaches every method of the type. Whether a method calls record is read without following the exported methods
// of the two types, which have lines of their own.
func Test_everyMethodIsListedForTheAuditLog(t *testing.T) {
	t.Parallel()

	methods := exportedMethods(t, "Manager", "Tenant")
	code := readPackageCode(t)
	if !code.records("Manager.DisableUser") || !code.records("Tenant.RemoveRole") || code.records("Manager.AssignRole") || code.records("Manager.RequireUser") {
		t.Fatal("the test no longer reads which methods call record: Manager.DisableUser and Tenant.RemoveRole do, Manager.AssignRole hands its write to Manager.AssignRoleFor, and Manager.RequireUser writes nothing")
	}

	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	listed := make(map[auditlog.Act]bool)
	for _, name := range names {
		rule, ok := recordedMethods[name]
		if !ok {
			t.Errorf("%s is not listed in recordedMethods: record its act (Manager.record) and list its acts, or say why it writes no row", name)

			continue
		}
		decisions := 0
		for _, decided := range []bool{len(rule.acts) > 0, rule.by != "", rule.none != ""} {
			if decided {
				decisions++
			}
		}
		switch {
		case decisions != 1:
			t.Errorf("recordedMethods lists %s with %d decisions, want one: the acts it records, the method it hands its write to, or why it writes no row", name, decisions)
		case len(rule.acts) > 0 && !code.records(name):
			t.Errorf("recordedMethods lists acts of %s, which holds no call of record: record each act (Manager.record), in its body or in an unexported function it calls", name)
		case rule.by != "" && len(recordedMethods[rule.by].acts) == 0:
			t.Errorf("recordedMethods lists %s as handing its write to %s, which it lists with no acts", name, rule.by)
		case rule.by != "" && !code.reached(name, true)[rule.by]:
			t.Errorf("recordedMethods lists %s as handing its write to %s, which the code it runs does not call", name, rule.by)
		}
		named := code.actsNamed(name)
		for _, act := range rule.acts {
			listed[act] = true
			if !named[act] {
				t.Errorf("recordedMethods lists the act %s of %s, which the code it runs does not name", act, name)
			}
		}
	}
	for name := range recordedMethods {
		if _, declared := methods[name]; !declared {
			t.Errorf("recordedMethods lists %s, which is no exported method of the package any more: remove the line", name)
		}
	}
	for _, act := range auditlog.Acts() {
		if !listed[act] && recordedElsewhere[act] == "" {
			t.Errorf("no line of recordedMethods lists the act %s, and recordedElsewhere does not name what records it: list it on the method that records it, or say in recordedElsewhere what does", act)
		}
	}
}

// packageCode is the package's own code, its tests apart: each function and method by
// its key (its name, Type.name for a method), the keys of the methods of each name, and
// the keys of the methods of each type.
type packageCode struct {
	funcs   map[string]*ast.FuncDecl
	methods map[string][]string
	ofType  map[string][]string
}

// readPackageCode parses the package's own files, its tests apart.
func readPackageCode(t *testing.T) *packageCode {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	code := &packageCode{funcs: make(map[string]*ast.FuncDecl), methods: make(map[string][]string), ofType: make(map[string][]string)}
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
			if !ok || fn.Body == nil {
				continue
			}
			key := fn.Name.Name
			if typ := receiverType(fn); typ != "" {
				key = typ + "." + key
				code.methods[fn.Name.Name] = append(code.methods[fn.Name.Name], key)
				code.ofType[typ] = append(code.ofType[typ], key)
			}
			code.funcs[key] = fn
		}
	}

	return code
}

// isListedMethod reports whether key is an exported method of the Manager or of a
// Tenant, which recordedMethods lists.
func isListedMethod(key string) bool {
	typ, name, ok := strings.Cut(key, ".")

	return ok && (typ == "Manager" || typ == "Tenant") && ast.IsExported(name)
}

// named answers the keys of the package's functions a node of fn's code names: a call
// or a method value on fn's receiver or on the Manager a Tenant holds is that type's
// method, any other call of a method every method of that name, a name a function of
// the package, and a value made of one of the package's types every method of the type,
// which whoever it is handed to may call (the kinds a tenant's delete is handed).
func (p *packageCode) named(fn *ast.FuncDecl, node ast.Node) []string {
	switch n := node.(type) {
	case *ast.SelectorExpr:
		receiver, manager := managerOf(fn)
		if receiver == "" {
			return nil
		}
		switch typeText(n.X) {
		case receiver:
			return p.known(receiverType(fn) + "." + n.Sel.Name)
		case manager:
			return p.known("Manager." + n.Sel.Name)
		}
	case *ast.CallExpr:
		if selector, ok := n.Fun.(*ast.SelectorExpr); ok {
			receiver, manager := managerOf(fn)
			if on := typeText(selector.X); receiver == "" || (on != receiver && on != manager) {
				return p.methods[selector.Sel.Name]
			}
		}
	case *ast.Ident:
		return p.known(n.Name)
	case *ast.CompositeLit:
		if typ, ok := n.Type.(*ast.Ident); ok {
			return p.ofType[typ.Name]
		}
	}

	return nil
}

// known answers key where the package declares it, and nothing where it does not.
func (p *packageCode) known(key string) []string {
	if _, ok := p.funcs[key]; ok {
		return []string{key}
	}

	return nil
}

// reached answers the keys of the code key runs: key, and each function it names, and
// each that one names, once each. An exported method of the Manager or of a Tenant
// that key names is among them; its own code is followed only where through says so.
func (p *packageCode) reached(key string, through bool) map[string]bool {
	seen := make(map[string]bool)
	var follow func(at string)
	follow = func(at string) {
		fn, ok := p.funcs[at]
		if !ok || seen[at] {
			return
		}
		seen[at] = true
		if at != key && isListedMethod(at) && !through {
			return
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			for _, next := range p.named(fn, node) {
				follow(next)
			}

			return true
		})
	}
	follow(key)

	return seen
}

// records reports whether key calls record: in its body, or in the code of a function
// it reaches without following an exported method of the Manager or of a Tenant.
func (p *packageCode) records(key string) bool {
	return p.reached(key, false)["Manager.record"]
}

// actsNamed answers the acts of the log the code key runs names, the exported methods
// it calls followed. An act is named by its constant, which is the act's stored name
// after Act, its first letter upper case (auditlog.ActRoleGiven is roleGiven).
func (p *packageCode) actsNamed(key string) map[auditlog.Act]bool {
	named := make(map[auditlog.Act]bool)
	for at := range p.reached(key, true) {
		ast.Inspect(p.funcs[at].Body, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok && typeText(selector.X) == "auditlog" {
				if act, ok := actOfConstant(selector.Sel.Name); ok {
					named[act] = true
				}
			}

			return true
		})
	}

	return named
}

// actOfConstant answers the act the constant of that name is, and false for a name
// that is no act's.
func actOfConstant(name string) (auditlog.Act, bool) {
	rest, ok := strings.CutPrefix(name, "Act")
	if !ok || rest == "" {
		return "", false
	}
	act := auditlog.Act(strings.ToLower(rest[:1]) + rest[1:])

	return act, slices.Contains(auditlog.Acts(), act)
}
