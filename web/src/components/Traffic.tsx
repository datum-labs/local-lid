import { CodeBlock } from '@/components/CodeBlock';
import { DetailSection } from '@/components/DetailPane';
import { KeyValueCard } from '@/components/KeyValueCard';
import { ToneBadge } from '@/components/ToneBadge';
import { usePoll } from '@/hooks/usePoll';
import { getJSON, postJSON, type Exchange, type ExchangeSummary, type Header, type ReplayResult } from '@/lid';
import { statusTone } from '@/lib/format';
import { Alert, AlertDescription } from '@datum-cloud/datum-ui/alert';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card } from '@datum-cloud/datum-ui/card';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@datum-cloud/datum-ui/table';
import { toast } from '@datum-cloud/datum-ui/toast';
import { Tooltip } from '@datum-cloud/datum-ui/tooltip';
import { Text } from '@datum-cloud/datum-ui/typography';
import { PlayIcon } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

const base = (serviceId: string) => `/api/inspect/${encodeURIComponent(serviceId)}/traffic`;

/** Captured requests for an inspected service, newest first. */
export function TrafficTable({ serviceId, replayEnabled }: { serviceId: string; replayEnabled: boolean }) {
  const [rows, setRows] = useState<ExchangeSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  usePoll(
    async (isCurrent) => {
      try {
        const next = await getJSON<ExchangeSummary[]>(base(serviceId));
        if (isCurrent()) {
          setRows(next);
          setError(null);
        }
      } catch (e) {
        if (isCurrent()) setError((e as Error).message);
      }
    },
    [serviceId],
  );

  if (error && !rows)
    return (
      <Alert variant="destructive">
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    );
  if (!rows) return <Skeleton className="h-32 w-full rounded-lg" />;
  if (!rows.length)
    return (
      <Alert variant="outline">
        <AlertDescription>No requests yet. Traffic from peers shows up here as it passes through the inspector.</AlertDescription>
      </Alert>
    );
  return (
    <>
      <Card size="sm" sectioned className="overflow-hidden py-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-24">Method</TableHead>
              <TableHead>Path</TableHead>
              <TableHead className="w-24">Status</TableHead>
              <TableHead className="w-24">Time</TableHead>
              <TableHead className="w-28">When</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows
              .slice()
              .reverse()
              .map((ex) => (
                <TableRow key={ex.id} className="cursor-pointer" onClick={() => setOpen(ex.id)}>
                  <TableCell className="font-mono font-semibold">{ex.method}</TableCell>
                  <TableCell className="max-w-0 truncate font-mono text-xs">{ex.path}</TableCell>
                  <TableCell>
                    <ToneBadge tone={statusTone(ex.response_status)}>{ex.response_status ?? '—'}</ToneBadge>
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono text-xs">
                    {ex.duration_ms != null ? `${ex.duration_ms} ms` : '—'}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{new Date(ex.timestamp_unix_ms).toLocaleTimeString()}</TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </Card>
      {open && (
        <ExchangeDialog serviceId={serviceId} exchangeId={open} replayEnabled={replayEnabled} onClose={() => setOpen(null)} />
      )}
    </>
  );
}

function formatHeaders(headers: Header[]): string {
  return headers.map(([k, v]) => `${k}: ${v}`).join('\n');
}

function Headers({ headers }: { headers: Header[] }) {
  if (!headers.length)
    return (
      <Text size="xs" textColor="muted">
        none
      </Text>
    );
  const text = formatHeaders(headers);
  return <CodeBlock copyValue={text}>{text}</CodeBlock>;
}

function Body({ body }: { body: string }) {
  return <CodeBlock copyValue={body || undefined}>{body || '(empty)'}</CodeBlock>;
}

function ExchangeDialog({
  serviceId,
  exchangeId,
  replayEnabled,
  onClose,
}: {
  serviceId: string;
  exchangeId: string;
  replayEnabled: boolean;
  onClose: () => void;
}) {
  const [exchange, setExchange] = useState<Exchange | null>(null);
  const [replay, setReplay] = useState<ReplayResult | null>(null);
  const [replaying, setReplaying] = useState(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const replayRef = useRef<HTMLDivElement>(null);
  const url = `${base(serviceId)}/${encodeURIComponent(exchangeId)}`;

  useEffect(() => {
    if (replay) replayRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }, [replay]);

  useEffect(() => {
    let current = true;
    getJSON<Exchange>(url)
      .then((ex) => current && setExchange(ex))
      .catch((e: Error) => {
        if (!current) return;
        toast.error(e.message);
        onCloseRef.current();
      });
    return () => {
      current = false;
    };
  }, [url]);

  const doReplay = async () => {
    setReplaying(true);
    try {
      setReplay(await postJSON<ReplayResult>(`${url}/replay`));
      toast.success('Replayed to the real target');
    } catch (e) {
      toast.error(`Replay failed: ${(e as Error).message}`);
    } finally {
      setReplaying(false);
    }
  };

  const truncated = !!exchange?.request.body_truncated;
  const replayButton = (
    <Button
      type="primary"
      theme="solid"
      size="xs"
      disabled={!exchange || truncated}
      loading={replaying}
      icon={<PlayIcon className="size-3.5" />}
      onClick={doReplay}>
      Replay this request
    </Button>
  );

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Content className="sm:max-w-3xl">
        <Dialog.Header
          title={<span className="font-mono text-sm break-all">{exchange ? `${exchange.method} ${exchange.path}` : 'Loading…'}</span>}
          onClose={onClose}
        />
        <Dialog.Body className="flex flex-col gap-5 px-5">
          {!exchange ? (
            <Skeleton className="h-40 w-full rounded-lg" />
          ) : (
            <>
              <KeyValueCard
                rows={[
                  ['Status', <ToneBadge tone={statusTone(exchange.response_status)}>{exchange.response_status ?? '—'}</ToneBadge>],
                  ['Host', exchange.host],
                  ['Time', new Date(exchange.timestamp_unix_ms).toLocaleString()],
                  ['Duration', exchange.duration_ms != null ? `${exchange.duration_ms} ms` : '—'],
                  ...(exchange.error ? ([['Error', exchange.error]] as [string, string][]) : []),
                ]}
              />
              <DetailSection title="Request headers">
                <Headers headers={exchange.request.headers} />
              </DetailSection>
              <DetailSection title={`Request body${exchange.request.body_truncated ? ' (truncated)' : ''}`}>
                <Body body={exchange.request.body} />
              </DetailSection>
              {exchange.response && (
                <>
                  <DetailSection title="Response headers">
                    <Headers headers={exchange.response.headers} />
                  </DetailSection>
                  <DetailSection title={`Response body${exchange.response.body_truncated ? ' (truncated)' : ''}`}>
                    <Body body={exchange.response.body} />
                  </DetailSection>
                </>
              )}
              {replay && (
                <div ref={replayRef}>
                  <DetailSection title={`Replay result: status ${replay.status}`}>
                    <Headers headers={replay.headers} />
                    <Body body={replay.body} />
                  </DetailSection>
                </div>
              )}
            </>
          )}
        </Dialog.Body>
        <Dialog.Footer className="flex justify-end gap-2">
          <Button type="quaternary" theme="outline" size="xs" onClick={onClose}>
            Close
          </Button>
          {/* replay_enabled off: no button at all (the server refuses too). */}
          {replayEnabled &&
            (truncated ? (
              <Tooltip message="Truncated body can't be replayed">
                <span tabIndex={0}>{replayButton}</span>
              </Tooltip>
            ) : (
              replayButton
            ))}
        </Dialog.Footer>
      </Dialog.Content>
    </Dialog>
  );
}
