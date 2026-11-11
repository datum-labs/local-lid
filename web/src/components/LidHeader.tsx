import { ConnectionStatus } from '@/components/ConnectionStatus';
import { Logo } from '@datum-cloud/datum-ui/logo';
import { Text } from '@datum-cloud/datum-ui/typography';

/** Header without the old token box: Local Lid holds the token, not the browser. */
export function LidHeader({
  connected,
  error,
  project,
  projects,
  onProjectChange,
}: {
  connected: boolean;
  error?: string;
  project: string;
  projects: string[];
  onProjectChange: (project: string) => void;
}) {
  return (
    <header className="bg-background flex h-14 shrink-0 items-center gap-4 border-b px-4">
      <div className="flex items-center gap-3">
        <Logo.Flat tone="mono-light" className="h-6 w-auto" />
        <span className="bg-border h-5 w-px" aria-hidden />
        <Text size="sm" weight="medium">
          Local Lid
        </Text>
        <Text size="xs" textColor="muted" className="hidden sm:inline">
          Connect dashboard (read-only)
        </Text>
      </div>
      <div className="ml-auto flex items-center gap-2">
        <ConnectionStatus connected={connected} error={error} />
        {projects.length > 1 ? (
          <select
            aria-label="Project"
            value={project}
            onChange={(e) => onProjectChange(e.target.value)}
            className="bg-background h-7 rounded border px-2 font-mono text-xs">
            {projects.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        ) : (
          <Text size="xs" className="font-mono">
            {project || 'no project configured'}
          </Text>
        )}
      </div>
    </header>
  );
}
