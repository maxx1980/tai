// Package update keeps a source-installed webssh current: it asks GitHub for
// the newest version tag, compares it with the running build, and — when the
// user asks for it — fetches that tag, rebuilds the binary with make and hands
// back to the caller to restart.
//
// It only knows how to update a git working copy, because that is the only
// install it can rebuild: install.sh clones the repository and runs make, so an
// update there is a rebuild and needs the same toolchain the install needed. A
// prebuilt install from a release archive has no checkout and no toolchain, so
// it is still told a newer version exists — the installer that put it there is
// what replaces it. Every reason an update cannot proceed is reported as data
// (Info.Blocker) rather than an error, because "this is a packaged copy, not a
// checkout" is a normal state, not a failure.
package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"webssh/internal/appwin"
	"webssh/internal/version"
)

// Timeouts. The build one is generous on purpose: `make build` runs npm and a
// cold Go build, which on a slow machine is minutes, and a rebuild killed
// halfway leaves the working copy on the new tag with the old binary.
const (
	gitTimeout   = 90 * time.Second
	buildTimeout = 20 * time.Minute
	httpTimeout  = 15 * time.Second

	// checkTTL caps how often GitHub is asked. The SPA checks on every load and
	// the unauthenticated API allows only 60 requests an hour per address.
	checkTTL = 15 * time.Minute

	// maxLogLines bounds the in-memory build log. npm is chatty and the log is
	// re-sent on every poll, so keep only the tail that is worth reading.
	maxLogLines = 500
)

// DefaultRepo is where releases come from. It is only consulted when there is
// no checkout to read an origin remote from — a source install always follows
// its own remote, so a fork updates from the fork.
const DefaultRepo = "maxx1980/tai"

// InstallCmd replaces a prebuilt install: get.sh downloads the newest release
// over the binary, icons and launcher it put there in the first place. Shown to
// the user, so it is written the way it would be pasted into a shell.
const InstallCmd = "curl -fsSL https://raw.githubusercontent.com/" + DefaultRepo +
	"/main/get.sh | bash"

// requiredTools are the commands the rebuild shells out to — the same list
// install.sh checks. They are verified up front rather than when they are
// reached, so a missing npm cannot leave the checkout on a new tag whose
// binary was never built.
var requiredTools = []string{"git", "make", "go", "npm", "rsvg-convert"}

var httpClient = &http.Client{Timeout: httpTimeout}

// Info is the answer to "should this install be updated, and can it be?".
type Info struct {
	Current   string    `json:"current"`           // running build, e.g. "v0.2.0-3-g40c40f1"
	Latest    string    `json:"latest"`            // newest tag on GitHub, "" if unknown
	Available bool      `json:"available"`         // Latest is newer than Current
	CanUpdate bool      `json:"can_update"`        // an in-place update would work
	Blocker   string    `json:"blocker,omitempty"` // why not, worded for the user
	SourceDir string    `json:"source_dir,omitempty"`
	Repo      string    `json:"repo,omitempty"`      // owner/name on GitHub
	NotesURL  string    `json:"notes_url,omitempty"` // where to read what changed
	CheckedAt time.Time `json:"checked_at"`
}

// Status is the live state of a running (or finished) update.
type Status struct {
	State   string    `json:"state"` // idle | running | done | error
	Step    string    `json:"step"`
	Log     []string  `json:"log"`
	Error   string    `json:"error,omitempty"`
	Target  string    `json:"target,omitempty"` // the tag being installed
	Backup  string    `json:"backup,omitempty"` // path of the database snapshot
	Started time.Time `json:"started,omitzero"`
	Done    time.Time `json:"done,omitzero"`
}

// Updater checks for new versions and runs at most one update at a time,
// keeping the build log in memory because the rebuild outlives any sane
// request timeout and the SPA polls it instead.
type Updater struct {
	mu      sync.Mutex
	st      Status
	running bool

	cacheMu sync.Mutex
	cached  Info
}

// New returns an idle Updater.
func New() *Updater {
	return &Updater{st: Status{State: "idle", Log: []string{}}}
}

// ---- checking ----

// Check compares the running build with the newest tag on GitHub. A result
// younger than checkTTL is reused unless force is set. It returns an error only
// when GitHub could not be reached — anything about this install that rules out
// updating comes back in Info.Blocker with CanUpdate false.
func (u *Updater) Check(ctx context.Context, force bool) (Info, error) {
	u.cacheMu.Lock()
	defer u.cacheMu.Unlock()
	if !force && !u.cached.CheckedAt.IsZero() && time.Since(u.cached.CheckedAt) < checkTTL {
		return u.cached, nil
	}

	info := Info{Current: Current(), CheckedAt: time.Now()}

	// A prebuilt install has no checkout to read the remote from, but it is
	// still worth telling it that a new version is out, so fall back to the
	// project's own repository.
	slug := DefaultRepo
	dir, dirErr := SourceDir()
	if dirErr == nil {
		info.SourceDir = dir
		s, err := repoSlug(ctx, dir)
		if err != nil {
			info.Blocker = err.Error()
			u.cached = info
			return info, nil
		}
		slug = s
	}
	info.Repo = slug

	latest, err := latestTag(ctx, slug)
	if err != nil {
		return info, err // network or rate limit — transient, do not cache
	}
	info.Latest = latest
	info.NotesURL = "https://github.com/" + slug + "/releases/tag/" + latest
	info.Available = version.IsNewer(latest, info.Current)

	if dirErr != nil {
		info.Blocker = dirErr.Error() + ".\nUpdate it by re-running the installer:\n\n" + InstallCmd
	} else {
		info.CanUpdate, info.Blocker = canUpdate(dir)
	}
	u.cached = info
	return info, nil
}

// canUpdate reports whether an in-place update would get as far as a rebuild,
// and if not, why — checked before the button is offered rather than after it
// is pressed.
func canUpdate(dir string) (bool, string) {
	if missing := missingTools(); len(missing) > 0 {
		return false, "missing build tools: " + strings.Join(missing, ", ") +
			" — not found in your login shell's PATH either; install them (see" +
			" install.sh), or update by hand with `git pull && make build`"
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	dirty, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err.Error()
	}
	if dirty != "" {
		return false, "the working copy in " + dir +
			" has uncommitted changes — commit or discard them first"
	}
	return true, ""
}

func missingTools() []string {
	var missing []string
	path := buildPATH()
	for _, t := range requiredTools {
		if _, ok := lookIn(path, t); !ok {
			missing = append(missing, t)
		}
	}
	return missing
}

// lookIn finds name as an executable in one of path's directories.
// exec.LookPath would do this, but only against the process's own PATH, which
// is exactly the value buildPATH exists to replace.
func lookIn(path, name string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		full := filepath.Join(dir, name)
		fi, err := os.Stat(full)
		if err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return full, true
		}
	}
	return "", false
}

// resolve turns a command name into the absolute path buildPATH says it has.
// This is not optional: exec.Command resolves a bare name against the *running
// process's* PATH and ignores cmd.Env entirely, so a tool that only buildPATH
// can see would still come out "executable file not found" — or, worse, would
// silently resolve to a different copy in /usr/bin than the one the user builds
// with. Unresolvable names are passed through so exec reports them.
func resolve(name string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	if full, ok := lookIn(buildPATH(), name); ok {
		return full
	}
	return name
}

// buildPATH is the PATH the tool check and the rebuild use — deliberately not
// the one webssh inherited.
//
// webssh is normally started from the application menu, and a desktop session
// hands its children a bare PATH: /usr/local/bin:/usr/bin:/bin and little else.
// Anything the user installed under their home — Go in ~/.local/go/bin, node
// under ~/.nvm — is invisible there, so the updater would report tools missing
// that are plainly installed and that `make build` in a terminal finds without
// trouble.
//
// The authoritative answer is the login shell's own PATH, because that is what
// the user's own `make build` runs with. Ask it once, and fall back to the
// usual install directories when it cannot be asked.
var buildPATH = sync.OnceValue(resolveBuildPATH)

func resolveBuildPATH() string {
	var dirs []string
	seen := map[string]bool{}
	add := func(list string) {
		for _, d := range filepath.SplitList(list) {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	add(loginShellPATH())  // what the user actually builds with
	add(os.Getenv("PATH")) // whatever we were started with
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range fallbackDirs(home) {
			if fi, serr := os.Stat(d); serr == nil && fi.IsDir() {
				add(d)
			}
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// shellTimeout bounds the login-shell probe. A shell rc that blocks would
// otherwise stall the first update check, which runs on page load.
const shellTimeout = 5 * time.Second

// loginShellPATH asks the user's login shell to print its PATH. The marker is
// there because an interactive shell may print a prompt or a banner first, and
// -i is needed alongside -l because zsh reads .zshrc — where a PATH is usually
// set — only when it is interactive.
func loginShellPATH() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lic", `printf 'WEBSSH_PATH=%s\n' "$PATH"`)
	// Output captures stdout only, so anything the rc prints to stderr is
	// discarded rather than mistaken for an answer.
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "WEBSSH_PATH="); ok {
			return v
		}
	}
	return ""
}

// fallbackDirs lists where the build tools are conventionally installed, for
// when the shell cannot be asked. Only directories that exist are used.
func fallbackDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "go", "bin"),
		filepath.Join(home, "go", "bin"),
		"/usr/local/go/bin",
		"/usr/local/bin",
	}
	// nvm keeps every node under its own version directory and puts none of
	// them anywhere stable, so take the newest rather than guess a version.
	if matches, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "v*", "bin")); len(matches) > 0 {
		newest := matches[0]
		for _, m := range matches[1:] {
			if version.Compare(version.Parse(nodeVersionOf(m)), version.Parse(nodeVersionOf(newest))) > 0 {
				newest = m
			}
		}
		dirs = append(dirs, newest)
	}
	return dirs
}

// nodeVersionOf pulls "v22.23.1" out of ".../versions/node/v22.23.1/bin".
func nodeVersionOf(binDir string) string { return filepath.Base(filepath.Dir(binDir)) }

// Current returns the running build's version. Normally that is the string the
// Makefile stamped in; a binary from a bare `go build` carries none, so fall
// back to asking git in the working copy — the same answer the Makefile would
// have produced.
func Current() string {
	if version.Version != "" && version.Version != "dev" {
		return version.Version
	}
	dir, err := SourceDir()
	if err != nil {
		return version.Version
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if out, err := git(ctx, dir, "describe", "--tags", "--always", "--dirty"); err == nil && out != "" {
		return out
	}
	return version.Version
}

// SourceDir returns the git working copy the running binary was built in: make
// writes ./webssh next to the Makefile, so it is the executable's directory.
// The error is worded for the user, since it is shown as the reason updating is
// unavailable.
func SourceDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errors.New("cannot locate the running binary, so there is nothing to update")
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	// .git is a directory in a normal clone and a file in a worktree; both work.
	if _, serr := os.Stat(filepath.Join(dir, ".git")); serr != nil {
		return "", fmt.Errorf("%s is not a git checkout — in-place updates only work on a source install", dir)
	}
	if _, serr := os.Stat(filepath.Join(dir, "Makefile")); serr != nil {
		return "", fmt.Errorf("%s has no Makefile, so there is nothing to rebuild", dir)
	}
	return dir, nil
}

// repoRe pulls owner/name out of either remote URL form git uses for GitHub:
// git@github.com:owner/name.git and https://github.com/owner/name.git.
var repoRe = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`)

func repoSlug(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("this checkout has no 'origin' remote to check for updates")
	}
	m := repoRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("origin (%s) is not a GitHub repository, so there is nowhere to check", out)
	}
	return m[1] + "/" + m[2], nil
}

// latestTag returns the highest version tag on the GitHub repo. Tags rather
// than releases: a rebuild needs a ref to check out, and every release is
// tagged while not every tag is released, so the tag list is the complete one.
func latestTag(ctx context.Context, slug string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+slug+"/tags?per_page=100", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "webssh-updater")

	res, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("GitHub has no repository %s (is it private?)", slug)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", errors.New("GitHub is rate-limiting this address — try again later")
	default:
		return "", fmt.Errorf("GitHub answered %s", res.Status)
	}

	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tags); err != nil {
		return "", fmt.Errorf("GitHub sent something unreadable: %w", err)
	}

	best, bestP := "", version.Parsed{}
	for _, t := range tags {
		p := version.Parse(t.Name)
		if !p.OK {
			continue
		}
		if best == "" || version.Compare(p, bestP) > 0 {
			best, bestP = t.Name, p
		}
	}
	if best == "" {
		return "", fmt.Errorf("%s has no version tags yet", slug)
	}
	return best, nil
}

// ---- running ----

// Status returns a copy of the current state, safe to hand to a handler while
// the update goroutine keeps appending to the real one.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	// Explicitly an empty slice, not nil: encoding/json turns a nil slice into
	// null, and the panel joins this without checking.
	s.Log = make([]string, len(u.st.Log))
	copy(s.Log, u.st.Log)
	return s
}

// Start begins the update in the background and returns as soon as it is under
// way. snapshot, when non-nil, is called first with the tag being installed and
// saves an encrypted backup; if it fails the update does not start, because the
// whole point of taking one is to have it before anything changes.
func (u *Updater) Start(tag string, snapshot func(string) (string, error)) error {
	dir, err := SourceDir()
	if err != nil {
		return err
	}

	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return errors.New("an update is already running")
	}
	u.running = true
	u.st = Status{State: "running", Step: "starting", Target: tag, Started: time.Now(), Log: []string{}}
	u.mu.Unlock()

	go func() {
		err := u.steps(dir, tag, snapshot)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.running = false
		u.st.Done = time.Now()
		if err != nil {
			u.st.State = "error"
			u.st.Error = err.Error()
			u.st.Log = append(u.st.Log, "✗ "+err.Error())
			return
		}
		u.st.State = "done"
		u.st.Step = "finished"
		u.st.Log = append(u.st.Log, "✓ "+tag+" is built — restart to run it")
	}()
	return nil
}

func (u *Updater) steps(dir, tag string, snapshot func(string) (string, error)) error {
	if missing := missingTools(); len(missing) > 0 {
		return errors.New("missing build tools: " + strings.Join(missing, ", "))
	}

	if snapshot != nil {
		u.step("backup", "saving an encrypted backup")
		path, err := snapshot(tag)
		if err != nil {
			return fmt.Errorf("backup failed, nothing was changed: %w", err)
		}
		u.mu.Lock()
		u.st.Backup = path
		u.mu.Unlock()
		u.logf("saved %s", path)
	}

	gitCtx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	// Re-check here and not only in canUpdate: minutes may have passed since
	// the panel was drawn, and checking out over local edits would either fail
	// halfway or carry them into the new version.
	u.step("verify", "checking the working copy is clean")
	dirty, err := git(gitCtx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("%s has uncommitted changes — commit or discard them first", dir)
	}

	u.step("fetch", "fetching "+tag+" from GitHub")
	if err := u.run(gitCtx, dir, "git", "fetch", "--tags", "--force", "--prune", "origin"); err != nil {
		return err
	}

	u.step("checkout", "checking out "+tag)
	// Detached on purpose: the tag is a fixed point, and moving a branch to it
	// would rewrite whatever the user had checked out. `git checkout <branch>`
	// puts them back.
	if err := u.run(gitCtx, dir, "git", "checkout", "--detach", "--force", tag); err != nil {
		return err
	}

	buildCtx, cancelBuild := context.WithTimeout(context.Background(), buildTimeout)
	defer cancelBuild()

	// The SPA is embedded in the binary, so its dependencies must exist before
	// make build — and a new tag may have added some.
	u.step("deps", "installing frontend dependencies")
	if err := u.run(buildCtx, dir, "make", "deps"); err != nil {
		return err
	}

	target := "build"
	if appwin.HasWebview {
		target = "build-webview" // keep the native window this build was made with
	}
	u.step("build", "rebuilding (make "+target+") — this takes a few minutes")
	return u.run(buildCtx, dir, "make", target)
}

// run executes a command in dir and streams every line it prints into the
// status log, so the panel shows real progress instead of a spinner.
func (u *Updater) run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, resolve(name), args...)
	cmd.Dir = dir
	cmd.Env = commandEnv()

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("cannot run %s: %w", name, err)
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // npm prints very long lines
		for sc.Scan() {
			u.logf("%s", sc.Text())
		}
	}()

	waitErr := cmd.Wait()
	pw.Close()
	<-drained

	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s timed out", name, strings.Join(args, " "))
		}
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), waitErr)
	}
	return nil
}

func (u *Updater) step(id, human string) {
	u.mu.Lock()
	u.st.Step = id
	u.mu.Unlock()
	u.logf("== %s", human)
}

func (u *Updater) logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st.Log = append(u.st.Log, line)
	if n := len(u.st.Log); n > maxLogLines {
		u.st.Log = append([]string(nil), u.st.Log[n-maxLogLines:]...)
	}
}

// git runs a git command in dir and returns its trimmed stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, resolve("git"), args...)
	cmd.Dir = dir
	cmd.Env = commandEnv()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// commandEnv disables every prompt git might raise. On a daemon with no
// terminal a credential or passphrase prompt does not fail, it hangs — and the
// user is left watching an update that never finishes.
func commandEnv() []string {
	// Drop the inherited PATH rather than append over it: with a duplicate key
	// glibc's getenv returns the first, so an appended one would be ignored and
	// make would go looking for go and npm on the desktop session's bare PATH.
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return append(env,
		"PATH="+buildPATH(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
		"GCM_INTERACTIVE=never",
	)
}
