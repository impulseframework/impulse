package generation

// The audit log, as an auth's generated management reads it. Every act that changes an
// account, who holds what, or how people sign in leaves one row in the auth's audit
// records table, written in the act's own transaction by the library that makes the
// act's write (the auditlog package). A row holds parts and no sentence, so what a person
// reads of it, who acted, what was done and to whom, is written by the library as the
// row is read (auditlog.Stored.Sentences). The generated management therefore reads the
// log in two computed resources beside the methods, the whole log and a tenant's, named
// as the methods are (CrewListedAuditRecord, and CrewSectorAuditRecord where the
// application has a tenant record), each written where the schema holds the table.
// Neither declares a write, a read route or a method, so no route changes or removes a
// row.

// The suffixes of the two lists of the audit log: the whole log's after the auth's name,
// and a tenant's after the auth's and the tenant record's (listName).
const (
	listedAuditRecordSuffix = "ListedAuditRecord"
	listAuditRecordsSuffix  = "AuditRecord"
)

// ListedAuditRecord is the struct of the whole audit log as a list shows it
// (CrewListedAuditRecord; its list is CrewListedAuditRecords).
func (d *identitySourceData) ListedAuditRecord() string {
	return d.auth + listedAuditRecordSuffix
}

// TenantAuditRecord is the struct of a tenant's audit log (CrewSectorAuditRecord).
func (d *identitySourceData) TenantAuditRecord() string {
	return d.listName(listAuditRecordsSuffix)
}

// auditLogSources are the source files of the audit log's lists: the whole log, where the
// schema holds the table, and a tenant's beside it where the application has a tenant
// record. A computed resource's file is named for the resource, in the plural, and the
// whole log's file holds what the two share.
func (r *resourceGenerator) auditLogSources(inst *identityInstance) []identitySourceFile {
	needs := []identityTable{identityAuditRecords}
	files := []identitySourceFile{{
		dir:      inst.methods,
		name:     fileStem(r.pluralize(inst.auth + listedAuditRecordSuffix)),
		template: identityListedAuditRecordsTemplate,
		needs:    needs,
	}}
	if inst.tenant != nil {
		files = append(files, identitySourceFile{
			dir:      inst.methods,
			name:     fileStem(r.pluralize(inst.listName(listAuditRecordsSuffix))),
			template: identityTenantAuditRecordsTemplate,
			needs:    needs,
			tenant:   true,
		})
	}

	return files
}
