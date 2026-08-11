<script lang="ts">
  import { toasts, dismiss } from "./store.svelte";
</script>

<div class="toasts">
  {#each toasts.items as t (t.id)}
    <div class="toast {t.kind}" role="alert">
      <span>{t.text}</span>
      <button class="dismiss" aria-label="Dismiss notification" onclick={() => dismiss(t.id)}>×</button>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    bottom: 16px;
    right: 16px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 100;
    max-width: 380px;
  }
  .toast {
    padding: 10px 14px;
    border-radius: 8px;
    box-shadow: var(--shadow);
    border: 1px solid var(--border);
    background: var(--panel);
    animation: slide 0.15s ease-out;
    display: flex;
    align-items: flex-start;
    gap: 10px;
  }
  .toast span { flex: 1; }
  .dismiss {
    border: 0;
    background: transparent;
    color: var(--muted);
    padding: 0 2px;
    min-width: auto;
    font-size: 18px;
    line-height: 1;
  }
  .toast.error { border-left: 4px solid var(--danger); }
  .toast.success { border-left: 4px solid var(--success); }
  .toast.info { border-left: 4px solid var(--accent); }
  @keyframes slide {
    from { transform: translateY(8px); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }
</style>
