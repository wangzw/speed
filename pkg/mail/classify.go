package mail

import "fmt"

// Classify maps a carrier's exact status code to a sentinel through a table and
// answers ErrUncertain for any code the table does not name.
func Classify(table map[string]error, carrier, code string) error {
	sentinel, verified := table[code]
	if !verified {
		sentinel = ErrUncertain
	}
	return fmt.Errorf("%w: %s answered %q", sentinel, carrier, code)
}

// Unsettled is the shape of a carrier call that failed before the carrier
// answered; it is uncertain, because the send is not idempotent.
func Unsettled(carrier string) error {
	return fmt.Errorf("%w: %s: the call did not complete", ErrUncertain, carrier)
}

// NoStatus is the shape of a carrier answer that carries no status, so the
// outcome is unknown.
func NoStatus(carrier string) error {
	return fmt.Errorf("%w: %s answered with no status", ErrUncertain, carrier)
}
