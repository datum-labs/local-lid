/**
 * The right-hand pane shows exactly one thing at a time. Keys are what
 * the Connect daemon uses: service id, dial port (unique per project on this
 * device), network name.
 */
export type Selection =
  | { kind: 'service'; id: string }
  /** Dials are keyed by their local bind port, as the daemon keys them (not the remote port). */
  | { kind: 'dial'; bind: number }
  | { kind: 'network'; name: string }
  | { kind: 'log'; name: string }
  | { kind: 'device' }
  | { kind: 'map' }
  | null;

export type SidebarTab = 'services' | 'dials' | 'networks' | 'logs';
export const TABS = ['services', 'dials', 'networks', 'logs'] as const;

export function sameSelection(a: Selection, b: Selection): boolean {
  if (!a || !b || a.kind !== b.kind) return false;
  if (a.kind === 'service' && b.kind === 'service') return a.id === b.id;
  if (a.kind === 'dial' && b.kind === 'dial') return a.bind === b.bind;
  if (a.kind === 'network' && b.kind === 'network') return a.name === b.name;
  if (a.kind === 'log' && b.kind === 'log') return a.name === b.name;
  return true;
}
