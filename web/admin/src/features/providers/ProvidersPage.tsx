import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, RefreshCw, TestTube2 } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { apiFetch } from '../../api/client';
import type {
  ListResponse,
  ProviderHealth,
  ProviderKey,
  ProviderKeyQuota,
  ProviderPool,
  ProviderPoolRow,
  ProviderQuota,
  QuotaMode,
  QuotaOperationalState,
  RefreshAllKeyQuotasResult,
} from '../../api/types';
import { Badge, Button, PageHeader, Panel, SummaryGrid, SummaryMetric, valueOf } from '../../components/ui';

const POOL_PROVIDERS = ['grok', 'tavily', 'firecrawl'] as const;
const PAGE_SIZE = 25;

const QUOTA_SOURCE_DISABLED = 'quota_disabled';
const QUOTA_SOURCE_NOT_CONFIGURED = 'quota_not_configured';

/** Pool table view: default hides rows that cannot be selected for inference. */
export type PoolRowView = 'enabled' | 'all';

/** True when the row is eligible for the default monitoring view (in the live selection pack). */
export function isEnabledPoolRow(row: ProviderPoolRow): boolean {
  return row.status !== 'disabled' && row.status !== 'archived';
}

export function ProvidersPage() {
  const qc = useQueryClient();
  const health = useQuery({ queryKey: ['provider-health'], queryFn: () => apiFetch<ListResponse<ProviderHealth>>('/admin/api/provider-health') });
  const quotas = useQuery({ queryKey: ['provider-quotas'], queryFn: () => apiFetch<ListResponse<ProviderQuota>>('/admin/api/provider-quotas') });
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
      <PageHeader
        actions={
          <Link
            className="inline-flex h-9 items-center rounded-md bg-zinc-950 px-3 text-sm font-medium text-white hover:bg-zinc-800"
            to="/provider-keys"
          >
            Manage Provider Endpoints
          </Link>
        }
        description="Inference readiness, selection order, cooldown, and per-endpoint quota."
        title="Provider Monitoring"
      />
      <Panel title="Provider readiness">
        <div className="grid gap-3 md:grid-cols-3">
          {(health.data?.items ?? []).map((item) => {
            const reason = (item.reasons ?? [])[0];
            return (
              <article
                className="rounded-lg border border-zinc-200 bg-white p-3"
                data-testid={`provider-health-${item.provider}`}
                key={item.provider}
              >
                <div className="flex items-center justify-between gap-2">
                  <strong className="capitalize">{item.provider}</strong>
                  <Badge tone={item.status === 'healthy' ? 'good' : item.status === 'missing_key' || item.status === 'degraded' ? 'bad' : 'warn'}>
                    {item.status}
                  </Badge>
                </div>
                <p className="mt-2 text-sm text-zinc-600">{item.enabled_key_count}/{item.key_count} active</p>
                {reason ? <p className="mt-1 truncate text-xs text-zinc-500" title={reason}>{reason}</p> : null}
                <Button
                  className="mt-3"
                  onClick={() => postAction.mutate(`/admin/api/providers/${item.provider}/test`)}
                  type="button"
                  variant="secondary"
                >
                  <TestTube2 size={16} />
                  Select key
                </Button>
              </article>
            );
          })}
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
  return valueOf<number>(key as Record<string, unknown>, 'ID', 'id', 0);
}

function keyName(key: ProviderKey): string {
  return valueOf<string>(key as Record<string, unknown>, 'Name', 'name', '');
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

/** Map quota sidecar state independently of inference pool status. */
export function deriveQuotaOperationalState(row: ProviderPoolRow): QuotaOperationalState {
  const mode = valueOf<QuotaMode | string>(row.key as Record<string, unknown>, 'QuotaMode', 'quota_mode', '');
  const configured = valueOf<boolean>(row.key as Record<string, unknown>, 'QuotaKeyConfigured', 'quota_key_configured', false);
  const source = row.quota?.source ?? '';

  if (mode === 'disabled' || source === QUOTA_SOURCE_DISABLED) {
    return 'disabled';
  }
  if (mode === 'separate_credentials' && !configured) {
    return 'not_configured';
  }
  if (source === QUOTA_SOURCE_NOT_CONFIGURED) {
    return 'not_configured';
  }
  if (!row.quota) {
    return 'not_refreshed';
  }
  if (row.quota.available) {
    return 'ok';
  }
  return 'unavailable';
}

function quotaStateLabel(state: QuotaOperationalState, row: ProviderPoolRow): string {
  switch (state) {
    case 'disabled':
      return 'disabled';
    case 'not_configured':
      return 'not configured';
    case 'not_refreshed':
      return 'not refreshed';
    case 'ok': {
      const remaining = row.quota ? quotaRemainingLabel(row.quota) : null;
      return remaining ?? 'available';
    }
    case 'unavailable':
      return row.quota?.message_redacted || 'unavailable';
    default:
      return '—';
  }
}

function quotaBadgeTone(state: QuotaOperationalState): 'good' | 'warn' | 'bad' {
  if (state === 'ok') return 'good';
  if (state === 'disabled' || state === 'not_configured' || state === 'not_refreshed') return 'warn';
  return 'bad';
}

function ProviderPoolSection({ provider, sampleQuota }: { provider: string; sampleQuota?: ProviderQuota }) {
  const qc = useQueryClient();
  const [offset, setOffset] = useState(0);
  // Server-side view: default enabled (selection-eligible); Show all for full inventory.
  const [view, setView] = useState<PoolRowView>('enabled');
  const [refreshAllResult, setRefreshAllResult] = useState<RefreshAllKeyQuotasResult | null>(null);
  const pool = useQuery({
    queryKey: ['provider-pools', provider, offset, view],
    queryFn: () =>
      apiFetch<ProviderPool>(
        `/admin/api/provider-pools/${provider}?limit=${PAGE_SIZE}&offset=${offset}&view=${view}`,
      ),
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
    mutationFn: () => apiFetch<RefreshAllKeyQuotasResult>(`/admin/api/provider-key-quotas/${provider}/refresh-all`, { method: 'POST' }),
    onSuccess: (data) => {
      setRefreshAllResult(data);
      invalidatePool();
    },
  });
  const refreshOne = useMutation({
    mutationFn: (id: number) => apiFetch(`/admin/api/provider-key-quotas/${id}/refresh`, { method: 'POST' }),
    onSuccess: invalidatePool,
  });
  const keyAction = useMutation({
    mutationFn: ({ id, path }: { id: number; path: string }) =>
      apiFetch(`/admin/api/provider-endpoints/${id}${path}`, { method: 'POST' }),
    onSuccess: () => {
      invalidatePool();
      void qc.invalidateQueries({ queryKey: ['provider-endpoints'] });
      void qc.invalidateQueries({ queryKey: ['provider-keys'] });
      void qc.invalidateQueries({ queryKey: ['provider-health'] });
    },
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
  const showSampleQuotaError = summary.refreshed_key_count === 0 && sampleQuota && !sampleQuota.available && sampleQuota.message_redacted;

  const items = pool.data.items ?? [];
  const inactiveInSummary = Math.max(0, (summary.key_count ?? 0) - (summary.enabled_key_count ?? 0));

  return (
    <div className="border-t border-zinc-200 pt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-zinc-950">{providerTitle(provider)}</h3>
        <div className="flex flex-wrap gap-2">
          <Button
            aria-label={`Refresh quota sample for ${provider}`}
            disabled={refreshSample.isPending}
            onClick={() => refreshSample.mutate()}
            type="button"
            variant="secondary"
          >
            <RefreshCw className={refreshSample.isPending ? 'animate-spin' : ''} size={14} />
            Refresh sample
          </Button>
          <Button
            aria-label={`Refresh all ${provider} endpoint quotas`}
            disabled={refreshAll.isPending}
            onClick={() => refreshAll.mutate()}
            type="button"
            variant="secondary"
          >
            <RefreshCw className={refreshAll.isPending ? 'animate-spin' : ''} size={14} />
            Refresh all quotas
          </Button>
        </div>
      </div>

      {summary ? (
        <div data-testid={`pool-summary-${provider}`}>
          <SummaryGrid className="mt-3 lg:grid-cols-5">
            <SummaryMetric label="Enabled" testId={`pool-summary-${provider}-enabled`} value={summary.enabled_key_count ?? 0} />
            <SummaryMetric label="Available" testId={`pool-summary-${provider}-available`} tone="good" value={summary.available_key_count ?? 0} />
            <SummaryMetric label="Cooling" testId={`pool-summary-${provider}-cooling`} tone="warn" value={summary.cooling_key_count ?? 0} />
            <SummaryMetric label="Refreshed" testId={`pool-summary-${provider}-refreshed`} value={summary.refreshed_key_count ?? 0} />
            {summary.known_remaining != null ? (
              <SummaryMetric label="Known remaining" testId={`pool-summary-${provider}-remaining`} value={summary.known_remaining} />
            ) : null}
          </SummaryGrid>
        </div>
      ) : null}

      <div className="mt-2 flex flex-wrap items-center gap-2" data-testid={`pool-view-${provider}`}>
        <span className="text-xs font-medium uppercase tracking-wide text-zinc-500">View</span>
        <Button
          aria-label={`Show enabled ${provider} endpoints only`}
          aria-pressed={view === 'enabled'}
          onClick={() => {
            setView('enabled');
            setOffset(0);
          }}
          type="button"
          variant={view === 'enabled' ? 'primary' : 'secondary'}
        >
          Enabled only
        </Button>
        <Button
          aria-label={`Show all ${provider} endpoints`}
          aria-pressed={view === 'all'}
          onClick={() => {
            setView('all');
            setOffset(0);
          }}
          type="button"
          variant={view === 'all' ? 'primary' : 'secondary'}
        >
          Show all
        </Button>
        {view === 'enabled' && inactiveInSummary > 0 ? (
          <span className="text-xs text-zinc-500" data-testid={`pool-view-hint-${provider}`}>
            {`${inactiveInSummary} disabled/archived in pool — Show all or manage on Provider Endpoints`}
          </span>
        ) : null}
      </div>

      {refreshAllResult ? (
        <p className="mt-1 text-xs text-zinc-600" data-testid={`refresh-all-result-${provider}`}>
          {`Refreshed ${refreshAllResult.succeeded} · Failed ${refreshAllResult.failed} · Skipped disabled ${refreshAllResult.skipped_disabled}${
            refreshAllResult.skipped_not_configured != null ? ` · Skipped not configured ${refreshAllResult.skipped_not_configured}` : ''
          }`}
        </p>
      ) : null}

      {showSampleQuotaError ? (
        <p className="mt-1 text-xs text-red-700">{sampleQuota.message_redacted}</p>
      ) : null}

      <div className="mt-3 overflow-x-auto">
        <table className="w-full min-w-[720px] text-left text-sm">
          <thead className="text-xs uppercase text-zinc-500">
            <tr className="border-b border-zinc-200">
              <th className="py-2 pr-3 font-medium">Endpoint</th>
              <th className="py-2 pr-3 font-medium">Inference</th>
              <th className="py-2 pr-3 font-medium">Cooldown</th>
              <th className="py-2 pr-3 font-medium">Pool order</th>
              <th className="py-2 pr-3 font-medium">Quota</th>
              <th className="py-2 pr-3 font-medium">Checked</th>
              <th className="py-2 font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {items.map((row) => {
              const id = keyID(row.key);
              const quotaState = deriveQuotaOperationalState(row);
              const cooldownReason = valueOf<string>(row.key as Record<string, unknown>, 'CooldownReason', 'cooldown_reason', '');
              const cooldownUntil = valueOf<string | undefined>(row.key as Record<string, unknown>, 'CooldownUntil', 'cooldown_until', undefined);
              const lastFailedAt = valueOf<string | undefined>(row.key as Record<string, unknown>, 'LastFailedAt', 'last_failed_at', undefined);
              const demoted = Boolean(lastFailedAt);
              const refreshDisabled = quotaState === 'disabled' || refreshOne.isPending || !id;
              return (
                <tr className="border-b border-zinc-100" key={id || keyName(row.key)}>
                  <td className="py-2 pr-3 font-medium text-zinc-900">{keyName(row.key)}</td>
                  <td className="py-2 pr-3">
                    <Badge tone={statusTone(row.status)}>{row.status}</Badge>
                  </td>
                  <td className="py-2 pr-3 text-xs text-zinc-600">
                    {cooldownReason || '—'}
                    {cooldownUntil ? <div className="text-[11px] text-zinc-400">until {formatChecked(cooldownUntil)}</div> : null}
                  </td>
                  <td className="py-2 pr-3 text-xs">
                    {demoted ? (
                      <span className="text-amber-700" title={lastFailedAt}>demoted · {formatChecked(lastFailedAt!)}</span>
                    ) : (
                      <span className="text-zinc-500">front pack</span>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-zinc-700">
                    <div className="flex flex-col gap-0.5">
                      <Badge tone={quotaBadgeTone(quotaState)}>{quotaState === 'ok' ? 'ok' : quotaState.replace('_', ' ')}</Badge>
                      <span className="text-xs">{quotaStateLabel(quotaState, row)}</span>
                      {quotaState === 'not_configured' ? (
                        <span className="text-[11px] text-zinc-500">
                          Configure quota credentials on{' '}
                          <Link className="underline" to="/provider-keys">
                            Provider Endpoints
                          </Link>
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className="py-2 pr-3 text-xs text-zinc-500">
                    {row.quota?.checked_at ? formatChecked(row.quota.checked_at) : '—'}
                  </td>
                  <td className="py-2 text-right">
                    <div className="flex flex-wrap justify-end gap-1">
                      <Button
                        aria-label={`Refresh key ${id}`}
                        disabled={refreshDisabled}
                        onClick={() => refreshOne.mutate(id)}
                        title={quotaState === 'disabled' ? 'Quota refresh disabled for this endpoint' : undefined}
                        type="button"
                        variant="secondary"
                      >
                        <RefreshCw className={refreshOne.isPending && refreshOne.variables === id ? 'animate-spin' : ''} size={14} />
                        Refresh
                      </Button>
                      <Button
                        aria-label={`Reset cool and order for key ${id}`}
                        disabled={keyAction.isPending || !id}
                        onClick={() => keyAction.mutate({ id, path: '/reset-cooldown' })}
                        type="button"
                        variant="secondary"
                      >
                        Reset
                      </Button>
                      <Button
                        aria-label={`Promote key ${id}`}
                        disabled={keyAction.isPending || !id || !demoted}
                        onClick={() => keyAction.mutate({ id, path: '/reset-selection' })}
                        type="button"
                        variant="secondary"
                      >
                        Promote
                      </Button>
                      <Button
                        aria-label={`Demote key ${id}`}
                        disabled={keyAction.isPending || !id}
                        onClick={() => keyAction.mutate({ id, path: '/demote' })}
                        type="button"
                        variant="secondary"
                      >
                        Demote
                      </Button>
                    </div>
                  </td>
                </tr>
              );
            })}
            {items.length === 0 ? (
              <tr>
                <td className="py-3 text-sm text-zinc-500" colSpan={7}>
                  {view === 'enabled' && inactiveInSummary > 0
                    ? 'No enabled endpoints — use Show all or check Provider Endpoints'
                    : 'No keys'}
                </td>
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
