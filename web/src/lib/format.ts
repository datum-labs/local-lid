export type Tone = 'success' | 'warning' | 'danger' | 'muted';

export function humanizeBytes(bytes: number | null | undefined): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  if (!bytes) return '0 B';
  let size = bytes;
  let i = 0;
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024;
    i++;
  }
  return `${size.toFixed(1)} ${units[i]}`;
}

export function statusTone(code: number | null): Tone {
  if (code == null) return 'muted';
  if (code >= 200 && code < 300) return 'success';
  if (code >= 400) return 'danger';
  return 'muted';
}

/**
 * conn_type comes straight from iroh (Endpoint::conn_type) — "direct" means
 * a real hole-punched peer-to-peer path, "relay"/"mixed" mean it's bouncing
 * through (or partly through) a relay server.
 */
export function connTypeTone(connType: string | null | undefined): Tone {
  if (connType === 'direct') return 'success';
  if (connType === 'relay' || connType === 'mixed') return 'warning';
  return 'muted';
}

export function shortId(id: string): string {
  return `${id.slice(0, 16)}…`;
}

export function humanizeDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m`;
  return `${s}s`;
}

/** "<1 ms", "12 ms", or "—". */
export function humanizeRtt(ms: number | null | undefined): string {
  if (ms == null) return '—';
  return ms < 1 ? '<1 ms' : `${Math.round(ms)} ms`;
}
