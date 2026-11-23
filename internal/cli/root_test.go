package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommandText(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	origVersion, origCommit, origDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
	})

	Version = "v0.1.0-test"
	Commit = "abc1234"
	Date = "2026-11-23T00:00:00Z"

	var buf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	want := "usage-cli v0.1.0-test (commit: abc1234, date: 2026-11-23T00:00:00Z)"
	if got != want {
		t.Fatalf("unexpected version output: got %q, want %q", got, want)
	}
}

func TestVersionCommandJSON(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	origVersion, origCommit, origDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
	})

	Version = "v0.2.0-test"
	Commit = "def5678"
	Date = "2026-11-23T12:34:56Z"

	var buf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version --json command failed: %v", err)
	}

	var got VersionInfo
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal version JSON %q: %v", buf.String(), err)
	}

	want := VersionInfo{
		Version: "v0.2.0-test",
		Commit:  "def5678",
		Date:    "2026-11-23T12:34:56Z",
	}
	if got != want {
		t.Fatalf("unexpected JSON output: got %+v, want %+v", got, want)
	}
}

func TestRootVersionFlag(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	origVersion, origCommit, origDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
	})

	Version = "v0.3.0-test"
	Commit = "999aaaa"
	Date = "2026-11-23T01:02:03Z"

	var buf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version flag failed: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	want := "usage-cli v0.3.0-test (commit: 999aaaa, date: 2026-11-23T01:02:03Z)"
	if got != want {
		t.Fatalf("unexpected --version output: got %q, want %q", got, want)
	}
}
