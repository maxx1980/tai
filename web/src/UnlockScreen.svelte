<script lang="ts">
  import { unlock } from "./api";
  import { lock, refresh, checkLock, notify } from "./store.svelte";

  let password = $state("");
  let busy = $state(false);
  let input: HTMLInputElement;

  $effect(() => {
    input?.focus();
  });

  async function submit(e: Event) {
    e.preventDefault();
    if (!password) return;
    busy = true;
    try {
      await unlock(password);
      lock.unlocked = true;
      password = "";
      await refresh();
      await checkLock();
    } catch (e) {
      notify("error", "Wrong password");
      password = "";
    } finally {
      busy = false;
    }
  }
</script>

<div class="lock">
  <form class="box" onsubmit={submit}>
    <div class="brand">🔐 webssh</div>
    <p class="muted">This panel is locked. Enter your master password.</p>
    <input
      bind:this={input}
      type="password"
      bind:value={password}
      placeholder="Master password"
      autocomplete="current-password"
    />
    <button class="primary" type="submit" disabled={busy || !password}>
      {busy ? "Unlocking…" : "Unlock"}
    </button>
  </form>
</div>

<style>
  .lock {
    position: fixed;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg);
    z-index: 100;
  }
  .box {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 28px;
    width: 320px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    box-shadow: var(--shadow);
  }
  .brand {
    font-weight: 600;
    font-size: 18px;
    text-align: center;
  }
  p {
    margin: 0;
    font-size: 13px;
    text-align: center;
  }
  input {
    text-align: center;
  }
</style>
