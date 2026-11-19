package accesstypes

import (
	"database/sql/driver"

	"github.com/go-playground/errors/v5"
)

// unreadableMaskWords is what a NullMaskWords holds for a column that is no mask's
// text. The session such a row belongs to is held to nothing (a mask is never read
// wider than it was written), so the words open with that, and say why.
const unreadableMaskWords = "nothing (the stored mask does not read)"

// NullMaskWords is the type of a field that reads a stored permission mask for a
// person. Its column is the one an impersonation record keeps the session's mask in:
// the mask's text (PermissionMask.Text), and NULL for the unrestricted mask, which is
// the Null of the name: the type reads NULL itself, so the field is no pointer. What
// the field holds is the mask in words (PermissionMask.Words): "everything", "reading
// only", "everything except credential actions". A list of impersonated sessions
// carries the field, so the list says each session's mask as a person would, and
// nobody is shown "*;-class:credential".
//
// A column that is no mask's text reads as the session it belongs to is held: to
// nothing, with the reason.
//
// It is read only. The session library writes the column, and a NullMaskWords refuses
// to be written to a database, so words never take the place of a mask's text.
type NullMaskWords string

// MaskWordsOf returns the words of the mask a column holds: column is the mask's text,
// nil for a NULL column.
func MaskWordsOf(column *string) NullMaskWords {
	if column == nil {
		return NullMaskWords(AllowAll().Words())
	}
	mask, err := ParseMask(*column)
	if err != nil {
		return unreadableMaskWords
	}

	return NullMaskWords(mask.Words())
}

// DecodeSpanner implements the Spanner client's Decoder: the mask's text of a STRING
// column, said in words, and "everything" for NULL.
func (w *NullMaskWords) DecodeSpanner(val any) error {
	switch t := val.(type) {
	case nil:
		*w = MaskWordsOf(nil)
	case *string:
		*w = MaskWordsOf(t)
	case string:
		*w = MaskWordsOf(&t)
	default:
		return errors.Newf("an accesstypes.NullMaskWords is read from a STRING column that holds a mask's text, not from a value of type %T", val)
	}

	return nil
}

// EncodeSpanner implements the Spanner client's Encoder, and refuses: the words of a
// mask are not its text, and a column they were written to would hold no mask.
func (NullMaskWords) EncodeSpanner() (any, error) {
	return nil, errNullMaskWordsWritten()
}

// Scan implements sql.Scanner: the mask's text of a text column, said in words, and
// "everything" for NULL.
func (w *NullMaskWords) Scan(src any) error {
	switch t := src.(type) {
	case nil:
		*w = MaskWordsOf(nil)
	case string:
		*w = MaskWordsOf(&t)
	case []byte:
		text := string(t)
		*w = MaskWordsOf(&text)
	default:
		return errors.Newf("an accesstypes.NullMaskWords is read from a text column that holds a mask's text, not from a value of type %T", src)
	}

	return nil
}

// Value implements driver.Valuer, and refuses, as EncodeSpanner does.
func (NullMaskWords) Value() (driver.Value, error) {
	return nil, errNullMaskWordsWritten()
}

// errNullMaskWordsWritten is the refusal of a write of a mask's words to a database.
func errNullMaskWordsWritten() error {
	return errors.New("an accesstypes.NullMaskWords is read only: it holds a mask in words, and the column holds the mask's text (accesstypes.PermissionMask.Text), which the session library writes")
}
