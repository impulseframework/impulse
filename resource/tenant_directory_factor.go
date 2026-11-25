package resource

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// A tenant vouches for its own directory. Where the application asks everyone for a
// second factor when they sign in, a tenant whose directory already asks its people for
// one says so on the directories record's second-factor column (@directorySecondFactor),
// and a sign-in through that directory is then not held for the application's mailed
// code (signin.DeclaredDirectory.SecondFactor), in that tenant and in every tenant that
// gave the person a role. It is the tenant's word and nothing checks it, so it is said
// by a person signed in through the directory itself, behind a warning with a typed
// word, and taken back by anyone the role file lets. The generator writes the two
// methods in the tenant's scope for a directories record that carries the column; the
// column is output only on the wire.

// VouchForDirectoryWord is the word a person types before a tenant says its directory
// asks its people for a second factor (the warning of
// VouchFor<Record>DirectorySecondFactor): the application then stops asking the
// directory's people for its own.
const VouchForDirectoryWord = "vouch"

// DirectorySecondFactor is the second factor's part of a tenant's sign-in settings:
// whether the tenant says its directory asks its people for a second factor of its own
// (Vouched), whether the caller's auth asks everyone for one now (Asked; true where no
// auth answers), the one sentence a page shows for the two (Says), and, while the tenant
// has not said so and the application asks, what saying so takes of the caller
// (Conditions).
type DirectorySecondFactor struct {
	Vouched bool   `json:"vouched"`
	Says    string `json:"says"`
	Asked   bool   `json:"asked"`
	// Conditions holds the one thing saying the word takes of the caller, said for them
	// in the words the method refuses in (VouchForDirectory). There is none while the
	// tenant vouches, and none while the application asks nobody.
	Conditions []TenantSignInCondition `json:"conditions,omitempty"`
}

// AuthSecondFactor answers, for one auth, whether its sign-in settings ask everyone for
// a second factor now, in the caller's transaction. applies is false where the auth asks
// nothing of the request: a call outside any request, or a request that came through
// another auth. The generated identity client of each auth supplies it
// (AuthSecondFactor); an application names none.
type AuthSecondFactor func(ctx context.Context, txn ReadWriteTransaction) (required, applies bool, err error)

// SecondFactorsBy answers the rules with what each of the application's auths says of
// its sign-in settings: a tenant vouches for its directory's own second factor only
// where the application asks somebody for one, since where it asks nobody there is no
// code to stop asking for. The generated frames of a tenant's own methods pass the
// reader of each auth's generated identity client. The rules ask every reader that
// applies, and the application asks somebody where any of them answers required. Where
// none applies, as for a caller no auth owns, the question is not asked and nothing is
// refused for it; rules built without readers ask nothing.
func (r *TenantDirectoryRules) SecondFactorsBy(readers ...AuthSecondFactor) *TenantDirectoryRules {
	asked := *r
	asked.secondFactors = readers

	return &asked
}

// VouchForDirectory holds a tenant's saying that its directory asks its people for a
// second factor of its own to its rules, and answers the tenant's directory row, which
// the generated method writes the word through. The tenant has a directory, the caller
// is signed in through it, and the application asks somebody for a second factor now. A
// tenant that vouches already is answered as it is, with nothing more asked: the write
// that leaves the word said is plain, as the write that leaves a tenant directory only
// is.
func (r *TenantDirectoryRules) VouchForDirectory(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantDirectory, error) {
	directory, err := r.vouchingRow(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if directory.SecondFactor {
		return directory, nil
	}
	if err := r.mayVouch(ctx, txn, directory); err != nil {
		return nil, err
	}

	return directory, nil
}

// VouchForDirectoryWarning says what a tenant's saying that its directory asks its
// people for a second factor would do, in the words a person reads before they confirm
// it, and the words for the button that does it. It asks what VouchForDirectory asks
// and answers the same refusals, so a page that asks for the warning first shows the
// refusal and opens no dialog. A tenant that vouches already has nothing to turn on, and
// is answered a Conflict that says so. The warning names nobody.
func (r *TenantDirectoryRules) VouchForDirectoryWarning(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*Warning, error) {
	directory, err := r.vouchingRow(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	through := r.directoryWords(directory.Directory)
	if directory.SecondFactor {
		return nil, httpio.NewConflictMessagef("This %s already says that %s asks its people for a second factor. There is nothing to turn on.", r.word, through)
	}
	if err := r.mayVouch(ctx, txn, directory); err != nil {
		return nil, err
	}

	return &Warning{
		Consequence: fmt.Sprintf("You are saying that %s asks its people for a second factor when they sign in. This application will stop asking them for a code.", through),
		Names:       nil,
		Confirm:     fmt.Sprintf("Stop asking people who sign in through %s for a code", through),
	}, nil
}

// StopVouchingForDirectory holds taking back a tenant's word for its directory's second
// factor to its rules, and answers the tenant's directory row, which the generated
// method writes through: the tenant has a directory. Nothing is asked of the caller, so
// anyone the role file lets takes the word back, from any sign-in. A tenant that does
// not vouch is answered as it is, and the method that calls it leaves the row so.
func (r *TenantDirectoryRules) StopVouchingForDirectory(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantDirectory, error) {
	return r.secondFactorRow(ctx, txn, tenant, fmt.Sprintf("This %s has no directory, so there is nothing to take back.", r.word))
}

// vouchingRow is the tenant's directory row as saying the word reads it, with the
// refusal for a tenant that has no directory to vouch for.
func (r *TenantDirectoryRules) vouchingRow(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantDirectory, error) {
	return r.secondFactorRow(ctx, txn, tenant, fmt.Sprintf("This %[1]s has no directory, so there is no directory to vouch for. Set its directory first: sign in through your organization's directory and make it this %[1]s's directory.", r.word))
}

// secondFactorRow reads tenant's directory row for one of the rules of the tenant's word
// for its directory's second factor: the record carries the column, and the tenant has
// a directory, or is refused with noDirectory.
func (r *TenantDirectoryRules) secondFactorRow(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, noDirectory string) (*TenantDirectory, error) {
	if !r.columns.hasDirectories() {
		return nil, errors.New("resource: a tenant's directory rules need the directories record's columns: build them with the generated New<Record>DirectoryRules")
	}
	if r.columns.SecondFactorColumn == "" {
		return nil, errors.New("resource: the directories record declares no second-factor column (@directorySecondFactor), so a tenant has no word to say for its directory")
	}
	directory, err := r.directoryRow(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if directory == nil {
		return nil, httpio.NewConflictMessage(noDirectory)
	}

	return directory, nil
}

// mayVouch holds the caller and the application to what saying the word takes: the
// caller is signed in through the tenant's directory, and the application asks
// somebody for a second factor now.
func (r *TenantDirectoryRules) mayVouch(ctx context.Context, txn ReadWriteTransaction, directory *TenantDirectory) error {
	if through := r.vouchesThroughDirectory(ctx, directory.Directory); !through.Met {
		return through.refusal()
	}
	asked, err := r.secondFactorAsked(ctx, txn)
	if err != nil {
		return err
	}
	if !asked {
		return httpio.NewConflictMessage(r.asksNobody(directory.Directory))
	}

	return nil
}

// asksNobody says that the application asks nobody for a second factor now, so a
// tenant's word for directory has nothing to stop: the refusal of the word (mayVouch)
// and what the tenant's sign-in settings say of a tenant that has not said it
// (secondFactorPart) are this one sentence.
func (r *TenantDirectoryRules) asksNobody(directory signin.Directory) string {
	return fmt.Sprintf("This application asks nobody for a second factor now, so there is nothing to turn off for people who sign in through %s.", r.directoryWords(directory))
}

// secondFactorPart says the second factor's part of tenant's sign-in settings for the
// caller (DirectorySecondFactor): nil where the directories record carries no
// second-factor column or the tenant has no directory. While the tenant has not said the
// word and the application asks somebody, the part holds what saying it takes of the
// caller (vouchesThroughDirectory), the value the method's refusal is built from.
func (r *TenantDirectoryRules) secondFactorPart(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*DirectorySecondFactor, error) {
	if r.columns.SecondFactorColumn == "" {
		return nil, nil
	}
	directory, err := r.directoryRow(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if directory == nil {
		return nil, nil
	}
	asked, err := r.secondFactorAsked(ctx, txn)
	if err != nil {
		return nil, err
	}
	through := r.directoryWords(directory.Directory)
	part := &DirectorySecondFactor{Vouched: directory.SecondFactor, Asked: asked}
	switch {
	case asked && !directory.SecondFactor:
		part.Says = fmt.Sprintf("People who sign in through %s are asked for a code mailed to their address, as everyone is.", through)
		part.Conditions = []TenantSignInCondition{r.vouchesThroughDirectory(ctx, directory.Directory)}
	case asked:
		part.Says = fmt.Sprintf("This %s says that %s asks its people for a second factor when they sign in, so they are not asked for a code.", r.word, through)
	case !directory.SecondFactor:
		part.Says = r.asksNobody(directory.Directory)
	default:
		part.Says = fmt.Sprintf("This %s says that %s asks its people for a second factor when they sign in. This application asks nobody for a second factor now.", r.word, through)
	}

	return part, nil
}

// vouchesThroughDirectory is what saying the word takes of the caller, said for them:
// that they are signed in through directory, the tenant's own. Nothing checks the
// tenant's word, so it is said by a person the directory itself let in. A caller who is
// not is told how they did sign in, and to sign in through the directory.
func (r *TenantDirectoryRules) vouchesThroughDirectory(ctx context.Context, directory signin.Directory) TenantSignInCondition {
	name := r.directoryName(directory)
	condition := TenantSignInCondition{
		Sentence: fmt.Sprintf("Only a person who is signed in through this %s's directory, %s, can say that it asks its people for a second factor.", r.word, name),
	}
	session := sessioninfo.DataFromCtx(ctx)
	established, ok := session.EstablishedBy()
	if ok && (signin.Directory{Method: established.Method, Connection: established.Connection}) == directory {
		condition.Met = true

		return condition
	}
	condition.ToDo = fmt.Sprintf("%s Sign in through %s and try again.", establishedSentence(session, r.namers), name)

	return condition
}

// secondFactorAsked answers whether the application asks somebody for a second factor
// now: every auth's reader that applies is asked (SecondFactorsBy), and any one that
// answers required is enough. Where none applies the question is not asked, and the
// answer is yes, so nothing is refused for it.
func (r *TenantDirectoryRules) secondFactorAsked(ctx context.Context, txn ReadWriteTransaction) (bool, error) {
	applies, required := false, false
	for _, read := range r.secondFactors {
		if read == nil {
			continue
		}
		asks, asked, err := read(ctx, txn)
		if err != nil {
			return false, errors.Wrap(err, "resource.AuthSecondFactor()")
		}
		if !asked {
			continue
		}
		applies = true
		required = required || asks
	}

	return !applies || required, nil
}
