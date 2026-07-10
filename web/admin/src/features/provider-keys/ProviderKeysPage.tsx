import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Plus } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { EndpointQuotaInput, ListResponse, ProviderKey, ProviderSetting, QuotaMode } from '../../api/types';
import { Badge, Button, Panel, valueOf } from '../../components/ui';
import { ResourceTable } from '../gateway-keys/GatewayKeysPage';
import { EndpointDefaultsPanel } from './EndpointDefaultsPanel';
import { EndpointEditorSheet, quotaModeLabel } from './EndpointEditorSheet';

const ENDPOINTS_PATH = '/admin/api/provider-endpoints';

function summarizeQuota(record: Record<string, unknown>): string {
  const mode = valueOf<QuotaMode | string>(record, 'QuotaMode', 'quota_mode', '');
  const flow = valueOf<string>(record, 'QuotaFlow', 'quota_flow', '');
  const configured = valueOf<boolean>(record, 'QuotaKeyConfigured', 'quota_key_configured', false);
  const quotaURL = valueOf<string>(record, 'QuotaBaseURL', 'quota_base_url', '') || '';
  const label = quotaModeLabel(mode as QuotaMode);
  if (mode === 'separate_credentials') {
    const conf = configured ? 'key set' : 'key not set';
    return `${label}${quotaURL ? ` · ${quotaURL}` : ''} · ${conf}${flow ? ` · ${flow}` : ''}`;
  }
  if (mode === 'endpoint_credentials') {
    return `${label}${flow ? ` · ${flow}` : ''}`;
  }
  if (mode === 'disabled') {
    return label;
  }
  return mode || '—';
}

export function ProviderKeysPage() {
  const qc = useQueryClient();
  const [sheet, setSheet] = useState<'create' | ProviderKey | null>(null);

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

  const createEndpoint = useMutation({
    mutationFn: (body: {
      provider: string;
      name: string;
      base_url: string;
      key: string;
      quota: EndpointQuotaInput;
    }) =>
      apiFetch<ProviderKey>(ENDPOINTS_PATH, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      setSheet(null);
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

  const saveEdit = useMutation({
    mutationFn: async (input: {
      id: number;
      base_url: string;
      key?: string;
      quota: EndpointQuotaInput;
      quota_key?: string;
    }) => {
      await apiFetch(`${ENDPOINTS_PATH}/${input.id}/update-base-url`, {
        method: 'POST',
        body: JSON.stringify({ base_url: input.base_url }),
      });
      if (input.key?.trim()) {
        await apiFetch(`${ENDPOINTS_PATH}/${input.id}/rotate-key`, {
          method: 'POST',
          body: JSON.stringify({ key: input.key }),
        });
      }
      await apiFetch(`${ENDPOINTS_PATH}/${input.id}/update-quota`, {
        method: 'POST',
        body: JSON.stringify({
          mode: input.quota.mode,
          flow: input.quota.flow,
          base_url: input.quota.base_url ?? '',
        }),
      });
      if (input.quota_key?.trim()) {
        await apiFetch(`${ENDPOINTS_PATH}/${input.id}/rotate-quota-key`, {
          method: 'POST',
          body: JSON.stringify({ key: input.quota_key }),
        });
      }
    },
    onSuccess: () => {
      setSheet(null);
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
      void qc.invalidateQueries({ queryKey: ['provider-pools'] });
    },
  });

  const editing = sheet && sheet !== 'create' ? sheet : null;

  return (
    <div>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">Provider Endpoints</h1>
          <p className="mt-1 text-sm text-zinc-600">
            Configure inference routes and quota sidecars. Endpoint name does not define routing priority.
          </p>
        </div>
        <Link className="text-sm font-medium text-zinc-900 underline underline-offset-2" to="/providers">
          Provider Monitoring
        </Link>
      </div>

      <Panel
        title="Endpoints"
        action={
          <Button onClick={() => setSheet('create')} type="button">
            <Plus size={16} />
            Add endpoint
          </Button>
        }
      >
        <div className="mb-4">
          <EndpointDefaultsPanel />
        </div>
        <ResourceTable
          empty="No provider endpoints"
          rows={(data?.items ?? []).map((row) => {
            const record = row as Record<string, unknown>;
            const id = valueOf<number>(record, 'ID', 'id', 0);
            const enabled = valueOf<boolean>(record, 'Enabled', 'enabled', false);
            const archived = valueOf<string | undefined>(record, 'ArchivedAt', 'archived_at', undefined);
            const rowBaseURL = valueOf<string>(record, 'BaseURL', 'base_url', '');
            const rowName = valueOf<string>(record, 'Name', 'name', '');
            const provider = valueOf<string>(record, 'Provider', 'provider', '');
            const keyPrefix = valueOf<string>(record, 'KeyPrefix', 'key_prefix', '');
            const quotaSummary = summarizeQuota(record);
            return {
              id,
              cols: [
                provider,
                rowName,
                <div key="inf" className="grid gap-0.5">
                  <span className="text-xs font-medium text-zinc-500">Inference</span>
                  <span>{rowBaseURL || '—'}</span>
                  <span className="text-xs text-zinc-500">{keyPrefix || '—'}</span>
                </div>,
                <div key="quota" className="grid gap-0.5">
                  <span className="text-xs font-medium text-zinc-500">Quota</span>
                  <span className="text-xs text-zinc-700">{quotaSummary}</span>
                </div>,
                archived ? (
                  <Badge tone="bad">archived</Badge>
                ) : (
                  <Badge tone={enabled ? 'good' : 'warn'}>{enabled ? 'enabled' : 'disabled'}</Badge>
                ),
              ],
              actions: (
                <>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => setSheet(row)}
                    type="button"
                    variant="secondary"
                  >
                    Edit
                  </Button>
                  <Button
                    disabled={action.isPending || Boolean(archived)}
                    onClick={() => action.mutate({ id, body: { enabled: !enabled } })}
                    type="button"
                    variant="secondary"
                  >
                    {enabled ? 'Disable' : 'Enable'}
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

      {sheet === 'create' ? (
        <EndpointEditorSheet
          defaultBaseURL={defaultURLByProvider.grok ?? ''}
          defaultURLByProvider={defaultURLByProvider}
          mode="create"
          onClose={() => setSheet(null)}
          onCreate={async (input) => {
            await createEndpoint.mutateAsync(input);
          }}
          pending={createEndpoint.isPending}
          settings={settings.data?.items}
        />
      ) : null}

      {editing ? (
        <EndpointEditorSheet
          endpoint={editing}
          mode="edit"
          onClose={() => setSheet(null)}
          onCreate={async () => undefined}
          onUpdate={async (input) => {
            await saveEdit.mutateAsync(input);
          }}
          pending={saveEdit.isPending}
        />
      ) : null}
    </div>
  );
}

