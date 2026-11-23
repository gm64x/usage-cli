## Summary

<!-- Describe what this PR changes and why. -->

## Changes Made

- 

## Verification Checklist

- [ ] `gofmt -l .` produces no output
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes with isolated `t.TempDir()` and `USAGE_CLI_STRICT=1`
- [ ] No real user `~/.claude`, `~/.gemini`, `%APPDATA%`, or `%LOCALAPPDATA%` directories are touched
- [ ] Documentation and comments are updated in English where needed
