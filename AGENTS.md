# AGENTS.md

## Project Shape
- This is a Go 1.22 CLI; the root `package.json` only installs `opencode-go-usage` and is not the app build system.
- CLI entrypoint is `cmd/hyperhive/main.go`; command routing and most behavior live in `internal/cli/cli.go`.
- API wire formats and endpoint paths live in `internal/api/client.go`; config path/permissions live in `internal/config/config.go`.

## Commands
- Run all tests: `go test ./...` or `make test`.
- Build the CLI: `go build -o hyperhive ./cmd/hyperhive` or `make build`.
- Run a single package test: `go test ./internal/cli -run TestName`.
- Prefer `go test ./...` before finishing changes; there is no separate lint/typecheck config in this repo.

## Runtime And Ops Gotchas
- User config defaults to `~/.config/hyperhive/config.json`; override with `HYPERHIVE_CONFIG=/path/config.json`.
- Config files intentionally store `email`, `password`, and `token`; `Save` enforces directory `0700` and file `0600`.
- `make install` uses `sudo`, installs `/usr/local/bin/hyperhive`, writes `/etc/systemd/system/hyperhive.service`, configures `/etc/hyperhive/config.json`, enables/restarts the service.
- `make uninstall` stops/disables the service, runs `remove_nfs` with the service config if present, removes the service, binary, and `/etc/hyperhive`.
- `install_nfs` and `remove_nfs` run `sudo mkdir`/`sudo mount` and `sudo umount`; avoid running them in tests or dry runs unless you intend host mounts.
- `systemdexec` is the root service loop: it reads `/proc/mounts`, pings the NFS host from `source`, mounts under `/mnt/hyperhive/{share.Name}`, logs to `/var/log/hyperhive/service.log`, and uses `HYPERHIVE_MOUNT_INTERVAL` in minutes (default `10`).

## API Quirks
- Base URLs are normalized by trimming trailing slash and dropping query/fragment; callers should not append endpoint paths before `setup`.
- Endpoints are fixed as `/login`, `/virsh/getallvms`, `/nfs/list`, and `/virsh/add_ssh_key/{vmName}`.
- VM responses may be either `{ "vms": [...] }` or a bare array; VM `state` may be string or libvirt numeric code.
- NFS responses may use wrapped PascalCase `NfsShare`/`Status` objects or simple camelCase fields; preserve both formats.
- `AddSSHKey` deliberately disables HTTP client timeout by copying the client with `Timeout = 0`; do not reintroduce the default timeout unless changing that behavior intentionally.

## Test Patterns
- CLI tests use `run(..., deps)` with fake `apiClient`, fake password reader, fake command runner, and temp config paths; keep new CLI behavior injectable through `deps` instead of shelling out in tests.
- HTTP API tests use a custom `RoundTripper`; add endpoint assertions there rather than requiring a live HyperHive API.
- SSH key input accepts only one-line public keys with supported types from `supportedSSHKeyTypes`; tests cover manual input, selected `~/.ssh/*.pub`, and explicit path flows.
