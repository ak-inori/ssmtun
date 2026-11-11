# AGENTS.md

Guidance for coding agents working in this repository.

## Project

ssmtun is a Go CLI that keeps named AWS SSM port-forwarding tunnels up. Each target runs in its own supervised worker process that drives `session-manager-plugin` directly (no AWS CLI) and reconnects with backoff when the session drops.

- `cmd/ssmtun/` — entry point
- `internal/cli/` — cobra commands (`up`, `down`, `status`, `login`, `targets`, `config`, `doctor`, hidden `worker`)
- `internal/tunnel/` — worker supervision loop, plugin process management, backoff, log prefixes
- `internal/awsx/` — SSO device-code login, StartSession / TerminateSession
- `internal/probe/` — end-to-end health probes
- `internal/config/`, `internal/state/`, `internal/ansi/` — YAML config, per-target state files, log colors

## Build and test

Use the Taskfile:

```sh
task build   # ./ssmtun with the version embedded
task lint    # go vet + gofmt check
task test    # go test ./...
```

CI runs `task lint`, `go test -race ./...` and `go build ./...`. Run at least `task lint` and `go test -race ./...` before handing work back.

## Writing

- Keep comments minimal; explain why, not what.
- `README.md` and `README.ja.md` must stay in sync. Update both when behavior or usage changes.
- Do not mention unrelated tools or products in the README, config examples or comments.

## Testing against real AWS

The developer's machine may have tunnels in daily use, started by ssmtun or by other tools. Test runs must never stop or interfere with them.

- Isolate test runs with `SSMTUN_CONFIG_DIR` and `SSMTUN_STATE_DIR` pointing at temporary directories. By default ssmtun uses the real `~/.config/ssmtun` and `~/.local/state/ssmtun`.
- Pick local ports that are not in use and not configured for any existing target.
- Never run `ssmtun down --all` or `--force` against the default config or state directory. `--force` kills any process holding a target's port, including tunnels started by other tools.
- `SSMTUN_NO_BROWSER=1` stops `login` from opening a browser.
- Agent shells usually have no TTY. To check colored output, allocate a pseudo-terminal with `expect` (`spawn`).

## Design decisions to preserve

- **Health probes must be side-effect free.** Only PostgreSQL is probed, using a CancelRequest for a non-existent backend (the server logs nothing). Do not add probes that open and abandon connections: MySQL counts each one as a handshake error against the bastion's IP and blocks it after `max_connect_errors`, and an SSLRequest-based Postgres probe logs "incomplete startup packet". MySQL and plain TCP tunnels are supervised through the plugin process alone.
- A TCP connect to the local port is not a health check: the plugin accepts locally even when the remote side is unreachable.
- A `TerminateSession` ValidationException ("Session is not in a valid state") after the plugin exits is expected and ignored.
- `down` must wait longer than a worker's own shutdown (plugin stop up to 3s plus `TerminateSession` up to 10s) before killing it.

## Releases

Homebrew distribution uses a hand-maintained source-build formula in `ak-inori/homebrew-tap`; goreleaser only uploads binaries to GitHub Releases (no `brews` section). To release:

1. Push a `vX.Y.Z` tag.
2. In the tap, update `url` and `sha256` in `Formula/ssmtun.rb`. Compute the checksum from GitHub's tag tarball with `shasum -a 256`.
