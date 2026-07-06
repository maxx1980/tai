<script lang="ts">
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { terminalURL } from "./api";

  let { hostId, alias, onclose }: { hostId: number; alias: string; onclose: () => void } =
    $props();

  let container: HTMLDivElement;

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

    const ws = new WebSocket(terminalURL(hostId));
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
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
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
    <span class="muted mono" style="font-size:12px">web terminal</span>
    <div class="spacer"></div>
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
