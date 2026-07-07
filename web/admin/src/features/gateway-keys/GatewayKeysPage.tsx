import { FormEvent, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, RotateCcw } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { GatewayKey, GatewayKeyCreateResponse, ListResponse } from '../../api/types';
import { Badge, Button, Field, Panel, valueOf } from '../../components/ui';
import { OneTimeGatewayKeyDialog } from './OneTimeGatewayKeyDialog';

export function GatewayKeysPage() {
  const qc = useQueryClient();
  const [name, setName] = useState('');
  const [oneTimeKey, setOneTimeKey] = useState('');
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

  function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim()) {
      createKey.mutate(name.trim());
    }
  }

  const rows = data?.items ?? [];
  return (
    <div>
      <h1 className="text-2xl font-semibold">Gateway Keys</h1>
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
        <ResourceTable
          empty={isLoading ? 'Loading' : 'No gateway keys'}
          rows={rows.map((row) => {
            const record = row as Record<string, unknown>;
            const id = valueOf<number>(record, 'ID', 'id', 0);
            const enabled = valueOf<boolean>(record, 'Enabled', 'enabled', false);
            const revoked = valueOf<string | undefined>(record, 'RevokedAt', 'revoked_at', undefined);
            return {
              id,
              cols: [
                valueOf<string>(record, 'Name', 'name', ''),
                valueOf<string>(record, 'Prefix', 'prefix', ''),
                valueOf<string>(record, 'Fingerprint', 'fingerprint', ''),
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
}: {
  rows: { id: number; cols: React.ReactNode[]; actions?: React.ReactNode }[];
  empty: string;
}) {
  if (rows.length === 0) {
    return <p className="text-sm text-zinc-500">{empty}</p>;
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
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
