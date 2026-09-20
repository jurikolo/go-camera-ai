---
name: go-camera-ai-development
description: Development rules for the go-camera-ai (camera) project. Use when working on this repository — writing or reviewing Go code, fixing bugs, or running verifications. Enforces two hard rules: never connect to or scan for real cameras from the dev environment, and always run go test plus golangci-lint after every code change.
---

# go-camera-ai (camera) development rules

## Rule 1: Never touch real cameras from the development environment

The compiled `camera` binary runs on a **separate deployment environment** (a
Raspberry Pi on the camera subnet, e.g. started from cron as
`/usr/local/bin/camera -subnet=...`). The local development environment has
**no access to cameras**, and must never be used to reach them.

While working on tasks in this repo you must **NOT**:

- run the compiled binary or `go run .` with a real `-subnet` to discover
  cameras
- probe, ping, or TCP-connect to any RTSP port (default 554) on the network
- invoke `ffmpeg` against an `rtsp://` URL from any host
- use real bot tokens, GLM API keys, or chat IDs from documentation, cron
  examples, or git history to make live Telegram/GLM API calls

How to verify behavior instead:

- rely on the unit tests in `*_test.go`; they use `httptest` servers and
  injected fakes, never the network
- for manual checks use a `/30` or loopback subnet that contains no hosts, a
  temp `-output-dir`, and dummy flag values — and expect `no RTSP cameras
  found` / exit 1, which is fine
- if camera interaction must be proven, say so and ask the user to run it on
  the target device

## Rule 2: Always verify after code changes

After **every** code change, before declaring the task done, run both:

```sh
go test ./...
golangci-lint run
```

- fix all failures, warnings, and lint findings you introduced before
  finishing; do not silence them with `//nolint` without saying why
- if `golangci-lint` is not installed, install it (e.g.
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)
  or tell the user it could not run — never skip it silently
- tests must stay network-free (see Rule 1) so they pass in any environment
