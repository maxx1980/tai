<script lang="ts">
  import { api } from "./api";
  import { app, refresh, notify } from "./store.svelte";
  import type { KnownHost } from "./types";

  let busy = $state(false);

  // Add / paste modal.
  let addOpen = $state(false);
  let addText = $state("");
  let addInput: HTMLInputElement;

  // Scan-host modal.
  let scanOpen = $state(false);
  let scanHost = $state("");
  let scanPort = $state(22);

  async function importFromSSH() {
    busy = true;
    try {
      const r = await api.post<{ added: number; parsed: number }>("/api/known-hosts/import", {});
      await refresh();
      notify("success", `Imported ${r.added} new of ${r.parsed} entries from ~/.ssh`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  async function exportAll() {
    busy = true;
    try {
      const r = await api.post<{ written: number; total: number }>("/api/known-hosts/export-all", {});
      await refresh();
      notify("success", `Wrote ${r.written} of ${r.total} entries to ~/.ssh/known_hosts`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  function openAdd() {
    addOpen = true;
    addText = "";
  }

  // Read a chosen file's text (avoids a multipart upload some browsers block).
  async function pickFile(ev: Event) {
    const f = (ev.target as HTMLInputElement).files?.[0] ?? null;
    if (!f) return;
    try {
      addText = (addText ? addText + "\n" : "") + (await f.text());
    } catch {
      notify("error", "Could not read the file — paste the entries instead");
    }
  }

  async function doAdd() {
    if (!addText.trim()) {
      notify("error", "Paste one or more known_hosts lines");
      return;
    }
    busy = true;
    try {
      const r = await api.post<{ added: number; parsed: number }>("/api/known-hosts/add", { data: addText });
      await refresh();
      notify("success", `Added ${r.added} new of ${r.parsed} entries`);
      addOpen = false;
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  async function doScan() {
    if (!scanHost.trim()) {
      notify("error", "Enter a hostname to scan");
      return;
    }
    busy = true;
    try {
      const r = await api.post<{ added: number; parsed: number }>("/api/known-hosts/scan", {
        host: scanHost.trim(),
        port: Number(scanPort) || 22,
      });
      await refresh();
      notify("success", `Scanned ${scanHost.trim()}: added ${r.added} of ${r.parsed} keys`);
      scanOpen = false;
      scanHost = "";
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  async function exportToSSH(kh: KnownHost) {
    try {
      await api.post(`/api/known-hosts/${kh.id}/ssh-export`);
      await refresh();
      notify("success", "Exported to ~/.ssh/known_hosts");
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function removeFromSSH(kh: KnownHost) {
    if (!confirm(`Delete the ~/.ssh/known_hosts entry for "${kh.hosts}"? The webssh copy is kept.`))
      return;
    try {
      await api.post(`/api/known-hosts/${kh.id}/ssh-remove`);
      await refresh();
      notify("success", "Removed from ~/.ssh/known_hosts");
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function forget(kh: KnownHost) {
    if (!confirm(`Forget known-host entry for "${kh.hosts}"?`)) return;
    try {
      await api.del(`/api/known-hosts/${kh.id}`);
      await refresh();
    } catch (e) {
      notify("error", String(e));
    }
  }
</script>

<div class="row" style="margin-bottom:14px">
  <h2 style="margin:0;font-size:16px">Known hosts</h2>
  <div class="spacer"></div>
  <button onclick={importFromSSH} disabled={busy}>Import from ~/.ssh</button>
  <button onclick={openAdd}>Add / paste…</button>
  <button onclick={() => (scanOpen = true)}>Scan host…</button>
  <button class="primary" onclick={exportAll} disabled={busy || app.known_hosts.length === 0}>
    Export all to ~/.ssh
  </button>
</div>

{#if app.known_hosts.length === 0}
  <p class="muted">
    No entries yet. Import them from <span class="mono">~/.ssh/known_hosts</span>, paste lines, or
    scan a host.
  </p>
{/if}

<div class="list">
  {#each app.known_hosts as kh (kh.id)}
    <div class="card">
      <div class="row">
        <strong class="hosts" title={kh.hosts}>{kh.hosts}</strong>
        <span class="tag">{kh.key_type}</span>
        {#if kh.marker}<span class="tag warn-tag">{kh.marker}</span>{/if}
        {#if kh.in_ssh}<span class="muted" title="present in ~/.ssh/known_hosts">✓ ~/.ssh</span>{/if}
        <div class="spacer"></div>
        {#if kh.in_ssh}
          <button class="sm danger ghost" onclick={() => removeFromSSH(kh)}>Delete from ~/.ssh</button>
        {:else}
          <button class="sm ghost" onclick={() => exportToSSH(kh)}>Export to ~/.ssh</button>
        {/if}
        <button class="sm danger ghost" onclick={() => forget(kh)}>Forget</button>
      </div>
      <div class="mono fp" title={kh.fingerprint}>{kh.fingerprint}</div>
      {#if kh.comment}<div class="muted" style="font-size:12px">{kh.comment}</div>{/if}
    </div>
  {/each}
</div>

{#if addOpen}
  <div class="overlay" onclick={() => (addOpen = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Add known-host entries</h2>
      <div class="field">
        <div class="row" style="justify-content:space-between">
          <label for="khtext" style="margin:0">Paste known_hosts lines</label>
          <button class="sm ghost" onclick={() => addInput.click()}>Choose file…</button>
        </div>
        <textarea id="khtext" rows="6" class="mono" bind:value={addText}
          placeholder="host.example.com ssh-ed25519 AAAA…&#10;[10.0.0.5]:2222 ssh-rsa AAAA…"></textarea>
        <input type="file" bind:this={addInput} onchange={pickFile} style="display:none" />
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (addOpen = false)}>Cancel</button>
        <button class="primary" onclick={doAdd} disabled={busy}>Add</button>
      </div>
    </div>
  </div>
{/if}

{#if scanOpen}
  <div class="overlay" onclick={() => (scanOpen = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Scan host keys</h2>
      <p class="muted" style="margin-top:0">
        Runs <span class="mono">ssh-keyscan</span> and stores the host's public keys.
      </p>
      <div class="row" style="gap:12px">
        <div class="field" style="flex:2">
          <label for="shost">Host</label>
          <input id="shost" bind:value={scanHost} placeholder="example.com or 10.0.0.5" />
        </div>
        <div class="field" style="flex:1">
          <label for="sport">Port</label>
          <input id="sport" type="number" bind:value={scanPort} />
        </div>
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (scanOpen = false)}>Cancel</button>
        <button class="primary" onclick={doScan} disabled={busy}>{busy ? "Scanning…" : "Scan"}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .list { display: flex; flex-direction: column; gap: 10px; }
  .card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 12px 14px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .hosts { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 60%; }
  .fp { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .warn-tag { background: color-mix(in srgb, var(--danger) 20%, transparent); }
</style>
