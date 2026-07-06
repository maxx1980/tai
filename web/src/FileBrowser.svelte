<script lang="ts">
  import { sftp, ApiError, type SftpEntry } from "./api";
  import { notify } from "./store.svelte";

  let { hostId, alias, onclose }: { hostId: number; alias: string; onclose: () => void } =
    $props();

  let cwd = $state("");
  let entries = $state<SftpEntry[]>([]);
  let busy = $state(false);
  let error = $state("");

  // One-shot password kept only in memory for this browser session.
  let password = $state<string | undefined>(undefined);
  let showPw = $state(false);
  let pwValue = $state("");
  let pending = $state<((pw?: string) => Promise<void>) | null>(null);

  let fileInput: HTMLInputElement;

  // op runs an SFTP action, injecting the session password and, on a
  // password-required response, opening the prompt and remembering the action.
  async function op(fn: (pw?: string) => Promise<void>) {
    busy = true;
    error = "";
    try {
      await fn(password);
    } catch (e) {
      if (e instanceof ApiError && e.data?.need_password) {
        pending = fn;
        showPw = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
        notify("error", error);
      }
    } finally {
      busy = false;
    }
  }

  function load(target: string) {
    return op(async (pw) => {
      const r = await sftp.list(hostId, target, pw);
      cwd = r.path;
      entries = r.entries;
    });
  }

  function submitPw() {
    password = pwValue;
    showPw = false;
    pwValue = "";
    const f = pending;
    pending = null;
    if (f) op(f);
  }

  function parent(p: string): string {
    const i = p.replace(/\/+$/, "").lastIndexOf("/");
    return i <= 0 ? "/" : p.slice(0, i);
  }

  const crumbs = $derived.by(() => {
    const parts = cwd.split("/").filter(Boolean);
    const out: { label: string; path: string }[] = [{ label: "/", path: "/" }];
    let acc = "";
    for (const p of parts) {
      acc += "/" + p;
      out.push({ label: p, path: acc });
    }
    return out;
  });

  function enter(e: SftpEntry) {
    if (e.is_dir) load(e.path);
    else download(e);
  }

  const download = (e: SftpEntry) =>
    op(async (pw) => {
      await sftp.download(hostId, e.path, pw);
    });

  const remove = (e: SftpEntry) => {
    if (!confirm(`Delete ${e.is_dir ? "folder" : "file"} "${e.name}"?${e.is_dir ? " (recursive)" : ""}`))
      return;
    op(async (pw) => {
      await sftp.remove(hostId, e.path, pw);
      notify("success", `Deleted ${e.name}`);
    }).then(() => load(cwd));
  };

  const rename = (e: SftpEntry) => {
    const name = prompt("Rename to:", e.name);
    if (!name || name === e.name) return;
    op(async (pw) => {
      await sftp.rename(hostId, e.path, `${cwd}/${name}`.replace("//", "/"), pw);
    }).then(() => load(cwd));
  };

  const mkdir = () => {
    const name = prompt("New folder name:");
    if (!name?.trim()) return;
    op(async (pw) => {
      await sftp.mkdir(hostId, `${cwd}/${name.trim()}`.replace("//", "/"), pw);
    }).then(() => load(cwd));
  };

  async function onFiles(ev: Event) {
    const files = (ev.target as HTMLInputElement).files;
    if (!files?.length) return;
    for (const f of Array.from(files)) {
      await op(async (pw) => {
        await sftp.upload(hostId, cwd, f, pw);
        notify("success", `Uploaded ${f.name}`);
      });
    }
    fileInput.value = "";
    load(cwd);
  }

  function fmtSize(n: number): string {
    if (n < 1024) return `${n} B`;
    const u = ["KB", "MB", "GB", "TB"];
    let i = -1;
    do {
      n /= 1024;
      i++;
    } while (n >= 1024 && i < u.length - 1);
    return `${n.toFixed(1)} ${u[i]}`;
  }

  const fmtDate = (unix: number) => new Date(unix * 1000).toLocaleString();

  $effect(() => {
    load(""); // initial: remote home
    return () => sftp.disconnect(hostId).catch(() => {});
  });
</script>

<div class="fb">
  <div class="bar">
    <strong>📁 {alias}</strong>
    <span class="muted" style="font-size:12px">SFTP</span>
    <div class="spacer"></div>
    <button class="sm" onclick={() => load(parent(cwd))} disabled={busy || cwd === "/"}>↑ Up</button>
    <button class="sm" onclick={mkdir} disabled={busy}>New folder</button>
    <button class="sm" onclick={() => fileInput.click()} disabled={busy}>Upload</button>
    <button class="sm" onclick={() => load(cwd)} disabled={busy}>↻</button>
    <button class="sm ghost" onclick={onclose}>Close ✕</button>
    <input type="file" multiple bind:this={fileInput} onchange={onFiles} style="display:none" />
  </div>

  <div class="crumbs">
    {#each crumbs as c, i}
      {#if i > 0}<span class="sep">/</span>{/if}
      <button class="crumb" onclick={() => load(c.path)}>{c.label}</button>
    {/each}
  </div>

  {#if error}
    <div class="err">{error}</div>
  {/if}

  <div class="listing scroll">
    {#if busy && entries.length === 0}
      <div class="muted pad">Loading…</div>
    {:else if entries.length === 0}
      <div class="muted pad">Empty directory</div>
    {/if}
    {#each entries as e (e.path)}
      <div class="entry">
        <button class="name" onclick={() => enter(e)} title={e.path}>
          <span class="icon">{e.is_dir ? "📁" : e.is_link ? "🔗" : "📄"}</span>
          <span class="label">{e.name}</span>
        </button>
        <span class="size muted">{e.is_dir ? "" : fmtSize(e.size)}</span>
        <span class="date muted">{fmtDate(e.mod_time)}</span>
        <span class="mode muted mono">{e.mode}</span>
        <span class="ops">
          {#if !e.is_dir}
            <button class="sm ghost" title="Download" onclick={() => download(e)}>⬇</button>
          {/if}
          <button class="sm ghost" title="Rename" onclick={() => rename(e)}>✎</button>
          <button class="sm ghost danger" title="Delete" onclick={() => remove(e)}>🗑</button>
        </span>
      </div>
    {/each}
  </div>
</div>

{#if showPw}
  <div class="overlay" onclick={() => (showPw = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(ev) => ev.stopPropagation()} style="max-width:420px">
      <h2>Password for {alias}</h2>
      <p class="muted" style="margin-top:0">
        No deployed key worked. Enter the SSH password to browse files (used for this
        session only, not stored). Deploy a key from the Keys tab for passwordless access.
      </p>
      <div class="field">
        <label for="sfpw">SSH password</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="sfpw" type="password" bind:value={pwValue} autofocus
          onkeydown={(ev) => ev.key === "Enter" && submitPw()} />
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (showPw = false)}>Cancel</button>
        <button class="primary" onclick={submitPw}>Connect</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .fb {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--panel-2);
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }
  .crumbs {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
    font-size: 13px;
  }
  .crumb { background: transparent; border: none; padding: 2px 6px; border-radius: 6px; color: var(--accent); }
  .crumb:hover { background: var(--panel-2); }
  .sep { color: var(--muted); }
  .err { color: var(--danger); padding: 8px 12px; font-size: 13px; }
  .listing { flex: 1; min-height: 0; }
  .pad { padding: 16px; }
  .entry {
    display: grid;
    grid-template-columns: 1fr 90px 160px 110px auto;
    align-items: center;
    gap: 8px;
    padding: 4px 12px;
    border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
  }
  .entry:hover { background: var(--panel-2); }
  .name {
    display: flex;
    align-items: center;
    gap: 8px;
    background: transparent;
    border: none;
    text-align: left;
    color: var(--text);
    overflow: hidden;
  }
  .name .label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .icon { flex-shrink: 0; }
  .size { text-align: right; font-size: 12px; }
  .date, .mode { font-size: 12px; white-space: nowrap; }
  .ops { display: flex; gap: 2px; justify-content: flex-end; }
  @media (max-width: 720px) {
    .entry { grid-template-columns: 1fr auto; }
    .size, .date, .mode { display: none; }
  }
</style>
