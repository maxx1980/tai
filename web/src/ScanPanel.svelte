<script lang="ts">
  // Network discovery modal: expand a target spec, probe it for open SSH/telnet
  // ports, then add the machines that answered to the inventory.
  import { api } from "./api";
  import { app, refresh, notify } from "./store.svelte";
  import { portsFromScan, type Host, type ScanHost, type ScanResult } from "./types";

  let { onClose }: { onClose: () => void } = $props();

  // ---- scan form ----
  // The port groups mirror the actions a host card can offer, so anything found
  // here maps straight onto a button.
  let targets = $state("");
  let wantSSH = $state(true);
  let wantTelnet = $state(true);
  let wantWeb = $state(true);
  let wantProxmox = $state(true);
  let extraPorts = $state("");
  let timeoutMs = $state(1500);
  let resolve = $state(true);

  let scanning = $state(false);
  let adding = $state(false);
  let result = $state<ScanResult | null>(null);

  // ---- result selection: keyed by address ----
  let picked = $state<Set<string>>(new Set());
  let aliases = $state<Record<string, string>>({});
  let user = $state("");
  let groupId = $state<number | null>(null);

  // ports assembles the port list to probe from the checkboxes plus any extras.
  function ports(): number[] {
    const out: number[] = [];
    if (wantSSH) out.push(22);
    if (wantTelnet) out.push(23);
    if (wantWeb) out.push(80, 443);
    if (wantProxmox) out.push(8006, 8007);
    for (const p of extraPorts.split(/[\s,;]+/)) {
      const n = Number(p);
      if (Number.isInteger(n) && n >= 1 && n <= 65535) out.push(n);
    }
    return [...new Set(out)];
  }

  // defaultAlias prefers the short reverse-DNS name, falling back to the address
  // with dots swapped for dashes so it is still a legal ssh_config alias.
  function defaultAlias(h: ScanHost): string {
    if (h.hostname) return h.hostname.split(".")[0];
    return h.address.replace(/[.:]/g, "-");
  }

  function serviceLabel(h: ScanHost): string {
    return h.services.map((s) => `${s.name}/${s.port}`).join(", ");
  }

  function bannerOf(h: ScanHost): string {
    return h.services.map((s) => s.banner).filter(Boolean).join(" · ");
  }

  async function doScan() {
    const list = ports();
    if (!targets.trim()) {
      notify("error", "Enter an address, a range or a network to scan");
      return;
    }
    if (list.length === 0) {
      notify("error", "Pick at least one port to probe");
      return;
    }
    scanning = true;
    result = null;
    try {
      const r = await api.post<ScanResult>("/api/scan", {
        targets,
        ports: list,
        timeout_ms: Number(timeoutMs) || 1500,
        resolve,
      });
      result = r;
      // Preselect everything found and seed the editable aliases.
      const names: Record<string, string> = {};
      for (const h of r.hosts) names[h.address] = defaultAlias(h);
      aliases = names;
      picked = new Set(r.hosts.map((h) => h.address));
      notify(
        r.hosts.length ? "success" : "info",
        `Scanned ${r.scanned} address${r.scanned === 1 ? "" : "es"} in ${(r.elapsed_ms / 1000).toFixed(1)}s — ${r.hosts.length} responded`,
      );
    } catch (e) {
      notify("error", String(e));
    } finally {
      scanning = false;
    }
  }

  function toggle(addr: string) {
    const next = new Set(picked);
    next.has(addr) ? next.delete(addr) : next.add(addr);
    picked = next;
  }

  function selectAll(on: boolean) {
    picked = on ? new Set((result?.hosts ?? []).map((h) => h.address)) : new Set();
  }

  // toHost turns a scan result into the inventory record we will create: each
  // recognised service lands in its own port field, so the card comes up with
  // exactly the buttons the machine can actually answer. No tags are added —
  // the active buttons already say which services a host has.
  function toHost(h: ScanHost): Partial<Host> {
    const found = new Date().toISOString().slice(0, 10);
    const banner = bannerOf(h);
    return {
      alias: (aliases[h.address] || defaultAlias(h)).trim(),
      hostname: h.address,
      user: user.trim(),
      ...portsFromScan(h),
      group_id: groupId,
      tags: [],
      notes: `Discovered by scan ${found}: ${serviceLabel(h)}${banner ? ` — ${banner}` : ""}`,
    };
  }

  async function addSelected() {
    const chosen = (result?.hosts ?? []).filter((h) => picked.has(h.address));
    if (chosen.length === 0) return;
    adding = true;
    try {
      const r = await api.post<{ created: number; failed: string[] }>("/api/hosts/bulk", {
        hosts: chosen.map(toHost),
      });
      await refresh();
      notify("success", `Added ${r.created} host${r.created === 1 ? "" : "s"} to the inventory`);
      for (const f of r.failed ?? []) notify("error", f);
      onClose();
    } catch (e) {
      notify("error", String(e));
    } finally {
      adding = false;
    }
  }
</script>

<div class="overlay" onclick={onClose}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal wide" onclick={(e) => e.stopPropagation()}>
    <h2>Scan the network for hosts</h2>

    <div class="field">
      <label for="scan-targets">Addresses to scan</label>
      <textarea
        id="scan-targets"
        rows="3"
        class="mono"
        bind:value={targets}
        placeholder="192.168.23.24&#10;192.168.1.2-32&#10;192.168.1.0/24"
      ></textarea>
      <p class="muted hint">
        One or more per line (or comma-separated): a single address
        <span class="mono">192.168.23.24</span>, a range
        <span class="mono">192.168.1.2-32</span>, or a network
        <span class="mono">192.168.1.0/24</span>. Hostnames are resolved.
      </p>
    </div>

    <div class="row opts">
      <label class="check"><input type="checkbox" bind:checked={wantSSH} /> SSH (22)</label>
      <label class="check"><input type="checkbox" bind:checked={wantTelnet} /> Telnet (23)</label>
      <label class="check"><input type="checkbox" bind:checked={wantWeb} /> Web (80, 443)</label>
      <label class="check">
        <input type="checkbox" bind:checked={wantProxmox} /> Proxmox (8006, 8007)
      </label>
      <label class="check"><input type="checkbox" bind:checked={resolve} /> Resolve names</label>
      <div class="field inline">
        <label for="scan-extra">Extra ports</label>
        <input id="scan-extra" class="mono" bind:value={extraPorts} placeholder="2222, 2022" />
      </div>
      <div class="field inline narrow">
        <label for="scan-timeout">Timeout, ms</label>
        <input id="scan-timeout" type="number" min="200" max="5000" bind:value={timeoutMs} />
      </div>
    </div>

    <div class="row">
      <p class="muted hint" style="margin:0">
        Connect-scan only — no login is attempted, so nothing lands in the target's auth log.
      </p>
      <div class="spacer"></div>
      <button class="primary" onclick={doScan} disabled={scanning}>
        {scanning ? "Scanning…" : "Scan"}
      </button>
    </div>

    {#if result}
      <hr />
      {#if result.hosts.length === 0}
        <p class="muted">
          Nothing answered on {result.ports.join(", ")} across {result.scanned} address{result.scanned ===
          1
            ? ""
            : "es"}. Try a longer timeout or a different range.
        </p>
      {:else}
        <div class="row" style="margin-bottom:8px">
          <strong>{result.hosts.length} host{result.hosts.length === 1 ? "" : "s"} found</strong>
          <span class="muted">({picked.size} selected)</span>
          <div class="spacer"></div>
          <button class="sm ghost" onclick={() => selectAll(true)}>All</button>
          <button class="sm ghost" onclick={() => selectAll(false)}>None</button>
        </div>

        <div class="results scroll">
          {#each result.hosts as h (h.address)}
            <div class="res">
              <input
                type="checkbox"
                class="pick"
                checked={picked.has(h.address)}
                onchange={() => toggle(h.address)}
                aria-label="Add {h.address}"
              />
              <div class="info">
                <div class="row" style="gap:6px">
                  <span class="mono addr">{h.address}</span>
                  {#each h.services as s}
                    <span class="tag" title={s.banner ?? ""}>{s.name}/{s.port}</span>
                  {/each}
                  {#if h.hostname}<span class="muted rdns">{h.hostname}</span>{/if}
                </div>
                {#if bannerOf(h)}
                  <div class="muted mono banner" title={bannerOf(h)}>{bannerOf(h)}</div>
                {/if}
              </div>
              <input
                class="alias mono"
                bind:value={aliases[h.address]}
                aria-label="Alias for {h.address}"
                placeholder={defaultAlias(h)}
              />
            </div>
          {/each}
        </div>

        <div class="row applies">
          <div class="field inline">
            <label for="scan-user">User for added hosts</label>
            <input id="scan-user" bind:value={user} placeholder="root" />
          </div>
          <div class="field inline">
            <label for="scan-group">Group</label>
            <select id="scan-group" bind:value={groupId}>
              <option value={null}>— none —</option>
              {#each app.groups as g (g.id)}
                <option value={g.id}>{g.name}</option>
              {/each}
            </select>
          </div>
        </div>
      {/if}
    {/if}

    <div class="row" style="margin-top:14px">
      <div class="spacer"></div>
      <button onclick={onClose}>Close</button>
      {#if result && result.hosts.length > 0}
        <button class="primary" onclick={addSelected} disabled={adding || picked.size === 0}>
          {adding ? "Adding…" : `Add ${picked.size} to inventory`}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .modal.wide { max-width: 760px; }
  .hint { font-size: 12px; margin: 6px 0 0; }
  .opts { flex-wrap: wrap; gap: 14px; margin-bottom: 12px; align-items: flex-end; }
  .check { display: flex; align-items: center; gap: 6px; margin: 0 0 8px; color: var(--text); }
  .check input { width: auto; }
  .field.inline { margin-bottom: 0; min-width: 140px; }
  .field.narrow { min-width: 110px; }
  hr { border: none; border-top: 1px solid var(--border); margin: 16px 0; }

  .results { display: flex; flex-direction: column; gap: 6px; max-height: 280px; }
  .res {
    display: flex; align-items: center; gap: 10px;
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 7px 10px;
  }
  .pick { width: auto; flex: none; }
  .info { flex: 1; min-width: 0; }
  .addr { font-size: 13px; }
  .rdns { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .banner {
    font-size: 11.5px; margin-top: 2px;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .alias { width: 170px; flex: none; padding: 4px 8px; font-size: 12.5px; }
  .applies { gap: 12px; margin-top: 12px; flex-wrap: wrap; }
</style>
