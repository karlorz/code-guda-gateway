import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import type { AuditEvent, ListResponse } from '../../api/types';
import { Panel, valueOf } from '../../components/ui';
import { formatDisplayTime } from '../../lib/formatDisplayTime';
import { useDisplayTimezone } from '../../lib/useDisplayTimezone';

export function AuditPage() {
  const { zone } = useDisplayTimezone();
  const { data } = useQuery({
    queryKey: ['audit-events'],
    queryFn: () => apiFetch<ListResponse<AuditEvent>>('/admin/api/audit-events?limit=100'),
  });
  return (
    <div>
      <h1 className="text-2xl font-semibold">Audit</h1>
      <Panel title="Events">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <tbody>
              {(data?.items ?? []).map((event) => {
                const record = event as Record<string, unknown>;
                const raw = valueOf<string>(record, 'OccurredAt', 'occurred_at', '');
                return (
                  <tr className="border-t border-zinc-200" key={valueOf<number>(record, 'ID', 'id', 0)}>
                    <td className="py-3 pr-4 whitespace-nowrap" title={raw}>
                      {formatDisplayTime(raw, zone)}
                    </td>
                    <td className="py-3 pr-4">{valueOf<string>(record, 'ActorKind', 'actor_kind', '')}</td>
                    <td className="py-3 pr-4">{valueOf<string>(record, 'Action', 'action', '')}</td>
                    <td className="py-3">{valueOf<string>(record, 'DetailRedacted', 'detail_redacted', '')}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>
    </div>
  );
}
