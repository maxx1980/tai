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
- **Quits with the browser** — each tab holds a presence websocket, and the
  daemon exits a few seconds after the last one closes, so it does not linger in
  the background. Reloading does not count; turn it off in Settings to keep it
  running.

## Security

The daemon is powerful, so it is locked down:

- binds to **loopback only** (refuses non-loopback `--addr`);
- every request needs the **startup token** (printed as a URL); a `SameSite=Strict`
  cookie bootstraps it, and the SPA sends it as `X-Auth-Token`;
- mutating requests must carry a **loopback Origin** (CSRF defense);
- deploy passwords are used once and never stored.

Do not expose it to the network.

## Build & run

Requirements: Go ≥ 1.25, Node ≥ 20, `rsvg-convert` (package `librsvg2-bin`, used to
render the icons), and `sshfs` / `ssh-keygen` on `PATH`.

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

## Install (Linux desktop)

`install.sh` builds everything and registers webssh in the application menu:

```sh
./install.sh          # deps if needed + build + desktop entry
./install.sh --run    # ...and start the server afterwards
```

It installs into your home directory only — no root, no system paths:

- icons → `~/.local/share/icons/hicolor/<size>/apps/webssh.png` (+ `scalable/…svg`);
- launcher → `~/.local/share/applications/webssh.desktop`, with `Exec` pointing at
  the binary in this checkout, so don't move the directory afterwards.

The same steps are available as `make install-desktop`. Some desktops only pick
up a new launcher after a re-login.

To remove it again:

```sh
./uninstall.sh          # stop the daemon, unmount sshfs, drop launcher + binary
./uninstall.sh --purge  # ...and delete the database, keys and ~/.ssh integration
```

The plain form keeps everything you own, so reinstalling resumes where you left
off. `--purge` asks before deleting and backs up `~/.ssh/config` before dropping
the `Include` line. Unmounting comes first on purpose — deleting a directory
while an sshfs mount is live would delete files on the remote host. If you only
want the launcher gone, `make uninstall-desktop` still does just that.

## Icons

All icons are rendered from one master, `assets/icon.svg`, by `assets/gen-icons.sh`
(`make icons`). `make build` runs it automatically, so the favicons land in
`web/public/` before Vite copies them into the embedded `web/dist/`.

An ELF binary cannot carry an icon, which is why Linux gets a `.desktop` entry
instead. Windows can, via a PE resource:

```sh
make build-windows    # go-winres builds the .syso, then GOOS=windows go build
```

That produces `webssh.exe` with the icon and version info embedded. Note it only
*builds* — the pty layer is a stub on Windows and the default terminal/mount
commands are Linux ones, so it does not run there yet.

## Layout

- `cmd/webssh` — entry point (flags, token, loopback bind).
- `internal/{store,sshconfig,keys,mount,launcher,pty,server,config}` — backend.
- `web/` — Svelte + Vite SPA, embedded into the binary via `web/embed.go`.
- `assets/` — master `icon.svg` and the script that renders every raster size.
- `packaging/` — `.desktop` template used by `make install-desktop`.
- `winres/` — resource manifest for the Windows icon and version info.

## Roadmap (v2)

Port forwarding (local/remote/SOCKS) with a tunnel monitor, background
health-checks, broadcast commands, `known_hosts` manager, encrypted secret
storage, command palette, Ansible/PuTTY import.
