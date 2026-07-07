import { FormEvent, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { ListResponse, ProviderKey } from '../../api/types';
import { Badge, Button, Field, Panel, valueOf } from '../../components/ui';
import { ResourceTable } from '../gateway-keys/GatewayKeysPage';

export function ProviderKeysPage() {
  const qc = useQueryClient();
  const [provider, setProvider] = useState('grok');
  const [name, setName] = useState('');
  const [rawKey, setRawKey] = useState('');
  const { data } = useQuery({ queryKey: ['provider-keys'], queryFn: () => apiFetch<ListResponse<ProviderKey>>('/admin/api/provider-keys') });
  const createKey = useMutation({
    mutationFn: () =>
      apiFetch<ProviderKey>('/admin/api/provider-keys', {
        method: 'POST',
        body: JSON.stringify({ provider, name, key: rawKey }),
      }),
    onSuccess: () => {
      setName('');
      setRawKey('');
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
    },
  });
  const action = useMutation({
    mutationFn: ({ id, path, body }: { id: number; path?: string; body?: unknown }) =>
      apiFetch(`/admin/api/provider-keys/${id}${path ?? ''}`, { method: path ? 'POST' : 'PATCH', body: body ? JSON.stringify(body) : undefined }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['provider-keys'] }),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim() && rawKey.trim()) {
      createKey.mutate();
    }
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold">Provider Keys</h1>
      <Panel
        title="Create"
        action={
          <form className="grid gap-2 md:grid-cols-[120px_1fr_1fr_auto]" onSubmit={submit}>
            <label className="grid gap-1 text-sm font-medium text-zinc-700">
              <span>Provider</span>
              <select className="h-9 rounded-md border border-zinc-300 bg-white px-3" onChange={(event) => setProvider(event.target.value)} value={provider}>
                <option value="grok">Grok</option>
                <option value="tavily">Tavily</option>
                <option value="firecrawl">Firecrawl</option>
              </select>
            </label>
            <Field label="Name" onChange={(event) => setName(event.target.value)} value={name} />
            <Field label="Key" onChange={(event) => setRawKey(event.target.value)} type="password" value={rawKey} />
            <Button className="mt-6" disabled={createKey.isPending || !name.trim() || !rawKey.trim()} type="submit">
              <Plus size={16} />
              Add
            </Button>
          </form>
        }
      >
        <ResourceTable
          empty="No provider keys"
          rows={(data?.items ?? []).map((row) => {
            const record = row as Record<string, unknown>;
            const id = valueOf<number>(record, 'ID', 'id', 0);
            const enabled = valueOf<boolean>(record, 'Enabled', 'enabled', false);
            const archived = valueOf<string | undefined>(record, 'ArchivedAt', 'archived_at', undefined);
            return {
              id,
              cols: [
                valueOf<string>(record, 'Provider', 'provider', ''),
                valueOf<string>(record, 'Name', 'name', ''),
                valueOf<string>(record, 'KeyPrefix', 'key_prefix', ''),
                archived ? <Badge tone="bad">archived</Badge> : <Badge tone={enabled ? 'good' : 'warn'}>{enabled ? 'enabled' : 'disabled'}</Badge>,
              ],
              actions: (
                <>
                  <Button disabled={action.isPending || Boolean(archived)} onClick={() => action.mutate({ id, body: { enabled: !enabled } })} type="button" variant="secondary">
                    {enabled ? 'Disable' : 'Enable'}
                  </Button>
                  <Button disabled={action.isPending || Boolean(archived)} onClick={() => action.mutate({ id, path: '/reset-cooldown' })} type="button" variant="secondary">
                    Reset cooldown
                  </Button>
                  <Button disabled={action.isPending} onClick={() => action.mutate({ id, path: archived ? '/restore' : '/archive' })} type="button" variant="danger">
                    {archived ? 'Restore' : 'Archive'}
                  </Button>
                </>
              ),
            };
          })}
        />
      </Panel>
    </div>
  );
}
