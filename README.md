**English** · [Українська](README.uk.md) · [Русский](README.ru.md)

# webssh

A **local web control panel** for managing lots of SSH connections — and the
telnet boxes and web appliances that sit alongside them. It is *not* a browser
SSH gateway — it runs as a daemon on your own machine with your privileges, so
it can edit `~/.ssh/config`, generate/deploy keys, mount remote directories over
`sshfs`, and launch your native terminal / file manager. A browser-based
terminal is included as a fallback for remote use.

## Features

- **Inventory** — hosts with groups (hierarchy: prod/staging, per-client), tags,
  and fuzzy search. Manual create/edit, `ProxyJump`/bastion, extra ssh options.
- **Per-host services** — besides SSH, a host records optional telnet, Proxmox VE,
  Proxmox Backup Server, HTTP and HTTPS ports. Its card shows only the buttons
  that machine can answer, and an empty SSH port marks a host with no SSH at all —
  a telnet-only switch, or an appliance you reach solely through its web UI.
- **Network discovery** — **Scan** finds hosts on a single address
  (`192.168.23.24`), a range (`192.168.1.2-32`) or a network (`192.168.1.0/24`),
  probing 22, 23, 80, 443, 8006 and 8007, and adds the ones you pick in one
  batch. **Rescan** on a card re-probes that host and opens its editor with the
  ports it found.
- **~/.ssh/config sync** — import existing config; export the inventory to a
  managed file `~/.ssh/config.d/inventory` wired in via a single `Include` line.
  Your main config is backed up and never overwritten. Hosts with no SSH port
  are left out of it.
- **Keys** — generate keypairs (ed25519/rsa/ecdsa) and deploy public keys to
  hosts (`authorized_keys`) for passwordless access. Tracks what is deployed where.
- **Known hosts** — import `~/.ssh/known_hosts`, paste entries or fetch them with
  `ssh-keyscan`, and export them back.
- **sshfs** — mount/unmount a host's home directory, open it in your file manager.
- **Web SFTP browser** — browse the remote filesystem, download/upload, mkdir,
  rename, delete, in the browser (agent/key auth, or a one-shot web password prompt).
- **Native launch** — configurable commands to open your terminal / file manager.
- **Web terminal** — xterm.js session over a websocket, speaking either SSH or
  telnet. Telnet is implemented in-process, so no `telnet(1)` binary is needed;
  Backspace defaults to Ctrl-H and toggles to DEL in the terminal bar.
- **Fewer buttons per card** — Settings picks whether a host card offers the web
  terminal, your system terminal, both or neither, and likewise the web SFTP
  browser, your file manager (which carries Mount/Unmount) or neither. Telnet
  always stays: it has no native counterpart.
- **Health checks** — background probes colour each host green (its port is
  open), yellow (answers ping only) or red (unreachable).
- **Master password** — optional gate over the whole panel, plus encrypted
  backup and restore of the inventory and keys.
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
- deploy passwords are used once and never stored;
- an optional **master password** gates every endpoint until you unlock;
- scanning and health checks only **open a TCP connection and close it** — no
  login is attempted, so nothing reaches the target's auth log, and one scan is
  capped at 4096 addresses and 20000 probes.

Do not expose it to the network.

## Build & run

Requirements: Go ≥ 1.25, Node ≥ 20, `rsvg-convert` (package `librsvg2-bin`, used to
render the icons), and `sshfs` / `ssh-keygen` on `PATH`.

```sh
git clone https://github.com/maxx1980/tai
cd tai
make deps    # install frontend deps (once)
make run     # build SPA + binary, start server, open browser
```

Or manually:

```sh
git clone https://github.com/maxx1980/tai
cd tai/web && npm install && npm run build && cd ..
go build -o webssh ./cmd/webssh
./webssh                 # prints http://127.0.0.1:8022/?token=…
```

Flags: `--addr 127.0.0.1:8022`, `--no-open`, `--ui browser|app|webview`.

## How the interface opens

Three modes, picked during install and changeable later in Settings:

| Mode | What you get | Needs |
|---|---|---|
| `app` (default) | a chromeless window — no tabs, no address bar, its own taskbar icon | any chromium-based browser |
| `browser` | a normal tab in your default browser | nothing |
| `webview` | a native GTK window inside the binary, no browser at all | a build with `make build-webview` |

The app window runs the browser with `--user-data-dir` pointing into
`~/.local/share/webssh/browser`, so it has its own cookie jar (the auth token
lives in one), no extensions, and no entanglement with your browsing session.
`--class=webssh` plus `StartupWMClass` in the `.desktop` file makes the window
group under the webssh icon rather than the browser's.

`install.sh` lists every chromium-based browser it finds and lets you pick one;
the choice is stored as `app_browser` and can be changed in Settings. Leave it
empty to detect one at startup.

Modes degrade instead of failing: a `webview` binary built without the tag falls
back to an app window, and an app window with no chromium-based browser
installed falls back to the default browser. Setting `browser_cmd` overrides the
mode entirely.

Together with "quits with the browser" above, the app window makes webssh behave
like an ordinary desktop application: close the window and the daemon stops.

The webview build additionally needs GTK and WebKit headers
(Debian/Deepin: `sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev`), which is
why it is opt-in — it turns on cgo, whereas the default build is pure Go and
cross-compiles to Windows unchanged.

## Install (Linux desktop)

`install.sh` builds everything and registers webssh in the application menu:

```sh
git clone https://github.com/maxx1980/tai
cd tai
./install.sh              # asks how the interface should open, then builds
./install.sh --ui app     # skip the question
./install.sh --run        # ...and start the server afterwards
```

It asks which of the three modes above to use, marking any that this machine
cannot provide, and records the answer where the Settings panel reads it.

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
- `internal/` — backend: `store`, `config`, `server`, `sshconfig`, `keys`,
  `knownhosts`, `mount`, `sftpbrowse`, `launcher`, `appwin`, `askpass`, `pty`,
  `telnet` (built-in telnet client), `netscan` (port discovery), `health`,
  `backup`.
- `web/` — Svelte + Vite SPA, embedded into the binary via `web/embed.go`.
- `assets/` — master `icon.svg` and the script that renders every raster size.
- `packaging/` — `.desktop` template used by `make install-desktop`.
- `winres/` — resource manifest for the Windows icon and version info.

## Roadmap

Port forwarding (local/remote/SOCKS) with a tunnel monitor, broadcast commands,
command palette, Ansible/PuTTY import.
