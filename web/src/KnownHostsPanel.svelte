<script lang="ts">
  import { api } from "./api";
  import Modal from "./Modal.svelte";
  import { app, refresh, notify } from "./store.svelte";
  import type { KnownHost, KnownHostMatch } from "./types";

  // ssh's HashKnownHosts writes "|1|salt|hash" instead of the host name.
  const isHashed = (kh: KnownHost) => kh.hosts.startsWith("|1|");
  // A hash says nothing to a person: name the entry by its matched host.
  const label = (kh: KnownHost) =>
    kh.matches?.length
      ? kh.matches.map((m) => m.alias).join(", ")
      : isHashed(kh)
        ? "a hashed host not in inventory"
        : kh.hosts;

  // known_hosts holds one line per host key type, so a server with ECDSA, RSA
  // and Ed25519 keys has three lines — deleting one left ssh trusting the
  // others. Lines are grouped per machine: by the inventory hosts they match,
  // or by their (plain) host field. Hashed lines no host matches carry a
  // different salt each and cannot be told apart, so they stay one per card.
  interface HostGroup {
    key: string;
    entries: KnownHost[];
    matches: KnownHostMatch[];
  }
  function groupKey(kh: KnownHost): string {
    if (kh.matches?.length) return "h:" + kh.matches.map((m) => m.host_id).sort((a, b) => a - b).join(",");
    if (!isHashed(kh)) return "p:" + kh.hosts;
    return "e:" + kh.id;
  }
  const groups = $derived.by(() => {
    const by = new Map<string, HostGroup>();
    for (const kh of app.known_hosts) {
      const k = groupKey(kh);
      let g = by.get(k);
      if (!g) by.set(k, (g = { key: k, entries: [], matches: [] }));
      g.entries.push(kh);
      for (const m of kh.matches ?? [])
        if (!g.matches.some((x) => x.host_id === m.host_id && x.name === m.name)) g.matches.push(m);
    }
    return [...by.values()];
  });
  const groupLabel = (g: HostGroup) => label(g.entries[0]);

  // Groups are split by whether an inventory host owns them. The rest are
  // still real machines ssh talks to (github.com, servers reached by another
  // name), so they get their own tab rather than looking like leftovers.
  type Tab = "inventory" | "other";
  let tab = $state<Tab>("inventory");
  const inInventory = $derived(groups.filter((g) => g.matches.length));
  const notInInventory = $derived(groups.filter((g) => !g.matches.length));
  const shown = $derived(tab === "inventory" ? inInventory : notInInventory);
  const stored = $derived(app.known_hosts.filter((kh) => kh.in_db));

  let busy = $state(false);

  // Add / paste modal.
  let addOpen = $state(false);
  let addText = $state("");
  let addInput = $state<HTMLInputElement>();

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

  // Group actions apply to every key line of the machine.
  async function each(g: HostGroup, pick: (kh: KnownHost) => boolean, run: (kh: KnownHost) => Promise<unknown>) {
    busy = true;
    try {
      for (const kh of g.entries.filter(pick)) await run(kh);
    } finally {
      await refresh();
      busy = false;
    }
  }

  async function exportToSSH(g: HostGroup) {
    try {
      await each(g, (kh) => !!kh.in_db && !kh.in_ssh, (kh) => api.post(`/api/known-hosts/${kh.id}/ssh-export`));
      notify("success", `Exported ${groupLabel(g)} to ~/.ssh/known_hosts`);
    } catch (e) {
      notify("error", String(e));
    }
  }

  // Lines that exist only in ~/.ssh/known_hosts: store them in webssh.
  async function addToDB(g: HostGroup) {
    const lines = g.entries.filter((kh) => !kh.in_db && kh.line).map((kh) => kh.line);
    try {
      busy = true;
      await api.post("/api/known-hosts/add", { data: lines.join("\n") });
      notify("success", `Added ${lines.length} key(s) of ${groupLabel(g)} to webssh`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      await refresh();
      busy = false;
    }
  }

  async function removeFromSSH(g: HostGroup) {
    const inSSH = g.entries.filter((kh) => kh.in_ssh);
    const kept = inSSH.every((kh) => kh.in_db)
      ? "The webssh copies are kept."
      : "webssh has no copy of some of them, so ssh will ask you to trust this machine again.";
    if (!confirm(`Delete all ${inSSH.length} key(s) of "${groupLabel(g)}" from ~/.ssh/known_hosts? ${kept}`)) return;
    try {
      await each(
        g,
        (kh) => !!kh.in_ssh,
        (kh) =>
          kh.in_db
            ? api.post(`/api/known-hosts/${kh.id}/ssh-remove`)
            : api.post("/api/known-hosts/ssh-remove-line", { line: kh.line }),
      );
      notify("success", `Removed ${groupLabel(g)} from ~/.ssh/known_hosts`);
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function forget(g: HostGroup) {
    const n = g.entries.filter((kh) => kh.in_db).length;
    if (!confirm(`Forget all ${n} stored key(s) of "${groupLabel(g)}"? ~/.ssh is not touched.`)) return;
    try {
      await each(g, (kh) => !!kh.in_db, (kh) => api.del(`/api/known-hosts/${kh.id}`));
    } catch (e) {
      notify("error", String(e));
    }
  }
</script>

<div class="row toolbar" style="margin-bottom:14px">
  <h2 style="margin:0;font-size:16px">Known hosts</h2>
  <div class="spacer"></div>
  <button onclick={importFromSSH} disabled={busy}>Import from ~/.ssh</button>
  <button onclick={openAdd}>Add / paste…</button>
  <button onclick={() => (scanOpen = true)}>Scan host…</button>
  <button class="primary" onclick={exportAll} disabled={busy || stored.length === 0}>
    Export all to ~/.ssh
  </button>
</div>

{#if app.known_hosts.length === 0}
  <p class="muted">
    No entries yet. Import them from <span class="mono">~/.ssh/known_hosts</span>, paste lines, or
    scan a host.
  </p>
{:else}
  <div class="tabs" role="tablist" aria-label="Known hosts">
    <button
      role="tab"
      id="kh-tab-inventory"
      aria-selected={tab === "inventory"}
      aria-controls="kh-panel"
      class:active={tab === "inventory"}
      onclick={() => (tab = "inventory")}>In inventory <span class="count">{inInventory.length}</span></button
    >
    <button
      role="tab"
      id="kh-tab-other"
      aria-selected={tab === "other"}
      aria-controls="kh-panel"
      class:active={tab === "other"}
      onclick={() => (tab = "other")}>Not in inventory <span class="count">{notInInventory.length}</span></button
    >
  </div>
  {#if tab === "other"}
    <p class="muted note">
      Keys of machines that no inventory host matches — services like github.com, or servers you
      reach by a name or address the inventory does not list. ssh still relies on them: deleting one
      from <span class="mono">~/.ssh</span> makes ssh ask you to trust that machine again.
    </p>
  {/if}
  {#if shown.length === 0}
    <p class="muted">
      {tab === "inventory"
        ? "No entry matches a host in your inventory yet."
        : "Every entry belongs to a host in your inventory."}
    </p>
  {/if}
{/if}

<div class="list" id="kh-panel" role="tabpanel" aria-labelledby={tab === "inventory" ? "kh-tab-inventory" : "kh-tab-other"}>
  {#each shown as g (g.key)}
    {@const first = g.entries[0]}
    {@const anyHashed = g.entries.some(isHashed)}
    {@const allInSSH = g.entries.every((kh) => kh.in_ssh)}
    {@const noneInDB = g.entries.every((kh) => !kh.in_db)}
    <div class="card">
      <div class="row hostrow">
        {#if g.matches.length}
          <strong class="hosts" title={g.entries.map((kh) => kh.hosts).join("\n")}>
            {[...new Set(g.matches.map((m) => m.alias))].join(", ")}
            <span class="muted mono name">{[...new Set(g.matches.map((m) => m.name))].join(", ")}</span>
          </strong>
        {:else if isHashed(first)}
          <strong class="hosts muted" title={first.hosts}>not in inventory</strong>
        {:else}
          <strong class="hosts" title={first.hosts}>{first.hosts}</strong>
        {/if}
        {#if anyHashed}
          <span
            class="tag"
            title="ssh stored only a hash of the host name (HashKnownHosts). It is matched against the hosts in your inventory; an entry no inventory host matches belongs to a machine that is not in your inventory."
            >hashed</span
          >
        {/if}
        <span class="tag" title="ssh keeps one known_hosts line per host key type">
          {g.entries.length} key{g.entries.length === 1 ? "" : "s"}
        </span>
        {#if g.entries.some((kh) => kh.marker)}<span class="tag warn-tag">{first.marker || "@marker"}</span>{/if}
        {#if noneInDB}
          <span class="tag" title="present in ~/.ssh/known_hosts but not stored in webssh">only in ~/.ssh</span>
        {:else if allInSSH}
          <span class="muted" title="every key is in ~/.ssh/known_hosts">✓ ~/.ssh</span>
        {:else if g.entries.some((kh) => kh.in_ssh)}
          <span class="muted" title="some keys are in ~/.ssh/known_hosts">partly in ~/.ssh</span>
        {/if}
        <div class="spacer"></div>
        {#if g.entries.some((kh) => !kh.in_db)}
          <button class="sm ghost" disabled={busy} onclick={() => addToDB(g)}>Add to webssh</button>
        {/if}
        {#if g.entries.some((kh) => kh.in_db && !kh.in_ssh)}
          <button class="sm ghost" disabled={busy} onclick={() => exportToSSH(g)}>Export to ~/.ssh</button>
        {/if}
        {#if g.entries.some((kh) => kh.in_ssh)}
          <button class="sm danger ghost" disabled={busy} onclick={() => removeFromSSH(g)}>Delete from ~/.ssh</button>
        {/if}
        {#if g.entries.some((kh) => kh.in_db)}
          <button class="sm danger ghost" disabled={busy} onclick={() => forget(g)}>Forget</button>
        {/if}
      </div>
      {#each g.entries as kh (kh.id)}
        <div class="mono fp" title={kh.fingerprint}>
          <span class="ktype">{kh.key_type}</span>
          {kh.fingerprint}
          {#if !kh.in_db}<span class="muted"> · only in ~/.ssh</span>{:else if !kh.in_ssh}<span class="muted"> · not in ~/.ssh</span>{/if}
        </div>
      {/each}
      {#if g.entries.some((kh) => kh.comment)}
        <div class="muted" style="font-size:12px">{g.entries.map((kh) => kh.comment).filter(Boolean).join(" · ")}</div>
      {/if}
    </div>
  {/each}
</div>

{#if addOpen}
  <Modal titleId="known-host-add-title" onclose={() => (addOpen = false)}>
      <h2 id="known-host-add-title">Add known-host entries</h2>
      <div class="field">
        <div class="row" style="justify-content:space-between">
          <label for="khtext" style="margin:0">Paste known_hosts lines</label>
          <button class="sm ghost" onclick={() => addInput?.click()}>Choose file…</button>
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
  </Modal>
{/if}

{#if scanOpen}
  <Modal titleId="known-host-scan-title" onclose={() => (scanOpen = false)}>
      <h2 id="known-host-scan-title">Scan host keys</h2>
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
  </Modal>
{/if}

<style>
  /* Narrow screens can't fit the toolbar or a host row's actions on one
     line; let both wrap instead of squeezing every button's own label
     into multiple lines. */
  .toolbar { flex-wrap: wrap; row-gap: 8px; }
  .hostrow { flex-wrap: wrap; row-gap: 6px; }
  .hostrow button { white-space: nowrap; }
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
  .hosts .name { font-weight: normal; font-size: 12px; margin-left: 6px; }
  .ktype { display: inline-block; min-width: 150px; color: var(--text); }
  .fp { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tabs { display: flex; gap: 4px; margin-bottom: 12px; border-bottom: 1px solid var(--border); }
  .tabs button {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    color: var(--muted);
    padding: 6px 10px;
    margin-bottom: -1px;
  }
  .tabs button.active { color: var(--text); border-bottom-color: var(--accent); }
  .tabs .count { font-size: 12px; color: var(--muted); margin-left: 4px; }
  .note { font-size: 12px; margin: 0 0 12px; }
  .warn-tag { background: color-mix(in srgb, var(--danger) 20%, transparent); }
</style>
