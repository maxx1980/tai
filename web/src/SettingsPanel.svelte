<script lang="ts">
  import {
    api,
    importUpload,
    setMasterPassword,
    backup,
    restore,
    resetAll,
    ApiError,
  } from "./api";
  import { app, lock, refresh, checkLock, notify } from "./store.svelte";

  let form = $state({ ...app.settings });
  let busy = $state(false);
  let importPath = $state("");
  let fileInput: HTMLInputElement;

  // ---- Security & backup ----
  type SecModal = null | "password" | "backup" | "restore" | "reset" | "disableauth";
  let modal = $state<SecModal>(null);
  let curPw = $state("");
  let newPw = $state("");
  let confirmPw = $state("");
  let opPw = $state(""); // password entered for backup/restore/reset
  let restoreData = $state("");
  let restoreName = $state("");
  let resetConfirm = $state(false);
  let restoreInput: HTMLInputElement;

  function openModal(m: SecModal) {
    modal = m;
    curPw = "";
    newPw = "";
    confirmPw = "";
    opPw = "";
    restoreData = "";
    restoreName = "";
    resetConfirm = false;
  }

  async function savePassword(remove: boolean) {
    if (!remove) {
      if (!newPw) {
        notify("error", "Enter a new password");
        return;
      }
      if (newPw !== confirmPw) {
        notify("error", "Passwords do not match");
        return;
      }
    }
    busy = true;
    try {
      await setMasterPassword(curPw, remove ? "" : newPw);
      await checkLock();
      notify("success", remove ? "Password protection removed" : "Master password saved");
      modal = null;
    } catch (e) {
      notify("error", e instanceof ApiError ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function doBackup() {
    busy = true;
    try {
      await backup(opPw);
      notify("success", "Encrypted backup downloaded");
      modal = null;
    } catch (e) {
      notify("error", e instanceof ApiError ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  // Read the backup file in-browser (JSON text), avoiding a multipart upload.
  async function pickRestoreFile(ev: Event) {
    const f = (ev.target as HTMLInputElement).files?.[0] ?? null;
    if (!f) return;
    try {
      restoreData = await f.text();
      restoreName = f.name;
    } catch {
      notify("error", "Could not read the file");
    }
  }

  async function doRestore() {
    if (!restoreData) {
      notify("error", "Choose a backup file");
      return;
    }
    busy = true;
    try {
      const r = await restore(restoreData, opPw);
      await refresh();
      await checkLock();
      notify("success", `Restored ${r.hosts} host(s) and ${r.keys} key(s)`);
      modal = null;
    } catch (e) {
      notify("error", e instanceof ApiError ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function doReset() {
    busy = true;
    try {
      await resetAll(opPw);
      await refresh();
      await checkLock();
      notify("success", "Everything was wiped");
      modal = null;
    } catch (e) {
      notify("error", e instanceof ApiError ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  // Keep the local form in sync if settings reload.
  $effect(() => {
    form = { ...app.settings };
  });

  async function save() {
    busy = true;
    try {
      await api.put("/api/settings", $state.snapshot(form));
      await refresh();
      notify("success", "Settings saved");
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  // Toggle "quit when the last tab closes". Takes effect immediately — the
  // daemon re-reads the setting when its grace timer fires.
  async function applyExitOnClose(enabled: boolean) {
    busy = true;
    try {
      await api.put("/api/settings", { exit_on_close: enabled ? "1" : "0" });
      await refresh();
      notify("success", enabled ? "Will quit when the last tab closes" : "Will keep running in the background");
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  // Toggle the API-token requirement. Disabling is gated by a warning modal
  // (see the "disableauth" modal); re-enabling is immediate.
  async function applyAuthDisabled(disabled: boolean) {
    busy = true;
    try {
      await api.put("/api/settings", { auth_disabled: disabled ? "1" : "" });
      await refresh();
      notify(disabled ? "info" : "success", disabled ? "API token disabled" : "API token now required");
      modal = null;
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
    } catch {
      // The daemon replaces its process image, so the connection drops — expected.
    }
  }

  async function importConfig(path?: string) {
    busy = true;
    try {
      const r = await api.post<{ imported: number; path?: string }>(
        "/api/import/ssh-config",
        path ? { path } : undefined,
      );
      await refresh();
      notify("success", `Imported ${r.imported} host(s) from ${r.path ?? "~/.ssh/config"}`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }

  async function onImportFile(ev: Event) {
    const file = (ev.target as HTMLInputElement).files?.[0];
    if (!file) return;
    busy = true;
    try {
      const r = await importUpload(file);
      await refresh();
      notify("success", `Imported ${r.imported} host(s) from ${file.name}`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
      fileInput.value = "";
    }
  }

  async function exportConfig() {
    busy = true;
    try {
      const r = await api.post<{ exported: number; path: string }>("/api/export/ssh-config");
      notify("success", `Exported ${r.exported} host(s) → ${r.path}`);
    } catch (e) {
      notify("error", String(e));
    } finally {
      busy = false;
    }
  }
</script>

<h2 style="margin:0 0 14px;font-size:16px">Settings</h2>

<div class="grid">
  <section>
    <h3>Applications</h3>
    <p class="muted">
      Command templates for the native launcher. Placeholders:
      <span class="mono">{"{{alias}} {{user}} {{host}} {{port}} {{mountpoint}}"}</span>
    </p>
    <div class="field">
      <label for="term">Terminal command</label>
      <input id="term" class="mono" bind:value={form.terminal_cmd} />
    </div>
    <div class="field">
      <label for="files">File manager command</label>
      <input id="files" class="mono" bind:value={form.files_cmd} />
    </div>
    <div class="field">
      <label for="browser">Browser command</label>
      <input id="browser" class="mono" bind:value={form.browser_cmd}
        placeholder="empty = system default (xdg-open)" />
      <span class="muted" style="font-size:12px">
        e.g. <span class="mono">firefox</span> or <span class="mono">chromium --new-window {"{{url}}"}</span>.
        Used when webssh opens on startup.
      </span>
    </div>
    <div class="field">
      <label for="mount">sshfs mount base directory</label>
      <input id="mount" class="mono" bind:value={form.mount_base_dir} />
    </div>
    <div class="field">
      <label for="theme">Theme</label>
      <select id="theme" bind:value={form.theme}>
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
    </div>
    <div class="row" style="flex-wrap:wrap">
      <button class="primary" onclick={save} disabled={busy}>Save settings</button>
      <button onclick={doRestart} disabled={busy}>Restart app</button>
    </div>
    <span class="muted" style="font-size:12px">
      Restart relaunches the daemon and reopens the app in your browser
      (per the browser command). This tab briefly loses connection.
    </span>
  </section>

  <section>
    <h3>~/.ssh/config sync</h3>
    <p class="muted">
      Import reads your existing <span class="mono">~/.ssh/config</span>. Export writes the inventory
      to <span class="mono">~/.ssh/config.d/inventory</span> and ensures an
      <span class="mono">Include</span> line (your main config is backed up, never overwritten).
    </p>
    <div class="row" style="flex-wrap:wrap">
      <button onclick={() => importConfig()} disabled={busy}>Import from ~/.ssh/config</button>
      <button onclick={exportConfig} disabled={busy}>Export to config.d/inventory</button>
    </div>

    <hr />

    <div class="field">
      <label for="ipath">Import from a custom file (path on this machine)</label>
      <div class="row">
        <input id="ipath" class="mono" bind:value={importPath}
          placeholder="/path/to/ssh_config or ~/configs/work" />
        <button onclick={() => importConfig(importPath)} disabled={busy || !importPath.trim()}>
          Import
        </button>
      </div>
    </div>

    <div class="field">
      <label>Or upload a config file from your device</label>
      <button onclick={() => fileInput.click()} disabled={busy}>Upload &amp; import…</button>
      <input type="file" bind:this={fileInput} onchange={onImportFile} style="display:none" />
    </div>
  </section>

  <section>
    <h3>Security &amp; backup</h3>
    <p class="muted">
      The master password locks this panel and encrypts backups. A backup contains your whole
      inventory — hosts, saved passwords, keys and settings — encrypted with that password.
    </p>
    <div class="row" style="flex-wrap:wrap">
      <button onclick={() => openModal("password")} disabled={busy}>
        {lock.has_password ? "Change password" : "Set password"}
      </button>
      <button onclick={() => openModal("backup")} disabled={busy || !lock.has_password}>
        Backup…
      </button>
      <button onclick={() => openModal("restore")} disabled={busy}>Restore…</button>
      <button class="danger ghost" onclick={() => openModal("reset")}
        disabled={busy || !lock.has_password}>Reset…</button>
    </div>
    {#if !lock.has_password}
      <p class="muted" style="margin-bottom:0">
        Set a master password to enable backup and reset.
      </p>
    {/if}

    <hr />

    <label class="pick">
      <input type="checkbox" style="width:auto" disabled={busy}
        checked={app.settings.exit_on_close !== "0"}
        onchange={(e) => applyExitOnClose(e.currentTarget.checked)} />
      <span>Quit when the last tab is closed</span>
    </label>
    <p class="muted">
      The daemon exits a few seconds after you close the last webssh tab, so it
      does not linger in the background. Reloading the page does not count.
      Turn this off to keep it running — then stop it from the terminal.
    </p>

    <hr />

    <label class="pick">
      <input type="checkbox" style="width:auto" disabled={busy}
        checked={app.settings.auth_disabled !== "1"}
        onchange={(e) => {
          if (!e.currentTarget.checked) {
            e.currentTarget.checked = true; // revert until confirmed
            modal = "disableauth";
          } else {
            applyAuthDisabled(false);
          }
        }} />
      <span>Require API token (recommended)</span>
    </label>
    <p class="muted" style="margin-bottom:0">
      The token authenticates the browser to the local API. Turning it off makes
      the API reachable by any local process — leave it on unless you understand
      the risk.
    </p>
  </section>
</div>

{#if modal === "password"}
  <div class="overlay" onclick={() => (modal = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>{lock.has_password ? "Change master password" : "Set master password"}</h2>
      {#if lock.has_password}
        <div class="field">
          <label for="curpw">Current password</label>
          <input id="curpw" type="password" bind:value={curPw} autocomplete="current-password" />
        </div>
      {/if}
      <div class="field">
        <label for="newpw">New password</label>
        <input id="newpw" type="password" bind:value={newPw} autocomplete="new-password" />
      </div>
      <div class="field">
        <label for="confpw">Confirm new password</label>
        <input id="confpw" type="password" bind:value={confirmPw} autocomplete="new-password" />
      </div>
      <div class="row">
        {#if lock.has_password}
          <button class="danger ghost" onclick={() => savePassword(true)} disabled={busy}>
            Remove protection
          </button>
        {/if}
        <div class="spacer"></div>
        <button onclick={() => (modal = null)}>Cancel</button>
        <button class="primary" onclick={() => savePassword(false)} disabled={busy}>Save</button>
      </div>
    </div>
  </div>
{/if}

{#if modal === "backup"}
  <div class="overlay" onclick={() => (modal = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Backup</h2>
      <p class="muted" style="margin-top:0">
        Enter your master password to download an encrypted backup file.
      </p>
      <div class="field">
        <label for="bpw">Master password</label>
        <input id="bpw" type="password" bind:value={opPw} autocomplete="current-password" />
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (modal = null)}>Cancel</button>
        <button class="primary" onclick={doBackup} disabled={busy || !opPw}>Download backup</button>
      </div>
    </div>
  </div>
{/if}

{#if modal === "restore"}
  <div class="overlay" onclick={() => (modal = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Restore</h2>
      <div class="warn">
        ⚠ Restoring <strong>replaces</strong> all current hosts, keys and settings with the
        backup's contents.
      </div>
      <div class="field">
        <label>Backup file</label>
        <button onclick={() => restoreInput.click()} disabled={busy}>
          {restoreName || "Choose file…"}
        </button>
        <input type="file" bind:this={restoreInput} onchange={pickRestoreFile} style="display:none" />
      </div>
      <div class="field">
        <label for="rpw">Backup password</label>
        <input id="rpw" type="password" bind:value={opPw} autocomplete="current-password" />
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (modal = null)}>Cancel</button>
        <button class="primary" onclick={doRestore} disabled={busy || !restoreData || !opPw}>
          Restore
        </button>
      </div>
    </div>
  </div>
{/if}

{#if modal === "disableauth"}
  <div class="overlay" onclick={() => (modal = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Disable API token?</h2>
      <div class="warn">
        ⚠ Insecure. Without the token, <strong>any local process or user</strong>
        on this machine can read your whole inventory and open SSH sessions using
        your saved passwords and keys. Loopback binding and the Origin check on
        mutations still apply (so remote and web-page access stay blocked), but
        local access becomes open.
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (modal = null)}>Cancel</button>
        <button class="danger" onclick={() => applyAuthDisabled(true)} disabled={busy}>
          Disable anyway
        </button>
      </div>
    </div>
  </div>
{/if}

{#if modal === "reset"}
  <div class="overlay" onclick={() => (modal = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()}>
      <h2>Reset everything</h2>
      <div class="warn">
        ⚠ This permanently deletes all hosts, groups, tags, keys (including files on disk),
        saved passwords and settings. This cannot be undone.
      </div>
      <div class="field">
        <label for="xpw">Master password</label>
        <input id="xpw" type="password" bind:value={opPw} autocomplete="current-password" />
      </div>
      <label class="pick">
        <input type="checkbox" bind:checked={resetConfirm} style="width:auto" />
        <span>I understand this erases everything</span>
      </label>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (modal = null)}>Cancel</button>
        <button class="danger" onclick={doReset} disabled={busy || !opPw || !resetConfirm}>
          Reset everything
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .grid { display: grid; gap: 20px; max-width: 640px; }
  section {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 16px;
  }
  h3 { margin: 0 0 8px; font-size: 14px; }
  p { margin-top: 0; font-size: 12.5px; }
  hr { border: none; border-top: 1px solid var(--border); margin: 14px 0; }
  .warn {
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
    border-radius: 8px;
    padding: 10px 12px;
    font-size: 13px;
    margin-bottom: 12px;
  }
  .pick { display: flex; align-items: center; gap: 8px; margin: 4px 0 12px; color: var(--text); font-size: 13px; }
</style>
