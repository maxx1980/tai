// API client: reads the startup token from the cookie set by the Go server on
// first navigation, and sends it as X-Auth-Token on every request.

function token(): string {
  const m = document.cookie.match(/(?:^|;\s*)webssh_token=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

// ApiError carries the HTTP status and parsed body so callers can react to
// specific conditions (e.g. a 428 asking for a mount/SFTP password).
export class ApiError extends Error {
  status: number;
  data: any;
  constructor(status: number, data: any) {
    super(data?.error || `${status}`);
    this.status = status;
    this.data = data;
  }
}

interface Opts {
  body?: unknown;
  headers?: Record<string, string>;
}

// netFetch wraps fetch so a network-level failure (server down, connection
// dropped) surfaces as a clear ApiError instead of a bare "TypeError: Failed to
// fetch".
export async function netFetch(input: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(input, init);
  } catch {
    throw new ApiError(0, {
      error: "Cannot reach the webssh server — is it still running? Restart it and reload.",
    });
  }
}

async function request<T>(method: string, path: string, opts: Opts = {}): Promise<T> {
  const headers: Record<string, string> = { "X-Auth-Token": token(), ...opts.headers };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  const res = await netFetch(path, {
    method,
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  // Bodies are normally JSON, but some middleware (e.g. the 423 lock gate) may
  // send plain text — don't let JSON.parse throw a bare SyntaxError.
  let data: any = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text.trim() };
    }
  }
  if (!res.ok) throw new ApiError(res.status, data);
  return data as T;
}

export const api = {
  get: <T>(p: string) => request<T>("GET", p),
  post: <T>(p: string, b?: unknown) => request<T>("POST", p, { body: b }),
  put: <T>(p: string, b?: unknown) => request<T>("PUT", p, { body: b }),
  del: <T>(p: string) => request<T>("DELETE", p),
};

// importUpload uploads an ssh_config file from the browser and imports it.
export async function importUpload(file: File): Promise<{ imported: number }> {
  const fd = new FormData();
  fd.append("file", file);
  const res = await netFetch("/api/import/upload", {
    method: "POST",
    headers: { "X-Auth-Token": token() },
    body: fd,
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

// importKey imports an existing keypair as JSON text (no multipart upload, so it
// works even where the browser blocks file uploads). Throws ApiError; a 409 with
// data.exists=true means the SPA should confirm overwrite and retry.
export function importKey(
  name: string,
  privateText: string,
  publicText: string,
  overwrite: boolean,
): Promise<{ id: number; name: string }> {
  return request("POST", "/api/keys/import", {
    body: { name, private: privateText, public: publicText || undefined, overwrite },
  });
}

// exportKey downloads a key's public or private file and returns the filename
// it was saved under, so the caller can say so — the app window has no download
// bar, and a silent save looks like a dead button.
export async function exportKey(id: number, kind: "public" | "private"): Promise<string> {
  const res = await netFetch(`/api/keys/${id}/export?kind=${kind}`, {
    headers: { "X-Auth-Token": token() },
  });
  if (!res.ok) {
    const t = await res.text();
    throw new ApiError(res.status, t ? JSON.parse(t) : {});
  }
  const cd = res.headers.get("Content-Disposition") || "";
  const m = cd.match(/filename="?([^"]+)"?/);
  const name = m ? m[1] : kind;
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  return name;
}

// ---- Master password / unlock gate + backup ----

export interface LockState {
  has_password: boolean;
  unlocked: boolean;
}

export const lockstate = () => request<LockState>("GET", "/api/lockstate");

export const unlock = (password: string) =>
  request<{ unlocked: boolean }>("POST", "/api/unlock", { body: { password } });

export const lock = () => request<{ unlocked: boolean }>("POST", "/api/lock", { body: {} });

// setMasterPassword sets/changes (current + new) or removes (empty new) the
// master password.
export const setMasterPassword = (current: string, next: string) =>
  request<{ has_password: boolean }>("PUT", "/api/settings/password", {
    body: { current, new: next },
  });

// backup verifies the password server-side and downloads the encrypted dump.
export async function backup(password: string): Promise<void> {
  const res = await netFetch("/api/backup", {
    method: "POST",
    headers: { "X-Auth-Token": token(), "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  });
  if (!res.ok) {
    const t = await res.text();
    throw new ApiError(res.status, t ? JSON.parse(t) : {});
  }
  const cd = res.headers.get("Content-Disposition") || "";
  const m = cd.match(/filename="?([^"]+)"?/);
  const name = m ? m[1] : "webssh-backup.enc";
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

// restore sends the backup file's text (read in-browser, no multipart) + password.
export const restore = (dataText: string, password: string) =>
  request<{ hosts: number; keys: number; groups: number }>("POST", "/api/restore", {
    body: { data: dataText, password },
  });

// resetAll wipes everything after verifying the master password.
export const resetAll = (password: string) =>
  request<{ ok: boolean }>("POST", "/api/reset", { body: { password } });

// ---- Self-update ----

export interface UpdateInfo {
  current: string;
  latest: string;
  available: boolean;
  can_update: boolean;
  blocker?: string;
  source_dir?: string;
  repo?: string;
  notes_url?: string;
  checked_at: string;
}

export interface UpdateStatus {
  state: "idle" | "running" | "done" | "error";
  step: string;
  log: string[];
  error?: string;
  target?: string;
  backup?: string;
}

// checkUpdate asks the daemon to compare this build with the newest tag on
// GitHub. force skips the server-side cache (GitHub rate-limits by address).
export const checkUpdate = (force = false) =>
  api.get<UpdateInfo>(`/api/update/check${force ? "?force=1" : ""}`);

// updateStatus is polled while an update runs: the rebuild takes minutes, so
// runUpdate only starts it and the log arrives here.
export const updateStatus = () => api.get<UpdateStatus>("/api/update/status");

export const runUpdate = (tag: string, backup: boolean) =>
  api.post<UpdateStatus>("/api/update/run", { tag, backup });

// terminalURL builds the websocket URL (token as query param) for a host.
// shell selects which client the daemon runs: ssh (default) or telnet.
export function terminalURL(hostId: number, shell: "ssh" | "telnet" = "ssh"): string {
  const ws = location.protocol === "https:" ? "wss" : "ws";
  const q = shell === "telnet" ? "&proto=telnet" : "";
  return `${ws}://${location.host}/api/terminal/${hostId}?token=${encodeURIComponent(token())}${q}`;
}

// presenceURL builds the websocket URL that tells the daemon this tab is open.
export function presenceURL(): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}/api/presence?token=${encodeURIComponent(token())}`;
}

// ---- SFTP browser ----

export interface SftpEntry {
  name: string;
  path: string;
  is_dir: boolean;
  is_link: boolean;
  size: number;
  mode: string;
  mod_time: number;
}

// pwHeader adds the one-shot SSH password header when supplied.
function pwHeader(password?: string): Record<string, string> {
  return password ? { "X-SSH-Password": password } : {};
}

export const sftp = {
  list: (id: number, path: string, password?: string) =>
    request<{ path: string; entries: SftpEntry[] }>(
      "GET",
      `/api/sftp/${id}/list?path=${encodeURIComponent(path)}`,
      { headers: pwHeader(password) },
    ),

  mkdir: (id: number, path: string, password?: string) =>
    request("POST", `/api/sftp/${id}/mkdir`, { body: { path }, headers: pwHeader(password) }),

  rename: (id: number, from: string, to: string, password?: string) =>
    request("POST", `/api/sftp/${id}/rename`, { body: { from, to }, headers: pwHeader(password) }),

  remove: (id: number, path: string, password?: string) =>
    request("DELETE", `/api/sftp/${id}/remove?path=${encodeURIComponent(path)}`, {
      headers: pwHeader(password),
    }),

  disconnect: (id: number) => request("POST", `/api/sftp/${id}/disconnect`),

  async download(id: number, path: string, password?: string): Promise<void> {
    const res = await netFetch(`/api/sftp/${id}/download?path=${encodeURIComponent(path)}`, {
      headers: { "X-Auth-Token": token(), ...pwHeader(password) },
    });
    if (!res.ok) {
      const text = await res.text();
      throw new ApiError(res.status, text ? JSON.parse(text) : {});
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = path.split("/").pop() || "download";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  },

  async upload(id: number, dir: string, file: File, password?: string): Promise<void> {
    const fd = new FormData();
    fd.append("file", file);
    const res = await netFetch(`/api/sftp/${id}/upload?dir=${encodeURIComponent(dir)}`, {
      method: "POST",
      headers: { "X-Auth-Token": token(), ...pwHeader(password) },
      body: fd,
    });
    if (!res.ok) {
      const text = await res.text();
      throw new ApiError(res.status, text ? JSON.parse(text) : {});
    }
  },
};
