import { api, ApiError, lockstate } from "./api";
import type { AppState } from "./types";

// Central reactive application state (Svelte 5 runes). Named `app` (not `state`)
// to avoid colliding with the `$state` rune.
export const app = $state<AppState>({
  hosts: [],
  groups: [],
  tags: [],
  keys: [],
  known_hosts: [],
  settings: {},
  mounts: [],
});

// Master-password lock gate. `unlocked` starts true so the app renders normally
// until checkLock proves a password is set and the session is locked.
export const lock = $state({ has_password: false, unlocked: true });

// Per-host reachability, keyed by host id: "green" | "yellow" | "red". A host id
// absent from the map is "unknown" (not yet probed). Populated by refreshHealth,
// which the app polls on a timer.
export const health = $state<{ map: Record<number, string> }>({ map: {} });

export async function refreshHealth(): Promise<void> {
  try {
    const h = await api.get<Record<string, string>>("/api/health");
    const next: Record<number, string> = {};
    for (const [k, v] of Object.entries(h ?? {})) next[Number(k)] = v;
    health.map = next;
  } catch {
    // Silently ignore (e.g. 423 while locked, or a transient network blip) so
    // the status dots simply don't update rather than spamming toasts.
  }
}

export async function checkLock(): Promise<void> {
  const s = await lockstate();
  lock.has_password = s.has_password;
  lock.unlocked = s.unlocked;
}

export async function refresh(): Promise<void> {
  let s: AppState;
  try {
    s = await api.get<AppState>("/api/state");
  } catch (e) {
    if (e instanceof ApiError && e.status === 423) {
      lock.has_password = true;
      lock.unlocked = false;
      return; // locked — the unlock screen takes over
    }
    throw e;
  }
  app.hosts = s.hosts ?? [];
  app.groups = s.groups ?? [];
  app.tags = s.tags ?? [];
  app.keys = s.keys ?? [];
  app.known_hosts = s.known_hosts ?? [];
  app.settings = s.settings ?? {};
  app.mounts = s.mounts ?? [];
}

// ---- toast notifications ----

export interface Toast {
  id: number;
  kind: "info" | "error" | "success";
  text: string;
}

export const toasts = $state<{ items: Toast[] }>({ items: [] });
let toastSeq = 0;

export function notify(kind: Toast["kind"], text: string): void {
  const id = ++toastSeq;
  toasts.items.push({ id, kind, text });
  setTimeout(() => {
    toasts.items = toasts.items.filter((t) => t.id !== id);
  }, kind === "error" ? 8000 : 4000);
}

export function dismiss(id: number): void {
  toasts.items = toasts.items.filter((t) => t.id !== id);
}

// isMounted returns true if the host's home directory is currently mounted.
export function isMounted(mountpoints: string[], baseDir: string, alias: string): boolean {
  const point = `${baseDir}/${alias}`;
  return mountpoints.includes(point);
}
