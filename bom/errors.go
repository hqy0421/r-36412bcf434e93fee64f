package bom

import (
	"errors"
	"fmt"
)

// Sentinel error kinds. Use Kind to determine which kind an error belongs to.
var (
	// ErrMissing indicates that a required record or field is absent: the
	// order, a recipe for a reachable material, a leaf stock record, or a
	// required attribute.
	ErrMissing = errors.New("bom: missing required record or field")

	// ErrCycle indicates that material dependencies form a cycle.
	ErrCycle = errors.New("bom: material dependency cycle")

	// ErrIdentityConflict indicates that one identity resolves to more than
	// one payload: the same usage line with different contents, duplicate
	// stock or recipe records with different data, a leaf that also has
	// usage lines, or a key that does not match its record.
	ErrIdentityConflict = errors.New("bom: identity conflict")

	// ErrOverflow indicates that a quantity or amount cannot be represented
	// in an int64.
	ErrOverflow = errors.New("bom: quantity or amount overflow")

	// ErrInvalidQuantity indicates a non-integer, negative or otherwise
	// invalid quantity or amount.
	ErrInvalidQuantity = errors.New("bom: invalid quantity or amount")

	// ErrEmptyExpansion indicates that a non-leaf material finished expanding
	// without revealing any usage lines. An empty loading region is never
	// treated as a leaf material.
	ErrEmptyExpansion = errors.New("bom: empty expansion for non-leaf material")

	// ErrTimeout indicates that the page did not finish expanding a material
	// within the configured timeout.
	ErrTimeout = errors.New("bom: page expansion timed out")
)

type bomError struct {
	kind   error
	detail string
}

func (e *bomError) Error() string {
	if e.detail == "" {
		return e.kind.Error()
	}
	return e.kind.Error() + ": " + e.detail
}

func wrap(kind error, format string, args ...interface{}) error {
	return &bomError{kind: kind, detail: fmt.Sprintf(format, args...)}
}

// Kind returns the sentinel error kind that err belongs to, or nil if the
// error did not originate from this package.
//
// The Go version supported by Agouti predates errors.Is, so this helper is
// provided instead.
func Kind(err error) error {
	for err != nil {
		if typed, ok := err.(*bomError); ok {
			return typed.kind
		}
		type unwrapper interface {
			Unwrap() error
		}
		if unwrapped, ok := err.(unwrapper); ok {
			err = unwrapped.Unwrap()
			continue
		}
		return nil
	}
	return nil
}
