import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bug, ChevronLeft, ChevronRight, ToggleLeft, ToggleRight } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { DisplayTimezoneSetting, PagedItems, ProxyAttempt, ProxyDebugAttemptsSetting } from '../../api/types';
import { Badge, Button, Panel } from '../../components/ui';
import { formatDisplayTime } from '../../lib/formatDisplayTime';

const PAGE_SIZE = 50;
const providers = ['all', 'tavily', 'firecrawl', 'grok'] as const;

export function DebugAttemptsPage() {
  const qc = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [provider, setProvider] = useState<(typeof providers)[number]>('all');
  const tz = useQuery({
    queryKey: ['display-timezone'],
    queryFn: () => apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone'),
  });
  const setting = useQuery({ queryKey: ['proxy-debug-attempts'], queryFn: () => apiFetch<ProxyDebugAttemptsSetting>('/admin/api/settings/proxy-debug-attempts') });
  const attempts = useQuery({ queryKey: ['proxy-attempts', offset], queryFn: () => apiFetch<PagedItems<ProxyAttempt>>(`/admin/api/proxy-attempts?limit=${PAGE_SIZE}&offset=${offset}`) });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => apiFetch('/admin/api/settings/proxy-debug-attempts', { method: 'PATCH', body: JSON.stringify({ enabled }) }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['proxy-debug-attempts'] }),
  });
  const rows = attempts.data?.items ?? [];
  const filtered = useMemo(() => provider === 'all' ? rows : rows.filter((row) => row.provider === provider), [provider, rows]);
  const enabled = setting.data?.enabled ?? false;
  const zone = tz.data?.timezone || 'UTC';
  return (
    <div>
      <h1 className="text-2xl font-semibold">Debug Attempts</h1>
      <Panel title="Attempt Logging" action={
        <Button aria-label={enabled ? 'Disable attempt logging' : 'Enable attempt logging'} onClick={() => toggle.mutate(!enabled)} type="button" variant="secondary">
          {enabled ? <ToggleRight size={16} /> : <ToggleLeft size={16} />}
          {enabled ? 'Disable' : 'Enable'}
        </Button>
      }>
        <p className="text-sm text-zinc-600">{enabled ? 'Logging enabled' : 'Logging disabled'}</p>
      </Panel>
      <Panel title="Attempts">
        <div className="mb-3 flex flex-wrap gap-2">
          {providers.map((p) => (
            <Button key={p} onClick={() => setProvider(p)} type="button" variant={provider === p ? 'primary' : 'secondary'}>
              <Bug size={16} />
              {p === 'all' ? 'All' : p[0].toUpperCase() + p.slice(1)}
            </Button>
          ))}
        </div>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <tbody>
              {filtered.map((row) => (
                <tr className="border-t border-zinc-200" key={row.id}>
                  <td className="py-3 pr-4 whitespace-nowrap" title={row.occurred_at ?? ''}>
                    {row.occurred_at ? formatDisplayTime(row.occurred_at, zone) : '-'}
                  </td>
                  <td className="py-3 pr-4">{row.request_id}</td>
                  <td className="py-3 pr-4"><Badge>{row.provider}</Badge></td>
                  <td className="py-3 pr-4">#{row.attempt_index}</td>
                  <td className="py-3 pr-4">{row.provider_key_name ?? row.provider_key_id ?? '-'}</td>
                  <td className="py-3 pr-4">{row.upstream_status ?? row.status_class}</td>
                  <td className="py-3 pr-4">{row.reason ?? '-'}</td>
                  <td className="py-3 pr-4">{row.terminal ? 'terminal' : 'retrying'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="mt-3 flex justify-end gap-2">
          <Button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} type="button" variant="secondary"><ChevronLeft size={16} />Prev</Button>
          <Button disabled={(attempts.data?.page.offset ?? 0) + (attempts.data?.page.limit ?? PAGE_SIZE) >= (attempts.data?.page.total ?? 0)} onClick={() => setOffset(offset + PAGE_SIZE)} type="button" variant="secondary">Next<ChevronRight size={16} /></Button>
        </div>
      </Panel>
    </div>
  );
}
