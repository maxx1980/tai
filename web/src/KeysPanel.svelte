<script lang="ts">
  import { api, importKey, exportKey, ApiError } from "./api";
  import { app, refresh, notify } from "./store.svelte";
  import type { Key } from "./types";

  let genOpen = $state(false);
  let gen = $state({ name: "", type: "ed25519", passphrase: "", comment: "" });
  let genConflict = $state<{ orphan: boolean } | null>(null);
  let busy = $state(false);

  // Import state.
  let importOpen = $state(false);
  let impName = $state("");
  let privText = $state("");
  let pubText = $state("");
  let overwriteWarn = $state(false);
  let privInput: HTMLInputElement;
  let pubInput: HTMLInputElement;

  function openImport() {
    importOpen = true;
    impName = "";
    privText = "";
    pubText = "";
    overwriteWarn = false;
  }

  // Reading the chosen file's text avoids a multipart upload (which some locked-down
  // browsers block); the user can also just paste the key.
  async function pickFile(ev: Event, target: "priv" | "pub") {
    const f = (ev.target as HTMLInputElement).files?.[0] ?? null;
    if (!f) return;
    try {
      const text = await f.text();
      if (target === "priv") {
        privText = text;
        if (!impName) impName = f.name.replace(/\.pub$/, "");
      } else {
        pubText = text;
      }
      overwriteWarn = false;
    } catch {
      notify("error", "Could not read the file — paste the key contents instead");
    }
  }

  async function doImport(overwrite: boolean) {
    if (!impName.trim()) {
      notify("error", "Key name required");
      return;
    }
    if (!privText.trim()) {
      notify("error", "Provide the private key (choose a file or paste it)");
      return;
    }
    busy = true;
    try {
      await importKey(impName.trim(), privText, pubText, overwrite);
      await refresh();
      notify("success", `Imported key ${impName.trim()}`);
      importOpen = false;
    } catch (e) {
      if (e instanceof ApiError && e.data?.exists) {
        overwriteWarn = true; // ask the user to confirm replacing
      } else {
        notify("error", String(e));
      }
    } finally {
      busy = false;
    }
  }

  async function exportOne(k: Key, kind: "public" | "private") {
    try {
      const name = await exportKey(k.id, kind);
      // The app window has no download bar, so without this the click looks
      // like it did nothing. Downloaded files are also world-readable.
      notify(
        "success",
        kind === "private"
          ? `Saved ${name} to your downloads folder — chmod 600 it, downloads are world-readable`
          : `Saved ${name} to your downloads folder`,
      );
    } catch (e) {
      notify("error", String(e));
    }
  }

  let deployKey = $state<Key | null>(null);
  let selectedHosts = $state<Set<number>>(new Set());
  let collapsed = $state<Set<number>>(new Set()); // collapsed group ids in the picker
  let password = $state("");
  let deployResults = $state<{ alias?: string; ok: boolean; error?: string }[] | null>(null);

  // ---- deploy host-picker tree (by group) ----
  function childGroups(parentId: number | null) {
    return app.groups
      .filter((g) => (g.parent_id ?? null) === parentId)
      .sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name));
  }
  function directHosts(groupId: number | null) {
    return app.hosts
      .filter((h) => (h.group_id ?? null) === groupId)
      .sort((a, b) => a.alias.localeCompare(b.alias));
  }
  // All host ids under a group, including nested subgroups.
  function groupHostIds(groupId: number): number[] {
    const ids = directHosts(groupId).map((h) => h.id);
    for (const g of childGroups(groupId)) ids.push(...groupHostIds(g.id));
    return ids;
  }
  // Tri-state of a group's checkbox: "none" | "some" | "all".
  function groupState(groupId: number): "none" | "some" | "all" {
    const ids = groupHostIds(groupId);
    if (ids.length === 0) return "none";
    const sel = ids.filter((id) => selectedHosts.has(id)).length;
    return sel === 0 ? "none" : sel === ids.length ? "all" : "some";
  }
  function toggleGroup(groupId: number) {
    const ids = groupHostIds(groupId);
    const next = new Set(selectedHosts);
    const allSel = ids.length > 0 && ids.every((id) => next.has(id));
    for (const id of ids) allSel ? next.delete(id) : next.add(id);
    selectedHosts = next;
  }
  function toggleCollapse(id: number) {
    const next = new Set(collapsed);
    next.has(id) ? next.delete(id) : next.add(id);
    collapsed = next;
  }
  function selectAll() {
    selectedHosts = new Set(app.hosts.map((h) => h.id));
  }
  function selectNone() {
    selectedHosts = new Set();
  }

  // ---- bulk "set credentials" (login / password / identity) ----
  let credKey = $state<Key | null>(null);
  let credUser = $state("");
  let credPassword = $state("");
  let credSetKey = $state(true);
  let credResults = $state<{ alias?: string; ok: boolean; error?: string }[] | null>(null);

  function openCred(k: Key) {
    credKey = k;
    selectedHosts = new Set();
    collapsed = new Set();
    credUser = "";
    credPassword = "";
    credSetKey = true;
    credResults = null;
  }

  async function doSetCredentials() {
    if (!credKey || selectedHosts.size === 0) return;
    if (!credUser.trim() && !credPassword && !credSetKey) {
      notify("error", "Nothing to apply — set a login, password, or the key");
      return;
    }
    busy = true;
    credResults = null;
    const results: { alias?: string; ok: boolean; error?: string }[] = [];
    for (const id of selectedHosts) {
      const host = app.hosts.find((h) => h.id === id);
      if (!host) continue;
      // Send the full host with only the credential fields overridden, so the
      // rest of its config (hostname, port, tags, extra options) is preserved.
      const payload: any = { ...host };
      if (credUser.trim()) payload.user = credUser.trim();
      if (credSetKey && credKey) payload.identity_file = credKey.private_path;
      if (credPassword) payload.password = credPassword;
      try {
        await api.put(`/api/hosts/${id}`, payload);
        results.push({ alias: host.alias, ok: true });
      } catch (e) {
        results.push({ alias: host.alias, ok: false, error: String(e) });
      }
    }
    credResults = results;
    await refresh();
    const ok = results.filter((r) => r.ok).length;
    notify(ok > 0 ? "success" : "error", `Updated ${ok}/${results.length} host(s)`);
    busy = false;
  }

  async function generate(overwrite: boolean) {
    if (!gen.name.trim()) {
      notify("error", "Key name required");
      return;
    }
    busy = true;
    try {
      await api.post("/api/keys/generate", { ...gen, overwrite });
      await refresh();
      notify("success", `Key ${gen.name} generated`);
      genOpen = false;
      genConflict = null;
      gen = { name: "", type: "ed25519", passphrase: "", comment: "" };
    } catch (e) {
      if (e instanceof ApiError && e.data?.exists) {
        genConflict = { orphan: !!e.data.orphan }; // ask before replacing
      } else {
        notify("error", String(e));
      }
    } finally {
      busy = false;
    }
  }

  // Deleting: the record always goes, the files only if asked. Leaving them
  // behind keeps the name unusable for a new key, so the choice is explicit.
  let delKey = $state<Key | null>(null);
  let delFiles = $state(false);

  function openDelete(k: Key) {
    delKey = k;
    delFiles = false;
  }

  async function doDelete() {
    if (!delKey) return;
    const k = delKey;
    busy = true;
    try {
      await api.del(`/api/keys/${k.id}${delFiles ? "?files=1" : ""}`);
      await refresh();
      notify(
        "success",
        delFiles ? `Deleted key ${k.name} and its files` : `Forgot key ${k.name} (files kept on disk)`,
      );
      delKey = null;
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  // Copy a managed key into ~/.ssh so plain `ssh` can use it.
  async function exportToSSH(k: Key) {
    try {
      await api.post(`/api/keys/${k.id}/ssh-export`);
      await refresh();
      notify("success", `Exported ${k.name} to ~/.ssh`);
    } catch (e) {
      notify("error", String(e));
    }
  }

  // Delete the ~/.ssh copy so only the webssh-managed copy is used.
  async function removeFromSSH(k: Key) {
    if (!confirm(`Delete the ~/.ssh copy of "${k.name}"? The webssh copy is kept.`)) return;
    try {
      await api.post(`/api/keys/${k.id}/ssh-remove`);
      await refresh();
      notify("success", `Removed ${k.name} from ~/.ssh`);
    } catch (e) {
      notify("error", String(e));
    }
  }

  function openDeploy(k: Key) {
    deployKey = k;
    selectedHosts = new Set();
    collapsed = new Set();
    password = "";
    deployResults = null;
  }

  function toggleHost(id: number) {
    const next = new Set(selectedHosts);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selectedHosts = next;
  }

  async function doDeploy() {
    if (!deployKey || selectedHosts.size === 0) return;
    busy = true;
    deployResults = null;
    try {
      const res = await api.post<{ results: any[] }>(`/api/keys/${deployKey.id}/deploy`, {
        host_ids: [...selectedHosts],
        password,
      });
      deployResults = res.results;
      await refresh();
      const ok = res.results.filter((r) => r.ok).length;
      notify(ok > 0 ? "success" : "error", `Deployed to ${ok}/${res.results.length} host(s)`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  function aliasOf(id: number): string {
    return app.hosts.find((h) => h.id === id)?.alias ?? String(id);
  }
</script>

<div class="row" style="margin-bottom:14px">
  <h2 style="margin:0;font-size:16px">SSH Keys</h2>
  <div class="spacer"></div>
  <button onclick={openImport}>Import key</button>
  <button class="primary" onclick={() => { genOpen = true; genConflict = null; }}>Generate key</button>
</div>

{#if app.keys.length === 0}
  <p class="muted">No keys yet. Generate one, then deploy it to hosts for passwordless access.</p>
{/if}

<div class="list">
  {#each app.keys as k (k.id)}
    <div class="card">
      <div class="row">
        <strong>{k.name}</strong>
        <span class="tag">{k.type}</span>
        {#if k.has_passphrase}<span class="muted" title="passphrase-protected">🔒</span>{/if}
        <div class="spacer"></div>
        <button class="sm ghost" title="Download public key" onclick={() => exportOne(k, "public")}>⬇ pub</button>
        <button class="sm ghost" title="Download private key" onclick={() => exportOne(k, "private")}>⬇ priv</button>
        {#if k.in_ssh}
          <button class="sm danger ghost" title={k.ssh_path ? `Delete ${k.ssh_path}` : "Delete the ~/.ssh copy"}
            onclick={() => removeFromSSH(k)}>Delete from ~/.ssh</button>
        {:else}
          <button class="sm ghost" title="Copy this key into ~/.ssh"
            onclick={() => exportToSSH(k)}>Export to ~/.ssh</button>
        {/if}
        <button class="sm" onclick={() => openDeploy(k)}>Deploy…</button>
        <button class="sm" onclick={() => openCred(k)} title="Save login/password and set this key for hosts">Set credentials…</button>
        <button class="sm danger ghost" onclick={() => openDelete(k)}>Delete…</button>
      </div>
      <div class="mono pub" title={k.public_key}>{k.public_key}</div>
      <div class="muted" style="font-size:12px">
        Deployed to {k.deployed_hosts.length} host(s){#if k.deployed_hosts.length}:
          {k.deployed_hosts.map(aliasOf).join(", ")}{/if}
      </div>
    </div>
  {/each}
</div>

{#if genOpen}
  <div class="overlay" onclick={() => (genOpen = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Generate SSH key</h2>
      <div class="field">
        <label for="kname">Name *</label>
        <input id="kname" bind:value={gen.name} placeholder="id_acme_prod"
          oninput={() => (genConflict = null)} />
      </div>
      <div class="field">
        <label for="ktype">Type</label>
        <select id="ktype" bind:value={gen.type}>
          <option value="ed25519">ed25519 (recommended)</option>
          <option value="rsa">rsa 4096</option>
          <option value="ecdsa">ecdsa</option>
        </select>
      </div>
      <div class="field">
        <label for="kpass">Passphrase (optional)</label>
        <input id="kpass" type="password" bind:value={gen.passphrase} />
      </div>
      <div class="field">
        <label for="kcomment">Comment</label>
        <input id="kcomment" bind:value={gen.comment} placeholder="me@laptop" />
      </div>
      {#if genConflict}
        <div class="warn">
          {#if genConflict.orphan}
            ⚠ Files for a key named <strong>{gen.name.trim()}</strong> are still on disk —
            left behind when it was forgotten without deleting them. Generating will
            <strong>replace</strong> those files.
          {:else}
            ⚠ A key named <strong>{gen.name.trim()}</strong> already exists. Generating will
            <strong>replace</strong> its files and record. Continue?
          {/if}
        </div>
      {/if}

      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (genOpen = false)}>Cancel</button>
        {#if genConflict}
          <button class="danger" onclick={() => generate(true)} disabled={busy}>Overwrite</button>
        {:else}
          <button class="primary" onclick={() => generate(false)} disabled={busy}>Generate</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if delKey}
  <div class="overlay" onclick={() => (delKey = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Delete key “{delKey.name}”</h2>
      <p class="muted" style="margin-top:0">
        The key is removed from webssh and from the list of keys deployed to hosts.
        Already-deployed public keys stay on the remote hosts.
      </p>
      <label class="pick" style="padding:0">
        <input type="checkbox" bind:checked={delFiles} style="width:auto" />
        <span>Also delete the key files from disk</span>
      </label>
      <div class="muted mono" style="font-size:12px;padding-left:26px">
        {delKey.private_path}<br />{delKey.private_path}.pub
      </div>
      {#if delFiles}
        <div class="warn" style="margin-top:12px">
          ⚠ The private key file is deleted for good. Download it first if you still need it.
        </div>
      {:else}
        <p class="muted" style="font-size:12px">
          Kept on disk, the files keep the name <span class="mono">{delKey.name}</span> taken
          for new keys.
        </p>
      {/if}
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (delKey = null)}>Cancel</button>
        <button class="danger" onclick={doDelete} disabled={busy}>
          {delFiles ? "Delete key and files" : "Forget key"}
        </button>
      </div>
    </div>
  </div>
{/if}

{#if importOpen}
  <div class="overlay" onclick={() => (importOpen = false)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Import SSH key</h2>
      <div class="field">
        <label for="iname">Name *</label>
        <input id="iname" bind:value={impName} placeholder="id_acme_prod"
          oninput={() => (overwriteWarn = false)} />
      </div>
      <div class="field">
        <div class="row" style="justify-content:space-between">
          <label for="ipriv" style="margin:0">Private key *</label>
          <button class="sm ghost" onclick={() => privInput.click()}>Choose file…</button>
        </div>
        <textarea id="ipriv" rows="5" class="mono" bind:value={privText}
          placeholder="Choose a file above, or paste the private key here&#10;-----BEGIN OPENSSH PRIVATE KEY-----&#10;…"
          oninput={() => (overwriteWarn = false)}></textarea>
        <input type="file" bind:this={privInput} onchange={(e) => pickFile(e, "priv")}
          style="display:none" />
      </div>
      <div class="field">
        <div class="row" style="justify-content:space-between">
          <label for="ipub" style="margin:0">Public key (.pub) — optional, derived if unencrypted</label>
          <button class="sm ghost" onclick={() => pubInput.click()}>Choose file…</button>
        </div>
        <textarea id="ipub" rows="2" class="mono" bind:value={pubText}
          placeholder="ssh-ed25519 AAAA… (optional)"></textarea>
        <input type="file" bind:this={pubInput} onchange={(e) => pickFile(e, "pub")}
          style="display:none" />
      </div>

      {#if overwriteWarn}
        <div class="warn">
          ⚠ A key named <strong>{impName.trim()}</strong> already exists. Importing will
          <strong>overwrite</strong> its files and record. Continue?
        </div>
      {/if}

      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (importOpen = false)}>Cancel</button>
        {#if overwriteWarn}
          <button class="danger" onclick={() => doImport(true)} disabled={busy}>Overwrite</button>
        {:else}
          <button class="primary" onclick={() => doImport(false)} disabled={busy}>Import</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if deployKey}
  <div class="overlay" onclick={() => (deployKey = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Deploy “{deployKey.name}”</h2>
      <p class="muted" style="margin-top:0">
        Appends the public key to <span class="mono">~/.ssh/authorized_keys</span> on the selected
        hosts. Each host's <strong>saved password</strong> is used; the fallback below applies only to
        hosts without one (used once, never stored).
      </p>
      <div class="field">
        {@render hostPicker()}
      </div>
      <div class="field">
        <label for="dpass">Fallback SSH password <span class="muted">(optional)</span></label>
        <input id="dpass" type="password" bind:value={password}
          placeholder="used only for hosts without a saved password" />
      </div>

      {#if deployResults}
        <div class="field results">
          {#each deployResults as r}
            <div class={r.ok ? "ok" : "fail"}>
              {r.ok ? "✓" : "✗"} {r.alias} {#if r.error}<span class="muted">— {r.error}</span>{/if}
            </div>
          {/each}
        </div>
      {/if}

      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (deployKey = null)}>Close</button>
        <button class="primary" onclick={doDeploy}
          disabled={busy || selectedHosts.size === 0}>
          {busy ? "Deploying…" : `Deploy to ${selectedHosts.size}`}
        </button>
      </div>
    </div>
  </div>
{/if}

{#if credKey}
  <div class="overlay" onclick={() => (credKey = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Set credentials</h2>
      <p class="muted" style="margin-top:0">
        Saves a login and/or password locally for the selected hosts, and can set
        <span class="mono">{credKey.name}</span> as their IdentityFile. Nothing is sent over
        SSH — this only updates the stored host records. Blank fields are left unchanged.
      </p>
      <div class="field">
        {@render hostPicker()}
      </div>
      <div class="row" style="gap:12px">
        <div class="field" style="flex:1">
          <label for="cuser">Login (User)</label>
          <input id="cuser" bind:value={credUser} placeholder="leave blank to keep" />
        </div>
        <div class="field" style="flex:1">
          <label for="cpass">Password</label>
          <input id="cpass" type="password" bind:value={credPassword} placeholder="leave blank to keep" />
        </div>
      </div>
      <label class="pick" style="padding:0">
        <input type="checkbox" bind:checked={credSetKey} style="width:auto" />
        <span>Set <strong>{credKey.name}</strong> as IdentityFile</span>
      </label>

      {#if credResults}
        <div class="field results">
          {#each credResults as r}
            <div class={r.ok ? "ok" : "fail"}>
              {r.ok ? "✓" : "✗"} {r.alias} {#if r.error}<span class="muted">— {r.error}</span>{/if}
            </div>
          {/each}
        </div>
      {/if}

      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (credKey = null)}>Close</button>
        <button class="primary" onclick={doSetCredentials}
          disabled={busy || selectedHosts.size === 0}>
          {busy ? "Saving…" : `Save to ${selectedHosts.size}`}
        </button>
      </div>
    </div>
  </div>
{/if}

{#snippet hostPicker()}
  <div class="row" style="justify-content:space-between;align-items:baseline">
    <label style="margin:0">Target hosts</label>
    <div class="muted" style="font-size:12px">
      {selectedHosts.size} selected ·
      <button class="linkbtn" onclick={selectAll}>All</button> ·
      <button class="linkbtn" onclick={selectNone}>None</button>
    </div>
  </div>
  <div class="scroll host-picker">
    {#each childGroups(null) as g (g.id)}
      {@render groupNode(g, 0)}
    {/each}
    {#each directHosts(null) as h (h.id)}
      {@render hostRow(h, 0)}
    {/each}
    {#if app.hosts.length === 0}
      <div class="muted" style="padding:6px">No hosts yet.</div>
    {/if}
  </div>
{/snippet}

{#snippet groupNode(group: import("./types").Group, depth: number)}
  <div class="grouprow" style="padding-left:{depth * 16}px">
    <button class="caret" title="Expand / collapse" onclick={() => toggleCollapse(group.id)}>
      {collapsed.has(group.id) ? "▸" : "▾"}
    </button>
    <input type="checkbox" style="width:auto"
      checked={groupState(group.id) === "all"}
      indeterminate={groupState(group.id) === "some"}
      disabled={groupHostIds(group.id).length === 0}
      onchange={() => toggleGroup(group.id)} />
    <button class="gname" onclick={() => toggleCollapse(group.id)}>📁 {group.name}</button>
    <span class="muted" style="font-size:12px">{groupHostIds(group.id).length}</span>
  </div>
  {#if !collapsed.has(group.id)}
    {#each childGroups(group.id) as g (g.id)}
      {@render groupNode(g, depth + 1)}
    {/each}
    {#each directHosts(group.id) as h (h.id)}
      {@render hostRow(h, depth + 1)}
    {/each}
  {/if}
{/snippet}

{#snippet hostRow(h: import("./types").Host, depth: number)}
  <label class="pick" style="padding-left:{depth * 16 + 20}px">
    <input type="checkbox" checked={selectedHosts.has(h.id)}
      onchange={() => toggleHost(h.id)} style="width:auto" />
    <span>{h.alias}</span>
    <span class="muted mono" style="font-size:12px">{h.user}@{h.hostname || h.alias}</span>
  </label>
{/snippet}

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
  .pub {
    font-size: 12px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .host-picker { max-height: 260px; border: 1px solid var(--border); border-radius: 8px; padding: 6px; }
  .pick { display: flex; align-items: center; gap: 8px; padding: 4px 6px; margin: 0; color: var(--text); }
  .pick:hover { background: var(--panel-2); border-radius: 6px; }
  .grouprow { display: flex; align-items: center; gap: 6px; padding: 4px 6px; }
  .grouprow:hover { background: var(--panel-2); border-radius: 6px; }
  .caret {
    background: none; border: none; color: var(--muted); cursor: pointer;
    padding: 0; width: 16px; font-size: 11px; line-height: 1;
  }
  .gname {
    background: none; border: none; color: var(--text); cursor: pointer;
    padding: 0; font-weight: 600; font-size: 13px;
  }
  .linkbtn {
    background: none; border: none; color: var(--accent, #4a9eff); cursor: pointer;
    padding: 0; font-size: 12px;
  }
  .linkbtn:hover { text-decoration: underline; }
  .results { display: flex; flex-direction: column; gap: 3px; font-size: 13px; }
  .results .ok { color: var(--success); }
  .results .fail { color: var(--danger); }
  .warn {
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
    border-radius: 8px;
    padding: 10px 12px;
    font-size: 13px;
    margin-bottom: 12px;
  }
</style>
