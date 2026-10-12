// Package validation provides a field-level validation error shared by services and the HTTP layer.
package validation

import "sort"

// Error lists invalid input fields with a human-readable reason each.
type Error struct {
	Fields map[string]string
}

func (e *Error) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msg := "validation failed:"
	for _, k := range keys {
		msg += " " + k + " (" + e.Fields[k] + ");"
	}
	return msg
}

// New returns an Error for a single field.
func New(field, reason string) *Error { return &Error{Fields: map[string]string{field: reason}} }
