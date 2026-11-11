import { SECTION_HELP } from '@/lib/help';
import { DetailPane, DetailSection, StatRow } from '@/components/DetailPane';
import { KeyValueCard } from '@/components/KeyValueCard';
import { StatCard } from '@/components/StatCard';
import { TextCopy } from '@/components/TextCopy';
import { ToneBadge } from '@/components/ToneBadge';
import {
  actorLabel,
  dialName,
  isAgentActor,
  peerForDial,
  portalBase,
  type DialState,
  type InspectView,
  type Note,
  type NetworkStatus,
  type ProjectStatus,
  type ServiceState,
} from '@/lid';
import { connTypeTone, humanizeRtt } from '@/lib/format';
import { dialStatus, networkStatus, serviceStatus, type ItemStatus } from '@/lib/status';
import { Alert, AlertDescription } from '@datum-cloud/datum-ui/alert';
import { LinkButton } from '@datum-cloud/datum-ui/button';
import { BotIcon, ExternalLinkIcon, ScanEyeIcon, StickyNoteIcon } from 'lucide-react';
import { Card } from '@datum-cloud/datum-ui/card';
import { CodeBlock } from '@/components/CodeBlock';
import { TrafficTable } from '@/components/Traffic';

// Detail panes render from the 3 s /api/status poll the sidebar already
// makes, so everything stays live without a second request.

interface Common {
  s: ProjectStatus;
  note?: Note;
  names: Record<string, string>;
  portalFallback: string;
}

function StateBadges({ status, actor, names }: { status: ItemStatus; actor: string; names: Record<string, string> }) {
  return (
    <div className="flex flex-wrap items-center gap-2 pt-1">
      <ToneBadge tone={status.tone}>{status.label}</ToneBadge>
      {isAgentActor(actor) && (
        <ToneBadge tone="warning" icon={<BotIcon className="size-3" />}>
          Last changed by {actorLabel(actor, names)}
        </ToneBadge>
      )}
    </div>
  );
}

/** Read-only: notes are set with local-lid note set, never from the dashboard. */
function NoteCard({ note }: { note?: Note }) {
  if (!note) return null;
  return (
    <Card size="sm" className="text-foreground flex-row items-start gap-2 px-3 py-2 text-sm">
      <StickyNoteIcon className="text-muted-foreground mt-0.5 size-3.5 shrink-0" />
      <span className="whitespace-pre-wrap">{note.text}</span>
    </Card>
  );
}

function ErrorLine({ error, stage }: { error?: string | null; stage?: string | null }) {
  if (!error) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription>
        {stage ? `${stage}: ` : ''}
        {error}
      </AlertDescription>
    </Alert>
  );
}

function PortalButton({ href, label }: { href: string; label: string }) {
  return (
    <LinkButton
      href={href}
      target="_blank"
      rel="noopener"
      type="secondary"
      theme="outline"
      size="xs"
      icon={<ExternalLinkIcon className="size-3.5" />}
      iconPosition="right">
      {label}
    </LinkButton>
  );
}

export function ServiceDetail({
  sv,
  s,
  note,
  names,
  portalFallback,
  inspected,
  replayEnabled,
}: Common & { sv: ServiceState; inspected?: InspectView; replayEnabled: boolean }) {
  const status = serviceStatus(sv);
  const endpoint = inspected?.original.endpoint ?? sv.endpoint;
  const canInspect = !sv.public && sv.protocol === 'tcp';
  const cmd = `local-lid inspect enable ${sv.endpoint}`;
  return (
    <DetailPane
      title={sv.hostname || endpoint}
      description={
        <div className="flex flex-wrap items-center gap-2">
          <StateBadges status={status} actor={sv.last_actor} names={names} />
          {inspected && (
            <div className="pt-1">
              <ToneBadge tone="success" icon={<ScanEyeIcon className="size-3" />}>
                Inspected
              </ToneBadge>
            </div>
          )}
        </div>
      }
      actions={<PortalButton href={`${portalBase(s, portalFallback)}/project/${s.project}/connectors`} label="Connectors" />}>
      <NoteCard note={note} />
      <ErrorLine error={sv.last_error} stage={sv.last_error_stage} />
      <DetailSection title="Sharing" help={SECTION_HELP.sharing}>
        <KeyValueCard
          rows={[
            ['Local endpoint', <TextCopy value={endpoint} className="text-xs" />],
            ...(inspected
              ? ([
                  ['Through inspector', <span className="font-mono text-xs">{inspected.listen} &rarr; {inspected.target}</span>],
                  ['Original service id', <TextCopy value={inspected.original_id} className="text-xs" />],
                ] as [string, React.ReactNode][])
              : []),
            ['Protocol', sv.protocol.toUpperCase()],
            ['Access', sv.public ? 'Public' : `Private: ${sv.allow.length} allowed peer${sv.allow.length === 1 ? '' : 's'}`],
            ...(sv.hostnames.length
              ? ([['Hostnames', <div className="flex flex-col gap-1">{sv.hostnames.map((h) => <TextCopy key={h} value={h} className="text-xs" />)}</div>]] as [string, React.ReactNode][])
              : []),
            ['Running / ready', `${sv.running ? 'running' : 'not running'} · ${sv.ready ? 'ready' : 'not ready'}`],
            ['Last changed by', actorLabel(sv.last_actor, names)],
            ['Service id', <TextCopy value={sv.id} className="text-xs" />],
          ]}
        />
      </DetailSection>
      {!sv.public && sv.allow.length > 0 && (
        <DetailSection title="Allowed peers" help={SECTION_HELP.allowedPeers}>
          <KeyValueCard rows={sv.allow.map((k, i) => [`Peer ${i + 1}`, <TextCopy value={k} className="text-xs" />])} />
        </DetailSection>
      )}
      <DetailSection title="Captured traffic" help={SECTION_HELP.capturedTraffic}>
        {inspected ? (
          <TrafficTable serviceId={sv.id} replayEnabled={replayEnabled} />
        ) : canInspect ? (
          <div className="flex flex-col gap-2">
            <Alert variant="outline">
              <AlertDescription>
                Not inspected. To see each HTTP request to this service, run the command below. It re-creates the service
                behind Local Lid's inspector, with a brief outage, and the service id changes.
              </AlertDescription>
            </Alert>
            <CodeBlock copyValue={cmd}>{cmd}</CodeBlock>
          </div>
        ) : (
          <Alert variant="outline">
            <AlertDescription>
              {sv.public
                ? "Public services can't be inspected yet: staging can't create them, so the swap can't be verified."
                : "The inspector is HTTP-only, so UDP services can't be inspected."}
            </AlertDescription>
          </Alert>
        )}
      </DetailSection>
      <DetailSection title="Connections" help={SECTION_HELP.connections}>
        <Alert variant="outline">
          <AlertDescription>
            The Connect daemon doesn't report per-service connections or inbound peers yet. Device totals are on the Device
            page.
          </AlertDescription>
        </Alert>
      </DetailSection>
    </DetailPane>
  );
}

export function DialDetail({ d, s, names, note }: Common & { d: DialState }) {
  const peer = peerForDial(s, d);
  const status = dialStatus(d, peer);
  return (
    <DetailPane title={dialName(d)} description={<StateBadges status={status} actor={d.last_actor} names={names} />}>
      <NoteCard note={note} />
      <ErrorLine error={d.last_error} stage={d.last_error_stage} />
      <DetailSection title="Connection" help={SECTION_HELP.dialConnection}>
        {peer ? (
          <StatRow>
            <StatCard label="Path" value={<ToneBadge tone={connTypeTone(peer.path)}>{peer.path}</ToneBadge>} />
            <StatCard label="RTT" value={humanizeRtt(peer.rtt_ms)} />
          </StatRow>
        ) : (
          <Alert variant="outline">
            <AlertDescription>No live connection to this peer right now. The dial connects when something uses its local port.</AlertDescription>
          </Alert>
        )}
      </DetailSection>
      <DetailSection title="Dial" help={SECTION_HELP.dialDetails}>
        <KeyValueCard
          rows={[
            ['Local address', <TextCopy value={`127.0.0.1:${d.local_port ?? d.bind}`} className="text-xs" />],
            ['Remote connector', d.connector_name ?? '—'],
            ['Remote port', `${d.port}/${d.protocol}`],
            ['Peer key', <TextCopy value={d.connector} className="text-xs" />],
            ...(peer?.remote ? ([['Remote address', peer.remote]] as [string, string][]) : []),
            ['Last changed by', actorLabel(d.last_actor, names)],
          ]}
        />
      </DetailSection>
    </DetailPane>
  );
}

export function NetworkDetail({ n, s, portalFallback, note }: Common & { n: NetworkStatus }) {
  const status = networkStatus(n);
  return (
    <DetailPane
      title={n.network}
      description={
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <ToneBadge tone={status.tone}>{status.label}</ToneBadge>
          <ToneBadge tone="muted">{n.mode}</ToneBadge>
          {n.prototype && <ToneBadge tone="muted">prototype</ToneBadge>}
        </div>
      }
      actions={<PortalButton href={`${portalBase(s, portalFallback)}/project/${s.project}`} label="Open project" />}>
      <NoteCard note={note} />
      <ErrorLine error={n.last_error} stage={n.last_error_stage} />
      {(n.packets_sent != null || n.packets_received != null) && (
        <DetailSection title="Packets" help={SECTION_HELP.networkPackets}>
          <StatRow>
            <StatCard label="Packets sent" value={n.packets_sent ?? 0} />
            <StatCard label="Packets received" value={n.packets_received ?? 0} />
          </StatRow>
        </DetailSection>
      )}
      <DetailSection title="Attachment" help={SECTION_HELP.networkAttachment}>
      <KeyValueCard
        rows={
          [
            ['Network', n.network],
            ['Mode', n.mode],
            ['State', n.state],
            ['Assigned address', n.assigned_address],
            ['Peer address', n.peer_address],
            ['Routes', n.routes?.length ? n.routes.join(', ') : null],
            ['Gateway location', n.gateway_location],
            ['Gateway', n.gateway_resource ?? n.gateway],
            ['Peer', n.peer],
            ['Interface', n.interface_name],
            ['MTU', n.mtu],
            ['Persistent', n.persistent == null ? null : n.persistent ? 'yes' : 'no (rejoin after restart)'],
          ].filter(([, v]) => v != null && v !== '') as [string, React.ReactNode][]
        }
      />
      </DetailSection>
    </DetailPane>
  );
}
