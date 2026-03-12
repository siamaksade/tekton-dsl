package dsl

// ValidationError represents a validation error with location context.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}
