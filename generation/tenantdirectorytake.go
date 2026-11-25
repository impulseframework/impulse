package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/resource"
)

// A tenant takes its organization's directory on the word of the person who makes it the
// tenant's: nobody checks who may act for an organization, so the act is attested. The
// generator writes it as one method in the tenant's scope for every tenant record that
// declares a directories record (@tenantDirectory), on every outlet that serves sessions
// and the record:
//
//   - Take<Record>Directory, POST <prefix>/<tenants>/{tenant}/take-<record>-directory,
//     with an empty body, makes the directory the caller is signed in through the
//     tenant's own. It declares a warning, the statement that the caller is authorized to
//     act on behalf of the organization and what taking its directory does
//     (resource.TenantDirectoryRules.TakeDirectoryWarning), which the dialog a page runs it
//     through shows before anything is saved, holding its button until the person has
//     typed the phrase (resource.TakeDirectoryPhrase).
//
// It is served to an Execute grant on it in the tenant, holds the session's directory to
// the rules of a tenant's directory (resource.TenantDirectoryRules.FromSession) and writes
// the row through the generated Set<Record>Directory, so its change event is written with
// it. It is the one way a tenant takes a directory: the directories record's own create
// is refused, naming the method (directoryCreateRefusal). Nothing of it is judged through
// an auth's management, so it needs no generated identity client.

// takeDirectoryMethodName is the method by which the record takes the directory its
// caller is signed in through, as grants and the client name it: TakeSectorDirectory.
func takeDirectoryMethodName(record string) string {
	return "Take" + record + "Directory"
}

// directoryTakeMethodsOf is the one method that takes the record's directory. It decides
// whose people the directory sends to the tenant, so it carries the class of the actions
// that change who may do what (accesstypes.ClassAccess), and it warns with the phrase.
func directoryTakeMethodsOf(record string) []tenantOwnMethod {
	return []tenantOwnMethod{
		{Name: takeDirectoryMethodName(record), Typed: resource.TakeDirectoryPhrase, Classes: []accesstypes.ActionClass{accesstypes.ClassAccess}},
	}
}

// directoryCreateRefusal is the sentence the directories record's create is refused with:
// a tenant's directory is taken with the tenant's own method, which says what taking it
// does first. word is what a sentence calls one tenant.
func directoryCreateRefusal(word, record string) string {
	tenant := words.WithArticle(word)
	if tenant != "" {
		tenant = strings.ToUpper(tenant[:1]) + tenant[1:]
	}

	return fmt.Sprintf("%s's directory is not created directly. Take it with %s, which says what taking it does before anything is written.", tenant, takeDirectoryMethodName(record))
}

// directoryTakeServing is where the application serves the method that takes a tenant's
// directory: the tenant record, its directories record, and the session outlets that
// serve the directories record.
type directoryTakeServing struct {
	record      *resourceInfo
	directories *resourceInfo
	outlets     []*routerOutlet
}

// servedOn reports whether the outlet serves the method. A nil serving serves none.
func (s *directoryTakeServing) servedOn(outlet string) bool {
	return s != nil && slices.ContainsFunc(s.outlets, func(on *routerOutlet) bool { return on.name == outlet })
}

// methods are the one method, none for a nil serving.
func (s *directoryTakeServing) methods() []tenantOwnMethod {
	if s == nil {
		return nil
	}

	return directoryTakeMethodsOf(s.record.Name())
}

// directoryTake resolves where the method that takes a tenant's directory is served, nil
// where the program writes none: the tenant record declares no directories record, or no
// session outlet serves it.
func (r *resourceGenerator) directoryTake() *directoryTakeServing {
	record := r.tenantRecord()
	if record == nil || !record.HasDeclaration() {
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

	return &directoryTakeServing{record: record, directories: directories, outlets: outlets}
}

// directoryTakeData feeds the handler package's file of the method that takes a tenant's
// directory: the method, how the tenant's key is handed to Set<Record>Directory, and the
// rules it is held to.
type directoryTakeData struct {
	Source  string
	Package string
	// Name is the tenant record's struct name and Word what a sentence calls one
	// tenant; Directories is the directories record's struct name.
	Name        string
	Word        string
	Directories string
	// Take is the method, and DomainRoute the tenant's own route as a doc names it.
	Take        tenantOwnMethod
	DomainRoute string
	// TenantIsUUID says the directories record's tenant column is an id, read from the
	// route's domain; a column that holds the id's text takes the domain as it is.
	TenantIsUUID bool
	// Rules is the expression that builds the rules of the record's directory, named by
	// the program's auths (namedDirectoryRules).
	Rules string

	LocalPackageImports string
	ResourcePackage     string
	RouterPackage       string
	ApplicationName     string
	ReceiverName        string
}

// generateDirectoryTake emits the handler package's file of the method that takes a
// tenant's directory, where the program serves it.
func (r *resourceGenerator) generateDirectoryTake() error {
	take := r.directoryTake()
	if take == nil {
		return nil
	}

	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(tenantDirectoryTakeOutputName))
	data := &directoryTakeData{
		Source:              r.resource.Dir(),
		Package:             r.handler.Package(),
		Name:                take.record.Name(),
		Word:                r.directoryTenantWord(take.record),
		Directories:         take.directories.Name(),
		Take:                take.methods()[0],
		DomainRoute:         fmt.Sprintf("%s/{%s}", r.domainRouteSegment, r.domainRouteParam),
		TenantIsUUID:        take.directories.DomainBinding.Anchor.Type() == impulseUUIDGoType,
		Rules:               r.namedDirectoryRules(take.record),
		LocalPackageImports: r.localPackageImports(),
		ResourcePackage:     r.resource.Package(),
		RouterPackage:       r.router.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	if err := r.writeFormattedGoFile(destinationFilePath, "tenantDirectoryTakeTemplate", tenantDirectoryTakeTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated the file of the method that takes a directory in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}
