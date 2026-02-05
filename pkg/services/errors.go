package services

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned when an entity is not found
	ErrNotFound = errors.New("entity not found")

	// ErrAlreadyExists is returned when attempting to create a duplicate entity
	ErrAlreadyExists = errors.New("entity already exists")

	// ErrInvalidInput is returned when input validation fails
	ErrInvalidInput = errors.New("invalid input")

	// ErrConcurrentModification is returned when optimistic locking fails
	ErrConcurrentModification = errors.New("concurrent modification detected")
)

// ValidationError wraps field-specific validation errors
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field '%s': %s", e.Field, e.Message)
}

// NewValidationError creates a new validation error
func NewValidationError(field, message string) error {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// IsValidationError checks if an error is a validation error
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
