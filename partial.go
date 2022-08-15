package partial

import (
	"reflect"
	"sync"

	"github.com/samber/lo"
	"gorm.io/gorm/schema"
)

// New builds a model from a domain object, tracking all the JSON fields of the model.
//
// This should be used only for objects loaded from the database, where we know all the
// fields are populated correctly. It should not be used with user constructed domain
// objects, as those should be built directly into Partial's using their codegen'd
// builders.
func New[T any](subjectPtr *T) (model Partial[T], err error) {
	s, err := schema.Parse(new(T), &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return model, err
	}

	base := *subjectPtr
	model = Partial[T]{
		FieldNames: []string{},
		apply: func(thing T) *T {
			return &thing
		},
	}

	model = model.Add(func(subject *T) []string {
		fieldNames := []string{}
		subjectType := reflect.TypeOf(subject).Elem()
		for idx := 0; idx < subjectType.NumField(); idx++ {
			field := subjectType.Field(idx)
			_, found := lo.Find(s.Fields, func(schemaField *schema.Field) bool {
				return schemaField.Name == field.Name && schemaField.DBName != ""
			})
			if !found {
				continue
			}

			fieldNames = append(fieldNames, field.Name)
			reflect.ValueOf(subject).Elem().FieldByIndex([]int{idx}).Set(
				reflect.ValueOf(base).FieldByIndex([]int{idx}),
			)
		}

		return fieldNames
	})

	return model, nil
}

// Partial wraps a domain object of type T, and maintains a list of columns that have
// been set for the model.
//
// Tracking columns allows us to control which fields we wish to create or update when
// calling gorm functions via the Querier, avoiding an issue with default field values and
// accidentally including columns in queries.
type Partial[T any] struct {
	Subject    T
	FieldNames []string `json:"-"`
	apply      func(T) *T
}

func (m Partial[T]) Empty() bool {
	return len(m.FieldNames) == 0
}

func (m *Partial[T]) SetApply(apply func(T) *T) {
	m.apply = apply
}

func (m Partial[T]) Apply(base T) *T {
	return m.apply(base)
}

// Match checks if the given object matches against the fields that are set on the tracked
// model.
//
// This helps check if applying the changes tracked in the model would result in any
// change, and is useful to check when building idempotent update methods.
func (m Partial[T]) Match(otherPtr *T) bool {
	// If we haven't built anything, we're a null object. It's sensible to consider nil as
	// equal to an empty built model.
	if otherPtr == nil && len(m.FieldNames) == 0 {
		return true
	}

	var (
		otherValue   = reflect.ValueOf(otherPtr).Elem()
		subjectValue = reflect.ValueOf(m.Subject)
	)
	for _, columnName := range m.FieldNames {
		match := reflect.DeepEqual(
			otherValue.FieldByName(columnName).Interface(),
			subjectValue.FieldByName(columnName).Interface(),
		)
		if !match {
			return false
		}
	}

	return true
}

// Merge combines one Partial with another of the same type, with the other fields
// taking precedence.
func (m Partial[T]) Merge(other Partial[T]) Partial[T] {
	return Partial[T]{
		Subject:    *other.Apply(m.Subject),
		FieldNames: append(m.FieldNames, other.FieldNames...),
		apply: func(subject T) *T {
			return other.apply(*m.apply(subject))
		},
	}
}

// Add returns a new Partial with additional setters, taking precendence over
// whatever was previously set.
func (m Partial[T]) Add(opts ...func(*T) []string) Partial[T] {
	for _, opt := range opts {
		m.FieldNames = append(m.FieldNames, opt(&m.Subject)...)
		m.apply = func(apply func(T) *T, opt func(*T) []string) func(T) *T {
			return func(subject T) *T {
				res := apply(subject)
				opt(res)

				return res
			}
		}(m.apply, opt)
	}

	return m
}

// Without removes the given field names from the model, causing these fields to be
// excluded from any queries.
func (m Partial[T]) Without(fieldNamesToRemove ...string) Partial[T] {
	fieldNames := []string{}
eachExistingFieldName:
	for _, fieldName := range m.FieldNames {
		for _, toRemove := range fieldNamesToRemove {
			if fieldName == toRemove {
				continue eachExistingFieldName
			}
		}

		fieldNames = append(fieldNames, fieldName)
	}

	return Partial[T]{
		Subject:    m.Subject,
		FieldNames: fieldNames,
		apply:      m.apply,
	}
}
