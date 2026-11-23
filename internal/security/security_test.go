package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretStringRedaction(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	raw := "sk-ant-api03-super-secret-value"
	sec := NewSecretString(raw)

	if got := sec.String(); got != RedactedPlaceholder {
		t.Fatalf("String() = %q, want %q", got, RedactedPlaceholder)
	}
	if got := sec.GoString(); got != RedactedPlaceholder {
		t.Fatalf("GoString() = %q, want %q", got, RedactedPlaceholder)
	}
	if got := fmt.Sprintf("%v|%s|%#v", sec, sec, sec); got != "[REDACTED]|[REDACTED]|[REDACTED]" {
		t.Fatalf("fmt formatting leaked secret: %q", got)
	}

	b, err := json.Marshal(struct {
		Token SecretString `json:"token"`
	}{Token: sec})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if string(b) != `{"token":"[REDACTED]"}` {
		t.Fatalf("MarshalJSON() = %s, want %s", string(b), `{"token":"[REDACTED]"}`)
	}
	if strings.Contains(string(b), raw) {
		t.Fatalf("serialized JSON contains raw secret")
	}

	if sec.Unwrap() != raw || sec.Value() != raw {
		t.Fatalf("Unwrap()/Value() did not return original raw secret")
	}
	if sec.IsEmpty() {
		t.Fatalf("IsEmpty() = true, want false")
	}
	if !NewSecretString("").IsEmpty() {
		t.Fatalf("empty SecretString reported IsEmpty() = false")
	}
}

func TestRedact(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	cases := []struct {
		name   string
		input  string
		secret string
	}{
		{
			name:   "anthropic api key",
			input:  "failed request with sk-ant-api03-AbCdEf12345_xyz-99",
			secret: "sk-ant-api03-AbCdEf12345_xyz-99",
		},
		{
			name:   "bearer token",
			input:  "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig_123",
			secret: "eyJhbGciOiJIUzI1NiJ9.payload.sig_123",
		},
		{
			name:   "standalone bearer token",
			input:  "token Bearer my.secret-token_42 rejected",
			secret: "my.secret-token_42",
		},
		{
			name:   "json accessToken and refreshToken",
			input:  `{"accessToken": "acc_secret_123", "refreshToken":"ref_secret_456"}`,
			secret: "acc_secret_123",
		},
		{
			name:   "json snake_case tokens and api_key",
			input:  `{"access_token":"tok1","refresh_token":"tok2","api_key":"tok3","x-api-key":"tok4","authorization":"Bearer tok5"}`,
			secret: "tok2",
		},
		{
			name:   "header x-api-key",
			input:  "x-api-key: secret-header-val",
			secret: "secret-header-val",
		},
		{
			name:   "header basic auth",
			input:  "Authorization: Basic dXNlcjpwYXNz",
			secret: "dXNlcjpwYXNz",
		},
		{
			name:   "header token auth",
			input:  "Authorization: Token tok_abc_123",
			secret: "tok_abc_123",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("Redact(%q) = %q, still contains secret %q", tc.input, got, tc.secret)
			}
			if !strings.Contains(got, RedactedPlaceholder) {
				t.Fatalf("Redact(%q) = %q, missing %q", tc.input, got, RedactedPlaceholder)
			}
		})
	}
}

func TestOpenReadOnly(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")

	if err := os.WriteFile(path, []byte("hello-readonly"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	f, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly failed: %v", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != "hello-readonly" {
		t.Fatalf("ReadAll = %q, want %q", string(data), "hello-readonly")
	}

	if _, writeErr := f.Write([]byte("mutate")); writeErr == nil {
		t.Fatalf("expected write to read-only file handle to fail")
	}
}

func TestSanitizeTelemetry(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	ev := TelemetryEvent{
		Timestamp:    now,
		Origin:       "win",
		Provider:     "claude",
		ToolName:     "Edit",
		Decision:     "allow",
		UsedPct5h:    42.5,
		UsedPct7d:    18.0,
		State:        "OK",
		PathBasename: `C:\Users\alice\secret-project\credentials\handoff.md`,
	}

	sanitized := SanitizeTelemetry(ev)
	if sanitized.PathBasename != "handoff.md" {
		t.Fatalf("PathBasename = %q, want %q", sanitized.PathBasename, "handoff.md")
	}
	if sanitized.Timestamp.Location() != time.UTC {
		t.Fatalf("Timestamp location = %v, want UTC", sanitized.Timestamp.Location())
	}

	unixEv := SanitizeTelemetry(TelemetryEvent{
		PathBasename: "/home/alice/repo/src/main.go",
	})
	if unixEv.PathBasename != "main.go" {
		t.Fatalf("unix PathBasename = %q, want %q", unixEv.PathBasename, "main.go")
	}

	emptyEv := SanitizeTelemetry(TelemetryEvent{
		PathBasename: "   ",
	})
	if emptyEv.PathBasename != "" {
		t.Fatalf("empty PathBasename = %q, want empty", emptyEv.PathBasename)
	}
}
