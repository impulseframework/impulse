package session

import (
	"github.com/impulseframework/impulse/session/internal/util"
)

// sameAddress reports whether two email addresses are one: the same but for the case of
// their ASCII letters. Unicode's case folding is not used: it takes the Kelvin sign
// (U+212A) for k and the long s (U+017F) for s, so under it an address written with one
// would be taken for the ASCII address, which is another mailbox.
func sameAddress(a, b string) bool {
	return util.EqualASCIIFold(a, b)
}
