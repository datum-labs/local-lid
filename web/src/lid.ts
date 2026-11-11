// Wire types for Local Lid's /api. /api/status is the daemon's /v1/status passed
// through unchanged; field names mirror connect-lib/daemon/src/model.rs and
// api.rs (datum-cloud/connect, feat/connect-daemon @ 61bb05d).

export interface LidInfo {
  hostname: string;
  os: string;
  arch: string;
  version: string;
  started_at_unix_ms: number;
  daemon_url: string;
  daemon_up: boolean;
  portal_base_url: string;
  replay_enabled: boolean;
  projects: string[];
  /** Daemon token id → human name, for tokens Local Lid knows about. */
  token_names: Record<string, string>;
}

export interface ConnectorState {
  name: string;
  uid: string;
  /** iroh public key; this device's "peer id". */
  public_key: string;
}

export interface ServiceState {
  id: string;
  endpoint: string;
  protocol: 'tcp' | 'udp';
  public: boolean;
  hostname: string | null;
  allow: string[];
  desired_active: boolean;
  running: boolean;
  ready: boolean;
  hostnames: string[];
  last_error?: string | null;
  last_error_stage?: string | null;
  /** `setup`, `operate:<token-id>` or `viewer:<token-id>`; "last touched by". */
  last_actor: string;
}

export interface DialState {
  connector: string;
  connector_name?: string | null;
  port: number;
  bind: number;
  local_port?: number | null;
  protocol: 'tcp' | 'udp';
  desired_active: boolean;
  running: boolean;
  last_error?: string | null;
  last_error_stage?: string | null;
  last_actor: string;
}

export interface TransportPeer {
  connector: string;
  path: string; // "direct" | "relay" | "mixed" | …
  remote: string | null;
  rtt_ms: number | null;
}

export interface Transport {
  active_tcp: number;
  active_udp: number;
  bytes_sent: number;
  bytes_received: number;
  errors: number;
  revoked: number;
  datagrams_dropped: number;
  peers: TransportPeer[];
}

export interface ProjectStatus {
  networking: unknown;
  /** null when the project runtime isn't running. */
  transport: Transport | null;
  project: string;
  desired_up: boolean;
  running: boolean;
  enrolled: boolean;
  credential_configured: boolean;
  authentication: { kind: string; session?: string | null } | null;
  connector: ConnectorState | null;
  services: ServiceState[];
  dials: DialState[];
  /** Shape not pinned down yet (Value in api.rs) */
  networks: unknown;
  last_error: string | null;
  last_error_stage: string | null;
}

export class LidError extends Error {
  constructor(
    message: string,
    /** daemon_unreachable | token_rejected | daemon_error | http */
    public kind: string,
  ) {
    super(message);
  }
}

export async function getJSON<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { cache: 'no-store' });
  } catch {
    throw new LidError('Local Lid is not running', 'lid_unreachable');
  }
  if (!res.ok) {
    let body: { error?: string; kind?: string } = {};
    try {
      body = await res.json();
    } catch {
      // non-JSON error body
    }
    throw new LidError(body.error || `HTTP ${res.status}`, body.kind || 'http');
  }
  return res.json();
}

/** "Local Lid", "setup (you)", or "agent (operate 1a2b…)" from the daemon's last_actor. */
export function actorLabel(actor: string | null | undefined, names: Record<string, string>): string {
  if (!actor) return '—';
  if (actor === 'setup') return 'setup (you)';
  const [role, id] = actor.split(':');
  if (id && names[id]) return names[id];
  if (role === 'operate') return `agent (operate ${id?.slice(0, 8)}…)`;
  return actor;
}

/**
 * One entry of status.networks. Built from several places in the Connect daemon
 * (galactic_masque.rs, local_ip.rs, peer_ip.rs, successor managed bindings,
 * and api.rs merge_managed_network_intent), so only the common fields are
 * guaranteed; the rest depend on mode.
 */
export interface NetworkStatus {
  network: string;
  mode: 'gateway' | 'peer' | string;
  state: string;
  running: boolean;
  connected: boolean;
  desired_attached?: boolean;
  persistent?: boolean;
  ephemeral?: boolean;
  prototype?: boolean;
  project?: string;
  binding_name?: string;
  gateway?: string;
  gateway_resource?: string;
  gateway_location?: string | null;
  peer?: string;
  assigned_address?: string | null;
  peer_address?: string | null;
  routes?: string[] | null;
  interface_name?: string;
  mtu?: number;
  packets_sent?: number;
  packets_received?: number;
  last_error?: string | null;
  last_error_stage?: string | null;
}

export function networksOf(s: ProjectStatus | null): NetworkStatus[] {
  return Array.isArray(s?.networks) ? (s!.networks as NetworkStatus[]) : [];
}

/**
 * Datum portal for the environment the daemon is logged in to. the daemon's
 * status says `user@api.staging.env.datum.net`; the portal is the same host
 * with `api.` → `cloud.`. Falls back to Local Lid's configured URL.
 */
export function portalBase(s: ProjectStatus | null, fallback: string): string {
  const session = s?.authentication?.session ?? '';
  const host = session.slice(session.lastIndexOf('@') + 1);
  if (host.startsWith('api.')) return `https://cloud.${host.slice(4)}`;
  return fallback;
}

export function isAgentActor(actor: string | null | undefined): boolean {
  return !!actor && actor.startsWith('operate:');
}

/** The transport peer entry for a dial (the daemon reports path/RTT per dial connector). */
export function peerForDial(s: ProjectStatus | null, d: DialState): TransportPeer | undefined {
  return s?.transport?.peers.find((p) => p.connector === d.connector);
}

export function dialName(d: DialState): string {
  return `${d.connector_name || d.connector.slice(0, 12) + '…'}:${d.port}`;
}

/** The endpoint people know: the original target when the service is inspected. */
export function serviceName(sv: ServiceState, inspected?: InspectView): string {
  return sv.hostname || inspected?.original.endpoint || sv.endpoint;
}

// ---- Inspector ----

/** One inspected service: Local Lid re-created it to point at its hop. */
export interface InspectView {
  project: string;
  original_id: string;
  original: { endpoint: string; protocol: string; public: boolean; hostname?: string | null; allow: string[] };
  /** The service id now pointing at the hop (what status.services shows). */
  service_id: string;
  listen: string;
  target: string;
  enabled_at_unix_ms: number;
  exchanges: number;
  running: boolean;
}

export interface ExchangeSummary {
  id: string;
  timestamp_unix_ms: number;
  method: string;
  path: string;
  response_status: number | null;
  duration_ms: number | null;
}

export type Header = [string, string];

export interface CapturedMessage {
  headers: Header[];
  body: string;
  body_truncated: boolean;
}

export interface Exchange extends ExchangeSummary {
  host: string;
  request: CapturedMessage;
  response: CapturedMessage | null;
  error?: string;
}

export interface ReplayResult {
  status: number;
  headers: Header[];
  body: string;
}

export async function postJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { method: 'POST' });
  if (!res.ok) {
    let body: { error?: string } = {};
    try {
      body = await res.json();
    } catch {
      // non-JSON error body
    }
    throw new LidError(body.error || `HTTP ${res.status}`, 'http');
  }
  return res.json();
}

// ---- Notes and logs ----

export interface Note {
  text: string;
  set_at_unix_ms: number;
}

/** Note keys match the CLI: services by endpoint (ids change on re-create). */
export const noteKey = {
  service: (sv: ServiceState, inspected?: InspectView) => `service:${inspected?.original.endpoint ?? sv.endpoint}`,
  // The daemon keys dials by local bind port; two dials can share a remote port.
  dial: (d: DialState) => `dial:${d.bind}`,
  network: (n: NetworkStatus) => `network:${n.network}`,
};

export interface LogSource {
  name: string;
  path: string;
  exists: boolean;
  size_bytes: number | null;
}
