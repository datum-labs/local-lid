import { CodeBlock } from '@/components/CodeBlock';
import { DetailPane } from '@/components/DetailPane';
import { NavItem } from '@/components/NavItem';
import { usePoll } from '@/hooks/usePoll';
import { getJSON, type LogSource } from '@/lid';
import { humanizeBytes } from '@/lib/format';
import { sameSelection, type Selection } from '@/lib/selection';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@datum-cloud/datum-ui/input-group';
import { SidebarMenu } from '@datum-cloud/datum-ui/sidebar';
import { SearchIcon } from 'lucide-react';
import { useLayoutEffect, useRef, useState } from 'react';

// Which files can be tailed is decided only on the CLI (local-lid logs add);
// the dashboard just reads registered names.

export function LogList({
  sources,
  selection,
  onSelect,
}: {
  sources: LogSource[];
  selection: Selection;
  onSelect: (name: string) => void;
}) {
  return (
    <SidebarMenu className="gap-0.5">
      {sources.map((src) => (
        <NavItem
          key={src.name}
          active={sameSelection(selection, { kind: 'log', name: src.name })}
          onSelect={() => onSelect(src.name)}
          status={src.exists ? { tone: 'success', label: 'File present' } : { tone: 'muted', label: 'No file yet' }}
          title={src.name}
          trailing={<span className="text-muted-foreground font-mono text-[11px]">{src.exists ? humanizeBytes(src.size_bytes) : '—'}</span>}
          subtitle={<span className="truncate font-mono">{src.path}</span>}
        />
      ))}
    </SidebarMenu>
  );
}

export function LogDetail({ name, source }: { name: string; source: LogSource | undefined }) {
  const [lines, setLines] = useState<{ name: string; lines: string[]; max: number } | null>(null);
  const [search, setSearch] = useState('');
  const preRef = useRef<HTMLPreElement>(null);
  const wasNearBottom = useRef(true);

  // Only the selected log is tailed, every 3 s.
  usePoll(
    async (isCurrent) => {
      try {
        const res = await getJSON<{ lines: string[]; max_lines: number }>(`/api/logs/${encodeURIComponent(name)}/tail`);
        if (isCurrent()) setLines({ name, lines: res.lines, max: res.max_lines });
      } catch (e) {
        if (isCurrent()) setLines({ name, lines: [(e as Error).message], max: 0 });
      }
    },
    [name],
  );

  const all = lines?.name === name ? lines.lines : [];
  const term = search.trim().toLowerCase();
  const shown = term ? all.filter((l) => l.toLowerCase().includes(term)) : all;
  const text = shown.length ? shown.join('\n') : lines?.name !== name ? 'Loading…' : term ? '(no matching lines)' : '(empty)';

  // Stick to the bottom on new lines only if the viewer was already there.
  useLayoutEffect(() => {
    const pre = preRef.current;
    if (pre && wasNearBottom.current) pre.scrollTop = pre.scrollHeight;
  }, [text]);

  return (
    <DetailPane
      title={name}
      description={
        <span className="font-mono text-xs">
          {source?.path}
          {source && !source.exists && ' (no file yet)'}
          {lines?.max ? ` · last ${lines.max} lines` : ''}
        </span>
      }>
      <InputGroup className="max-w-md">
        <InputGroupAddon>
          <SearchIcon />
        </InputGroupAddon>
        <InputGroupInput
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Filter lines (e.g. error)…"
          aria-label="Filter log lines"
          className="h-full border-0 bg-transparent shadow-none focus-visible:ring-0 focus-visible:ring-offset-0"
        />
      </InputGroup>
      <CodeBlock
        preRef={preRef}
        copyValue={shown.join('\n')}
        className="max-h-[calc(100vh-16rem)] overflow-y-auto"
        onScroll={(e) => {
          const pre = e.currentTarget;
          wasNearBottom.current = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 30;
        }}>
        {text}
      </CodeBlock>
    </DetailPane>
  );
}
