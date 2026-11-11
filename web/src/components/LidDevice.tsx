import { SECTION_HELP } from '@/lib/help';
import { DetailPane, DetailSection, StatRow } from '@/components/DetailPane';
import { KeyValueCard } from '@/components/KeyValueCard';
import { StatCard } from '@/components/StatCard';
import { TextCopy } from '@/components/TextCopy';
import { ToneBadge } from '@/components/ToneBadge';
import type { Lid } from '@/hooks/useLid';
import { portalBase } from '@/lid';
import { humanizeBytes, humanizeDuration } from '@/lib/format';
import { Alert, AlertDescription } from '@datum-cloud/datum-ui/alert';
import { LinkButton } from '@datum-cloud/datum-ui/button';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { ExternalLinkIcon } from 'lucide-react';

/** Device page: this machine, the Connect daemon, and the selected project's state. */
export function LidDevice({ lid }: { lid: Lid }) {
  const { info, status, project } = lid;

  if (info.status === 'error') {
    return (
      <div className="p-8">
        <Alert variant="outline">
          <AlertDescription>{info.error}</AlertDescription>
        </Alert>
      </div>
    );
  }
  if (info.status !== 'ok') {
    return (
      <div className="p-8">
        <Skeleton className="h-40 w-full rounded-lg" />
      </div>
    );
  }
  const i = info.data;
  const s = status.status === 'ok' ? status.data : null;
  const t = s?.transport ?? null;
  const projectUrl = `${portalBase(s, i.portal_base_url)}/project/${project}`;

  const unreachable = !i.daemon_up || (status.status === 'error' && status.kind === 'daemon_unreachable');

  return (
    <DetailPane
      title={i.hostname}
      description={
        <span className="text-sm">
          {i.os} · {i.arch} · Local Lid v{i.version} · up {humanizeDuration(Date.now() - i.started_at_unix_ms)}
        </span>
      }
      actions={
        project && (
          <div className="flex gap-2">
            <LinkButton
              href={`${projectUrl}/connectors`}
              target="_blank"
              rel="noopener"
              type="secondary"
              theme="outline"
              size="xs"
              icon={<ExternalLinkIcon className="size-3.5" />}
              iconPosition="right">
              Connectors
            </LinkButton>
            <LinkButton
              href={projectUrl}
              target="_blank"
              rel="noopener"
              type="secondary"
              theme="outline"
              size="xs"
              icon={<ExternalLinkIcon className="size-3.5" />}
              iconPosition="right">
              Open project
            </LinkButton>
          </div>
        )
      }>
      {unreachable && (
        <Alert variant="outline">
          <AlertDescription>
            Connect daemon unreachable at {i.daemon_url}. Is datum-connectd running?
          </AlertDescription>
        </Alert>
      )}
      {!unreachable && status.status === 'error' && (
        <Alert variant="outline">
          <AlertDescription>
            {status.kind === 'token_rejected'
              ? `The daemon rejected Local Lid's viewer token for ${project}. Run local-lid init --project ${project} again.`
              : status.error}
          </AlertDescription>
        </Alert>
      )}

      {s && (
        <>
          <DetailSection title="Project" help={SECTION_HELP.project}>
            <div className="flex flex-wrap gap-2">
              <ToneBadge tone={s.enrolled ? 'success' : 'warning'}>{s.enrolled ? 'enrolled' : 'not enrolled'}</ToneBadge>
              <ToneBadge tone={s.running ? 'success' : s.desired_up ? 'warning' : 'muted'}>
                {s.running ? 'running' : s.desired_up ? 'starting' : 'down'}
              </ToneBadge>
            </div>
            {s.last_error && (
              <Alert variant="outline">
                <AlertDescription>
                  {s.last_error_stage ? `${s.last_error_stage}: ` : ''}
                  {s.last_error}
                </AlertDescription>
              </Alert>
            )}
            <StatRow>
              <StatCard label="Services" value={s.services.length} />
              <StatCard label="Services ready" value={s.services.filter((x) => x.ready).length} />
              <StatCard label="Dials" value={s.dials.length} />
              <StatCard
                label="Changed by agent"
                value={[...s.services, ...s.dials].filter((x) => x.last_actor.startsWith('operate:')).length}
              />
            </StatRow>
          </DetailSection>

          <DetailSection title="Traffic" help={SECTION_HELP.traffic}>
            {t ? (
              <StatRow>
                <StatCard label="Sent" value={humanizeBytes(t.bytes_sent)} />
                <StatCard label="Received" value={humanizeBytes(t.bytes_received)} />
                <StatCard label="Active TCP" value={t.active_tcp} />
                <StatCard label="Active UDP" value={t.active_udp} />
                <StatCard label="Errors" value={t.errors} bad={t.errors > 0} />
                <StatCard label="Peers" value={t.peers.length} />
              </StatRow>
            ) : (
              <Alert variant="outline">
                <AlertDescription>Project isn't running, so there are no traffic stats.</AlertDescription>
              </Alert>
            )}
          </DetailSection>

          <DetailSection title="Connector" help={SECTION_HELP.connector}>
            {s.connector ? (
              <KeyValueCard
                rows={[
                  ['Name', s.connector.name],
                  ['Peer id', <TextCopy value={s.connector.public_key} className="text-xs" />],
                  ['UID', s.connector.uid],
                  ['Auth', s.authentication ? `${s.authentication.kind}${s.authentication.session ? ` (${s.authentication.session})` : ''}` : '—'],
                ]}
              />
            ) : (
              <Alert variant="outline">
                <AlertDescription>No connector yet. Run datumctl connect up for this project.</AlertDescription>
              </Alert>
            )}
          </DetailSection>
        </>
      )}

      <DetailSection title="Local Lid" help={SECTION_HELP.localLid}>
        <KeyValueCard
          rows={[
            ['Hostname', i.hostname],
            ['Platform', `${i.os} · ${i.arch}`],
            ['Version', i.version],
            ['Started', new Date(i.started_at_unix_ms).toLocaleString()],
            ['Daemon', i.daemon_url],
            ['Project', project ? <TextCopy value={project} className="text-xs" /> : '—'],
            ['Replay', i.replay_enabled ? 'enabled' : 'disabled'],
          ]}
        />
      </DetailSection>
    </DetailPane>
  );
}
