package auditlog

import (
	"time"

	"github.com/impulseframework/impulse/eventsource"
)

// Stored is one row of an auth's audit log as the table keeps it: the parts of the act,
// each name as it was when the act happened. A read fills one from a row to write what a
// person reads (Sentences). Nothing makes a Record of it.
//
// Every field is a plain value: the empty text, the empty list or the zero time where the
// row sets none.
type Stored struct {
	ID                string             // the row's id
	At                time.Time          // when the act's transaction committed: the table's CreatedAt
	Act               Act                // the act's fixed name
	Form              Form               // which form of the act, where it has more than one
	Source            eventsource.Stored // who acted: the row's nine columns of who made a change
	Directory         string             // the directory that acted, by the name the sign-in page gave it
	SubjectKind       SubjectKind        // what the act was done to
	SubjectID         string             // its id: the account's, the invitation's, the group's, the tenant's key, the setting's fixed name
	SubjectName       string             // its name when the act happened
	TenantID          string             // the tenant the act was done in
	TenantName        string             // what the application called that tenant then
	RoleNames         []string           // the roles the act names, by their stored names: the row's RoleName, then its OtherRoleNames
	RoleScope         RoleScope          // where that role is held
	RecordKind        string             // the record the role is held on, by its resource name, where it is held on one
	RecordID          string             // that record's key
	RecordWord        string             // what one record of its kind is called
	RecordName        string             // that record's name then, "" where its kind declares none
	GroupID           string             // the group the act names besides its subject
	GroupName         string             // that group's name then
	OldValue          string             // what the act replaced
	NewValue          string             // what the act set
	PeriodStart       time.Time          // the start of the role or the place the act gives
	PeriodEnd         time.Time          // the end of the role or the place the act gives
	SeatingTenantID   string             // the tenant that seated the person, in a row of what a move took
	SeatingTenantName string             // what the application called that tenant then
	Reason            string             // the reason the act carried
}
