package db

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// classifyRowErrorsCallbackName is the name the row chain's classification is
// registered under on every handle this package opens.
const classifyRowErrorsCallbackName = "db:classify-constraint-violations"

// constraintViolation is a driver's error that a unique or a foreign key
// constraint refused a statement, carried together with the class of that
// refusal.
//
// It reads exactly as the driver's error does, and both are in its chain:
// errors.Is finds the class — gorm.ErrDuplicatedKey or
// gorm.ErrForeignKeyViolated — and errors.As still finds the driver's own
// error, with the constraint's name and the engine's detail on it. Replacing
// the driver's error with the bare class, which is what GORM's own translation
// does, would take that diagnosis away from every caller and from every log
// line the error reaches.
type constraintViolation struct {
	class error
	err   error
}

func (v *constraintViolation) Error() string { return v.err.Error() }

func (v *constraintViolation) Unwrap() []error { return []error{v.err, v.class} }

// classifyViolation returns err with the class of the constraint refusal it
// carries attached, or err as it is when it carries none. classify is the
// engine's Spec.ConstraintViolation; any answer from it other than the two
// classes is no classification, so the two engines cannot come to classify
// different sets of refusals. An error already classified is returned as it is,
// which keeps an error raised again on another session from being wrapped
// twice.
func classifyViolation(classify func(error) error, err error) error {
	if err == nil {
		return nil
	}
	var classified *constraintViolation
	if errors.As(err, &classified) {
		return err
	}
	class := classify(err)
	if errors.Is(class, gorm.ErrDuplicatedKey) || errors.Is(class, gorm.ErrForeignKeyViolated) {
		return &constraintViolation{class: class, err: err}
	}
	return err
}

// classifyingDialector is the dialector every handle this package opens is
// built on: the engine's own, with the constraint refusals its driver reports
// classified as they reach a statement's error.
//
// GORM hands every error a statement collects to the dialector's Translate when
// the handle is opened with TranslateError, and that is the one place every
// path meets — a statement's own execution, a callback's refusal, the COMMIT
// that reports a deferred constraint. So the classification is installed there
// rather than on the callback chains, which a COMMIT never runs.
//
// The dialector GORM holds is this value, so each optional interface GORM looks
// for on a dialector is answered here by forwarding to the engine's; one left
// out would quietly switch off what the engine does through it — PostgreSQL's
// identifier length limit comes in through Apply, a nested transaction's
// savepoint through SavePoint.
type classifyingDialector struct {
	gorm.Dialector
	classify func(error) error
}

// The optional interfaces GORM asserts on a dialector, answered by the value
// every handle is opened with, so a drift from them is caught where the type is
// written.
var (
	_ gorm.ErrorTranslator                   = classifyingDialector{}
	_ gorm.SavePointerDialectorInterface     = classifyingDialector{}
	_ interface{ Apply(*gorm.Config) error } = classifyingDialector{}
)

// Translate classifies a constraint refusal, and leaves every other error as
// the driver reported it.
func (d classifyingDialector) Translate(err error) error {
	return classifyViolation(d.classify, err)
}

// Apply lets the engine's dialector adjust the configuration it is opened with.
func (d classifyingDialector) Apply(config *gorm.Config) error {
	if applier, ok := d.Dialector.(interface{ Apply(*gorm.Config) error }); ok {
		return applier.Apply(config)
	}
	return nil
}

// SavePoint opens a savepoint through the engine's dialector, and reports the
// error GORM reports for a dialector without savepoints when it has none.
func (d classifyingDialector) SavePoint(tx *gorm.DB, name string) error {
	if savePointer, ok := d.Dialector.(gorm.SavePointerDialectorInterface); ok {
		return savePointer.SavePoint(tx, name)
	}
	return gorm.ErrUnsupportedDriver
}

// RollbackTo rolls back to a savepoint through the engine's dialector, on the
// same terms as SavePoint.
func (d classifyingDialector) RollbackTo(tx *gorm.DB, name string) error {
	if savePointer, ok := d.Dialector.(gorm.SavePointerDialectorInterface); ok {
		return savePointer.RollbackTo(tx, name)
	}
	return gorm.ErrUnsupportedDriver
}

// ClauseBuilders hands on the clause builders the engine's dialector carries.
func (d classifyingDialector) ClauseBuilders() map[string]clause.ClauseBuilder {
	if builders, ok := d.Dialector.(interface {
		ClauseBuilders() map[string]clause.ClauseBuilder
	}); ok {
		return builders.ClauseBuilders()
	}
	return nil
}

// installRowClassification classifies the error the row chain collects without
// handing it to Translate.
//
// GORM's row step assigns the error of opening a result set straight to the
// statement, around the path every other error takes. PostgreSQL's driver runs
// the statement and reads its first row when the result set is opened, so an
// INSERT … RETURNING that a unique constraint refuses — read with Raw and Scan,
// or with Rows — reports the refusal there, and it would reach the caller
// unclassified on this engine while the same statement on SQLite, whose driver
// reports it on the first row read, is classified.
func installRowClassification(session *gorm.DB, classify func(error) error) error {
	return session.Callback().Row().After("gorm:row").
		Register(classifyRowErrorsCallbackName, func(tx *gorm.DB) {
			tx.Error = classifyViolation(classify, tx.Error)
		})
}
