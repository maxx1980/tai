<script lang="ts">
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { api, terminalURL } from "./api";
  import { app, notify } from "./store.svelte";

  let {
    hostId,
    alias,
    shell = "ssh",
    onclose,
  }: {
    hostId: number;
    alias: string;
    shell?: "ssh" | "telnet";
    onclose: () => void;
  } = $props();

  let container: HTMLDivElement;

  // xterm.js sends DEL (0x7F) for Backspace. Telnet cannot negotiate which byte
  // means erase, so the far end just expects one of the two — network gear and
  // older Unix want BS (0x08), which is why that is the default. SSH is left
  // alone: its PTY carries the real terminal settings.
  const backspaceMode = $derived(app.settings.telnet_backspace ?? "bs");

  // rewriteInput is applied to telnet input only, and reads the setting at
  // keystroke time rather than in the $effect below — depending on it there
  // would tear down and rebuild the whole session on every toggle.
  function rewriteInput(d: string): string {
    if (shell !== "telnet") return d;
    if ((app.settings.telnet_backspace ?? "bs") !== "bs") return d;
    return d.replaceAll("\x7f", "\b");
  }

  async function toggleBackspace() {
    const next = backspaceMode === "bs" ? "del" : "bs";
    try {
      app.settings = await api.put<Record<string, string>>("/api/settings", {
        telnet_backspace: next,
      });
    } catch (e) {
      notify("error", String(e));
    }
  }

  $effect(() => {
    const term = new Terminal({
      fontSize: 13,
      fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
      cursorBlink: true,
      theme: { background: "#0b0e14", foreground: "#c8d0da" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);
    fit.fit();
    term.focus();

    const ws = new WebSocket(terminalURL(hostId, shell));
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();

    const sendResize = () => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      }
    };

    ws.onopen = () => sendResize();
    ws.onmessage = (e) => {
      if (typeof e.data === "string") term.write(e.data);
      else term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => term.write("\r\n\x1b[31m[disconnected]\x1b[0m\r\n");
    ws.onerror = () => term.write("\r\n\x1b[31m[connection error]\x1b[0m\r\n");

    const dataSub = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(rewriteInput(d)));
    });

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
        sendResize();
      } catch {}
    });
    ro.observe(container);

    return () => {
      dataSub.dispose();
      ro.disconnect();
      ws.close();
      term.dispose();
    };
  });
</script>

<div class="term-wrap">
  <div class="term-bar">
    <span class="dot"></span>
    <strong>{alias}</strong>
    <span class="muted mono" style="font-size:12px">{shell} terminal</span>
    <div class="spacer"></div>
    {#if shell === "telnet"}
      <button
        class="sm ghost"
        onclick={toggleBackspace}
        title="What Backspace sends. Switch if the remote host ignores it or prints ^H."
      >
        ⌫ sends {backspaceMode === "bs" ? "^H" : "^?"}
      </button>
    {/if}
    <button class="sm ghost" onclick={onclose}>Close ✕</button>
  </div>
  <div class="term" bind:this={container}></div>
</div>

<style>
  .term-wrap {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: #0b0e14;
    border-radius: 10px;
    overflow: hidden;
    border: 1px solid var(--border);
  }
  .term-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: #12161f;
    color: #c8d0da;
    border-bottom: 1px solid #1e2430;
  }
  .dot { width: 10px; height: 10px; border-radius: 50%; background: var(--success); }
  .term { flex: 1; padding: 6px 8px; min-height: 0; }
</style>
