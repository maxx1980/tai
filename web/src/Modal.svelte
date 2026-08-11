<script lang="ts">
  import { onMount, tick, type Snippet } from "svelte";

  let {
    titleId,
    onclose,
    wide = false,
    maxWidth,
    children,
  }: {
    titleId: string;
    onclose: () => void;
    wide?: boolean;
    maxWidth?: string;
    children: Snippet;
  } = $props();

  let dialog: HTMLDialogElement;

  const focusable =
    'button:not([disabled]), [href], input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  onMount(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.showModal();
    void tick().then(() => {
      (dialog.querySelector<HTMLElement>(focusable) ?? dialog).focus();
    });
    return () => {
      if (dialog.open) dialog.close();
      previous?.focus();
    };
  });

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      onclose();
      return;
    }
    if (event.key !== "Tab") return;

    const items = [...dialog.querySelectorAll<HTMLElement>(focusable)];
    if (items.length === 0) {
      event.preventDefault();
      dialog.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<dialog
  class="overlay"
  aria-labelledby={titleId}
  bind:this={dialog}
  onclick={(event) => event.target === event.currentTarget && onclose()}
  onkeydown={handleKeydown}
  oncancel={(event) => {
    event.preventDefault();
    onclose();
  }}
>
  <div class:wide class="modal" style:max-width={maxWidth}>
    {@render children()}
  </div>
</dialog>

<style>
  dialog.overlay {
    width: auto;
    height: auto;
    max-width: none;
    max-height: none;
    margin: 0;
    border: 0;
    color: var(--text);
  }
  dialog.overlay::backdrop { background: transparent; }
  .modal.wide { max-width: 760px; }
</style>
