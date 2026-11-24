package db

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const classifyRowErrorsCallbackName = "db:classify-constraint-violations"

type constraintViolation struct {
	class error
	err   error
}

func (v *constraintViolation) Error() string { return v.err.Error() }

func (v *constraintViolation) Unwrap() []error { return []error{v.err, v.class} }

// An error already classified is returned as it is, so an error raised again on
// another session is not wrapped twice.
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

// Every optional interface GORM looks for on a dialector is forwarded to the
// engine's: one left out quietly switches off what the engine does through it.
type classifyingDialector struct {
	gorm.Dialector
	classify func(error) error
}

var (
	_ gorm.ErrorTranslator                   = classifyingDialector{}
	_ gorm.SavePointerDialectorInterface     = classifyingDialector{}
	_ interface{ Apply(*gorm.Config) error } = classifyingDialector{}
)

func (d classifyingDialector) Translate(err error) error {
	return classifyViolation(d.classify, err)
}

func (d classifyingDialector) Apply(config *gorm.Config) error {
	if applier, ok := d.Dialector.(interface{ Apply(*gorm.Config) error }); ok {
		return applier.Apply(config)
	}
	return nil
}

func (d classifyingDialector) SavePoint(tx *gorm.DB, name string) error {
	if savePointer, ok := d.Dialector.(gorm.SavePointerDialectorInterface); ok {
		return savePointer.SavePoint(tx, name)
	}
	return gorm.ErrUnsupportedDriver
}

func (d classifyingDialector) RollbackTo(tx *gorm.DB, name string) error {
	if savePointer, ok := d.Dialector.(gorm.SavePointerDialectorInterface); ok {
		return savePointer.RollbackTo(tx, name)
	}
	return gorm.ErrUnsupportedDriver
}

func (d classifyingDialector) ClauseBuilders() map[string]clause.ClauseBuilder {
	if builders, ok := d.Dialector.(interface {
		ClauseBuilders() map[string]clause.ClauseBuilder
	}); ok {
		return builders.ClauseBuilders()
	}
	return nil
}

// GORM's row step assigns the error of opening a result set straight to the
// statement, around Translate; PostgreSQL's driver reports a refused INSERT …
// RETURNING there, so that path needs its own classification.
func installRowClassification(session *gorm.DB, classify func(error) error) error {
	return session.Callback().Row().After("gorm:row").
		Register(classifyRowErrorsCallbackName, func(tx *gorm.DB) {
			tx.Error = classifyViolation(classify, tx.Error)
		})
}
