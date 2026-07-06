# webssh

A **local web control panel** for managing lots of SSH connections. It is *not* a
browser SSH gateway — it runs as a daemon on your own machine with your
privileges, so it can edit `~/.ssh/config`, generate/deploy keys, mount remote
directories over `sshfs`, and launch your native terminal / file manager. A
browser-based terminal is included as a fallback for remote use.

## Features (v1)

- **Inventory** — hosts with groups (hierarchy: prod/staging, per-client), tags,
  and fuzzy search. Manual create/edit, `ProxyJump`/bastion, extra ssh options.
- **~/.ssh/config sync** — import existing config; export the inventory to a
  managed file `~/.ssh/config.d/inventory` wired in via a single `Include` line.
  Your main config is backed up and never overwritten.
- **Keys** — generate keypairs (ed25519/rsa/ecdsa) and deploy public keys to
  hosts (`authorized_keys`) for passwordless access. Tracks what is deployed where.
- **sshfs** — mount/unmount a host's home directory, open it in your file manager.
- **Web SFTP browser** — browse the remote filesystem, download/upload, mkdir,
  rename, delete, in the browser (agent/key auth, or a one-shot web password prompt).
- **Native launch** — configurable commands to open your terminal / file manager.
- **Web terminal** — xterm.js session over a websocket (fallback to native apps).

## Security

The daemon is powerful, so it is locked down:

- binds to **loopback only** (refuses non-loopback `--addr`);
- every request needs the **startup token** (printed as a URL); a `SameSite=Strict`
  cookie bootstraps it, and the SPA sends it as `X-Auth-Token`;
- mutating requests must carry a **loopback Origin** (CSRF defense);
- deploy passwords are used once and never stored.

Do not expose it to the network.

## Build & run

Requirements: Go ≥ 1.25, Node ≥ 20, and `sshfs` / `ssh-keygen` on `PATH`.

```sh
make deps    # install frontend deps (once)
make run     # build SPA + binary, start server, open browser
```

Or manually:

```sh
cd web && npm install && npm run build && cd ..
go build -o webssh ./cmd/webssh
./webssh                 # prints http://127.0.0.1:8022/?token=…
```

Flags: `--addr 127.0.0.1:8022`, `--no-open`.

## Layout

- `cmd/webssh` — entry point (flags, token, loopback bind).
- `internal/{store,sshconfig,keys,mount,launcher,pty,server,config}` — backend.
- `web/` — Svelte + Vite SPA, embedded into the binary via `web/embed.go`.

## Roadmap (v2)

Port forwarding (local/remote/SOCKS) with a tunnel monitor, background
health-checks, broadcast commands, `known_hosts` manager, encrypted secret
storage, command palette, Ansible/PuTTY import.
