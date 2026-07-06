<script lang="ts">
  import { api } from "./api";
  import { app, refresh, notify } from "./store.svelte";
  import type { Host, Group, Key } from "./types";

  let { host, groups, onclose }: { host: Host; groups: Group[]; onclose: () => void } =
    $props();

  // Local editable copy.
  let form = $state<Host>({ ...host, extra_options: { ...(host.extra_options ?? {}) } });
  let tagsText = $state(host.tags.join(", "));
  let passwordInput = $state("");
  let clearPassword = $state(false);
  let extraText = $state(
    Object.entries(host.extra_options ?? {})
      .map(([k, v]) => `${k} ${v}`)
      .join("\n"),
  );
  let saving = $state(false);

  // currentId flips from 0 once a new host is persisted, so a later Save updates
  // (PUT) instead of re-creating (POST) and hitting the unique-alias constraint.
  let currentId = $state(host.id);
  const isNew = $derived(currentId === 0);

  // ---- IdentityFile as a dropdown of known keys ----
  const CUSTOM = "__custom__";
  let identSel = $state("");
  let customPath = $state("");
  // Initialise selection from the host's existing IdentityFile path.
  {
    const known = app.keys.find((k) => k.private_path === host.identity_file);
    if (host.identity_file) {
      if (known) identSel = known.private_path;
      else {
        identSel = CUSTOM;
        customPath = host.identity_file;
      }
    }
  }
  function onIdentChange() {
    form.identity_file = identSel === CUSTOM ? customPath : identSel;
  }
  // The key that would be deployed: the known key whose file matches IdentityFile.
  const deployKey = $derived<Key | null>(
    app.keys.find((k) => k.private_path === form.identity_file) ?? null,
  );

  // ---- Deploy this key to this host ----
  let deployOpen = $state(false);
  let deployPw = $state("");
  let deploying = $state(false);
  let deployMsg = $state<{ ok: boolean; text: string } | null>(null);

  function parseExtra(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const line of text.split("\n")) {
      const t = line.trim();
      if (!t) continue;
      const i = t.search(/\s/);
      if (i < 0) out[t] = "";
      else out[t.slice(0, i)] = t.slice(i + 1).trim();
    }
    return out;
  }

  // persist saves the current form (create or update) and returns the host id.
  async function persist(): Promise<number> {
    const payload: Host = {
      ...form,
      alias: form.alias.trim(),
      port: Number(form.port) || 22,
      tags: tagsText
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean),
      extra_options: parseExtra(extraText),
      password: passwordInput || undefined,
      clear_password: clearPassword || undefined,
    };
    if (isNew) {
      const created = await api.post<Host>("/api/hosts", payload);
      currentId = created.id;
    } else {
      await api.put(`/api/hosts/${currentId}`, payload);
    }
    return currentId;
  }

  async function save() {
    if (!form.alias.trim()) {
      notify("error", "Alias is required");
      return;
    }
    saving = true;
    try {
      const created = isNew;
      await persist();
      await refresh();
      notify("success", created ? "Host created" : "Host saved");
      onclose();
    } catch (e) {
      notify("error", String(e));
    } finally {
      saving = false;
    }
  }

  // deploy saves the host (so the key lands on it with the current settings),
  // then appends the selected key's public part to the host's authorized_keys.
  async function doDeploy() {
    if (!deployKey) return;
    if (!form.alias.trim()) {
      notify("error", "Alias is required");
      return;
    }
    deploying = true;
    deployMsg = null;
    try {
      const id = await persist();
      await refresh();
      const res = await api.post<{ results: { ok: boolean; error?: string }[] }>(
        `/api/keys/${deployKey.id}/deploy`,
        { host_ids: [id], password: deployPw },
      );
      const r = res.results[0];
      if (r?.ok) {
        deployMsg = { ok: true, text: `Deployed ${deployKey.name} to ${form.alias.trim()}` };
        notify("success", deployMsg.text);
        deployPw = "";
      } else {
        deployMsg = { ok: false, text: r?.error || "deploy failed" };
        notify("error", deployMsg.text);
      }
      await refresh();
    } catch (e) {
      deployMsg = { ok: false, text: String(e) };
      notify("error", String(e));
    } finally {
      deploying = false;
    }
  }
</script>

<div class="overlay" onclick={onclose}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal" onclick={(e) => e.stopPropagation()}>
    <h2>{isNew ? "New host" : `Edit ${host.alias}`}</h2>

    <div class="field">
      <label for="alias">Alias (ssh_config Host) *</label>
      <input id="alias" bind:value={form.alias} placeholder="prod-web-1" />
    </div>

    <div class="row" style="gap:12px">
      <div class="field" style="flex:2">
        <label for="hostname">HostName</label>
        <input id="hostname" bind:value={form.hostname} placeholder="10.0.0.5 or example.com" />
      </div>
      <div class="field" style="flex:1">
        <label for="port">Port</label>
        <input id="port" type="number" bind:value={form.port} />
      </div>
    </div>

    <div class="row" style="gap:12px">
      <div class="field" style="flex:1">
        <label for="user">User</label>
        <input id="user" bind:value={form.user} placeholder="root" />
      </div>
      <div class="field" style="flex:1">
        <label for="group">Group</label>
        <select id="group" bind:value={form.group_id}>
          <option value={null}>— none —</option>
          {#each groups as g}
            <option value={g.id}>{g.name}</option>
          {/each}
        </select>
      </div>
    </div>

    <div class="field">
      <label for="identity">IdentityFile</label>
      <div class="row" style="gap:8px">
        <select id="identity" bind:value={identSel} onchange={onIdentChange} style="flex:1">
          <option value="">— none —</option>
          {#each app.keys as k (k.id)}
            <option value={k.private_path}>{k.name} ({k.type})</option>
          {/each}
          <option value={CUSTOM}>Custom path…</option>
        </select>
        <button onclick={() => (deployOpen = !deployOpen)} disabled={!deployKey}
          title={deployKey ? "Deploy this key to this host" : "Select a known key to enable deploy"}>
          Deploy…
        </button>
      </div>
      {#if identSel === CUSTOM}
        <input class="mono" style="margin-top:6px" bind:value={customPath}
          oninput={() => (form.identity_file = customPath)} placeholder="~/.ssh/id_ed25519" />
      {/if}
      {#if app.keys.length === 0}
        <div class="muted" style="font-size:12px;margin-top:4px">
          No known keys yet — generate or import one in the Keys tab.
        </div>
      {/if}

      {#if deployOpen && deployKey}
        <div class="deploybox">
          <div class="muted" style="font-size:12px">
            Deploy <strong>{deployKey.name}</strong> to
            <span class="mono">{form.user || "user"}@{form.hostname || form.alias || "host"}</span>.
            Appends its public key to the host's <span class="mono">authorized_keys</span>. The host is
            saved first and its <strong>saved password</strong> is used — enter one below only if the host
            has none (used once, never stored).
          </div>
          <div class="row" style="gap:8px;margin-top:8px">
            <input type="password" bind:value={deployPw}
              placeholder={host.has_password ? "using saved password" : "SSH password"} style="flex:1" />
            <button class="primary" onclick={doDeploy} disabled={deploying}>
              {deploying ? "Deploying…" : "Deploy now"}
            </button>
          </div>
          {#if deployMsg}
            <div class={deployMsg.ok ? "okmsg" : "failmsg"}>
              {deployMsg.ok ? "✓" : "✗"} {deployMsg.text}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <div class="field">
      <label for="proxy">ProxyJump (bastion)</label>
      <input id="proxy" bind:value={form.proxy_jump} placeholder="bastion-host" />
    </div>

    <div class="field">
      <label for="pw">Password {#if host.has_password}<span class="tag">saved</span>{/if}</label>
      <input id="pw" type="password" bind:value={passwordInput} disabled={clearPassword}
        placeholder={host.has_password ? "•••••• — leave blank to keep" : "optional"} />
      <div class="muted" style="font-size:12px;margin-top:4px">
        Used for sshfs mount, SFTP and the web terminal when no key works. Stored locally
        (DB is user-only) and never sent back to the browser.
      </div>
      {#if host.has_password}
        <label style="margin-top:6px;display:flex;align-items:center;gap:6px">
          <input type="checkbox" bind:checked={clearPassword} style="width:auto" />
          Clear saved password
        </label>
      {/if}
    </div>

    <div class="field">
      <label for="tags">Tags (comma separated)</label>
      <input id="tags" bind:value={tagsText} placeholder="prod, web, client-acme" />
    </div>

    <div class="field">
      <label for="extra">Extra ssh options (one per line: Keyword value)</label>
      <textarea id="extra" rows="3" bind:value={extraText} class="mono"
        placeholder="ForwardAgent yes&#10;ServerAliveInterval 30"></textarea>
    </div>

    <div class="field">
      <label for="notes">Notes</label>
      <textarea id="notes" rows="2" bind:value={form.notes}></textarea>
    </div>

    <div class="row">
      <div class="spacer"></div>
      <button onclick={onclose}>Cancel</button>
      <button class="primary" onclick={save} disabled={saving}>
        {saving ? "Saving…" : "Save"}
      </button>
    </div>
  </div>
</div>

<style>
  .deploybox {
    margin-top: 8px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 12px;
  }
  .okmsg { color: var(--success); font-size: 12.5px; margin-top: 6px; }
  .failmsg { color: var(--danger); font-size: 12.5px; margin-top: 6px; }
</style>
