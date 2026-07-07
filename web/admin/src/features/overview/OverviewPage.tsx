import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import type { AuditEvent, GatewayKey, ListResponse, ProviderHealth } from '../../api/types';
import { Badge, Panel, valueOf } from '../../components/ui';

export function OverviewPage() {
  const gatewayKeys = useQuery({ queryKey: ['gateway-keys'], queryFn: () => apiFetch<ListResponse<GatewayKey>>('/admin/api/gateway-keys') });
  const health = useQuery({ queryKey: ['provider-health'], queryFn: () => apiFetch<ListResponse<ProviderHealth>>('/admin/api/provider-health') });
  const audit = useQuery({ queryKey: ['audit-events', 'overview'], queryFn: () => apiFetch<ListResponse<AuditEvent>>('/admin/api/audit-events?limit=5') });
  const missingGateway = (gatewayKeys.data?.items ?? []).length === 0;
  const missingProvider = (health.data?.items ?? []).some((item) => item.status === 'missing_key');
  return (
    <div>
      <h1 className="text-2xl font-semibold">Overview</h1>
      <Panel title="Readiness">
        <div className="grid gap-2 md:grid-cols-2">
          <Checklist label="Gateway key" ok={!missingGateway} />
          <Checklist label="Provider keys" ok={!missingProvider} />
        </div>
      </Panel>
      <Panel title="Provider health">
        <div className="grid gap-2 md:grid-cols-3">
          {(health.data?.items ?? []).map((item) => (
            <div className="border-t border-zinc-200 py-3" key={item.provider}>
              <div className="flex items-center justify-between">
                <strong>{item.provider}</strong>
                <Badge tone={item.status === 'healthy' ? 'good' : 'warn'}>{item.status}</Badge>
              </div>
              <p className="mt-2 text-sm text-zinc-600">{item.reasons.join(', ') || item.base_url}</p>
            </div>
          ))}
        </div>
      </Panel>
      <Panel title="Recent audit">
        {(audit.data?.items ?? []).map((event) => {
          const record = event as Record<string, unknown>;
          return (
            <div className="grid grid-cols-[160px_1fr] border-t border-zinc-200 py-2 text-sm" key={valueOf<number>(record, 'ID', 'id', 0)}>
              <span className="text-zinc-500">{valueOf<string>(record, 'ActorKind', 'actor_kind', '')}</span>
              <span>{valueOf<string>(record, 'Action', 'action', '')}</span>
            </div>
          );
        })}
      </Panel>
    </div>
  );
}

function Checklist({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div className="flex items-center justify-between border-t border-zinc-200 py-3">
      <span>{label}</span>
      <Badge tone={ok ? 'good' : 'warn'}>{ok ? 'ready' : 'needed'}</Badge>
    </div>
  );
}
