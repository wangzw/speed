package sms

import "fmt"

// Classify maps a carrier's exact status code to a sentinel through a table and
// answers ErrUncertain for any code the table does not name. The code is carried
// into the text; it holds no credential and no message code.
func Classify(table map[string]error, carrier, code string) error {
	sentinel, verified := table[code]
	if !verified {
		sentinel = ErrUncertain
	}
	return fmt.Errorf("%w: %s answered %q", sentinel, carrier, code)
}

// Unsettled is the shape of a carrier call that failed before the carrier
// answered. It is uncertain: the send interface is not idempotent, so a call
// that fell over may still have delivered the message.
func Unsettled(carrier string) error {
	return fmt.Errorf("%w: %s: the call did not complete", ErrUncertain, carrier)
}

// NoStatus is the shape of a carrier answer that carries no status at all, so
// there is nothing to classify and the outcome is unknown.
func NoStatus(carrier string) error {
	return fmt.Errorf("%w: %s answered with no status", ErrUncertain, carrier)
}
