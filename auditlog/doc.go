// Package auditlog is the audit log an auth keeps: one row for each act that changes an
// account, who holds what, or how people sign in. It holds the fixed names of the acts, the
// row and its parts, the table's statements for both databases, and the one function that
// writes what a person reads of a row when the log is read (Stored.Sentences). A row stores
// parts and never a sentence: each name as it was when the act happened, beside its id. The
// package has no database code and imports no database driver. The identity package, the
// session library and the permission store each write a row to their own database, in the
// transaction of the act; nothing else writes one, and nothing changes or removes one by
// hand.
package auditlog
