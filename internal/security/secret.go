package security

// RedactedPlaceholder is the canonical replacement string for sensitive values.
const RedactedPlaceholder = "[REDACTED]"

// SecretString wraps a sensitive string so formatting and serialization always redact it.
type SecretString struct {
	value string
}

// NewSecretString creates a SecretString wrapping the given raw secret.
func NewSecretString(raw string) SecretString {
	return SecretString{value: raw}
}

// String implements fmt.Stringer and always returns "[REDACTED]".
func (s SecretString) String() string {
	return RedactedPlaceholder
}

// GoString implements fmt.GoStringer and always returns "[REDACTED]".
func (s SecretString) GoString() string {
	return RedactedPlaceholder
}

// MarshalJSON implements json.Marshaler and always serializes as "[REDACTED]".
func (s SecretString) MarshalJSON() ([]byte, error) {
	return []byte(`"` + RedactedPlaceholder + `"`), nil
}

// MarshalText implements encoding.TextMarshaler and always encodes as "[REDACTED]".
func (s SecretString) MarshalText() ([]byte, error) {
	return []byte(RedactedPlaceholder), nil
}

// Unwrap returns the underlying raw secret value for internal authorized use.
func (s SecretString) Unwrap() string {
	return s.value
}

// Value returns the underlying raw secret value for internal authorized use.
func (s SecretString) Value() string {
	return s.value
}

// IsEmpty reports whether the wrapped secret is empty.
func (s SecretString) IsEmpty() bool {
	return s.value == ""
}
