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
  port: number;
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
    identity_file: "",
    proxy_jump: "",
    extra_options: {},
    group_id: null,
    notes: "",
    tags: [],
    has_password: false,
  };
}
