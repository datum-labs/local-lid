import type { DialState, NetworkStatus, ServiceState, TransportPeer } from '@/lid';
import type { Tone } from '@/lib/format';

export interface ItemStatus {
  tone: Tone;
  label: string;
}

// One set of rules for the sidebar dot, the detail badge and the map line,
// so the three can never disagree about what state something is in.

export function serviceStatus(sv: ServiceState): ItemStatus {
  if (!sv.desired_active) return { tone: 'muted', label: 'Stopped' };
  if (sv.last_error) return { tone: 'danger', label: `Needs attention (${sv.last_error_stage ?? 'error'})` };
  if (sv.running && sv.ready) return { tone: 'success', label: 'Ready' };
  return { tone: 'warning', label: 'Starting' };
}

export function dialStatus(d: DialState, peer: TransportPeer | undefined): ItemStatus {
  if (!d.desired_active) return { tone: 'muted', label: 'Stopped' };
  if (d.last_error) return { tone: 'danger', label: `Needs attention (${d.last_error_stage ?? 'error'})` };
  if (!d.running) return { tone: 'warning', label: 'Starting' };
  if (peer?.path === 'direct') return { tone: 'success', label: 'Connected · direct' };
  if (peer?.path === 'relay' || peer?.path === 'mixed') return { tone: 'warning', label: `Connected · ${peer.path}` };
  return { tone: 'success', label: 'Listening' };
}

export function networkStatus(n: NetworkStatus): ItemStatus {
  if (n.connected) return { tone: 'success', label: 'Connected' };
  if (n.last_error) return { tone: 'danger', label: n.state || 'error' };
  if (n.desired_attached) return { tone: 'warning', label: n.state || 'Connecting' };
  return { tone: 'muted', label: n.state || 'Inactive' };
}

export const RANK: Record<Tone, number> = { danger: 0, warning: 1, success: 2, muted: 3 };
