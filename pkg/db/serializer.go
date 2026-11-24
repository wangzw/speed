package db

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// encryptedSerializerName is what a model names to encrypt a field:
// `gorm:"serializer:encrypted"`.
const encryptedSerializerName = "encrypted"

const keyInjectionCallbackName = "db:encryption-keys"

type encryptionKeysContextKey struct{}

func contextWithKeys(ctx context.Context, keys *keyRing) context.Context {
	return context.WithValue(ctx, encryptionKeysContextKey{}, keys)
}

// keysInContext reports whether the context was one this package put a ring in.
// A context carrying a nil ring reports true: that says the assembly configured
// no root key, not that the injection never ran.
func keysInContext(ctx context.Context) (*keyRing, bool) {
	keys, ok := ctx.Value(encryptionKeysContextKey{}).(*keyRing)
	return keys, ok
}

// installEncryption registers the serializer and puts this section's key
// material on one handle's context, so every statement running on it or derived
// from it finds the key.
//
// The serializer is registered process-wide, because GORM has no per-instance
// registration; what is per-instance is the key, so the registered value holds
// no state and the key travels on each statement's context. It is put on the
// context at every statement rather than once on the handle because
// WithContext replaces the statement's context. The handle's own context
// carries it too: that is how ScanRows, which runs no callback chain, finds it.
//
// An assembly with no root key installs a nil ring all the same, so the
// serializer reports the missing key rather than an unassembled handle.
func installEncryption(session *gorm.DB, namespace string, cfg Config) error {
	keys, err := newKeyRing(namespace, cfg)
	if err != nil {
		return err
	}
	// The table is process-wide; registering again keeps the registration
	// inside the assembly.
	schema.RegisterSerializer(encryptedSerializerName, encryptedSerializer{})
	session.Statement.Context = contextWithKeys(session.Statement.Context, keys)

	inject := func(tx *gorm.DB) {
		tx.Statement.Context = contextWithKeys(tx.Statement.Context, keys)
	}
	cb := session.Callback()
	// The sealing and the guard are anchored after the injection, whose context
	// they need, and before the step that builds the SQL.
	return errors.Join(
		cb.Create().Before("gorm:create").Register(keyInjectionCallbackName, inject),
		cb.Update().Before("gorm:update").Register(keyInjectionCallbackName, inject),
		cb.Delete().Before("gorm:delete").Register(keyInjectionCallbackName, inject),
		cb.Query().Before("gorm:query").Register(keyInjectionCallbackName, inject),
		cb.Row().Before("gorm:row").Register(keyInjectionCallbackName, inject),
		cb.Raw().Before("gorm:raw").Register(keyInjectionCallbackName, inject),

		cb.Create().Before("gorm:create").After(keyInjectionCallbackName).
			Register(sealWritesCallbackName, sealCreate),
		cb.Create().After("gorm:create").Before("gorm:save_after_associations").
			Register(restoreWritesCallbackName, restoreCallerValues),
		cb.Update().Before("gorm:update").After(keyInjectionCallbackName).
			Register(sealWritesCallbackName, sealUpdate),
		cb.Update().After("gorm:update").Before("gorm:save_after_associations").
			Register(restoreWritesCallbackName, restoreCallerValues),
		cb.Query().Before("gorm:query").After(keyInjectionCallbackName).
			Register(guardReadsCallbackName, guardReads),
		cb.Row().Before("gorm:row").After(keyInjectionCallbackName).
			Register(guardRowReadsCallbackName, guardRowReads),
	)
}

// encryptedSerializer holds nothing on purpose: anything it held would be shared
// by every database in the process, so the key arrives with each statement.
type encryptedSerializer struct{}

var _ schema.SerializerInterface = encryptedSerializer{}

func (encryptedSerializer) Value(ctx context.Context, field *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	keys, err := keysOfStatement(ctx, field)
	if err != nil {
		return nil, err
	}
	plaintext, ok := plaintextOf(fieldValue)
	if !ok {
		return nil, fmt.Errorf("db: field %s is marked serializer:%s, which encrypts a string or a "+
			"byte slice, and it holds %T", fieldName(field), encryptedSerializerName, fieldValue)
	}
	if plaintext == nil {
		// Absence stays absent: a seal over nothing reads back as a value
		// that was never there.
		return nil, nil
	}
	return keys.encrypt(plaintext), nil
}

func (encryptedSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	if dbValue == nil {
		field.ReflectValueOf(ctx, dst).Set(reflect.Zero(field.FieldType))
		return nil
	}
	keys, err := keysOfStatement(ctx, field)
	if err != nil {
		return err
	}
	var stored []byte
	switch value := dbValue.(type) {
	case string:
		stored = []byte(value)
	case []byte:
		stored = value
	default:
		return fmt.Errorf("db: field %s is marked serializer:%s and its column came back as %T, "+
			"which is not the text this module writes into one", fieldName(field),
			encryptedSerializerName, dbValue)
	}
	plaintext, err := keys.decrypt(string(stored))
	if err != nil {
		return fmt.Errorf("db: reading encrypted field %s: %w", fieldName(field), err)
	}
	return setPlaintext(ctx, field, dst, plaintext)
}

// keysOfStatement reports both ways of finding no material rather than
// encrypting around them: sealing under a key nobody chose would write a column
// nothing can open, with the error nowhere near it.
func keysOfStatement(ctx context.Context, field *schema.Field) (*keyRing, error) {
	keys, ok := keysInContext(ctx)
	switch {
	case !ok:
		return nil, fmt.Errorf("db: field %s is encrypted, and the statement it travelled on "+
			"carries no encryption key. Either the handle is not one this module opened, or the "+
			"field was reached by a call that runs no callback chain on a context the caller "+
			"replaced — ScanRows on a handle given WithContext is one: call ScanRows on the "+
			"handle Rows was called on, or on the delivered handle itself",
			fieldName(field))
	case keys == nil:
		return nil, fmt.Errorf("db: field %s is encrypted, and the database this statement ran on "+
			"was assembled without %s: set it to encrypt this field",
			fieldName(field), encryptionKeyKey)
	}
	return keys, nil
}

// plaintextOf reports whether the type is one this serializer reads and writes.
// A nil pointer and a nil slice report true with no plaintext.
func plaintextOf(fieldValue any) ([]byte, bool) {
	switch value := fieldValue.(type) {
	case string:
		return []byte(value), true
	case []byte:
		return value, true
	case *string:
		if value == nil {
			return nil, true
		}
		return []byte(*value), true
	case *[]byte:
		if value == nil {
			return nil, true
		}
		return *value, true
	default:
		return nil, false
	}
}

func setPlaintext(ctx context.Context, field *schema.Field, dst reflect.Value, plaintext []byte) error {
	target := field.ReflectValueOf(ctx, dst)
	holder, pointer := target, false
	if target.Kind() == reflect.Pointer {
		holder = reflect.New(target.Type().Elem()).Elem()
		pointer = true
	}
	switch {
	case holder.Kind() == reflect.String:
		holder.SetString(string(plaintext))
	case holder.Kind() == reflect.Slice && holder.Type().Elem().Kind() == reflect.Uint8:
		holder.SetBytes(plaintext)
	default:
		return fmt.Errorf("db: field %s is marked serializer:%s and is declared %s; a column it "+
			"encrypts is read back into a string, a byte slice, or a pointer to either",
			fieldName(field), encryptedSerializerName, field.FieldType)
	}
	if pointer {
		target.Set(holder.Addr())
	}
	return nil
}

func fieldName(field *schema.Field) string {
	if field.Schema != nil && field.Schema.Name != "" {
		return field.Schema.Name + "." + field.BindName()
	}
	return field.BindName()
}
