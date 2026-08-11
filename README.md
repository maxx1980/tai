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
- **In-place update** — the panel compares its build with the newest tag on
  GitHub; when one is newer, an Update tab appears left of Inventory. It offers
  to save an encrypted backup first, then fetches the tag into your checkout,
  rebuilds and restarts. Needs the same tools the install did (go, npm, make,
  rsvg-convert) and a clean working copy. A prebuilt install has no checkout to
  rebuild, so the tab shows the one-line installer to re-run instead.
- **Rollback** — every backup lands in `~/.local/share/webssh/backups`, the ones
  taken before an update and the ones the Backup button makes, in the same
  encrypted format. Settings lists them and restores, downloads or deletes any
  of them, so a bad version can be undone: restore the data there, then check
  the old tag out and rebuild.
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

## Install (prebuilt binary)

One line, no toolchain — it downloads the newest release, checks its SHA-256 and
registers webssh in the application menu:

```sh
curl -fsSL https://raw.githubusercontent.com/maxx1980/tai/main/get.sh | bash
```

Everything lands under `$HOME`; nothing needs root:

- binary → `~/.local/bin/webssh`
- icons → `~/.local/share/icons/hicolor/<size>/apps/webssh.png` (+ `scalable/…svg`)
- launcher → `~/.local/share/applications/webssh.desktop`
- data → `~/.local/share/webssh` (untouched by a re-install)

Options go after `-s --`, because the script is being piped into bash:

```sh
... | bash -s -- --ui browser        # a plain browser tab, not an app window
... | bash -s -- --version v0.4.0    # one specific release
... | bash -s -- --prefix ~/apps     # binary in ~/apps/bin instead
... | bash -s -- --run               # start it once installed
... | bash -s -- --uninstall         # remove binary, icons and launcher
```

Linux x86-64 and arm64. The release binary is static (`CGO_ENABLED=0`), so it
does not care about your libc, and it covers the `browser` and `app` modes; the
native `webview` window needs cgo and webkit at build time and is source-only.

Windows has no working native build yet — `internal/pty` has no real ConPTY
backend, so the web terminal cannot start a shell. Until that lands, webssh
runs inside **WSL** instead, driven from the Windows side by three small
native helpers built from `cmd/webssh-setup`, `cmd/webssh-launcher` and
`cmd/webssh-uninstall` (pure Go, no cgo — same as the Linux/macOS binaries):

Download `webssh-setup.exe` from the [releases page](https://github.com/maxx1980/tai/releases)
and double-click it. It installs WSL and a distro if either is missing
(asking for administrator rights through the normal UAC prompt only for
that step, and telling you — in a message box, not just console text easy to
miss — to reboot and run it again if Windows needs one first), installs
webssh inside via `get.sh`, then adds a **webssh** shortcut to your Desktop
and Start Menu and registers it under **Settings → Apps** with a working
Uninstall button. From then on, the shortcut is all you need: it starts
webssh inside WSL, reads the URL it prints, and opens it — in a chromeless
app window when a chromium-based browser is findable (Edge ships inbox with
every modern Windows install, so this is almost always the case), your
regular browser otherwise.

Uninstalling (Settings → Apps → webssh → Uninstall, or running
`webssh-uninstall.exe` from `%LOCALAPPDATA%\webssh` directly) removes the
shortcuts and that registration; it leaves webssh and your data inside WSL
alone unless you pass `-purge`, which also runs `get.sh --uninstall` there —
same as the *Rollback* bullet under Features above, it removes the binary
but always leaves your inventory and keys in place.

Prefer to read a script before running it, or already have WSL and a distro
set up the way you want? `get.ps1`/`get.bat` do the same install step (not
the shortcut/uninstaller registration) more transparently — see
[`get.ps1`](https://raw.githubusercontent.com/maxx1980/tai/main/get.ps1) and
[`get.bat`](https://raw.githubusercontent.com/maxx1980/tai/main/get.bat), or:

```powershell
irm https://raw.githubusercontent.com/maxx1980/tai/main/get.ps1 | iex
```

Either way, everything running inside WSL is plain Linux — the pty, sshfs,
ping — so nothing else in this README changes; WSL2 forwards `127.0.0.1` to
Windows automatically, which is what lets the token URL open directly in a
Windows browser without anything extra installed inside the distro.
`webssh-setup.exe` also installs `sshfs` inside the distro, and webssh
detects it is running under WSL and points the Terminal/Files "system"
buttons at Windows itself (`wsl.exe`/`explorer.exe` via WSL2 interop)
instead of the Linux desktop commands — both work without touching
Settings.

Re-running the one-liner upgrades in place — it is also what the Update tab
tells a prebuilt install to do, since there is no checkout there to rebuild.
Prefer to install offline? Download the `.tar.gz` from the
[releases page](https://github.com/maxx1980/tai/releases), unpack it and run the
`get.sh` inside it: it installs the files next to it instead of downloading.

### Android (via Termux)

Android has no WSL-style compatibility layer, but it doesn't need one:
[Termux](https://f-droid.org/packages/com.termux/) is a real Linux userland
app. Install it from **F-Droid**, not the Play Store build, which is
abandoned and can no longer reach GitHub or its own package mirrors. Then,
*inside Termux*:

```sh
curl -fsSL https://raw.githubusercontent.com/maxx1980/tai/main/packaging/termux/install.sh | bash
```

This is a different installer from the one above, and deliberately so: no
release *binary* runs under Termux's bionic dynamic linker on modern
Android — a plain `CGO_ENABLED=0 GOOS=linux` build is rejected outright
(non-PIE), and even a PIE rebuild fails bionic's TLS-segment alignment check.
The only binary that actually runs is one built with `GOOS=android
CGO_ENABLED=1` — exactly what `pkg install golang` defaults to inside Termux
itself — so this script installs Go and `openssh` (the ssh client webssh's
terminal shells out to), downloads the matching source tarball (`web/dist`
ships prebuilt in it, so Termux never needs Node) and **builds webssh
on-device**. A cold build compiles the whole module graph and can take a
couple of minutes; re-runs are faster since Go's build cache survives
between them.

webssh then defaults every host card to its web-only buttons instead of ones
that would fail on first tap: Android has no FUSE (so no `sshfs` mount, and
no "Files" button) and no `gnome-terminal`/`xdg-open` equivalent (so no
"Terminal" button) — use the **Web term** and **Browse** (SFTP) buttons
instead, which need nothing beyond `openssh`. Auto-opening the token URL in a
browser also needs a hop through Android, not a Linux `xdg-open`: install
`pkg install termux-api` (and the
[Termux:API](https://f-droid.org/packages/com.termux.api/) companion app) so
webssh can hand the URL to Android's default browser; without it, the URL is
still printed to the terminal, which is tap-to-open there.

The installer also drops a one-tap launcher at `~/.shortcuts/webssh.sh` —
install [Termux:Widget](https://f-droid.org/packages/com.termux.widget/),
long-press the home screen, add a Termux:Widget widget, and pick **webssh**:
it starts the daemon, waits for the token URL, and opens it the same way.

Re-run the same one-liner to rebuild against the newest release; remove
everything with `... | bash -s -- --uninstall` (data under
`~/.local/share/webssh` is kept, same as the desktop uninstaller).

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

Flags: `--addr 127.0.0.1:8022`, `--no-open`, `--ui browser|app|webview`, `--version`.

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

## Install from source (Linux desktop)

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

(`uninstall.sh` drives `make`, so it is for source installs. A prebuilt one is
removed with `get.sh --uninstall`.)

## Releases

```sh
make dist              # one archive for this machine's architecture
make dist-termux-src   # source tarball packaging/termux/install.sh builds from
make dist-windows      # webssh-setup.exe, versioned
make dist-all          # amd64 + arm64 + termux-src + webssh-setup.exe + SHA256SUMS covering all of it
```

Each Linux archive is `dist/webssh-<tag>-linux-<arch>.tar.gz` and holds the
binary, the icons, the `.desktop` template and `get.sh` itself, which is what
makes it installable offline; `dist-windows` instead produces one file,
`dist/webssh-setup-<tag>.exe` (`webssh-launcher.exe`/`webssh-uninstall.exe`
are embedded inside it, not published separately). `dist-termux-src` produces
`dist/webssh-<tag>-termux-src.tar.gz` — no release *binary* runs under
Termux's bionic dynamic linker (see [Android](#android-via-termux) above), so
this ships `cmd/`, `internal/` and `web/` (with `web/dist` already built, so
Termux never needs Node) for `packaging/termux/install.sh` to compile
on-device with Termux's own Go toolchain. Publishing a release means
tagging, running `make dist-all` on a clean checkout of that tag, and
attaching every archive, the `.exe`, **and** `SHA256SUMS` — `get.sh` and
`packaging/termux/install.sh` both refuse to install without the checksums
file.

## Icons

All icons are rendered from one master, `assets/icon.svg`, by `assets/gen-icons.sh`
(`make icons`). `make build` runs it automatically, so the favicons land in
`web/public/` before Vite copies them into the embedded `web/dist/`.

An ELF binary cannot carry an icon, which is why Linux gets a `.desktop` entry
instead. Windows can, via a PE resource:

```sh
make build-windows            # go-winres builds the .syso, then GOOS=windows go build
make build-windows-setup      # webssh-setup.exe (embeds the two below)
make build-windows-launcher   # webssh-launcher.exe on its own
make build-windows-uninstall  # webssh-uninstall.exe on its own
```

`build-windows` produces `webssh.exe` with the icon and version info
embedded. Note it only *builds* — the pty layer is a stub on Windows and the
default terminal/mount commands are Linux ones, so it does not run there
yet. `build-windows-setup`/`-launcher`/`-uninstall` are the three Windows
helpers described under [Install](#install-prebuilt-binary) that run webssh
inside WSL instead, until a real ConPTY backend lands.

## Layout

- `cmd/webssh` — entry point (flags, token, loopback bind).
- `cmd/webssh-setup`, `cmd/webssh-launcher`, `cmd/webssh-uninstall` —
  Windows-only (see [Install](#install-prebuilt-binary)): install WSL/webssh
  and register a shortcut, start webssh in WSL and open it, and remove both.
- `internal/` — backend: `store`, `config`, `server`, `sshconfig`, `keys`,
  `knownhosts`, `mount`, `sftpbrowse`, `launcher`, `appwin`, `askpass`, `pty`,
  `telnet` (built-in telnet client), `netscan` (port discovery), `health`,
  `backup`, `wslutil`/`wininstall` (Windows-only, shared by the three above).
- `web/` — Svelte + Vite SPA, embedded into the binary via `web/embed.go`.
- `assets/` — master `icon.svg` and the script that renders every raster size.
- `packaging/` — `.desktop` template used by `make install-desktop` and
  `get.sh`; `packaging/termux/` holds the Android installer (`install.sh`,
  builds on-device) and the Termux:Widget one-tap launcher (`webssh.sh`).
- `get.sh` — the one-line installer for a prebuilt release (also removes it).
- `get.ps1` — Windows installer: sets up WSL and a distro, then runs `get.sh` in it.
- `get.bat` — double-click launcher for `get.ps1`, self-elevating via UAC when needed.
- `winres/` — resource manifests for the Windows icon and version info, one
  per `.exe`.

## Roadmap

Port forwarding (local/remote/SOCKS) with a tunnel monitor, broadcast commands,
command palette, Ansible/PuTTY import, and native Windows support (needs a
ConPTY-backed `internal/pty`; run it under WSL until then).
