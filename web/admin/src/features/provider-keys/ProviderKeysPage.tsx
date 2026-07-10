import { FormEvent, useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { ListResponse, ProviderKey, ProviderSetting } from '../../api/types';
import { Badge, Button, Dialog, Field, Panel, valueOf } from '../../components/ui';
import { ResourceTable } from '../gateway-keys/GatewayKeysPage';

const ENDPOINTS_PATH = '/admin/api/provider-endpoints';

type EditURLState = { id: number; baseURL: string; name: string } | null;
type RotateKeyState = { id: number; name: string } | null;

export function ProviderKeysPage() {
  const qc = useQueryClient();
  const [provider, setProvider] = useState('grok');
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [rawKey, setRawKey] = useState('');
  const [editURL, setEditURL] = useState<EditURLState>(null);
  const [rotateKey, setRotateKey] = useState<RotateKeyState>(null);
  const [editURLValue, setEditURLValue] = useState('');
  const [rotateKeyValue, setRotateKeyValue] = useState('');

  const { data } = useQuery({
    queryKey: ['provider-endpoints'],
    queryFn: () => apiFetch<ListResponse<ProviderKey>>(ENDPOINTS_PATH),
  });
  const settings = useQuery({
    queryKey: ['provider-settings'],
    queryFn: () => apiFetch<ListResponse<ProviderSetting>>('/admin/api/provider-settings'),
  });

  const defaultURLByProvider = useMemo(() => {
    const map: Record<string, string> = {};
    for (const item of settings.data?.items ?? []) {
      map[item.provider] = item.base_url;
    }
    return map;
  }, [settings.data?.items]);

  // Prefill base URL from provider default when provider changes and field is empty
  // or still holds a previous provider's default.
  useEffect(() => {
    const def = defaultURLByProvider[provider] ?? '';
    setBaseURL((current) => {
      const otherDefaults = Object.entries(defaultURLByProvider)
        .filter(([p]) => p !== provider)
        .map(([, u]) => u);
      if (!current.trim() || otherDefaults.includes(current)) {
        return def;
      }
      return current;
    });
  }, [provider, defaultURLByProvider]);

  const createKey = useMutation({
    mutationFn: () =>
      apiFetch<ProviderKey>(ENDPOINTS_PATH, {
        method: 'POST',
        body: JSON.stringify({ provider, name, base_url: baseURL, key: rawKey }),
      }),
    onSuccess: () => {
      setName('');
      setRawKey('');
      setBaseURL(defaultURLByProvider[provider] ?? '');
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
    },
  });
  const action = useMutation({
    mutationFn: ({ id, path, body }: { id: number; path?: string; body?: unknown }) =>
      apiFetch(`${ENDPOINTS_PATH}/${id}${path ?? ''}`, {
        method: path ? 'POST' : 'PATCH',
        body: body ? JSON.stringify(body) : undefined,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
      void qc.invalidateQueries({ queryKey: ['provider-pools'] });
    },
  });
  const updateBaseURL = useMutation({
    mutationFn: ({ id, base_url }: { id: number; base_url: string }) =>
      apiFetch(`${ENDPOINTS_PATH}/${id}/update-base-url`, {
        method: 'POST',
        body: JSON.stringify({ base_url }),
      }),
    onSuccess: () => {
      setEditURL(null);
      setEditURLValue('');
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
      void qc.invalidateQueries({ queryKey: ['provider-pools'] });
    },
  });
  const rotateKeyMutation = useMutation({
    mutationFn: ({ id, key }: { id: number; key: string }) =>
      apiFetch(`${ENDPOINTS_PATH}/${id}/rotate-key`, {
        method: 'POST',
        body: JSON.stringify({ key }),
      }),
    onSuccess: () => {
      setRotateKey(null);
      setRotateKeyValue('');
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
      void qc.invalidateQueries({ queryKey: ['provider-pools'] });
    },
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim() && rawKey.trim() && baseURL.trim()) {
      createKey.mutate();
    }
  }

  return (
    <div>
      <h1 className="text-2xl font-semibold">Provider Endpoints</h1>
      <Panel
        title="Create"
        action={
          <form className="grid gap-2 md:grid-cols-[120px_1fr_1fr_1fr_auto]" onSubmit={submit}>
            <label className="grid gap-1 text-sm font-medium text-zinc-700">
              <span>Provider</span>
              <select
                className="h-9 rounded-md border border-zinc-300 bg-white px-3"
                onChange={(event) => setProvider(event.target.value)}
                value={provider}
              >
                <option value="grok">Grok</option>
                <option value="tavily">Tavily</option>
                <option value="firecrawl">Firecrawl</option>
              </select>
            </label>
            <Field label="Name" onChange={(event) => setName(event.target.value)} value={name} />
            <Field label="Base URL" onChange={(event) => setBaseURL(event.target.value)} value={baseURL} />
            <Field label="Key" onChange={(event) => setRawKey(event.target.value)} type="password" value={rawKey} autoComplete="off" />
            <Button
              className="mt-6"
              disabled={createKey.isPending || !name.trim() || !rawKey.trim() || !baseURL.trim()}
              type="submit"
            >
              <Plus size={16} />
              Add
            </Button>
          </form>
        }
      >
        <ResourceTable
          empty="No provider endpoints"
          rows={(data?.items ?? []).map((row) => {
            const record = row as Record<string, unknown>;
            const id = valueOf<number>(record, 'ID', 'id', 0);
            const enabled = valueOf<boolean>(record, 'Enabled', 'enabled', false);
            const archived = valueOf<string | undefined>(record, 'ArchivedAt', 'archived_at', undefined);
            const cooldownReason = valueOf<string>(record, 'CooldownReason', 'cooldown_reason', '');
            const lastFailedAt = valueOf<string | undefined>(record, 'LastFailedAt', 'last_failed_at', undefined);
            const cooldownUntil = valueOf<string | undefined>(record, 'CooldownUntil', 'cooldown_until', undefined);
            const rowBaseURL = valueOf<string>(record, 'BaseURL', 'base_url', '');
            const rowName = valueOf<string>(record, 'Name', 'name', '');
            return {
              id,
              cols: [
                valueOf<string>(record, 'Provider', 'provider', ''),
                rowName,
                rowBaseURL || '—',
                valueOf<string>(record, 'KeyPrefix', 'key_prefix', ''),
                archived ? (
                  <Badge tone="bad">archived</Badge>
                ) : (
                  <Badge tone={enabled ? 'good' : 'warn'}>{enabled ? 'enabled' : 'disabled'}</Badge>
                ),
                cooldownReason || cooldownUntil ? (
                  <span className="text-xs text-zinc-600">
                    {cooldownReason || 'cooling'}
                    {cooldownUntil ? ` · until ${new Date(cooldownUntil).toLocaleString()}` : ''}
                  </span>
                ) : (
                  '—'
                ),
                lastFailedAt ? (
                  <span className="text-xs text-amber-700" title={lastFailedAt}>
                    demoted {new Date(lastFailedAt).toLocaleString()}
                  </span>
                ) : (
                  <span className="text-xs text-zinc-500">front</span>
                ),
              ],
              actions: (
                <>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => action.mutate({ id, body: { enabled: !enabled } })}
                    type="button"
                    variant="secondary"
                  >
                    {enabled ? 'Disable' : 'Enable'}
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => {
                      setEditURL({ id, baseURL: rowBaseURL, name: rowName });
                      setEditURLValue(rowBaseURL);
                    }}
                    type="button"
                    variant="secondary"
                  >
                    Edit URL
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => {
                      setRotateKey({ id, name: rowName });
                      setRotateKeyValue('');
                    }}
                    type="button"
                    variant="secondary"
                  >
                    Rotate key
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => action.mutate({ id, path: '/reset-cooldown' })}
                    type="button"
                    variant="secondary"
                  >
                    Reset cool+order
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived) || !lastFailedAt}
                    onClick={() => action.mutate({ id, path: '/reset-selection' })}
                    type="button"
                    variant="secondary"
                  >
                    Promote
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => action.mutate({ id, path: '/demote' })}
                    type="button"
                    variant="secondary"
                  >
                    Demote
                  </Button>
                  <Button
                    disabled={action.isPending}
                    onClick={() => action.mutate({ id, path: archived ? '/restore' : '/archive' })}
                    type="button"
                    variant="danger"
                  >
                    {archived ? 'Restore' : 'Archive'}
                  </Button>
                </>
              ),
            };
          })}
        />
      </Panel>

      {editURL ? (
        <Dialog title={`Edit base URL · ${editURL.name || editURL.id}`} onClose={() => setEditURL(null)}>
          <form
            className="grid gap-3"
            onSubmit={(event) => {
              event.preventDefault();
              if (editURLValue.trim()) {
                updateBaseURL.mutate({ id: editURL.id, base_url: editURLValue.trim() });
              }
            }}
          >
            <Field
              label="Base URL"
              onChange={(event) => setEditURLValue(event.target.value)}
              value={editURLValue}
            />
            <div className="flex justify-end gap-2">
              <Button onClick={() => setEditURL(null)} type="button" variant="secondary">
                Cancel
              </Button>
              <Button disabled={updateBaseURL.isPending || !editURLValue.trim()} type="submit">
                Save URL
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}

      {rotateKey ? (
        <Dialog title={`Rotate key · ${rotateKey.name || rotateKey.id}`} onClose={() => setRotateKey(null)}>
          <form
            className="grid gap-3"
            onSubmit={(event) => {
              event.preventDefault();
              if (rotateKeyValue.trim()) {
                rotateKeyMutation.mutate({ id: rotateKey.id, key: rotateKeyValue });
              }
            }}
          >
            <Field
              autoComplete="off"
              label="New key"
              onChange={(event) => setRotateKeyValue(event.target.value)}
              type="password"
              value={rotateKeyValue}
            />
            <p className="text-xs text-zinc-500">Existing secret is never shown. Paste the new raw key only.</p>
            <div className="flex justify-end gap-2">
              <Button
                onClick={() => {
                  setRotateKey(null);
                  setRotateKeyValue('');
                }}
                type="button"
                variant="secondary"
              >
                Cancel
              </Button>
              <Button disabled={rotateKeyMutation.isPending || !rotateKeyValue.trim()} type="submit">
                Confirm rotate
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
