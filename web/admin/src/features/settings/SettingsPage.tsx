import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import type { DisplayTimezoneSetting } from '../../api/types';
import { Badge, Button, PageHeader, Panel } from '../../components/ui';

export function SettingsPage() {
  const qc = useQueryClient();
  const tzQuery = useQuery({
    queryKey: ['display-timezone'],
    queryFn: () => apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone'),
  });
  const [draft, setDraft] = useState('');
  useEffect(() => {
    if (tzQuery.data?.timezone) setDraft(tzQuery.data.timezone);
  }, [tzQuery.data?.timezone]);

  const save = useMutation({
    mutationFn: (timezone: string) =>
      apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone', {
        method: 'PATCH',
        body: JSON.stringify({ timezone }),
      }),
    onSuccess: (data) => {
      void qc.setQueryData(['display-timezone'], data);
      setDraft(data.timezone);
    },
  });
  const useHost = useMutation({
    mutationFn: () =>
      apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone', {
        method: 'PATCH',
        body: JSON.stringify({ use_host: true }),
      }),
    onSuccess: (data) => {
      void qc.setQueryData(['display-timezone'], data);
      setDraft(data.timezone);
    },
  });

  const source = tzQuery.data?.source ?? 'host';
  const errorMsg =
    (save.error as Error | undefined)?.message ||
    (useHost.error as Error | undefined)?.message ||
    '';

  return (
    <div>
      <PageHeader
        description="Runtime information, display timezone, and guidance for endpoint creation defaults."
        title="Settings"
      />
      <Panel title="Runtime">
        <dl className="grid gap-3 text-sm text-zinc-700">
          <div className="grid gap-1 border-t border-zinc-200 py-3 md:grid-cols-[180px_1fr]">
            <dt className="font-medium text-zinc-900">Admin base path</dt>
            <dd>
              <strong>/admin</strong>
              <p className="mt-1 text-xs text-zinc-500">The embedded administration SPA is served beneath this path.</p>
            </dd>
          </div>
          <div className="grid gap-1 border-t border-zinc-200 py-3 md:grid-cols-[180px_1fr]">
            <dt className="font-medium text-zinc-900">Deployment runtime</dt>
            <dd>
              <strong>Go binary</strong>
              <p className="mt-1 text-xs text-zinc-500">The React admin is built and embedded into the gateway binary.</p>
            </dd>
          </div>
        </dl>
      </Panel>
      <Panel title="Display timezone">
        <p className="mb-3 max-w-3xl text-sm text-zinc-600">
          Used only for admin display of logs and audit times. Stored timestamps remain UTC.
        </p>
        <div className="mb-2 flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium text-zinc-900">Source</span>
          <Badge>{source === 'stored' ? 'stored' : 'host default'}</Badge>
        </div>
        <label className="grid max-w-xl gap-1 text-sm" htmlFor="display-timezone">
          <span className="font-medium text-zinc-900">Timezone (IANA)</span>
          <input
            className="rounded border border-zinc-300 px-3 py-2"
            id="display-timezone"
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Asia/Seoul"
            value={draft}
          />
        </label>
        {errorMsg ? <p className="mt-2 text-sm text-red-600">{errorMsg}</p> : null}
        <div className="mt-3 flex flex-wrap gap-2">
          <Button
            disabled={save.isPending || !draft.trim()}
            onClick={() => save.mutate(draft.trim())}
            type="button"
          >
            Save
          </Button>
          <Button
            disabled={useHost.isPending}
            onClick={() => useHost.mutate()}
            type="button"
            variant="secondary"
          >
            Use host timezone
          </Button>
        </div>
      </Panel>
      <Panel
        action={<Link className="text-sm font-medium underline underline-offset-2" to="/provider-keys">Manage Provider Endpoints</Link>}
        title="Provider endpoint defaults"
      >
        <p className="max-w-3xl text-sm text-zinc-600">
          Provider defaults apply only to newly created endpoints. Changing a default never mutates existing endpoint rows and is never used as an inference fallback.
        </p>
      </Panel>
    </div>
  );
}
