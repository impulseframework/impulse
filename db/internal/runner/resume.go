package runner

import (
	"github.com/go-playground/errors/v5"
)

// Resume decides where an up run continues on a dirty version: the index of the first
// statement still to run, or a refusal that names the two ways out. The version's
// operation, if any, must be resolved into Applied before the call. The file is the up
// migration for the dirty version, nil when the source does not carry it, and stmts are its
// statements.
func Resume(v Version, file *Migration, stmts []string) (int, error) {
	if !v.Dirty {
		return 0, errors.Newf("version %d is not dirty; nothing to resume", v.Number)
	}
	if v.Operation != "" {
		return 0, errors.Newf("version %d has DDL operation %s unresolved", v.Number, v.Operation)
	}
	if !v.Progress {
		return 0, errors.Newf("version %d is dirty from a run that recorded no progress; force the version that matches the database's real state", v.Number)
	}
	if file == nil {
		return 0, errors.Newf("the database is at version %d, which this build does not carry", v.Number)
	}
	if err := checkApplied(v, file, stmts); err != nil {
		return 0, err
	}

	return v.Applied, nil
}

// checkApplied refuses a file that changed in the part a stopped run applied: its first
// Applied statements must hash to the checkpoint recorded on the version row.
func checkApplied(v Version, file *Migration, stmts []string) error {
	if v.Applied > len(stmts) || Checkpoint(stmts[:v.Applied]) != v.Checkpoint {
		return errors.Newf("%s changed in the part already applied (%s); restore the file or force a version", file.File, plural(v.Applied, "statement"))
	}

	return nil
}

// inFlight returns the DDL batch a version row names an operation for: the file's DDL group
// that starts at the row's Applied. While an operation is named, the row's checkpoint covers
// the file through that batch, so a file that changed in its applied part or in the batch is
// refused, before anything is sent or settled.
func inFlight(v Version, file *Migration, stmts []string) (Group, error) {
	groups := Groups(stmts, v.Applied)
	if len(groups) == 0 || groups[0].Kind != DDL || Checkpoint(stmts[:v.Applied+len(groups[0].Statements)]) != v.Checkpoint {
		return Group{}, errors.Newf("%s changed in the part already applied (%s) or in the DDL batch after it, which the version row names as operation %s; restore the file or force a version",
			file.File, plural(v.Applied, "statement"), v.Operation)
	}

	return groups[0], nil
}
