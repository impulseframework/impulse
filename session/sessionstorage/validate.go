package sessionstorage

import (
	"regexp"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,127}$`)

// validateCustomSessionData validates the table name and the tag-derived column names
// for a custom session data configuration.
func validateCustomSessionData(tableName string, columns []string) error {
	if !validIdentifier.MatchString(tableName) {
		return errors.Newf("invalid table name: %s. Table names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", tableName)
	}

	for _, name := range columns {
		if !validIdentifier.MatchString(name) {
			return errors.Newf("invalid column name: %s. Column names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", name)
		}
		if dbtype.IsReservedCustomColumn(name) {
			return errors.Newf("invalid column name: %s. This column name is reserved and cannot be used as a custom session data column.", name)
		}
	}

	return nil
}

// validateCustomUserData validates the table name and the tag-derived column names for
// a custom user data configuration.
func validateCustomUserData(tableName string, columns []string) error {
	if !validIdentifier.MatchString(tableName) {
		return errors.Newf("invalid table name: %s. Table names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", tableName)
	}

	for _, name := range columns {
		if !validIdentifier.MatchString(name) {
			return errors.Newf("invalid column name: %s. Column names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", name)
		}
		if dbtype.IsReservedCustomUserColumn(name) {
			return errors.Newf("invalid column name: %s. This column name is reserved and cannot be used as a custom user data column.", name)
		}
	}

	return nil
}
