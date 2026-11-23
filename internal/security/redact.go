package security

import "regexp"

var (
	// jsonSecretKVPattern matches JSON key-value pairs for sensitive token/key fields.
	jsonSecretKVPattern = regexp.MustCompile(
		`(?i)("(?:accessToken|refreshToken|access_token|refresh_token|api_key|x-api-key|authorization)"\s*:\s*)"([^"]*)"`,
	)

	// headerSecretKVPattern matches HTTP headers or key=value pairs for sensitive token/key fields.
	headerSecretKVPattern = regexp.MustCompile(
		`(?i)(\b(?:accessToken|refreshToken|access_token|refresh_token|api_key|x-api-key|authorization)\s*[:=]\s*)(?:"[^"]*"|'[^']*'|(?:(?:Bearer|Basic|Token)\s+)?[A-Za-z0-9._~+/=-]+)`,
	)

	// bearerTokenPattern matches standalone Bearer authorization tokens.
	bearerTokenPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)

	// anthropicKeyPattern matches Anthropic API / OAuth key prefixes.
	anthropicKeyPattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`)
)

// Redact masks API keys, Bearer tokens, and sensitive JSON/header credentials in input.
func Redact(input string) string {
	if input == "" {
		return ""
	}
	out := jsonSecretKVPattern.ReplaceAllString(input, `${1}"`+RedactedPlaceholder+`"`)
	out = headerSecretKVPattern.ReplaceAllString(out, `${1}`+RedactedPlaceholder)
	out = bearerTokenPattern.ReplaceAllString(out, "Bearer "+RedactedPlaceholder)
	out = anthropicKeyPattern.ReplaceAllString(out, RedactedPlaceholder)
	return out
}
