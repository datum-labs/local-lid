import { FOOTER_HELP, TAB_HELP } from '@/lib/help';
import { ListToolbar } from '@/components/ListToolbar';
import { NavItem } from '@/components/NavItem';
import { NavEmpty } from '@/components/NavText';
import { TextCopy } from '@/components/TextCopy';
import type { Lid } from '@/hooks/useLid';
import { useStoredState } from '@/hooks/useStoredState';
import { actorLabel, dialName, isAgentActor, networksOf, peerForDial, serviceName, type InspectView, type Note, type ProjectStatus, noteKey } from '@/lid';
import { humanizeRtt } from '@/lib/format';
import { dialStatus, networkStatus, RANK, serviceStatus } from '@/lib/status';
import { sameSelection, type Selection, type SidebarTab } from '@/lib/selection';
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuSkeleton,
} from '@datum-cloud/datum-ui/sidebar';
import { Tabs, TabsList, TabsTrigger } from '@datum-cloud/datum-ui/tabs';
import { Tooltip } from '@datum-cloud/datum-ui/tooltip';
import { cn } from '@datum-cloud/datum-ui/utils';
import { BotIcon, ChevronRightIcon, GlobeIcon, LaptopIcon, ScanEyeIcon, StickyNoteIcon, WaypointsIcon } from 'lucide-react';
import { LogList } from '@/components/LogView';
import { useState } from 'react';

const SORTS = { status: 'Status', name: 'Name' } as const;
type Sort = keyof typeof SORTS;

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true });
function matches(query: string, ...fields: (string | null | undefined)[]): boolean {
  const q = query.trim().toLowerCase();
  return !q || fields.some((f) => f?.toLowerCase().includes(q));
}

export function LidSidebar({
  lid,
  tab,
  onTabChange,
  selection,
  onSelect,
}: {
  lid: Lid;
  tab: SidebarTab;
  onTabChange: (tab: SidebarTab) => void;
  selection: Selection;
  onSelect: (selection: Selection) => void;
}) {
  const [sort, setSort] = useStoredState<Sort>('lid_sort', 'status', ['status', 'name']);
  const [queries, setQueries] = useState<Record<SidebarTab, string>>({ services: '', dials: '', networks: '', logs: '' });
  const query = queries[tab];
  const setQuery = (q: string) => setQueries((prev) => ({ ...prev, [tab]: q }));

  const s = lid.status.status === 'ok' ? lid.status.data : null;
  const names = lid.info.status === 'ok' ? lid.info.data.token_names : {};
  const counts: Record<SidebarTab, number | null> = {
    services: s ? s.services.length : null,
    dials: s ? s.dials.length : null,
    networks: s ? networksOf(s).length : null,
    logs: lid.logs ? lid.logs.length : null,
  };

  return (
    <Sidebar collapsible="none" className="h-full">
      <SidebarHeader className="gap-3 border-b p-0 pt-3">
        <Tabs value={tab} onValueChange={(v) => onTabChange(v as SidebarTab)} className="px-2">
          <TabsList variant="line" className="w-full">
            <Tab value="services" label="Services" count={counts.services} />
            <Tab value="dials" label="Dials" count={counts.dials} />
            <Tab value="networks" label="Networks" count={counts.networks} />
            <Tab value="logs" label="Logs" count={counts.logs} />
          </TabsList>
        </Tabs>
        {(s || tab === 'logs') && (
          <ListToolbar
            query={query}
            onQueryChange={setQuery}
            placeholder={`Search ${tab}…`}
            sort={{ value: sort, onChange: setSort, options: SORTS, label: `Sort ${tab}` }}
          />
        )}
      </SidebarHeader>

      <SidebarContent className="px-2 py-2">
        {tab === 'logs' ? (
          <Logs lid={lid} query={query} selection={selection} onSelect={onSelect} />
        ) : (
        <Gate lid={lid}>
          {(s) =>
            tab === 'services' ? (
              <Services s={s} names={names} inspected={lid.inspected} notes={lid.notes} query={query} sort={sort} selection={selection} onSelect={onSelect} />
            ) : tab === 'dials' ? (
              <Dials s={s} names={names} notes={lid.notes} query={query} sort={sort} selection={selection} onSelect={onSelect} />
            ) : (
              <Networks s={s} notes={lid.notes} query={query} sort={sort} selection={selection} onSelect={onSelect} />
            )
          }
        </Gate>
        )}
      </SidebarContent>

      <SidebarFooter className="gap-2 border-t px-2 py-2">
        <MapButton lid={lid} active={selection?.kind === 'map'} onSelect={() => onSelect({ kind: 'map' })} />
        <DeviceButton lid={lid} active={selection?.kind === 'device'} onSelect={() => onSelect({ kind: 'device' })} />
      </SidebarFooter>
    </Sidebar>
  );
}

function Gate({ lid, children }: { lid: Lid; children: (s: ProjectStatus) => React.ReactNode }) {
  const { status, info } = lid;
  if (info.status === 'ok' && !info.data.projects.length)
    return <NavEmpty title="No project configured. Run local-lid init --project <id>." />;
  if (status.status === 'idle')
    return (
      <div className="flex flex-col gap-1">
        <SidebarMenuSkeleton showIcon />
        <SidebarMenuSkeleton showIcon />
      </div>
    );
  if (status.status === 'error')
    return (
      <NavEmpty
        title={status.kind === 'daemon_unreachable' ? 'Connect daemon unreachable. Is datum-connectd running?' : status.error}
        error
      />
    );
  return <>{children(status.data)}</>;
}

interface ListProps {
  s: ProjectStatus;
  notes: Record<string, Note>;
  query: string;
  sort: Sort;
  selection: Selection;
  onSelect: (selection: Selection) => void;
}

function AgentBadge({ actor, names }: { actor: string; names: Record<string, string> }) {
  if (!isAgentActor(actor)) return null;
  const who = actorLabel(actor, names);
  return (
    <Tooltip message={`Last changed by ${who}`}>
      <span className="text-warning flex items-center" aria-label={`Last changed by ${who}`}>
        <BotIcon className="size-3.5" />
      </span>
    </Tooltip>
  );
}

function Services({
  s,
  notes,
  names,
  inspected,
  query,
  sort,
  selection,
  onSelect,
}: ListProps & { names: Record<string, string>; inspected: Record<string, InspectView> }) {
  if (!s.services.length) return <NavEmpty title="No services. Share one with datumctl connect serve." />;
  const shown = s.services
    .filter((sv) => matches(query, sv.id, sv.endpoint, inspected[sv.id]?.original.endpoint, sv.hostname, ...sv.hostnames))
    .sort((a, b) =>
      sort === 'status'
        ? RANK[serviceStatus(a).tone] - RANK[serviceStatus(b).tone] || byName(serviceName(a, inspected[a.id]), serviceName(b, inspected[b.id]))
        : byName(serviceName(a, inspected[a.id]), serviceName(b, inspected[b.id])),
    );
  if (!shown.length) return <NavEmpty title={`No services match “${query}”.`} />;
  return (
    <SidebarMenu className="gap-0.5">
      {shown.map((sv) => (
        <NavItem
          key={sv.id}
          active={sameSelection(selection, { kind: 'service', id: sv.id })}
          onSelect={() => onSelect({ kind: 'service', id: sv.id })}
          status={serviceStatus(sv)}
          title={<span className="font-mono text-xs">{inspected[sv.id]?.original.endpoint ?? sv.endpoint}</span>}
          trailing={
            <>
              {sv.public && (
                <Tooltip message="Public">
                  <GlobeIcon className="text-muted-foreground size-3.5" aria-label="Public" />
                </Tooltip>
              )}
              {inspected[sv.id] && (
                <Tooltip message={`Inspected: ${inspected[sv.id].exchanges} request${inspected[sv.id].exchanges === 1 ? '' : 's'} captured`}>
                  <ScanEyeIcon className="size-3.5 text-green-500" aria-label="Inspected" />
                </Tooltip>
              )}
              <span className="text-muted-foreground font-mono text-[10px] uppercase">{sv.protocol}</span>
              <AgentBadge actor={sv.last_actor} names={names} />
            </>
          }
          subtitle={
            sv.hostnames[0] ? (
              <TextCopy value={sv.hostnames[0]} />
            ) : (
              <span>{sv.public ? '(no hostname yet)' : `private · ${sv.allow.length} allowed peer${sv.allow.length === 1 ? '' : 's'}`}</span>
            )
          }
          meta={<NoteLine note={notes[noteKey.service(sv, inspected[sv.id])]} fallback={<span className="font-mono">{sv.id}</span>} />}
        />
      ))}
    </SidebarMenu>
  );
}

function Dials({ s, notes, names, query, sort, selection, onSelect }: ListProps & { names: Record<string, string> }) {
  if (!s.dials.length) return <NavEmpty title="No dials. Reach another connector with datumctl connect dial." />;
  const shown = s.dials
    .filter((d) => matches(query, d.connector, d.connector_name, String(d.port), String(d.local_port ?? '')))
    .sort((a, b) =>
      sort === 'status'
        ? RANK[dialStatus(a, peerForDial(s, a)).tone] - RANK[dialStatus(b, peerForDial(s, b)).tone] ||
          byName(dialName(a), dialName(b))
        : byName(dialName(a), dialName(b)),
    );
  if (!shown.length) return <NavEmpty title={`No dials match “${query}”.`} />;
  return (
    <SidebarMenu className="gap-0.5">
      {shown.map((d) => {
        const peer = peerForDial(s, d);
        return (
          <NavItem
            key={d.bind}
            active={sameSelection(selection, { kind: 'dial', bind: d.bind })}
            onSelect={() => onSelect({ kind: 'dial', bind: d.bind })}
            status={dialStatus(d, peer)}
            title={dialName(d)}
            trailing={
              <>
                <span className="text-muted-foreground font-mono text-[11px]">
                  {peer?.rtt_ms != null ? humanizeRtt(peer.rtt_ms) : (peer?.path ?? '')}
                </span>
                <AgentBadge actor={d.last_actor} names={names} />
              </>
            }
            meta={<NoteLine note={notes[noteKey.dial(d)]} />}
            subtitle={
              <>
                <span className="shrink-0">local</span>
                <TextCopy value={`127.0.0.1:${d.local_port ?? d.bind}`} />
              </>
            }
          />
        );
      })}
    </SidebarMenu>
  );
}

function Networks({ s, notes, query, sort, selection, onSelect }: ListProps) {
  const all = networksOf(s);
  if (!all.length) return <NavEmpty title="No networks. Join a VPC with datumctl connect join." />;
  const shown = all
    .filter((n) => matches(query, n.network, n.mode, n.assigned_address, n.gateway_location ?? ''))
    .sort((a, b) =>
      sort === 'status'
        ? RANK[networkStatus(a).tone] - RANK[networkStatus(b).tone] || byName(a.network, b.network)
        : byName(a.network, b.network),
    );
  if (!shown.length) return <NavEmpty title={`No networks match “${query}”.`} />;
  return (
    <SidebarMenu className="gap-0.5">
      {shown.map((n) => (
        <NavItem
          key={n.network}
          active={sameSelection(selection, { kind: 'network', name: n.network })}
          onSelect={() => onSelect({ kind: 'network', name: n.network })}
          status={networkStatus(n)}
          title={n.network}
          trailing={<span className="text-muted-foreground font-mono text-[10px] uppercase">{n.mode}</span>}
          subtitle={<span className="font-mono">{n.routes?.[0] ?? n.assigned_address ?? n.state}</span>}
          meta={<NoteLine note={notes[noteKey.network(n)]} fallback={n.gateway_location ? <span>via {n.gateway_location}</span> : undefined} />}
        />
      ))}
    </SidebarMenu>
  );
}

/** The map is a page, not a list, so it opens from the footer like the Device page. */
function MapButton({ lid, active, onSelect }: { lid: Lid; active: boolean; onSelect: () => void }) {
  const s = lid.status.status === 'ok' ? lid.status.data : null;
  const parts = s
    ? [`${s.services.length} in`, `${s.dials.length + networksOf(s).length} out`]
    : [FOOTER_HELP.map];
  return (
    <FooterButton
      active={active}
      onSelect={onSelect}
      title={FOOTER_HELP.map}
      icon={<WaypointsIcon className="text-muted-foreground size-4 shrink-0" />}>
      <span className="truncate text-sm font-medium">Network map</span>
      <span className="text-muted-foreground truncate text-[11px]">{parts.join(' · ')}</span>
    </FooterButton>
  );
}

function FooterButton({
  active,
  onSelect,
  title,
  icon,
  children,
}: {
  active: boolean;
  onSelect: () => void;
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onSelect}
      aria-current={active || undefined}
      data-active={active}
      className={cn(
        'flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left outline-hidden',
        'hover:bg-sidebar-accent focus-visible:ring-sidebar-ring focus-visible:ring-2',
        'data-[active=true]:bg-sidebar-accent data-[active=true]:shadow-[inset_2px_0_0_var(--primary)]',
      )}>
      {icon}
      <span className="flex min-w-0 flex-1 flex-col">{children}</span>
      <ChevronRightIcon className="text-muted-foreground size-4 shrink-0" />
    </button>
  );
}

function Tab({ value, label, count }: { value: SidebarTab; label: string; count: number | null }) {
  return (
    // The tooltip goes inside the trigger: datum-ui's Tooltip wraps its child
    // in an inline span, which would stop the tabs stretching to fill the bar.
    <TabsTrigger value={value} className="flex-1 text-xs">
      <Tooltip message={TAB_HELP[value]} side="bottom">
        <span className="flex items-center gap-1.5">
          {label}
          {count != null && <span className="text-muted-foreground font-mono text-[11px] tabular-nums">{count}</span>}
        </span>
      </Tooltip>
    </TabsTrigger>
  );
}

function DeviceButton({ lid, active, onSelect }: { lid: Lid; active: boolean; onSelect: () => void }) {
  if (lid.info.status !== 'ok') return null;
  const i = lid.info.data;
  const connector = lid.status.status === 'ok' ? lid.status.data.connector?.name : undefined;
  return (
    <FooterButton
      active={active}
      onSelect={onSelect}
      title={FOOTER_HELP.device}
      icon={<LaptopIcon className="text-muted-foreground size-4 shrink-0" />}>
      <span className="truncate text-sm font-medium">{connector ?? i.hostname}</span>
      <span className="text-muted-foreground truncate text-[11px]">
        {i.hostname} · {i.os} · {i.arch}
      </span>
    </FooterButton>
  );
}

function NoteLine({ note, fallback }: { note?: Note; fallback?: React.ReactNode }) {
  if (!note) return fallback ? <>{fallback}</> : null;
  return (
    <span className="flex min-w-0 items-center gap-1">
      <StickyNoteIcon className="size-3 shrink-0" />
      <span className="truncate">{note.text}</span>
    </span>
  );
}

function Logs({
  lid,
  query,
  selection,
  onSelect,
}: {
  lid: Lid;
  query: string;
  selection: Selection;
  onSelect: (selection: Selection) => void;
}) {
  if (!lid.logs)
    return (
      <div className="flex flex-col gap-1">
        <SidebarMenuSkeleton showIcon />
      </div>
    );
  if (!lid.logs.length) return <NavEmpty title="No logs registered. Add one with local-lid logs add <name> <path>." />;
  const shown = lid.logs.filter((l) => matches(query, l.name, l.path));
  if (!shown.length) return <NavEmpty title={`No logs match “${query}”.`} />;
  return <LogList sources={shown} selection={selection} onSelect={(name) => onSelect({ kind: 'log', name })} />;
}
