export interface Group {
  id: number;
  name: string;
  parent_id: number | null;
  sort: number;
}

export interface Host {
  id: number;
  alias: string;
  hostname: string;
  user: string;
  // port is the SSH port and also the switch for every SSH-backed action:
  // 0 means "no SSH on this host" and the card hides those buttons. The other
  // *_port fields are optional services; 0 means the host does not expose it.
  port: number;
  telnet_port: number;
  pve_port: number;
  pbs_port: number;
  http_port: number;
  https_port: number;
  identity_file: string;
  proxy_jump: string;
  extra_options: Record<string, string> | null;
  group_id: number | null;
  notes: string;
  tags: string[];
  created_at?: string;
  updated_at?: string;
  // Password is write-only: has_password reflects whether one is saved server-side.
  has_password?: boolean;
  password?: string;
  clear_password?: boolean;
}

export interface Key {
  id: number;
  name: string;
  type: string;
  public_key: string;
  private_path: string;
  has_passphrase: boolean;
  deployed_hosts: number[];
  in_ssh?: boolean;
  ssh_path?: string;
}

export interface KnownHost {
  id: number;
  marker: string;
  hosts: string;
  key_type: string;
  key_data: string;
  comment: string;
  in_ssh?: boolean;
  fingerprint?: string;
}

export interface Mount {
  mountpoint: string;
  source: string;
}

// ---- network scan (POST /api/scan) ----

export interface ScanService {
  port: number;
  name: string; // "ssh" | "telnet" | "tcp"
  banner?: string;
}

export interface ScanHost {
  address: string;
  hostname?: string; // reverse DNS, may be absent
  services: ScanService[];
}

export interface ScanResult {
  hosts: ScanHost[];
  scanned: number;
  ports: number[];
  elapsed_ms: number;
}

// DISCOVERY_PORTS are probed when working out what a host runs: the services a
// card can act on. Mirrors netscan.DefaultPorts on the server.
export const DISCOVERY_PORTS = [22, 23, 80, 443, 8006, 8007];

// HostPorts is the port block of a Host — everything a scan can determine.
export type HostPorts = Pick<
  Host,
  "port" | "telnet_port" | "pve_port" | "pbs_port" | "http_port" | "https_port"
>;

// portsFromScan maps a scanned host's open services onto the port fields. A
// service that did not answer stays 0, which is exactly what hides its button.
export function portsFromScan(h: ScanHost): HostPorts {
  const of = (name: string) => h.services.find((s) => s.name === name)?.port ?? 0;
  return {
    port: of("ssh"),
    telnet_port: of("telnet"),
    pve_port: of("pve"),
    pbs_port: of("pbs"),
    http_port: of("http"),
    https_port: of("https"),
  };
}

// serviceKind names the web UIs a host card can open. Kept in sync with
// serviceURL in internal/server/handlers_services.go.
export type ServiceKind = "pve" | "pbs" | "http" | "https";

// serviceUrl mirrors the server's URL construction for tooltips and link text.
// The server builds the real URL it opens; this is display only. The port is
// dropped only when the scheme already implies it — :8006 must stay on a PVE
// URL or the link would point at 443.
export function serviceUrl(h: Host, kind: ServiceKind): string {
  const host = h.hostname || h.alias;
  const spec: Record<ServiceKind, { scheme: "http" | "https"; port: number }> = {
    pve: { scheme: "https", port: h.pve_port },
    pbs: { scheme: "https", port: h.pbs_port },
    http: { scheme: "http", port: h.http_port },
    https: { scheme: "https", port: h.https_port },
  };
  const { scheme, port } = spec[kind];
  const implied = scheme === "http" ? 80 : 443;
  return port === implied ? `${scheme}://${host}/` : `${scheme}://${host}:${port}/`;
}

export interface AppState {
  hosts: Host[];
  groups: Group[];
  tags: string[];
  keys: Key[];
  known_hosts: KnownHost[];
  settings: Record<string, string>;
  mounts: Mount[];
}

export function emptyHost(): Host {
  return {
    id: 0,
    alias: "",
    hostname: "",
    user: "",
    port: 22,
    telnet_port: 0,
    pve_port: 0,
    pbs_port: 0,
    http_port: 0,
    https_port: 0,
    identity_file: "",
    proxy_jump: "",
    extra_options: {},
    group_id: null,
    notes: "",
    tags: [],
    has_password: false,
  };
}
