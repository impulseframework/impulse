package identity

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
)

// SkippedStoredGrants answers, for each row of the auth's role grants table given, why
// the permission engine leaves the row out when it loads the policy, in the order given,
// and "" for a row its role holds (access.UserManager.SkippedStoredGrants). The generated
// list of the custom roles' grants asks it of the rows it read.
func (m *Manager) SkippedStoredGrants(grants []access.StoredGrant) ([]string, error) {
	skipped, err := m.access.UserManager().SkippedStoredGrants(grants)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.SkippedStoredGrants()")
	}

	return skipped, nil
}
