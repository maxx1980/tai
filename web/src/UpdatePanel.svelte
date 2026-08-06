<script lang="ts">
  import { api, runUpdate, updateStatus, type UpdateStatus } from "./api";
  import { update, refreshUpdate, notify } from "./store.svelte";

  // The confirmation step. The user asked for an update, we ask about the
  // backup before touching anything — see the note in the dialog.
  let confirming = $state(false);
  let wantBackup = $state(true);
  let busy = $state(false);

  let status = $state<UpdateStatus | null>(null);
  let logBox: HTMLPreElement | undefined = $state();

  const info = $derived(update.info);
  const running = $derived(status?.state === "running");

  // Poll the build log while an update is under way. It also runs once on
  // mount, so reloading the page mid-update reattaches to it instead of
  // showing an idle panel over a build that is still going.
  $effect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;

    async function tick() {
      try {
        const s = await updateStatus();
        if (stop) return;
        const finished = status?.state === "running" && s.state !== "running";
        status = s;
        if (finished && s.state === "done") {
          notify("success", `Built ${s.target} — restart to run it`);
        } else if (finished && s.state === "error") {
          notify("error", s.error || "Update failed");
        }
      } catch {
        // The daemon may be briefly unreachable (it is rebuilding itself);
        // keep the last log on screen and try again.
      }
      if (!stop) timer = setTimeout(tick, status?.state === "running" ? 1000 : 5000);
    }

    tick();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  });

  // Follow the tail of the build log, the way a terminal would.
  $effect(() => {
    if (logBox && status?.log.length) logBox.scrollTop = logBox.scrollHeight;
  });

  async function start() {
    if (!info) return;
    busy = true;
    try {
      status = await runUpdate(info.latest, wantBackup);
      confirming = false;
      notify("info", "Update started");
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  async function doRestart() {
    notify("info", "Restarting…");
    try {
      await api.post("/api/restart");
      // The daemon re-execs; give it a moment before reloading into the new build.
      setTimeout(() => location.reload(), 1500);
    } catch (e) {
      notify("error", String(e));
    }
  }
</script>

<div class="grid">
  <section>
    <h3>Update</h3>

    {#if !info}
      <p class="muted">Checking GitHub…</p>
    {:else}
      <div class="versions">
        <div>
          <span class="muted">Installed</span>
          <div class="mono big">{info.current || "unknown"}</div>
        </div>
        {#if info.available}
          <div class="arrow">→</div>
          <div>
            <span class="muted">Available</span>
            <div class="mono big new">{info.latest}</div>
          </div>
        {/if}
      </div>

      {#if info.available && info.can_update}
        <p>
          A newer version is tagged on GitHub. Updating fetches that tag into
          <span class="mono">{info.source_dir}</span> and rebuilds the binary there — the same
          steps <span class="mono">install.sh</span> runs, so it needs go, npm, make and
          rsvg-convert. Your hosts, keys and settings live in the database and are not touched.
        </p>
      {:else if info.available}
        <p>
          A newer version is out, but this copy cannot update itself. Your hosts, keys and
          settings live in the database, so whatever you do below leaves them alone.
        </p>
      {:else if info.latest}
        <p class="muted">
          {info.current} is the newest version — {info.latest} is the latest tag on
          {info.repo}.
        </p>
      {/if}

      {#if info.blocker}
        <div class="warn">{info.blocker}</div>
      {/if}

      <div class="row" style="flex-wrap:wrap">
        {#if info.available && info.can_update}
          <button class="primary" onclick={() => (confirming = true)} disabled={busy || running}>
            {running ? "Updating…" : "Update to " + info.latest}
          </button>
        {/if}
        <button onclick={() => refreshUpdate(true)} disabled={update.checking || running}>
          {update.checking ? "Checking…" : "Check again"}
        </button>
        {#if info.notes_url}
          <a class="muted" href={info.notes_url} target="_blank" rel="noreferrer noopener">
            What changed
          </a>
        {/if}
      </div>
    {/if}
  </section>

  {#if status && status.state !== "idle"}
    <section>
      <h3>
        {#if status.state === "running"}
          <!-- The step, because the log is mostly npm and vite chatter and the
               tail of it does not say which phase is running. -->
          Updating to {status.target} — {status.step}…
        {:else if status.state === "done"}Ready to restart
        {:else}Update failed ({status.step}){/if}
      </h3>

      {#if status.backup}
        <p class="muted">Database copied to <span class="mono">{status.backup}</span></p>
      {/if}

      {#if status.state === "error"}
        <div class="warn">{status.error}</div>
        <p class="muted">
          Nothing was installed. The working copy may be left on the new tag — check it with
          <span class="mono">git status</span> in {info?.source_dir}.
        </p>
      {/if}

      <pre bind:this={logBox} class="log mono">{status.log.join("\n")}</pre>

      {#if status.state === "done"}
        <div class="row">
          <button class="primary" onclick={doRestart}>Restart into {status.target}</button>
        </div>
        <span class="muted" style="font-size:12px">
          The new binary is built but the old one is still running. Restarting reloads this tab.
        </span>
      {/if}
    </section>
  {/if}
</div>


{#if confirming}
  <div class="overlay" onclick={() => (confirming = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Update to {info?.latest}</h2>
      <p class="muted" style="margin-top:0">
        An update rebuilds webssh from the new tag. It does not change your data — but a new
        version may migrate the database, and a migration is the one step checking the old tag
        back out will not undo.
      </p>
      <label class="pick">
        <input type="checkbox" bind:checked={wantBackup} />
        Copy the database to ~/.local/share/webssh/backups first
      </label>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (confirming = false)} disabled={busy}>Cancel</button>
        <button class="primary" onclick={start} disabled={busy}>
          {wantBackup ? "Back up and update" : "Update without a backup"}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .grid { display: grid; gap: 20px; max-width: 720px; }
  section {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 16px;
  }
  h3 { margin: 0 0 8px; font-size: 14px; }
  p { margin-top: 0; font-size: 12.5px; }
  .versions { display: flex; align-items: flex-end; gap: 16px; margin-bottom: 12px; }
  .versions span { display: block; font-size: 11px; text-transform: uppercase; letter-spacing: 0.4px; }
  .big { font-size: 16px; }
  .new { color: var(--accent); }
  .arrow { color: var(--muted); padding-bottom: 2px; }
  .warn {
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
    border-radius: 8px;
    padding: 10px 12px;
    font-size: 13px;
    margin-bottom: 12px;
    /* A blocker may end in a command to run; keep it on its own line. */
    white-space: pre-line;
  }
  .log {
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 12px;
    margin: 0 0 12px;
    max-height: 320px;
    overflow: auto;
    font-size: 12px;
    line-height: 1.45;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .pick { display: flex; align-items: center; gap: 8px; margin: 4px 0 12px; font-size: 13px; }
</style>
