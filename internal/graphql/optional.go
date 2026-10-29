package graphql

import "encoding/json"

type Optional[T any] struct {
	Value T
	Set   bool
	Nil   bool
}

func (o Optional[T]) IsZero() bool {
	return !o.Set
}

func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set {
		return []byte("null"), nil
	}
	if o.Nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func Opt[T any](value T) Optional[T] {
	return Optional[T]{Set: true, Value: value}
}

func OptPtr[T any](value *T) Optional[T] {
	if value == nil {
		return Optional[T]{Set: false}
	}
	return Optional[T]{Set: true, Value: *value}
}

func OptNil[T any](value *T) Optional[T] {
	if value == nil {
		return Optional[T]{Set: true, Nil: true}
	}
	return Optional[T]{Set: true, Value: *value}
}
