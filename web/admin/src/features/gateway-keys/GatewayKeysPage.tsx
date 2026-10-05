import { FormEvent, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, RotateCcw, Trash2 } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { GatewayKey, GatewayKeyCreateResponse, ListResponse } from '../../api/types';
import { Badge, Button, Field, Panel, valueOf } from '../../components/ui';
import { OneTimeGatewayKeyDialog } from './OneTimeGatewayKeyDialog';

type FilterChip = 'Enabled' | 'All' | 'Revoked' | 'operator' | 'open' | 'ref_code';

export function GatewayKeysPage() {
  const qc = useQueryClient();
  const [name, setName] = useState('');
  const [oneTimeKey, setOneTimeKey] = useState('');
  const [filter, setFilter] = useState<FilterChip>('Enabled');

  const { data, isLoading } = useQuery({
    queryKey: ['gateway-keys'],
    queryFn: () => apiFetch<ListResponse<GatewayKey>>('/admin/api/gateway-keys'),
  });

  const createKey = useMutation({
    mutationFn: (keyName: string) =>
      apiFetch<GatewayKeyCreateResponse>('/admin/api/gateway-keys', { method: 'POST', body: JSON.stringify({ name: keyName }) }),
    onSuccess: (created) => {
      setOneTimeKey(created.raw_key);
      setName('');
      void qc.invalidateQueries({ queryKey: ['gateway-keys'] });
    },
  });

  const action = useMutation({
    mutationFn: ({ id, path, body }: { id: number; path?: string; body?: unknown }) =>
      apiFetch(`/admin/api/gateway-keys/${id}${path ?? ''}`, { method: path ? 'POST' : 'PATCH', body: body ? JSON.stringify(body) : undefined }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['gateway-keys'] }),
  });

  const revokeUnused = useMutation({
    mutationFn: () =>
      apiFetch<{ status: string; revoked_count: number }>('/admin/api/gateway-keys/revoke-unused', {
        method: 'POST',
        body: JSON.stringify({ days: 30 }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['gateway-keys'] });
    },
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim()) {
      createKey.mutate(name.trim());
    }
  }

  const allRows = data?.items ?? [];

  // Filter logic
  const filteredRows = allRows.filter((row) => {
    const record = row as Record<string, unknown>;
    const revoked = valueOf<string | undefined>(record, 'RevokedAt', 'revoked_at', undefined);
    const issuedVia = valueOf<string>(record, 'IssuedVia', 'issued_via', 'operator');

    switch (filter) {
      case 'Enabled':
        // Default: hide revoked stale keys; disabled-but-live keys stay visible.
        return !revoked;
      case 'Revoked':
        return Boolean(revoked);
      case 'All':
        return true;
      case 'operator':
        return issuedVia === 'operator';
      case 'open':
        return issuedVia === 'open_register';
      case 'ref_code':
        return issuedVia === 'ref_code';
      default:
        return true;
    }
  });

  const chips: FilterChip[] = ['Enabled', 'All', 'Revoked', 'operator', 'open', 'ref_code'];

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-4 mb-4">
        <h1 className="text-2xl font-semibold">Gateway Keys</h1>
        <Button
          disabled={revokeUnused.isPending}
          onClick={() => {
            if (confirm('Revoke all keys that have not been used in the last 30 days?')) {
              revokeUnused.mutate();
            }
          }}
          type="button"
          variant="secondary"
        >
          <Trash2 size={16} />
          Revoke unused (&gt;30d)
        </Button>
      </div>

      <Panel
        title="Create"
        action={
          <form className="flex gap-2" onSubmit={submit}>
            <Field aria-label="Gateway key name" label="Name" onChange={(event) => setName(event.target.value)} value={name} />
            <Button className="mt-6" disabled={createKey.isPending || !name.trim()} type="submit">
              <Plus size={16} />
              Create
            </Button>
          </form>
        }
      >
        <div className="mb-4 flex flex-wrap gap-2">
          {chips.map((c) => (
            <button
              key={c}
              type="button"
              onClick={() => setFilter(c)}
              className={`rounded-full px-3 py-1 text-xs font-medium transition-colors ${
                filter === c
                  ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900'
                  : 'bg-zinc-100 text-zinc-600 hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-400'
              }`}
            >
              {c === 'operator' ? 'via: operator' : c === 'open' ? 'via: open' : c === 'ref_code' ? 'via: ref_code' : c}
            </button>
          ))}
        </div>

        <ResourceTable
          empty={isLoading ? 'Loading' : 'No gateway keys matching filter'}
          headers={['Name / Label', 'Prefix', 'Fingerprint', 'Issued Via', 'Last Used', 'Status']}
          rows={filteredRows.map((row) => {
            const record = row as Record<string, unknown>;
            const id = valueOf<number>(record, 'ID', 'id', 0);
            const nameVal = valueOf<string>(record, 'Name', 'name', '');
            const agentLabel = valueOf<string>(record, 'AgentLabel', 'agent_label', '');
            const issuedVia = valueOf<string>(record, 'IssuedVia', 'issued_via', 'operator');
            const lastUsed = valueOf<string | undefined>(record, 'LastUsedAt', 'last_used_at', undefined);
            const enabled = valueOf<boolean>(record, 'Enabled', 'enabled', false);
            const revoked = valueOf<string | undefined>(record, 'RevokedAt', 'revoked_at', undefined);

            const displayLabel = agentLabel && agentLabel !== nameVal ? `${nameVal} (${agentLabel})` : nameVal;
            const issuedViaBadgeTone = issuedVia === 'operator' ? 'good' : issuedVia === 'ref_code' ? 'warn' : 'neutral';

            return {
              id,
              cols: [
                displayLabel,
                valueOf<string>(record, 'Prefix', 'prefix', ''),
                valueOf<string>(record, 'Fingerprint', 'fingerprint', ''),
                <Badge tone={issuedViaBadgeTone as any}>{issuedVia}</Badge>,
                lastUsed ? new Date(lastUsed).toLocaleDateString() : 'Never',
                revoked ? <Badge tone="bad">revoked</Badge> : <Badge tone={enabled ? 'good' : 'warn'}>{enabled ? 'enabled' : 'disabled'}</Badge>,
              ],
              actions: (
                <>
                  <Button disabled={action.isPending || Boolean(revoked)} onClick={() => action.mutate({ id, body: { enabled: !enabled } })} type="button" variant="secondary">
                    <RotateCcw size={16} />
                    {enabled ? 'Disable' : 'Enable'}
                  </Button>
                  <Button disabled={action.isPending || Boolean(revoked)} onClick={() => action.mutate({ id, path: '/revoke' })} type="button" variant="danger">
                    Revoke
                  </Button>
                </>
              ),
            };
          })}
        />
      </Panel>
      {oneTimeKey ? <OneTimeGatewayKeyDialog rawKey={oneTimeKey} onClose={() => setOneTimeKey('')} /> : null}
    </div>
  );
}

export function ResourceTable({
  rows,
  empty,
  headers,
}: {
  rows: { id: number; cols: React.ReactNode[]; actions?: React.ReactNode }[];
  empty: string;
  headers?: string[];
}) {
  if (rows.length === 0) {
    return <p className="text-sm text-zinc-500">{empty}</p>;
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        {headers ? (
          <thead>
            <tr className="border-b border-zinc-200 text-xs font-semibold uppercase text-zinc-500">
              {headers.map((h, i) => (
                <th className="py-2 pr-4" key={i}>
                  {h}
                </th>
              ))}
              <th className="py-2 text-right">Actions</th>
            </tr>
          </thead>
        ) : null}
        <tbody>
          {rows.map((row) => (
            <tr className="border-t border-zinc-200" key={row.id}>
              {row.cols.map((col, index) => (
                <td className="py-3 pr-4" key={index}>
                  {col}
                </td>
              ))}
              <td className="flex justify-end gap-2 py-3">{row.actions}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
