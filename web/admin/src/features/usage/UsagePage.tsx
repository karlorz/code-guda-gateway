import { useQuery } from '@tanstack/react-query';
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { apiFetch } from '../../api/client';
import type { ListResponse, UsageDaily } from '../../api/types';
import { Panel, valueOf } from '../../components/ui';

export function UsagePage() {
  const { data } = useQuery({ queryKey: ['usage-daily'], queryFn: () => apiFetch<ListResponse<UsageDaily>>('/admin/api/usage-daily') });
  const rows = (data?.items ?? []).map((row) => {
    const record = row as Record<string, unknown>;
    return {
      day: valueOf<string>(record, 'Day', 'day', ''),
      provider: valueOf<string>(record, 'Provider', 'provider', ''),
      count: valueOf<number>(record, 'RequestCount', 'request_count', 0),
    };
  });
  return (
    <div>
      <h1 className="text-2xl font-semibold">Usage</h1>
      <Panel title="Daily requests">
        <div className="h-72 border-t border-zinc-200 pt-4">
          <ResponsiveContainer height="100%" width="100%">
            <BarChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="day" />
              <YAxis />
              <Tooltip />
              <Bar dataKey="count" fill="#18181b" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </Panel>
    </div>
  );
}
