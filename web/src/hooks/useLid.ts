import { actorLabel, dialName, getJSON, isAgentActor, LidError, serviceName, type InspectView, type LidInfo, type LogSource, type Note, type ProjectStatus } from '@/lid';
import { toast } from '@datum-cloud/datum-ui/toast';
import { usePoll } from '@/hooks/usePoll';
import { useRef, useState } from 'react';

export type Loadable<T> = { status: 'idle' } | { status: 'ok'; data: T } | { status: 'error'; error: string };

/**
 * Local Lid's top-level data: /api/info and the selected project's
 * /api/status, both polled every 3 s. No token lives in the browser; Local
 * Lid adds the project's Viewer token on its side.
 */
export function useLid() {
  const [info, setInfo] = useState<Loadable<LidInfo>>({ status: 'idle' });
  const [status, setStatus] = useState<Loadable<ProjectStatus> & { kind?: string }>({ status: 'idle' });
  const [storedProject, setStoredProject] = useState<string>(() => {
    try {
      return localStorage.getItem('lid_project') ?? '';
    } catch {
      return '';
    }
  });
  const setProject = (next: string) => {
    setStoredProject(next);
    try {
      localStorage.setItem('lid_project', next);
    } catch {
      // storage blocked: the choice lasts for this page load only
    }
  };

  const [inspected, setInspected] = useState<Record<string, InspectView>>({});
  const [notes, setNotes] = useState<Record<string, Note>>({});
  const [logs, setLogs] = useState<LogSource[] | null>(null);
  const previous = useRef<ProjectStatus | null>(null);
  const tokenNames = useRef<Record<string, string>>({});
  if (info.status === 'ok') tokenNames.current = info.data.token_names;

  const projects = info.status === 'ok' ? info.data.projects : [];
  const project = projects.includes(storedProject) ? storedProject : (projects[0] ?? '');

  usePoll(async (isCurrent) => {
    try {
      const next = await getJSON<LidInfo>('/api/info');
      if (isCurrent()) setInfo({ status: 'ok', data: next });
    } catch (e) {
      if (isCurrent()) setInfo({ status: 'error', error: (e as Error).message });
    }
  }, []);

  usePoll(
    async (isCurrent) => {
      if (!project) return;
      try {
        const next = await getJSON<ProjectStatus>(`/api/status?project=${encodeURIComponent(project)}`);
        if (!isCurrent()) return;
        announceAgentChanges(previous.current, next, tokenNames.current);
        previous.current = next;
        setStatus({ status: 'ok', data: next });
      } catch (e) {
        if (isCurrent())
          setStatus({ status: 'error', error: (e as Error).message, kind: e instanceof LidError ? e.kind : undefined });
      }
    },
    [project],
  );
  // A different project is a fresh baseline, not a list of "changes".
  const lastProject = useRef(project);
  if (lastProject.current !== project) {
    lastProject.current = project;
    previous.current = null;
  }

  usePoll(async (isCurrent) => {
    try {
      const views = await getJSON<InspectView[]>('/api/inspect');
      if (isCurrent()) setInspected(Object.fromEntries(views.map((v) => [v.service_id, v])));
    } catch {
      // Local Lid unreachable: the info poll already reports that.
    }
  }, []);

  // Notes and registered logs: small local files, polled with everything else.
  usePoll(async (isCurrent) => {
    try {
      const [n, l] = await Promise.all([getJSON<Record<string, Note>>('/api/notes'), getJSON<LogSource[]>('/api/logs')]);
      if (isCurrent()) {
        setNotes(n);
        setLogs(l);
      }
    } catch {
      // Local Lid unreachable: the info poll already reports that.
    }
  }, []);

  return { info, status, project, projects, setProject, inspected, notes, logs };
}

export type Lid = ReturnType<typeof useLid>;

/**
 * Visible signal: a toast the moment an agent (an Operate token, not your
 * setup token) turns a service or dial on. Compared with the previous poll,
 * so it fires once per change, never on first load. the daemon's last_actor is
 * "last touched by", so this keys on the item turning on, not on the actor
 * field alone.
 */
function announceAgentChanges(prev: ProjectStatus | null, next: ProjectStatus, names: Record<string, string>) {
  if (!prev) return;
  for (const sv of next.services) {
    const was = prev.services.find((p) => p.id === sv.id);
    if (sv.desired_active && (!was || !was.desired_active) && isAgentActor(sv.last_actor))
      toast.warning(`${actorLabel(sv.last_actor, names)} started service ${serviceName(sv)}`, { duration: 10000 });
  }
  for (const d of next.dials) {
    const was = prev.dials.find((p) => p.bind === d.bind);
    if (d.desired_active && (!was || !was.desired_active) && isAgentActor(d.last_actor))
      toast.warning(`${actorLabel(d.last_actor, names)} started dial ${dialName(d)}`, { duration: 10000 });
  }
}
