package authn

import "errors"

// usernameShape carries the kind of an ErrInvalidIdentifier that came from a
// username. It is not a failure of its own: every value it wraps also answers
// errors.Is(err, ErrInvalidIdentifier), because the class is the same. It is
// what lets an answer name the field: a body with a username, an email address
// and a password in it can only say which value is wrong if the username's
// shape is distinguishable from the rest.
type usernameShape struct{ err error }

func (e *usernameShape) Error() string { return e.err.Error() }

func (e *usernameShape) Unwrap() error { return e.err }

// UsernameShape marks a normalization failure as one of a username.
func UsernameShape(err error) error {
	if err == nil {
		return nil
	}
	return &usernameShape{err: err}
}

// IsUsernameShape reports whether err is an identifier that failed to normalize
// as a username.
func IsUsernameShape(err error) bool {
	var shape *usernameShape
	return errors.As(err, &shape)
}
