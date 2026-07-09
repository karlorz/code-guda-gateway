import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, RefreshCw, Save, TestTube2 } from 'lucide-react';
import { useState } from 'react';
import { apiFetch } from '../../api/client';
import type {
  ListResponse,
  ProviderHealth,
  ProviderKey,
  ProviderKeyQuota,
  ProviderPool,
  ProviderPoolRow,
  ProviderQuota,
  ProviderSetting,
} from '../../api/types';
import { Badge, Button, Field, Panel, valueOf } from '../../components/ui';

const POOL_PROVIDERS = ['grok', 'tavily', 'firecrawl'] as const;
const PAGE_SIZE = 25;

export function ProvidersPage() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['provider-settings'], queryFn: () => apiFetch<ListResponse<ProviderSetting>>('/admin/api/provider-settings') });
  const health = useQuery({ queryKey: ['provider-health'], queryFn: () => apiFetch<ListResponse<ProviderHealth>>('/admin/api/provider-health') });
  const quotas = useQuery({ queryKey: ['provider-quotas'], queryFn: () => apiFetch<ListResponse<ProviderQuota>>('/admin/api/provider-quotas') });
  const saveSetting = useMutation({
    mutationFn: ({ provider, baseURL }: { provider: string; baseURL: string }) =>
      apiFetch(`/admin/api/provider-settings/${provider}`, { method: 'PATCH', body: JSON.stringify({ base_url: baseURL }) }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['provider-settings'] }),
  });
  const postAction = useMutation({
    mutationFn: (path: string) => apiFetch(path, { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['provider-health'] });
      void qc.invalidateQueries({ queryKey: ['provider-quotas'] });
    },
  });

  const quotaByProvider = Object.fromEntries((quotas.data?.items ?? []).map((q) => [q.provider, q]));

  return (
    <div>
      <h1 className="text-2xl font-semibold">Providers</h1>
      <Panel title="Settings">
        <div className="grid gap-3">
          {(settings.data?.items ?? []).map((setting) => (
            <ProviderSettingRow
              key={setting.provider}
              onSave={(baseURL) => saveSetting.mutate({ provider: setting.provider, baseURL })}
              setting={setting}
            />
          ))}
        </div>
      </Panel>
      <Panel title="Health">
        <div className="grid gap-2 md:grid-cols-3">
          {(health.data?.items ?? []).map((item) => (
            <div className="border-t border-zinc-200 py-3" key={item.provider}>
              <div className="flex items-center justify-between">
                <strong>{item.provider}</strong>
                <Badge tone={item.status === 'healthy' ? 'good' : item.status === 'missing_key' || item.status === 'degraded' ? 'bad' : 'warn'}>{item.status}</Badge>
              </div>
              <p className="mt-2 text-sm text-zinc-600">{item.enabled_key_count}/{item.key_count} enabled</p>
              {(item.reasons ?? []).map((reason) => (
                <p className="mt-1 text-xs text-zinc-500" key={reason}>{reason}</p>
              ))}
              <Button className="mt-3" onClick={() => postAction.mutate(`/admin/api/providers/${item.provider}/test`)} type="button" variant="secondary">
                <TestTube2 size={16} />
                Select key
              </Button>
            </div>
          ))}
        </div>
      </Panel>
      <Panel title="Provider Pools">
        <div className="grid gap-6">
          {POOL_PROVIDERS.map((provider) => (
            <ProviderPoolSection key={provider} provider={provider} sampleQuota={quotaByProvider[provider]} />
          ))}
        </div>
      </Panel>
    </div>
  );
}

function statusTone(status: ProviderPoolRow['status']) {
  if (status === 'available') return 'good' as const;
  if (status === 'cooling' || status === 'not_refreshed') return 'warn' as const;
  return 'bad' as const;
}

function keyID(key: ProviderKey): number {
  return key.id ?? key.ID ?? 0;
}

function keyName(key: ProviderKey): string {
  return key.name ?? key.Name ?? '';
}

function detailNumber(details: ProviderQuota['details'] | ProviderKeyQuota['details'], key: string): number | undefined {
  const v = details?.[key];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

function quotaRemainingLabel(quota: ProviderQuota | ProviderKeyQuota): string | null {
  if (!quota.available) return null;
  if (quota.remaining != null) {
    if (quota.limit_value != null) return `${quota.remaining} / ${quota.limit_value} remaining`;
    const plan = detailNumber(quota.details, 'plan_credits');
    const extra = detailNumber(quota.details, 'extra_credits_remaining');
    if (plan != null && extra != null && extra > 0) {
      return `${quota.remaining} credits remaining (${plan} plan + ${extra} one-time)`;
    }
    return `${quota.remaining} remaining`;
  }
  // A quota row exists (key was refreshed) but the provider didn't return a
  // computable remaining (e.g. Tavily has no top-level key.limit). Surface the
  // usage we do have instead of falling back to "not refreshed".
  if (quota.used != null) {
    return quota.limit_value != null ? `used ${quota.used} / ${quota.limit_value}` : `used ${quota.used}`;
  }
  return null;
}

function formatChecked(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function providerTitle(provider: string): string {
  return `${provider[0].toUpperCase()}${provider.slice(1)} Pool`;
}

function ProviderPoolSection({ provider, sampleQuota }: { provider: string; sampleQuota?: ProviderQuota }) {
  const qc = useQueryClient();
  const [offset, setOffset] = useState(0);
  const pool = useQuery({
    queryKey: ['provider-pools', provider, offset],
    queryFn: () => apiFetch<ProviderPool>(`/admin/api/provider-pools/${provider}?limit=${PAGE_SIZE}&offset=${offset}`),
  });

  const invalidatePool = () => {
    void qc.invalidateQueries({ queryKey: ['provider-pools', provider] });
    void qc.invalidateQueries({ queryKey: ['provider-quotas'] });
  };

  const refreshSample = useMutation({
    mutationFn: () => apiFetch(`/admin/api/provider-quotas/${provider}/refresh`, { method: 'POST' }),
    onSuccess: invalidatePool,
  });
  const refreshAll = useMutation({
    mutationFn: () => apiFetch(`/admin/api/provider-key-quotas/${provider}/refresh-all`, { method: 'POST' }),
    onSuccess: invalidatePool,
  });
  const refreshOne = useMutation({
    mutationFn: (id: number) => apiFetch(`/admin/api/provider-key-quotas/${id}/refresh`, { method: 'POST' }),
    onSuccess: invalidatePool,
  });

  // Only render after data loads so tests waiting on the title also wait for pool fetch.
  if (!pool.data) {
    return null;
  }

  const summary = pool.data.summary;
  const page = pool.data.page;
  const total = page?.total ?? 0;
  const canPrev = offset > 0;
  const canNext = offset + PAGE_SIZE < total;

  return (
    <div className="border-t border-zinc-200 pt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-zinc-950">{providerTitle(provider)}</h3>
        <div className="flex flex-wrap gap-2">
          <Button
            aria-label={`Refresh sample for ${provider}`}
            disabled={refreshSample.isPending}
            onClick={() => refreshSample.mutate()}
            type="button"
            variant="secondary"
          >
            <RefreshCw className={refreshSample.isPending ? 'animate-spin' : ''} size={14} />
            Refresh sample
          </Button>
          <Button
            aria-label={`Refresh all ${provider} keys`}
            disabled={refreshAll.isPending}
            onClick={() => refreshAll.mutate()}
            type="button"
            variant="secondary"
          >
            <RefreshCw className={refreshAll.isPending ? 'animate-spin' : ''} size={14} />
            Refresh all
          </Button>
        </div>
      </div>

      {summary ? (
        <p className="mt-2 text-sm text-zinc-600">
          {`Enabled ${summary.enabled_key_count ?? 0} · Available ${summary.available_key_count ?? 0} · Cooling ${summary.cooling_key_count ?? 0} · Refreshed ${summary.refreshed_key_count ?? 0}${summary.known_remaining != null ? ` · KnownRemaining ${summary.known_remaining}` : ''}`}
        </p>
      ) : null}

      {sampleQuota && !sampleQuota.available && sampleQuota.message_redacted ? (
        <p className="mt-1 text-xs text-red-700">{sampleQuota.message_redacted}</p>
      ) : null}

      <div className="mt-3 overflow-x-auto">
        <table className="w-full min-w-[640px] text-left text-sm">
          <thead className="text-xs uppercase text-zinc-500">
            <tr className="border-b border-zinc-200">
              <th className="py-2 pr-3 font-medium">Name</th>
              <th className="py-2 pr-3 font-medium">Status</th>
              <th className="py-2 pr-3 font-medium">Cooldown</th>
              <th className="py-2 pr-3 font-medium">Quota</th>
              <th className="py-2 pr-3 font-medium">Checked</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {(pool.data.items ?? []).map((row) => {
              const id = keyID(row.key);
              const remaining = row.quota ? quotaRemainingLabel(row.quota) : null;
              const cooldownReason = valueOf<string>(row.key as Record<string, unknown>, 'CooldownReason', 'cooldown_reason', '');
              return (
                <tr className="border-b border-zinc-100" key={id || keyName(row.key)}>
                  <td className="py-2 pr-3 font-medium text-zinc-900">{keyName(row.key)}</td>
                  <td className="py-2 pr-3">
                    <Badge tone={statusTone(row.status)}>{row.status}</Badge>
                  </td>
                  <td className="py-2 pr-3 text-xs text-zinc-600">{cooldownReason || '—'}</td>
                  <td className="py-2 pr-3 text-zinc-700">{remaining ?? (row.quota ? 'available' : 'not refreshed')}</td>
                  <td className="py-2 pr-3 text-xs text-zinc-500">
                    {row.quota?.checked_at ? formatChecked(row.quota.checked_at) : '—'}
                  </td>
                  <td className="py-2 text-right">
                    <Button
                      aria-label={`Refresh key ${id}`}
                      disabled={refreshOne.isPending || !id}
                      onClick={() => refreshOne.mutate(id)}
                      type="button"
                      variant="secondary"
                    >
                      <RefreshCw className={refreshOne.isPending && refreshOne.variables === id ? 'animate-spin' : ''} size={14} />
                      Refresh
                    </Button>
                  </td>
                </tr>
              );
            })}
            {(pool.data.items?.length ?? 0) === 0 ? (
              <tr>
                <td className="py-3 text-sm text-zinc-500" colSpan={6}>No keys</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <Button disabled={!canPrev} onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))} type="button" variant="secondary">
          <ChevronLeft size={14} />
          Prev
        </Button>
        <Button disabled={!canNext} onClick={() => setOffset((o) => o + PAGE_SIZE)} type="button" variant="secondary">
          Next
          <ChevronRight size={14} />
        </Button>
        <span className="text-xs text-zinc-500">
          {total > 0 ? `${offset + 1}–${Math.min(offset + PAGE_SIZE, total)} of ${total}` : '0 of 0'}
        </span>
      </div>
    </div>
  );
}

function ProviderSettingRow({ setting, onSave }: { setting: ProviderSetting; onSave: (baseURL: string) => void }) {
  return (
    <form
      className="grid gap-2 border-t border-zinc-200 py-3 md:grid-cols-[120px_1fr_auto]"
      onSubmit={(event) => {
        event.preventDefault();
        const data = new FormData(event.currentTarget);
        onSave(String(data.get('base_url') ?? ''));
      }}
    >
      <strong className="pt-2 capitalize">{setting.provider}</strong>
      <Field defaultValue={setting.base_url} label="Base URL" name="base_url" />
      <Button className="mt-6" type="submit" variant="secondary">
        <Save size={16} />
        Save
      </Button>
    </form>
  );
}
