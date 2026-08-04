// Holds a websocket open for as long as this tab lives, so the daemon can tell
// whether anyone is still using it. Closing the tab drops the socket without
// any help from us — no beforeunload handler, which would also fire on reload
// and would be skipped entirely if the browser were killed.
//
// The daemon waits a few seconds after the last socket goes away, so a reload
// (which reconnects almost immediately) does not look like a close.
import { presenceURL } from "./api";

const RETRY_MS = [1000, 2000, 5000];

let sock: WebSocket | null = null;
let attempt = 0;
let timer: number | undefined;

function connect() {
  timer = undefined;
  sock = new WebSocket(presenceURL());

  sock.onopen = () => {
    attempt = 0;
  };

  // A drop is usually the daemon restarting (POST /api/restart re-execs it), so
  // reconnect rather than leaving it thinking the tab is gone. If the daemon is
  // really down the retries simply keep failing, which is harmless.
  sock.onclose = () => {
    sock = null;
    const delay = RETRY_MS[Math.min(attempt, RETRY_MS.length - 1)];
    attempt++;
    timer = setTimeout(connect, delay);
  };
}

export function startPresence() {
  if (sock || timer) return;
  connect();
}
