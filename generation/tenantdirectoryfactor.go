package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// A tenant's word for its directory's second factor is the directories record's
// second-factor column (@directorySecondFactor): where the application asks everyone for
// a second factor at sign-in, a tenant whose directory already asks its people for one
// says so there, and a sign-in through that directory is then not held for the
// application's mailed code (resource.TenantDirectoryRules.VouchForDirectory). The word
// is the tenant's own, so the generator writes it as two methods in the tenant's scope
// for a directories record that carries the column, on every outlet that serves sessions
// and the record:
//
//   - VouchFor<Record>DirectorySecondFactor,
//     POST <prefix>/<tenants>/{tenant}/vouch-for-<record>-directory-second-factor, says
//     it. Nothing checks the word, so it is said by a person signed in through the
//     directory itself, while the application asks somebody for a second factor, and it
//     declares a warning, which the dialog a page runs it through shows before anything
//     is saved (resource.TenantDirectoryRules.VouchForDirectoryWarning), holding its
//     button until the person has typed the warning's word;
//   - StopVouchingFor<Record>DirectorySecondFactor,
//     POST <prefix>/<tenants>/{tenant}/stop-vouching-for-<record>-directory-second-factor,
//     takes it back, which is plain.
//
// Each is served to an Execute grant on it in the tenant and writes the column through
// the directories record's update patch, so its change event is written with it. The
// record's own update never writes the column: it decodes output-only
// (resourceField.IsDirectorySecondFactor). Unlike the methods of the default role,
// nothing of them is judged through an auth's management, so they need no one auth: the
// rules ask every auth's generated identity client whether its sign-in settings ask for
// a second factor (resource.TenantDirectoryRules.SecondFactorsBy), and a program that
// generates no auth's management asks none.

// vouchForDirectoryMethodName is the method by which the record says its directory asks
// its people for a second factor of its own, as grants and the client name it:
// VouchForSectorDirectorySecondFactor.
func vouchForDirectoryMethodName(record string) string {
	return "VouchFor" + record + "DirectorySecondFactor"
}

// stopVouchingForDirectoryMethodName is the method by which the record takes that word
// back: StopVouchingForSectorDirectorySecondFactor.
func stopVouchingForDirectoryMethodName(record string) string {
	return "StopVouchingFor" + record + "DirectorySecondFactor"
}

// directoryFactorMethodsOf are the two methods of the record's word for its directory's
// second factor: the one that says it, which warns, and the one that takes it back. Each
// changes what a sign-in needs for people nobody named, so both carry the class of the
// actions that change who may do what (accesstypes.ClassAccess).
func directoryFactorMethodsOf(record string) []tenantOwnMethod {
	changesAccess := []accesstypes.ActionClass{accesstypes.ClassAccess}

	return []tenantOwnMethod{
		{Name: vouchForDirectoryMethodName(record), Typed: resource.VouchForDirectoryWord, Classes: changesAccess},
		{Name: stopVouchingForDirectoryMethodName(record), Typed: "", Classes: changesAccess},
	}
}

// directoryFactorServing is where the application serves the two methods of a tenant's
// word for its directory's second factor: the tenant record, its directories record, and
// the session outlets that serve the directories record.
type directoryFactorServing struct {
	record      *resourceInfo
	directories *resourceInfo
	outlets     []*routerOutlet
}

// servedOn reports whether the outlet serves the methods. A nil serving serves none.
func (s *directoryFactorServing) servedOn(outlet string) bool {
	return s != nil && slices.ContainsFunc(s.outlets, func(on *routerOutlet) bool { return on.name == outlet })
}

// methods are the two methods, none for a nil serving.
func (s *directoryFactorServing) methods() []tenantOwnMethod {
	if s == nil {
		return nil
	}

	return directoryFactorMethodsOf(s.record.Name())
}

// directoryFactor resolves where the methods of a tenant's word for its directory's
// second factor are served, nil where the program writes none: the tenant record
// declares no directories record, the record carries no second-factor column, or no
// session outlet serves it.
func (r *resourceGenerator) directoryFactor() *directoryFactorServing {
	record := r.tenantRecord()
	if record == nil || !record.HasDeclaration() || record.declaration.Directories.DirectorySecondFactorField == nil {
		return nil
	}
	directories := record.declaration.Directories
	var outlets []*routerOutlet
	for _, outlet := range r.allOutlets() {
		if outlet.servesSessions && directories.OnOutlet(outlet.name) {
			outlets = append(outlets, outlet)
		}
	}
	if len(outlets) == 0 {
		return nil
	}

	return &directoryFactorServing{record: record, directories: directories, outlets: outlets}
}

// directoryFactorData feeds the handler package's file of the two methods: the methods,
// the directories record's second-factor column as the methods write it, and the rules
// they are held to.
type directoryFactorData struct {
	Source  string
	Package string
	// Name is the tenant record's struct name and Word what a sentence calls one
	// tenant; Directories is the directories record's struct name and Field its
	// second-factor field, FlagType the field's type where it is one named over a
	// boolean, empty for a plain bool.
	Name        string
	Word        string
	Directories string
	Field       string
	FlagType    string
	// Vouch is the method that says the word and Stop the one that takes it back;
	// DomainRoute is the tenant's own route as a doc names it.
	Vouch       tenantOwnMethod
	Stop        tenantOwnMethod
	DomainRoute string
	// Rules is the expression that builds the rules of the record's directory, named by
	// the program's auths and told what each auth's sign-in settings ask
	// (switchDirectoryRules).
	Rules string
	// IdentityAccessor is the application's accessor of the generated identity client
	// whose audit log the word is recorded in, "" where none is (tenantSignInRecorder).
	IdentityAccessor string

	LocalPackageImports string
	ResourcePackage     string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
}

// generateDirectoryFactor emits the handler package's file of the two methods of a
// tenant's word for its directory's second factor, where the program serves them.
func (r *resourceGenerator) generateDirectoryFactor() error {
	factor := r.directoryFactor()
	if factor == nil {
		return nil
	}
	methods := factor.methods()
	field := factor.directories.DirectorySecondFactorField

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantDirectoryFactorOutputName))
	data := &directoryFactorData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                factor.record.Name(),
		Word:                r.directoryTenantWord(factor.record),
		Directories:         factor.directories.Name(),
		Field:               field.Name(),
		Vouch:               methods[0],
		Stop:                methods[1],
		DomainRoute:         fmt.Sprintf("%s/{%s}", r.domainRouteSegment, r.domainRouteParam),
		Rules:               r.switchDirectoryRules(factor.record),
		IdentityAccessor:    r.tenantSignInRecorder(),
		LocalPackageImports: r.localPackageImports(),
		ResourcePackage:     r.resource.Package(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	if flagType := field.Type(); flagType != "bool" {
		data.FlagType = flagType
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantDirectoryFactorTemplate", tenantDirectoryFactorTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated the directory's second factor file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}
