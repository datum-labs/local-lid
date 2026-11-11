import { MAP_HELP } from '@/lib/help';
import { DetailPane } from '@/components/DetailPane';
import { dialName, networksOf, peerForDial, type InspectView, type Note, type ProjectStatus, noteKey } from '@/lid';
import type { Selection } from '@/lib/selection';
import { dialStatus, networkStatus, serviceStatus, type ItemStatus } from '@/lib/status';
import { Card } from '@datum-cloud/datum-ui/card';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
import { Text } from '@datum-cloud/datum-ui/typography';
import { cn } from '@datum-cloud/datum-ui/utils';
import { humanizeRtt } from '@/lib/format';
import { useEffect, useRef } from 'react';

/**
 * Subway map of this device on the Connect daemon. Adapted from the old
 * NetworkMap: same stations/lines/interchange drawing, new data.
 *
 * Left of the hub: services (traffic coming in). Right: dials and VPC
 * networks (traffic going out). Line color says how healthy it is; VPC
 * networks get their own color and a "Datum VPC" interchange.
 */
type LineColor = 'hub' | 'good' | 'warn' | 'bad' | 'vpc' | 'dim';

const STROKE: Record<LineColor, string> = {
  hub: 'stroke-violet-400',
  good: 'stroke-green-500',
  warn: 'stroke-amber-400',
  bad: 'stroke-red-500',
  vpc: 'stroke-sky-400',
  dim: 'stroke-muted-foreground',
};
const SWATCH: Record<LineColor, string> = {
  hub: 'bg-violet-400',
  good: 'bg-green-500',
  warn: 'bg-amber-400',
  bad: 'bg-red-500',
  vpc: 'bg-sky-400',
  dim: 'bg-muted-foreground',
};
const LEGEND: [LineColor, string][] = [
  ['hub', 'This device'],
  ['good', 'Ready / direct'],
  ['warn', 'Via relay / starting'],
  ['bad', 'Needs attention'],
  ['vpc', 'VPC network (connected)'],
  ['dim', 'Stopped / inactive'],
];

function lineColor(st: ItemStatus, vpc = false): LineColor {
  if (st.tone === 'danger') return 'bad';
  if (st.tone === 'muted') return 'dim';
  if (st.tone === 'warning') return 'warn';
  return vpc ? 'vpc' : 'good';
}

interface Station {
  key: string;
  label: string;
  caption: string;
  color: LineColor;
  target: Selection;
}
interface Block {
  kind: 'spoke' | 'interchange';
  side: 'left' | 'right';
  title: string;
  items: Station[];
  interchangeLabel?: string;
  top: number;
  midY: number;
}

export function LidMap({
  s,
  notes,
  inspected = {},
  onSelect,
}: {
  s: ProjectStatus;
  notes?: Record<string, Note>;
  inspected?: Record<string, InspectView>;
  onSelect: (t: Selection) => void;
}) {
  const services: Station[] = s.services.map((sv) => {
    const st = serviceStatus(sv);
    return {
      key: `service:${sv.id}`,
      label: sv.hostname || inspected[sv.id]?.original.endpoint || sv.endpoint,
      caption: notes?.[noteKey.service(sv, inspected[sv.id])]?.text || `${sv.public ? 'public' : `private · ${sv.allow.length} peer${sv.allow.length === 1 ? '' : 's'}`}${inspected[sv.id] ? ' · inspected' : ''} · ${st.label}`,
      color: lineColor(st),
      target: { kind: 'service', id: sv.id },
    };
  });
  const dials: Station[] = s.dials.map((d) => {
    const peer = peerForDial(s, d);
    const st = dialStatus(d, peer);
    return {
      key: `dial:${d.bind}`,
      label: dialName(d),
      caption:
        notes?.[noteKey.dial(d)]?.text ||
        `local :${d.local_port ?? d.bind}${peer ? ` · ${peer.path}${peer.rtt_ms != null ? ` · ${humanizeRtt(peer.rtt_ms)}` : ''}` : ''}`,
      color: lineColor(st),
      target: { kind: 'dial', bind: d.bind },
    };
  });
  const networks: Station[] = networksOf(s).map((n) => {
    const st = networkStatus(n);
    return {
      key: `network:${n.network}`,
      label: n.network,
      // The VPC's routes say what this line reaches; the device's own /128 is on the detail page.
      caption: notes?.[noteKey.network(n)]?.text || [n.routes?.[0] ?? n.assigned_address, n.gateway_location, st.label].filter(Boolean).join(' · '),
      color: lineColor(st, true),
      target: { kind: 'network', name: n.network },
    };
  });

  const flash = useChangeFlash([...services, ...dials, ...networks]);
  const hubLabel = s.connector?.name ?? 'This device';

  if (!services.length && !dials.length && !networks.length) {
    return (
      <DetailPane title="Network map">
        <EmptyContent title="Nothing shared, dialed or joined on this device yet. Services, dials and networks appear here live." />
      </DetailPane>
    );
  }

  const ROW_H = 64;
  const TOP_PAD = 48;
  const BLOCK_GAP = 48;
  const HUB_X = 0;
  const INTERCHANGE_X = 170;
  const R_STATION_X = 300;
  const R_LABEL_X = 318;
  const L_STATION_X = -220;
  const L_LABEL_X = -238;
  const VIEW_MIN_X = -470;
  const VIEW_MAX_X = 660;

  const blocks: Block[] = [];
  const push = (b: Omit<Block, 'top' | 'midY'>) => b.items.length && blocks.push({ ...b, top: 0, midY: 0 });
  push({ kind: 'spoke', side: 'left', title: 'Services (in)', items: services });
  push({ kind: 'spoke', side: 'right', title: 'Dials (out)', items: dials });
  push({ kind: 'interchange', side: 'right', title: 'Networks (out)', items: networks, interchangeLabel: 'Datum VPC' });

  const sideHeight = (side: Block['side']) => {
    const bs = blocks.filter((b) => b.side === side);
    return Math.max(bs.reduce((h, b) => h + b.items.length * ROW_H + BLOCK_GAP, 0) - BLOCK_GAP, 0);
  };
  const totalH = Math.max(Math.max(sideHeight('right'), sideHeight('left')) + TOP_PAD * 2, 200);
  const hubY = totalH / 2;
  for (const side of ['left', 'right'] as const) {
    let y = hubY - sideHeight(side) / 2;
    for (const b of blocks.filter((x) => x.side === side)) {
      b.top = y;
      b.midY = y + (b.items.length * ROW_H) / 2;
      y += b.items.length * ROW_H + BLOCK_GAP;
    }
  }
  const stationY = (b: Block, i: number) => b.top + i * ROW_H + ROW_H / 2;

  return (
    <DetailPane title="Network map" description={MAP_HELP}>
      <Card size="sm" className="gap-4 p-4">
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {LEGEND.map(([color, label]) => (
            <span key={color} className="flex items-center gap-1.5">
              <span className={cn('inline-block h-1 w-4 rounded-full', SWATCH[color])} />
              <Text size="xs" textColor="muted">
                {label}
              </Text>
            </span>
          ))}
        </div>
        {/* Never scaled below ~0.8x, so labels stay readable; narrow windows scroll sideways instead. */}
        <div className="overflow-x-auto">
        <svg
          viewBox={`${VIEW_MIN_X} 0 ${VIEW_MAX_X - VIEW_MIN_X} ${totalH}`}
          preserveAspectRatio="xMidYMid meet"
          className="block h-auto w-full"
          style={{ minWidth: Math.round((VIEW_MAX_X - VIEW_MIN_X) * 0.8) }}
          role="img"
          aria-label="Network map">
          {blocks.map((b) => {
            const left = b.side === 'left';
            const stationX = left ? L_STATION_X : R_STATION_X;
            const fromX = b.kind === 'interchange' ? INTERCHANGE_X : HUB_X;
            const fromY = b.kind === 'interchange' ? b.midY : hubY;
            return (
              <g key={`lines:${b.title}`}>
                <text
                  x={stationX}
                  y={b.top - 10}
                  textAnchor={left ? 'end' : 'start'}
                  className="fill-muted-foreground text-[12px] tracking-wider uppercase">
                  {b.title}
                </text>
                {b.kind === 'interchange' && (
                  <polyline points={`${HUB_X},${hubY} ${INTERCHANGE_X},${b.midY}`} className={STROKE.vpc} strokeWidth={5} fill="none" strokeLinecap="round" />
                )}
                {b.items.map((item, i) => (
                  <polyline
                    key={`${item.key}:${item.color}`}
                    points={`${fromX},${fromY} ${stationX},${stationY(b, i)}`}
                    className={cn(STROKE[item.color], flash(item) && 'map-flash-line')}
                    strokeWidth={5}
                    fill="none"
                    strokeLinecap="round"
                    opacity={item.color === 'dim' ? 0.4 : 1}
                  />
                ))}
              </g>
            );
          })}

          <circle cx={HUB_X} cy={hubY} r={11} className={cn('fill-background', STROKE.hub)} strokeWidth={4} />
          <text x={HUB_X} y={hubY - 22} textAnchor="middle" className="fill-violet-400 text-[15px] font-semibold">
            {hubLabel}
          </text>

          {blocks.map((b) => (
            <g key={`stations:${b.title}`}>
              {b.kind === 'interchange' && (
                <>
                  <circle cx={INTERCHANGE_X} cy={b.midY} r={11} className={cn('fill-card', STROKE.vpc)} strokeWidth={4} />
                  <circle cx={INTERCHANGE_X} cy={b.midY} r={5} className="fill-sky-400" />
                  <text x={INTERCHANGE_X} y={b.midY - 20} textAnchor="middle" className="fill-foreground text-xs">
                    {b.interchangeLabel}
                  </text>
                </>
              )}
              {b.items.map((item, i) => {
                const y = stationY(b, i);
                const left = b.side === 'left';
                return (
                  <g
                    key={`${item.key}:${item.color}`}
                    role="button"
                    tabIndex={0}
                    aria-label={`Open ${item.label}`}
                    className={cn('group cursor-pointer outline-hidden', flash(item) && 'map-flash-station')}
                    onClick={() => onSelect(item.target)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onSelect(item.target);
                      }
                    }}>
                    <circle
                      cx={left ? L_STATION_X : R_STATION_X}
                      cy={y}
                      r={9}
                      className={cn('fill-background group-hover:brightness-125', STROKE[item.color])}
                      strokeWidth={4}
                    />
                    <text
                      x={left ? L_LABEL_X : R_LABEL_X}
                      y={y - 8}
                      textAnchor={left ? 'end' : 'start'}
                      className="fill-foreground group-hover:fill-sky-400 group-focus-visible:fill-sky-400 text-[15px] font-medium">
                      {item.label}
                    </text>
                    <title>{`${item.label}: ${item.caption}`}</title>
                    <text
                      x={left ? L_LABEL_X : R_LABEL_X}
                      y={y + 14}
                      textAnchor={left ? 'end' : 'start'}
                      className="fill-muted-foreground font-mono text-[12px]">
                      {shorten(item.caption)}
                    </text>
                  </g>
                );
              })}
            </g>
          ))}
        </svg>
        </div>
      </Card>
    </DetailPane>
  );
}

/** Map captions share the line with long notes; keep them to one readable line. */
const CAPTION_MAX = 34;
function shorten(text: string): string {
  return text.length > CAPTION_MAX ? `${text.slice(0, CAPTION_MAX - 1).trimEnd()}…` : text;
}

/** One-shot flash when a station's color changes between polls; never on first sight. */
function useChangeFlash(stations: Station[]) {
  const lastColor = useRef(new Map<string, LineColor>());
  const flashed = useRef(new Set<string>());
  for (const s of stations) {
    const prev = lastColor.current.get(s.key);
    if (prev !== undefined && prev !== s.color) flashed.current.add(`${s.key}:${s.color}`);
  }
  useEffect(() => {
    for (const s of stations) lastColor.current.set(s.key, s.color);
  });
  return (s: Station) => flashed.current.has(`${s.key}:${s.color}`);
}
