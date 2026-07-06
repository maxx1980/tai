<script lang="ts">
  import Fuse from "fuse.js";
  import { api, ApiError } from "./api";
  import { app, refresh, notify } from "./store.svelte";
  import type { Host, Group } from "./types";

  let {
    onEditHost,
    onNewHost,
    onOpenTerminal,
    onBrowse,
  }: {
    onEditHost: (h: Host) => void;
    onNewHost: () => void;
    onOpenTerminal: (h: Host) => void;
    onBrowse: (h: Host) => void;
  } = $props();

  let query = $state("");
  let selectedGroup = $state<number | null>(null);
  let activeTags = $state<Set<string>>(new Set());
  let busyId = $state<number | null>(null);
  let mountPw = $state<{ host: Host; afterFiles: boolean } | null>(null);
  let mountPwValue = $state("");

  // Tree state: collapsed node ids (-1 = the "All hosts" node), drag/drop.
  const ALL = -1;
  let collapsed = $state<Set<number>>(new Set());
  let dragHostId = $state<number | null>(null);
  let dropTarget = $state<number | null>(null); // group id, or ALL, or null

  const baseDir = $derived(app.settings.mount_base_dir ?? "");
  const mountedPoints = $derived(new Set(app.mounts.map((m) => m.mountpoint)));

  function isMounted(h: Host): boolean {
    return mountedPoints.has(`${baseDir}/${h.alias}`);
  }

  // ---- tree helpers ----

  function childGroups(parentId: number | null): Group[] {
    return app.groups
      .filter((g) => (g.parent_id ?? null) === parentId)
      .sort((a, b) => a.sort - b.sort || a.name.localeCompare(b.name));
  }

  // directHosts returns hosts assigned exactly to groupId (null = ungrouped).
  function directHosts(groupId: number | null): Host[] {
    return app.hosts
      .filter((h) => (h.group_id ?? null) === groupId)
      .sort((a, b) => a.alias.localeCompare(b.alias));
  }

  function hasChildren(groupId: number): boolean {
    return childGroups(groupId).length > 0 || directHosts(groupId).length > 0;
  }

  const isExpanded = (id: number) => !collapsed.has(id);
  function toggleExpand(id: number) {
    const n = new Set(collapsed);
    n.has(id) ? n.delete(id) : n.add(id);
    collapsed = n;
  }

  // ---- drag & drop: move a host into a group ----

  function onHostDragStart(e: DragEvent, h: Host) {
    dragHostId = h.id;
    e.dataTransfer?.setData("text/plain", String(h.id));
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function onDrop(e: DragEvent, groupId: number | null) {
    e.preventDefault();
    const id = dragHostId;
    dropTarget = null;
    dragHostId = null;
    if (id != null) assignGroup(id, groupId);
  }

  async function assignGroup(hostId: number, groupId: number | null) {
    const h = app.hosts.find((x) => x.id === hostId);
    if (!h || (h.group_id ?? null) === groupId) return;
    try {
      await api.put(`/api/hosts/${hostId}`, { ...h, group_id: groupId });
      await refresh();
      const dest = groupId == null ? "All hosts" : app.groups.find((g) => g.id === groupId)?.name;
      notify("info", `Moved ${h.alias} → ${dest}`);
    } catch (e) {
      notify("error", String(e));
    }
  }

  // Descendant group ids (so selecting a parent includes children).
  function descendants(id: number): Set<number> {
    const set = new Set<number>([id]);
    let added = true;
    while (added) {
      added = false;
      for (const g of app.groups) {
        if (g.parent_id != null && set.has(g.parent_id) && !set.has(g.id)) {
          set.add(g.id);
          added = true;
        }
      }
    }
    return set;
  }

  const fuse = $derived(
    new Fuse(app.hosts, {
      keys: ["alias", "hostname", "user", "tags", "notes"],
      threshold: 0.4,
      ignoreLocation: true,
    }),
  );

  const visible = $derived.by(() => {
    let hosts = query.trim() ? fuse.search(query).map((r) => r.item) : app.hosts;
    if (selectedGroup != null) {
      const ids = descendants(selectedGroup);
      hosts = hosts.filter((h) => h.group_id != null && ids.has(h.group_id));
    }
    if (activeTags.size > 0) {
      hosts = hosts.filter((h) => [...activeTags].every((t) => h.tags.includes(t)));
    }
    return hosts;
  });

  function toggleTag(t: string) {
    const next = new Set(activeTags);
    next.has(t) ? next.delete(t) : next.add(t);
    activeTags = next;
  }

  function countIn(groupId: number): number {
    const ids = descendants(groupId);
    return app.hosts.filter((h) => h.group_id != null && ids.has(h.group_id)).length;
  }

  async function newGroup() {
    const name = prompt(
      selectedGroup != null
        ? "New subgroup name (under selected group):"
        : "New group name:",
    );
    if (!name?.trim()) return;
    try {
      await api.post("/api/groups", { name: name.trim(), parent_id: selectedGroup });
      await refresh();
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function deleteGroup(g: Group, e: Event) {
    e.stopPropagation();
    if (!confirm(`Delete group "${g.name}"? Hosts are kept (ungrouped).`)) return;
    try {
      await api.del(`/api/groups/${g.id}`);
      if (selectedGroup === g.id) selectedGroup = null;
      await refresh();
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function deleteHost(h: Host) {
    if (!confirm(`Delete host "${h.alias}"?`)) return;
    try {
      await api.del(`/api/hosts/${h.id}`);
      await refresh();
    } catch (e) {
      notify("error", String(e));
    }
  }

  async function act(h: Host, fn: () => Promise<void>) {
    busyId = h.id;
    try {
      await fn();
    } catch (e) {
      notify("error", String(e));
    } finally {
      busyId = null;
    }
  }

  const launchTerminal = (h: Host) =>
    act(h, async () => {
      await api.post(`/api/launch/terminal/${h.id}`);
      notify("info", `Opening terminal for ${h.alias}`);
    });

  // mountHost mounts h; on a password-required response it opens the web
  // password prompt (afterFiles remembers whether to open the file manager next)
  // and returns false. Returns true once mounted.
  async function mountHost(h: Host, afterFiles: boolean, password?: string): Promise<boolean> {
    try {
      const r = await api.post<{ mountpoint: string }>(
        `/api/mounts/${h.id}`,
        password ? { password } : undefined,
      );
      notify("success", `Mounted at ${r.mountpoint}`);
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.data?.need_password) {
        mountPwValue = "";
        mountPw = { host: h, afterFiles };
        return false;
      }
      throw e;
    }
  }

  const openFiles = (h: Host) =>
    act(h, async () => {
      if (!isMounted(h)) {
        if (!(await mountHost(h, true))) return; // waiting for password
      }
      await api.post(`/api/launch/files/${h.id}`);
      await refresh();
    });

  const toggleMount = (h: Host) =>
    act(h, async () => {
      if (isMounted(h)) {
        await api.del(`/api/mounts/${h.id}`);
        notify("info", `Unmounted ${h.alias}`);
      } else {
        await mountHost(h, false);
      }
      await refresh();
    });

  async function submitMountPw() {
    if (!mountPw) return;
    const { host, afterFiles } = mountPw;
    const pw = mountPwValue;
    mountPw = null;
    await act(host, async () => {
      if (!(await mountHost(host, afterFiles, pw))) return;
      if (afterFiles) await api.post(`/api/launch/files/${host.id}`);
      await refresh();
    });
  }
</script>

{#snippet hostNode(h: Host, depth: number)}
  <div
    class="tnode host {dragHostId === h.id ? 'dragging' : ''}"
    style="padding-left:{10 + depth * 14}px"
    role="treeitem"
    aria-selected="false"
    tabindex="0"
    draggable="true"
    ondragstart={(e) => onHostDragStart(e, h)}
    ondragend={() => {
      dragHostId = null;
      dropTarget = null;
    }}
    onclick={() => onEditHost(h)}
    onkeydown={(e) => e.key === "Enter" && onEditHost(h)}
    title="Drag onto a group to move · click to edit"
  >
    <span class="ticon">{isMounted(h) ? "🟢" : "🖥️"}</span>
    <span class="tlabel mono">{h.alias}</span>
  </div>
{/snippet}

{#snippet groupNode(group: Group, depth: number)}
  <div
    class="tnode grp {selectedGroup === group.id ? 'active' : ''} {dropTarget === group.id
      ? 'drop'
      : ''}"
    style="padding-left:{6 + depth * 14}px"
    role="treeitem"
    aria-selected={selectedGroup === group.id}
    tabindex="0"
    ondragover={(e) => {
      e.preventDefault();
      dropTarget = group.id;
    }}
    ondragleave={() => {
      if (dropTarget === group.id) dropTarget = null;
    }}
    ondrop={(e) => onDrop(e, group.id)}
    onclick={() => (selectedGroup = group.id)}
    onkeydown={(e) => e.key === "Enter" && (selectedGroup = group.id)}
  >
    <button
      class="caret"
      onclick={(e) => {
        e.stopPropagation();
        toggleExpand(group.id);
      }}
    >
      {hasChildren(group.id) ? (isExpanded(group.id) ? "▾" : "▸") : ""}
    </button>
    <span class="ticon">📂</span>
    <span class="tlabel">{group.name}</span>
    <span class="spacer"></span>
    <span class="muted count">{countIn(group.id)}</span>
    <span
      class="del"
      role="button"
      tabindex="0"
      onclick={(e) => deleteGroup(group, e)}
      onkeydown={() => {}}>✕</span
    >
  </div>
  {#if isExpanded(group.id)}
    {#each childGroups(group.id) as cg (cg.id)}
      {@render groupNode(cg, depth + 1)}
    {/each}
    {#each directHosts(group.id) as h (h.id)}
      {@render hostNode(h, depth + 1)}
    {/each}
  {/if}
{/snippet}

<div class="inv">
  <aside class="sidebar">
    <input class="search" placeholder="Fuzzy search…" bind:value={query} />

    <div class="side-head">
      <span>Inventory</span>
      <button class="sm ghost" onclick={newGroup} title="New group">+ Group</button>
    </div>
    <div class="tree scroll">
      <!-- All hosts / ungrouped node (drop here to remove from a group) -->
      <div
        class="tnode grp {selectedGroup === null ? 'active' : ''} {dropTarget === ALL ? 'drop' : ''}"
        role="treeitem"
        aria-selected={selectedGroup === null}
        tabindex="0"
        ondragover={(e) => {
          e.preventDefault();
          dropTarget = ALL;
        }}
        ondragleave={() => {
          if (dropTarget === ALL) dropTarget = null;
        }}
        ondrop={(e) => onDrop(e, null)}
        onclick={() => (selectedGroup = null)}
        onkeydown={(e) => e.key === "Enter" && (selectedGroup = null)}
      >
        <button
          class="caret"
          onclick={(e) => {
            e.stopPropagation();
            toggleExpand(ALL);
          }}
        >
          {directHosts(null).length ? (isExpanded(ALL) ? "▾" : "▸") : ""}
        </button>
        <span class="ticon">🗂️</span>
        <span class="tlabel">All hosts</span>
        <span class="spacer"></span>
        <span class="muted count">{app.hosts.length}</span>
      </div>
      {#if isExpanded(ALL)}
        {#each directHosts(null) as h (h.id)}
          {@render hostNode(h, 1)}
        {/each}
      {/if}

      {#each childGroups(null) as g (g.id)}
        {@render groupNode(g, 0)}
      {/each}
    </div>

    {#if app.tags.length}
      <div class="side-head"><span>Tags</span></div>
      <div class="tags">
        {#each app.tags as t}
          <button
            class="tag-btn {activeTags.has(t) ? 'on' : ''}"
            onclick={() => toggleTag(t)}
          >{t}</button>
        {/each}
      </div>
    {/if}
  </aside>

  <main class="hosts scroll">
    <div class="row" style="margin-bottom:12px">
      <strong>{visible.length} host{visible.length === 1 ? "" : "s"}</strong>
      <div class="spacer"></div>
      <button class="primary" onclick={onNewHost}>+ New host</button>
    </div>

    {#if visible.length === 0}
      <p class="muted">No hosts. Add one, or import from ~/.ssh/config in Settings.</p>
    {/if}

    <div class="cards">
      {#each visible as h (h.id)}
        <div
          class="hcard {dragHostId === h.id ? 'dragging' : ''}"
          draggable="true"
          ondragstart={(e) => onHostDragStart(e, h)}
          ondragend={() => {
            dragHostId = null;
            dropTarget = null;
          }}
          role="listitem"
        >
          <div class="row">
            <strong title="Drag onto a group in the sidebar to move">⠿ {h.alias}</strong>
            {#if isMounted(h)}<span class="tag" title="sshfs mounted">mounted</span>{/if}
            <div class="spacer"></div>
            <button class="sm ghost" onclick={() => onEditHost(h)}>Edit</button>
            <button class="sm ghost danger" onclick={() => deleteHost(h)}>Del</button>
          </div>
          <div class="muted mono addr">
            {h.user ? h.user + "@" : ""}{h.hostname || h.alias}{h.port && h.port !== 22
              ? ":" + h.port
              : ""}
          </div>
          {#if h.tags.length}
            <div class="row" style="flex-wrap:wrap;gap:4px">
              {#each h.tags as t}<span class="tag">{t}</span>{/each}
            </div>
          {/if}
          <div class="row actions">
            <button class="sm" disabled={busyId === h.id} onclick={() => onOpenTerminal(h)}>
              Web term
            </button>
            <button class="sm" disabled={busyId === h.id} onclick={() => launchTerminal(h)}>
              Terminal
            </button>
            <button class="sm" disabled={busyId === h.id} onclick={() => openFiles(h)}>
              Files
            </button>
            <button class="sm" disabled={busyId === h.id} onclick={() => onBrowse(h)}>
              Browse
            </button>
            <button class="sm" disabled={busyId === h.id} onclick={() => toggleMount(h)}>
              {isMounted(h) ? "Unmount" : "Mount"}
            </button>
          </div>
        </div>
      {/each}
    </div>
  </main>
</div>

{#if mountPw}
  <div class="overlay" onclick={() => (mountPw = null)}>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal" onclick={(e) => e.stopPropagation()} style="max-width:420px">
      <h2>Password for {mountPw.host.alias}</h2>
      <p class="muted" style="margin-top:0">
        No deployed key worked for this host. Enter the SSH password to mount over sshfs
        (used once, not stored). Tip: deploy a key from the Keys tab for passwordless access.
      </p>
      <div class="field">
        <label for="mpw">SSH password</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="mpw" type="password" bind:value={mountPwValue} autofocus
          onkeydown={(e) => e.key === "Enter" && submitMountPw()} />
      </div>
      <div class="row">
        <div class="spacer"></div>
        <button onclick={() => (mountPw = null)}>Cancel</button>
        <button class="primary" onclick={submitMountPw}>Mount</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .inv { display: grid; grid-template-columns: 240px 1fr; gap: 16px; height: 100%; min-height: 0; }
  .sidebar { display: flex; flex-direction: column; gap: 10px; min-height: 0; }
  .search { margin-bottom: 2px; }
  .side-head {
    display: flex; align-items: center; justify-content: space-between;
    font-size: 12px; text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--muted); margin-top: 4px;
  }
  .tree { display: flex; flex-direction: column; gap: 1px; flex: 1; min-height: 0; }
  .tnode {
    display: flex; align-items: center; gap: 4px; width: 100%;
    padding: 5px 8px; border-radius: 6px; color: var(--text);
    cursor: pointer; user-select: none;
    border: 1px solid transparent;
  }
  .tnode:hover { background: var(--panel-2); }
  .tnode.grp.active { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent); }
  .tnode.drop { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 22%, transparent); }
  .tnode.host { cursor: grab; font-size: 13px; }
  .tnode.host:active { cursor: grabbing; }
  .tnode.dragging { opacity: 0.4; }
  .caret {
    width: 16px; flex-shrink: 0; background: transparent; border: none; padding: 0;
    color: var(--muted); font-size: 10px; text-align: center;
  }
  .ticon { flex-shrink: 0; font-size: 13px; }
  .tlabel { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .count { font-size: 11.5px; }
  .del { visibility: hidden; color: var(--danger); font-size: 11px; padding-left: 4px; }
  .tnode:hover .del { visibility: visible; }
  .tags { display: flex; flex-wrap: wrap; gap: 5px; }
  .tag-btn {
    padding: 2px 9px; font-size: 12px; border-radius: 999px;
    background: var(--panel-2);
  }
  .tag-btn.on { background: var(--accent); color: var(--accent-fg); border-color: var(--accent); }

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 12px;
    align-content: start;
  }
  .hcard {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 12px 14px;
    display: flex; flex-direction: column; gap: 8px;
  }
  .hcard.dragging { opacity: 0.4; }
  .hcard strong { cursor: grab; }
  .addr { font-size: 12.5px; }
  .actions { flex-wrap: wrap; gap: 6px; margin-top: 2px; }
</style>
