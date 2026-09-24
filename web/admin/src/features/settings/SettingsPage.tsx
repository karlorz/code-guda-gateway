import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import type { DisplayTimezoneSetting } from '../../api/types';
import { Badge, Button, PageHeader, Panel } from '../../components/ui';
import { displayTimezoneQueryKey, useDisplayTimezone } from '../../lib/useDisplayTimezone';

export function SettingsPage() {
  const qc = useQueryClient();
  const tzQuery = useDisplayTimezone();
  const [draft, setDraft] = useState('');
  useEffect(() => {
    if (tzQuery.data?.timezone) setDraft(tzQuery.data.timezone);
  }, [tzQuery.data?.timezone]);

  const patch = useMutation({
    mutationFn: (body: { timezone?: string; use_host?: boolean }) =>
      apiFetch<DisplayTimezoneSetting>('/admin/api/settings/display-timezone', {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onSuccess: (data) => {
      void qc.setQueryData(displayTimezoneQueryKey, data);
    },
  });

  const source = tzQuery.data?.source ?? 'host';
  const errorMsg = (patch.error as Error | undefined)?.message || '';
  const [operatorPassword, setOperatorPassword] = useState('');
  const [operatorConfirm, setOperatorConfirm] = useState('');
  const [operatorSaved, setOperatorSaved] = useState(false);
  const operator = useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/admin/api/operator-password', {
        method: 'POST',
        body: JSON.stringify({ password: operatorPassword, confirm: operatorConfirm }),
      }),
    onSuccess: () => {
      setOperatorPassword('');
      setOperatorConfirm('');
      setOperatorSaved(true);
    },
  });
  const operatorError = (operator.error as Error | undefined)?.message || '';

  return (
    <div>
      <PageHeader
        description="Runtime information, display timezone, connector consent, and guidance for endpoint creation defaults."
        title="Settings"
      />
      <Panel title="MCP connector consent">
        <p className="mb-3 max-w-3xl text-sm text-zinc-600">
          This password is what ChatGPT, Doubao, and Cursor type on /authorize. It is not the admin token and not a gsk_ gateway key. The old value cannot be shown. Saving replaces it for the next consent check.
        </p>
        <div className="grid max-w-xl gap-3">
          <label className="grid gap-1 text-sm" htmlFor="operator-password">
            <span className="font-medium text-zinc-900">New operator password</span>
            <input
              autoComplete="new-password"
              className="rounded border border-zinc-300 px-3 py-2"
              id="operator-password"
              onChange={(e) => {
                setOperatorSaved(false);
                setOperatorPassword(e.target.value);
              }}
              type="password"
              value={operatorPassword}
            />
          </label>
          <label className="grid gap-1 text-sm" htmlFor="operator-confirm">
            <span className="font-medium text-zinc-900">Confirm</span>
            <input
              autoComplete="new-password"
              className="rounded border border-zinc-300 px-3 py-2"
              id="operator-confirm"
              onChange={(e) => {
                setOperatorSaved(false);
                setOperatorConfirm(e.target.value);
              }}
              type="password"
              value={operatorConfirm}
            />
          </label>
        </div>
        {operatorError ? <p className="mt-2 text-sm text-red-600">{operatorError}</p> : null}
        {operatorSaved ? <p className="mt-2 text-sm text-zinc-700">Saved. Use this password on the connector consent page.</p> : null}
        <div className="mt-3">
          <Button
            disabled={operator.isPending}
            onClick={() => operator.mutate()}
            type="button"
          >
            Set operator password
          </Button>
        </div>
      </Panel>
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
            disabled={patch.isPending || !draft.trim()}
            onClick={() => patch.mutate({ timezone: draft.trim() })}
            type="button"
          >
            Save
          </Button>
          <Button
            disabled={patch.isPending}
            onClick={() => patch.mutate({ use_host: true })}
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
