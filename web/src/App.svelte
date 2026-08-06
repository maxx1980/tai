<script lang="ts">
  import { app, lock, refresh, refreshHealth, checkLock, notify, update, refreshUpdate } from "./store.svelte";
  import { lock as apiLock } from "./api";
  import { emptyHost, type Host } from "./types";
  import Inventory from "./Inventory.svelte";
  import KeysPanel from "./KeysPanel.svelte";
  import KnownHostsPanel from "./KnownHostsPanel.svelte";
  import SettingsPanel from "./SettingsPanel.svelte";
  import UpdatePanel from "./UpdatePanel.svelte";
  import HostEditor from "./HostEditor.svelte";
  import Terminal from "./Terminal.svelte";
  import FileBrowser from "./FileBrowser.svelte";
  import UnlockScreen from "./UnlockScreen.svelte";
  import Toasts from "./Toasts.svelte";

  type View = "update" | "inventory" | "keys" | "known-hosts" | "settings";
  let view = $state<View>("inventory");
  let editing = $state<Host | null>(null);
  // The web terminal runs either ssh or telnet, chosen by the card button.
  let termHost = $state<{ host: Host; shell: "ssh" | "telnet" } | null>(null);
  let browseHost = $state<Host | null>(null);
  let loaded = $state(false);

  // Initial load: discover the lock state first, then load data unless locked.
  $effect(() => {
    checkLock()
      .then(() => refresh())
      .then(() => (loaded = true))
      .catch((e) => notify("error", "Failed to load: " + e));
  });

  // Ask GitHub whether a newer version is tagged, once the panel is unlocked.
  // Only a positive answer surfaces anything (the Update tab), so a machine
  // that is offline or rate-limited simply sees the interface it always had.
  $effect(() => {
    if (!loaded || !lock.unlocked) return;
    refreshUpdate();
  });

  // Poll host reachability every 60s once loaded and unlocked; the backend
  // re-probes on the same cadence.
  $effect(() => {
    if (!loaded || !lock.unlocked) return;
    refreshHealth();
    const id = setInterval(refreshHealth, 60000);
    return () => clearInterval(id);
  });

  async function doLock() {
    try {
      await apiLock();
      lock.unlocked = false;
    } catch (e) {
      notify("error", String(e));
    }
  }

  // Apply theme selection to <html>.
  $effect(() => {
    const t = app.settings.theme ?? "system";
    if (t === "system") delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = t;
  });
</script>

{#if lock.has_password && !lock.unlocked}
  <UnlockScreen />
{:else}
<div class="app">
  <header>
    <div class="brand">🔐 webssh</div>
    <nav>
      {#if update.info?.available}
        <button
          class="upd"
          class:active={view === "update"}
          onclick={() => (view = "update")}
          title="{update.info.latest} is available"
        >
          ⬆ Update
        </button>
      {/if}
      <button class:active={view === "inventory"} onclick={() => (view = "inventory")}
        >Inventory</button
      >
      <button class:active={view === "keys"} onclick={() => (view = "keys")}>Keys</button>
      <button class:active={view === "known-hosts"} onclick={() => (view = "known-hosts")}
        >Known hosts</button
      >
      <button class:active={view === "settings"} onclick={() => (view = "settings")}
        >Settings</button
      >
    </nav>
    <div class="spacer"></div>
    {#if lock.has_password}
      <button class="sm ghost" onclick={doLock} title="Lock panel">🔒 Lock</button>
    {/if}
    <button class="sm ghost" onclick={() => refresh()} title="Reload">↻</button>
  </header>

  <div class="content">
    {#if !loaded}
      <p class="muted">Loading…</p>
    {:else if view === "update"}
      <div class="scroll pad"><UpdatePanel /></div>
    {:else if view === "inventory"}
      <Inventory
        onNewHost={() => (editing = emptyHost())}
        onEditHost={(h) => (editing = h)}
        onOpenTerminal={(h, shell) => (termHost = { host: h, shell })}
        onBrowse={(h) => (browseHost = h)}
      />
    {:else if view === "keys"}
      <div class="scroll pad"><KeysPanel /></div>
    {:else if view === "known-hosts"}
      <div class="scroll pad"><KnownHostsPanel /></div>
    {:else if view === "settings"}
      <div class="scroll pad"><SettingsPanel /></div>
    {/if}
  </div>
</div>
{/if}

{#if editing}
  <HostEditor host={editing} groups={app.groups} onclose={() => (editing = null)} />
{/if}

{#if termHost}
  <div class="term-overlay">
    <Terminal
      hostId={termHost.host.id}
      alias={termHost.host.alias}
      shell={termHost.shell}
      onclose={() => (termHost = null)}
    />
  </div>
{/if}

{#if browseHost}
  <div class="term-overlay">
    <FileBrowser hostId={browseHost.id} alias={browseHost.alias} onclose={() => (browseHost = null)} />
  </div>
{/if}

<Toasts />

<style>
  .app { display: flex; flex-direction: column; height: 100%; }
  header {
    display: flex;
    align-items: center;
    gap: 16px;
    padding: 10px 18px;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
  }
  .brand { font-weight: 600; font-size: 15px; }
  nav { display: flex; gap: 4px; }
  nav button { background: transparent; border: none; padding: 6px 12px; color: var(--muted); }
  nav button.active { color: var(--text); background: var(--panel-2); }
  /* The one nav entry that appears on its own, so it says why it is there. */
  nav button.upd { color: var(--accent); font-weight: 600; }
  .content { flex: 1; min-height: 0; padding: 16px 18px; }
  .pad { height: 100%; }
  .term-overlay {
    position: fixed;
    inset: 5% 5%;
    z-index: 40;
    box-shadow: var(--shadow);
  }
</style>
