import { DialDetail, NetworkDetail, ServiceDetail } from '@/components/LidDetails';
import { LidDevice } from '@/components/LidDevice';
import { LidHeader } from '@/components/LidHeader';
import { LidMap } from '@/components/LidMap';
import { LidSidebar } from '@/components/LidSidebar';
import { useLid } from '@/hooks/useLid';
import { useStoredState } from '@/hooks/useStoredState';
import { networksOf, noteKey, type ProjectStatus } from '@/lid';
import { LogDetail } from '@/components/LogView';
import { TABS, type Selection, type SidebarTab } from '@/lib/selection';
import { Alert, AlertDescription } from '@datum-cloud/datum-ui/alert';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
import { SidebarInset, SidebarProvider } from '@datum-cloud/datum-ui/sidebar';
import { useEffect, useState } from 'react';

export function App() {
  const lid = useLid();
  const [tab, setTab] = useStoredState<SidebarTab>('lid_tab', 'services', TABS);
  // Start on the Device page: it's useful even before anything is shared.
  const [selection, setSelection] = useState<Selection>({ kind: 'device' });

  const changeTab = (next: SidebarTab) => setTab(next);
  // A map station opens the same view its sidebar row would, and switches
  // to that row's tab so it shows as selected there.
  const open = (target: Selection) => {
    if (target?.kind === 'service') setTab('services');
    if (target?.kind === 'dial') setTab('dials');
    if (target?.kind === 'network') setTab('networks');
    if (target?.kind === 'log') setTab('logs');
    setSelection(target);
  };

  const s = lid.status.status === 'ok' ? lid.status.data : null;

  // The selected item disappeared since the last poll (deleted, maybe from
  // another client): go back to the empty state instead of showing a ghost.
  useEffect(() => {
    if (!s || !selection) return;
    const gone =
      (selection.kind === 'service' && !s.services.some((x) => x.id === selection.id)) ||
      (selection.kind === 'dial' && !s.dials.some((x) => x.bind === selection.bind)) ||
      (selection.kind === 'network' && !networksOf(s).some((x) => x.network === selection.name)) ||
      (selection.kind === 'log' && !!lid.logs && !lid.logs.some((x) => x.name === selection.name));
    if (gone) setSelection(null);
  }, [s, selection, lid.logs]);

  const error =
    lid.info.status === 'error' ? lid.info.error : lid.status.status === 'error' ? lid.status.error : undefined;

  return (
    <div className="flex h-svh w-full flex-col overflow-hidden">
      <LidHeader
        connected={lid.status.status === 'ok'}
        error={error}
        project={lid.project}
        projects={lid.projects}
        onProjectChange={lid.setProject}
      />
      <SidebarProvider className="min-h-0 flex-1 overflow-hidden" style={{ '--sidebar-width': '23rem' } as React.CSSProperties}>
        <LidSidebar lid={lid} tab={tab} onTabChange={changeTab} selection={selection} onSelect={setSelection} />
        <SidebarInset className="min-h-0 overflow-y-auto">
          <Detail lid={lid} s={s} selection={selection} onOpen={open} />
        </SidebarInset>
      </SidebarProvider>
    </div>
  );
}

function Detail({
  lid,
  s,
  selection,
  onOpen,
}: {
  lid: ReturnType<typeof useLid>;
  s: ProjectStatus | null;
  selection: Selection;
  onOpen: (t: Selection) => void;
}) {
  if (selection?.kind === 'device') return <LidDevice lid={lid} />;
  if (selection?.kind === 'log')
    return <LogDetail key={selection.name} name={selection.name} source={lid.logs?.find((l) => l.name === selection.name)} />;
  if (!s && lid.status.status === 'error') {
    return (
      <div className="p-8">
        <Alert variant="destructive">
          <AlertDescription>
            {lid.status.kind === 'daemon_unreachable'
              ? 'Connect daemon unreachable. Is datum-connectd running? This page reconnects on its own when it is back.'
              : lid.status.error}
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  const names = lid.info.status === 'ok' ? lid.info.data.token_names : {};
  const portalFallback = lid.info.status === 'ok' ? lid.info.data.portal_base_url : 'https://cloud.datum.net';
  if (s && selection) {
    const common = { s, names, portalFallback };
    switch (selection.kind) {
      case 'map':
        return <LidMap s={s} inspected={lid.inspected} notes={lid.notes} onSelect={onOpen} />;
      case 'service': {
        const sv = s.services.find((x) => x.id === selection.id);
        if (sv)
          return (
            <ServiceDetail
              sv={sv}
              {...common}
              note={lid.notes[noteKey.service(sv, lid.inspected[sv.id])]}
              inspected={lid.inspected[sv.id]}
              replayEnabled={lid.info.status === 'ok' && lid.info.data.replay_enabled}
            />
          );
        break;
      }
      case 'dial': {
        const d = s.dials.find((x) => x.bind === selection.bind);
        if (d) return <DialDetail d={d} {...common} note={lid.notes[noteKey.dial(d)]} />;
        break;
      }
      case 'network': {
        const n = networksOf(s).find((x) => x.network === selection.name);
        if (n) return <NetworkDetail n={n} {...common} note={lid.notes[noteKey.network(n)]} />;
        break;
      }
    }
  }
  return (
    <div className="flex h-full items-center justify-center p-8">
      <EmptyContent title="Select a service, dial, network or log, or open the network map." />
    </div>
  );
}
