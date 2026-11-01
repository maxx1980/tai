<script lang="ts" module>
  export interface MenuItem {
    label: string;
    action: () => void;
    danger?: boolean;
    disabled?: boolean;
    /** Draw a separator above this item. */
    separator?: boolean;
  }
</script>

<script lang="ts">
  // A right-click menu of our own, replacing the browser's inside the page.
  // It opens at (x, y), is kept inside the viewport, and closes on Escape, a
  // click outside, scrolling, resizing or the window losing focus.
  import { onMount, tick } from "svelte";

  let {
    x,
    y,
    items,
    label = "Actions",
    onclose,
  }: { x: number; y: number; items: MenuItem[]; label?: string; onclose: () => void } = $props();

  let menu = $state<HTMLDivElement>();
  let left = $state(0);
  let top = $state(0);

  const buttons = () => [...(menu?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])];

  onMount(async () => {
    left = x;
    top = y;
    await tick();
    if (!menu) return;
    const r = menu.getBoundingClientRect();
    const pad = 4;
    if (left + r.width > window.innerWidth - pad) left = Math.max(pad, window.innerWidth - r.width - pad);
    if (top + r.height > window.innerHeight - pad) top = Math.max(pad, window.innerHeight - r.height - pad);
    buttons()[0]?.focus();
  });

  function choose(item: MenuItem) {
    if (item.disabled) return;
    onclose();
    item.action();
  }

  function onkeydown(e: KeyboardEvent) {
    const list = buttons();
    const i = list.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === "Escape" || e.key === "Tab") {
      e.preventDefault();
      onclose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      list[(i + 1) % list.length]?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      list[(i - 1 + list.length) % list.length]?.focus();
    } else if (e.key === "Home") {
      e.preventDefault();
      list[0]?.focus();
    } else if (e.key === "End") {
      e.preventDefault();
      list[list.length - 1]?.focus();
    }
  }

  function onpointerdown(e: PointerEvent) {
    if (menu && !menu.contains(e.target as Node)) onclose();
  }
</script>

<svelte:window
  onpointerdown={onpointerdown}
  onresize={onclose}
  onblur={onclose}
  onscrollcapture={onclose}
  oncontextmenu={(e) => {
    // A right-click elsewhere closes this menu; the target may open its own.
    if (menu && !menu.contains(e.target as Node)) onclose();
  }}
/>

<div
  class="ctxmenu"
  role="menu"
  aria-label={label}
  tabindex="-1"
  bind:this={menu}
  style="left:{left}px; top:{top}px"
  {onkeydown}
  oncontextmenu={(e) => e.preventDefault()}
>
  {#each items as item}
    {#if item.separator}<div class="sep" role="separator"></div>{/if}
    <button
      role="menuitem"
      class:danger={item.danger}
      disabled={item.disabled}
      onclick={() => choose(item)}>{item.label}</button
    >
  {/each}
</div>

<style>
  .ctxmenu {
    position: fixed;
    z-index: 1000;
    min-width: 170px;
    padding: 4px;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow), 0 8px 24px rgba(0, 0, 0, 0.18);
    display: flex;
    flex-direction: column;
  }
  .ctxmenu button {
    text-align: left;
    background: none;
    border: none;
    border-radius: 5px;
    padding: 6px 10px;
    color: var(--text);
    font-size: 13px;
    cursor: pointer;
  }
  .ctxmenu button:hover:not(:disabled),
  .ctxmenu button:focus-visible {
    background: var(--panel-2);
    outline: none;
  }
  .ctxmenu button:disabled { color: var(--muted); opacity: 0.6; cursor: default; }
  .ctxmenu button.danger { color: var(--danger); }
  .sep { height: 1px; margin: 4px 2px; background: var(--border); }
</style>
