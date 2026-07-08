import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, Save, TestTube2 } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { ListResponse, ProviderHealth, ProviderKey, ProviderQuota, ProviderSetting } from '../../api/types';
import { Badge, Button, Field, Panel } from '../../components/ui';

export function ProvidersPage() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['provider-settings'], queryFn: () => apiFetch<ListResponse<ProviderSetting>>('/admin/api/provider-settings') });
  const health = useQuery({ queryKey: ['provider-health'], queryFn: () => apiFetch<ListResponse<ProviderHealth>>('/admin/api/provider-health') });
  const quotas = useQuery({ queryKey: ['provider-quotas'], queryFn: () => apiFetch<ListResponse<ProviderQuota>>('/admin/api/provider-quotas') });
  const providerKeys = useQuery({ queryKey: ['provider-keys'], queryFn: () => apiFetch<ListResponse<ProviderKey>>('/admin/api/provider-keys') });
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
  const refreshingPath = postAction.isPending && postAction.variables ? postAction.variables : null;

  return (
    <div>
      <h1 className="text-2xl font-semibold">Providers</h1>
      <Panel title="Settings">
        <div className="grid gap-3">
          {(settings.data?.items ?? []).map((setting) => (
            <ProviderSettingRow key={setting.provider} onSave={(baseURL) => saveSetting.mutate({ provider: setting.provider, baseURL })} setting={setting} />
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
      <Panel title="Quotas">
        <div className="grid gap-3 md:grid-cols-3">
          {(quotas.data?.items ?? []).map((quota) => (
            <QuotaCard
              key={quota.provider}
              onRefresh={() => postAction.mutate(`/admin/api/provider-quotas/${quota.provider}/refresh`)}
              providerKeys={providerKeys.data?.items ?? []}
              quota={quota}
              refreshing={refreshingPath === `/admin/api/provider-quotas/${quota.provider}/refresh`}
            />
          ))}
        </div>
      </Panel>
    </div>
  );
}

function detailNumber(details: ProviderQuota['details'], key: string): number | undefined {
  const v = details?.[key];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

function quotaRemainingLabel(quota: ProviderQuota): string | null {
  if (!quota.available || quota.remaining == null) return null;
  if (quota.limit_value != null) return `${quota.remaining} / ${quota.limit_value} remaining`;
  const plan = detailNumber(quota.details, 'plan_credits');
  const extra = detailNumber(quota.details, 'extra_credits_remaining');
  if (plan != null && extra != null && extra > 0) {
    return `${quota.remaining} credits remaining (${plan} plan + ${extra} one-time)`;
  }
  return `${quota.remaining} remaining`;
}

function QuotaCard({
  quota,
  onRefresh,
  providerKeys,
  refreshing,
}: {
  quota: ProviderQuota;
  onRefresh: () => void;
  providerKeys: ProviderKey[];
  refreshing: boolean;
}) {
  const remainingLabel = quotaRemainingLabel(quota);
  const scopedKeys = providerKeys.filter(
    (key) =>
      (key.provider ?? key.Provider) === quota.provider &&
      (key.enabled ?? key.Enabled) &&
      !(key.archived_at ?? key.ArchivedAt),
  );
  const quotaKey = scopedKeys.find((key) => (key.id ?? key.ID) === quota.provider_key_id);
  const quotaKeyName = quotaKey?.name ?? quotaKey?.Name;

  return (
    <div className="border-t border-zinc-200 py-3">
      <div className="flex items-center justify-between gap-2">
        <strong className="capitalize">{quota.provider}</strong>
        <Badge tone={quota.available ? 'good' : 'bad'}>{quota.available ? 'available' : 'not available'}</Badge>
      </div>
      {remainingLabel ? <p className="mt-2 text-sm font-medium text-zinc-800">{remainingLabel}</p> : null}
      {quota.used != null ? <p className="text-sm text-zinc-600">Used: {quota.used}</p> : null}
      <p className="mt-1 text-xs text-zinc-500">Source: {quota.source || '—'}</p>
      {quota.provider_key_id ? (
        <p className="text-xs text-zinc-500">Quota key: {quotaKeyName ? `${quotaKeyName} ` : ''}(#{quota.provider_key_id})</p>
      ) : null}
      {quota.provider_key_id && scopedKeys.length > 1 ? (
        <p className="text-xs text-zinc-500">Quota refresh uses 1 of {scopedKeys.length} enabled keys.</p>
      ) : null}
      {quota.checked_at ? <p className="text-xs text-zinc-500">Checked: {formatChecked(quota.checked_at)}</p> : null}
      {!quota.available && quota.message_redacted ? (
        <p className="mt-2 text-sm text-red-700">{quota.message_redacted}</p>
      ) : null}
      <Button
        aria-label={`Refresh quota for ${quota.provider}`}
        className="mt-3"
        disabled={refreshing}
        onClick={onRefresh}
        type="button"
        variant="secondary"
      >
        <RefreshCw className={refreshing ? 'animate-spin' : ''} size={16} />
        {refreshing ? 'Refreshing…' : 'Refresh'}
      </Button>
    </div>
  );
}

function formatChecked(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
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
