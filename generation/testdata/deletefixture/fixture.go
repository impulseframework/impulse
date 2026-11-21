// Package deletefixture provides parsed-struct fixtures for what the generator knows
// about deleting a record: the delete rule a resource declares (@validateDeleteType),
// the records under a record (deleteFixtureTables in the tests carries the foreign keys
// and the interleave), and what a record declares its delete takes (@deleteCascade). A
// client has projects, a project has tasks and notes, a task has comments, and a client
// has invoices. A ledger holds a client's key with no foreign key on it, and a supplier
// is under nothing.
package deletefixture

import "github.com/impulseframework/impulse"

// Struct-level annotations only parse from a TypeSpec doc comment, so the structs sit
// in grouped type blocks like real project sources do.
type (
	// Client is the record the others are under. Its delete takes its projects and what
	// is under them, and an invoice refuses it.
	//
	// @resource
	// @deleteCascade(Project, Task, TaskComment, ProjectNote, refusedBy: Invoice)
	Client struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Project belongs to a client: a foreign key to Clients.
	//
	// @resource
	Project struct {
		ID       impulse.UUID `spanner:"Id"`
		ClientID impulse.UUID `spanner:"ClientId"`
		Name     string       `spanner:"Name"`
	}

	// Task belongs to a project: a foreign key to Projects, two links below a client.
	//
	// @resource
	Task struct {
		ID        impulse.UUID `spanner:"Id"`
		ProjectID impulse.UUID `spanner:"ProjectId"`
		Title     string       `spanner:"Title"`
	}

	// TaskComment belongs to a task: a foreign key to Tasks, three links below a client.
	//
	// @resource
	TaskComment struct {
		ID     impulse.UUID `spanner:"Id"`
		TaskID impulse.UUID `spanner:"TaskId"`
		Body   string       `spanner:"Body"`
	}

	// ProjectNote is interleaved in Projects: its key opens with the project's.
	//
	// @resource
	ProjectNote struct {
		ProjectID impulse.UUID `spanner:"Id"`
		Sequence  int64        `spanner:"Sequence"`
		Body      string       `spanner:"Body"`
	}

	// Ledger holds a client's key in a column no foreign key is on.
	//
	// @resource
	Ledger struct {
		ID       impulse.UUID `spanner:"Id"`
		ClientID impulse.UUID `spanner:"ClientId"`
		Balance  int64        `spanner:"Balance"`
	}

	// Supplier is under no record.
	//
	// @resource
	Supplier struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// Invoice belongs to a client and declares a delete rule.
	//
	// @resource
	// @validateDeleteType(InvoiceDeleteRule)
	Invoice struct {
		ID       impulse.UUID `spanner:"Id"`
		ClientID impulse.UUID `spanner:"ClientId"`
		Number   string       `spanner:"Number"`
	}
)

// InvoiceDeleteRule is the type Invoice's @validateDeleteType names. In an application it
// carries a Validate method taking the generated InvoiceDeletePatch; the fixture is not
// generated from, so the type alone stands here.
type InvoiceDeleteRule struct{}
