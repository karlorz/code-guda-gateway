import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import type { DisplayTimezoneSetting, InviteCode, ListResponse, PublicIssuanceMode, PublicIssuanceSetting } from '../../api/types';
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

  // Public issuance setting
  const issuanceQuery = useQuery({
    queryKey: ['public-issuance'],
    queryFn: () => apiFetch<PublicIssuanceSetting>('/admin/api/public-issuance'),
  });
  const [issuanceMode, setIssuanceMode] = useState<PublicIssuanceMode>('off');
  useEffect(() => {
    if (issuanceQuery.data?.value) {
      setIssuanceMode(issuanceQuery.data.value);
    }
  }, [issuanceQuery.data?.value]);

  const [issuanceSaved, setIssuanceSaved] = useState(false);
  const patchIssuance = useMutation({
    mutationFn: (val: PublicIssuanceMode) =>
      apiFetch<PublicIssuanceSetting>('/admin/api/public-issuance', {
        method: 'PATCH',
        body: JSON.stringify({ value: val }),
      }),
    onSuccess: (data) => {
      void qc.setQueryData(['public-issuance'], data);
      setIssuanceSaved(true);
    },
  });
  const issuanceError = (patchIssuance.error as Error | undefined)?.message || '';

  // Invite codes
  const invitesQuery = useQuery({
    queryKey: ['invite-codes'],
    queryFn: () => apiFetch<ListResponse<InviteCode>>('/admin/api/invite-codes'),
  });

  const [inviteBind, setInviteBind] = useState('');
  const [inviteMax, setInviteMax] = useState('1');
  const [inviteDays, setInviteDays] = useState('30');
  const createInvite = useMutation({
    mutationFn: () => {
      const days = parseInt(inviteDays, 10) || 30;
      const exp = new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString();
      return apiFetch<InviteCode>('/admin/api/invite-codes', {
        method: 'POST',
        body: JSON.stringify({
          agent_label_bind: inviteBind.trim(),
          max_redemptions: parseInt(inviteMax, 10) || 1,
          expires_at: exp,
        }),
      });
    },
    onSuccess: () => {
      setInviteBind('');
      setInviteMax('1');
      void qc.invalidateQueries({ queryKey: ['invite-codes'] });
    },
  });

  const revokeInvite = useMutation({
    mutationFn: (id: number) =>
      apiFetch(`/admin/api/invite-codes/${id}/revoke`, { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['invite-codes'] });
    },
  });

  const invites = invitesQuery.data?.items ?? [];

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
      <Panel title="Public issuance">
        <p className="mb-3 max-w-3xl text-sm text-zinc-600">
          Control how agents mint MCP gateway keys. When off, only operator password mints. When open, any agent can authorize without a password. When ref_code, an invite code is required unless an operator password is supplied.
        </p>
        <div className="flex flex-wrap gap-4 text-sm">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="issuance_mode"
              value="off"
              checked={issuanceMode === 'off'}
              onChange={() => {
                setIssuanceSaved(false);
                setIssuanceMode('off');
              }}
            />
            <span className="font-medium text-zinc-900">Off (operator password only)</span>
          </label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="issuance_mode"
              value="open"
              checked={issuanceMode === 'open'}
              onChange={() => {
                setIssuanceSaved(false);
                setIssuanceMode('open');
              }}
            />
            <span className="font-medium text-zinc-900">Open (no password required)</span>
          </label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="issuance_mode"
              value="ref_code"
              checked={issuanceMode === 'ref_code'}
              onChange={() => {
                setIssuanceSaved(false);
                setIssuanceMode('ref_code');
              }}
            />
            <span className="font-medium text-zinc-900">Invite code required</span>
          </label>
        </div>
        {issuanceError ? <p className="mt-2 text-sm text-red-600">{issuanceError}</p> : null}
        {issuanceSaved ? <p className="mt-2 text-sm text-zinc-700">Public issuance mode updated.</p> : null}
        <div className="mt-3">
          <Button
            disabled={patchIssuance.isPending || issuanceMode === issuanceQuery.data?.value}
            onClick={() => patchIssuance.mutate(issuanceMode)}
            type="button"
          >
            Save issuance mode
          </Button>
        </div>
      </Panel>
      <Panel title="Invite codes">
        <p className="mb-3 max-w-3xl text-sm text-zinc-600">
          Mint multi-use or single-use invite codes for ref_code mode. You can optionally bind a code to an exact agent label.
        </p>
        <div className="mb-4 grid max-w-2xl gap-3 sm:grid-cols-3">
          <label className="grid gap-1 text-sm">
            <span className="font-medium text-zinc-900">Agent label bind (optional)</span>
            <input
              className="rounded border border-zinc-300 px-3 py-2"
              placeholder="e.g. Cursor"
              value={inviteBind}
              onChange={(e) => setInviteBind(e.target.value)}
            />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="font-medium text-zinc-900">Max redemptions</span>
            <input
              type="number"
              min="1"
              className="rounded border border-zinc-300 px-3 py-2"
              value={inviteMax}
              onChange={(e) => setInviteMax(e.target.value)}
            />
          </label>
          <label className="grid gap-1 text-sm">
            <span className="font-medium text-zinc-900">Validity (days)</span>
            <input
              type="number"
              min="1"
              className="rounded border border-zinc-300 px-3 py-2"
              value={inviteDays}
              onChange={(e) => setInviteDays(e.target.value)}
            />
          </label>
        </div>
        <div className="mb-4">
          <Button
            disabled={createInvite.isPending}
            onClick={() => createInvite.mutate()}
            type="button"
          >
            Create invite code
          </Button>
        </div>

        {invites.length === 0 ? (
          <p className="text-sm text-zinc-500">No invite codes generated yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-200 text-xs font-semibold uppercase text-zinc-500">
                  <th className="py-2 pr-4">Code</th>
                  <th className="py-2 pr-4">Agent bind</th>
                  <th className="py-2 pr-4">Redemptions</th>
                  <th className="py-2 pr-4">Expires</th>
                  <th className="py-2 pr-4">Status</th>
                  <th className="py-2 text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {invites.map((inv) => {
                  const isRevoked = Boolean(inv.revoked_at);
                  const isExpired = new Date(inv.expires_at).getTime() < Date.now();
                  const isExhausted = inv.redemption_count >= inv.max_redemptions;
                  return (
                    <tr className="border-t border-zinc-200" key={inv.id}>
                      <td className="py-3 pr-4 font-mono font-medium text-zinc-900">{inv.code}</td>
                      <td className="py-3 pr-4 text-zinc-600">{inv.agent_label_bind || '—'}</td>
                      <td className="py-3 pr-4 text-zinc-600">{inv.redemption_count} / {inv.max_redemptions}</td>
                      <td className="py-3 pr-4 text-xs text-zinc-500">{new Date(inv.expires_at).toLocaleDateString()}</td>
                      <td className="py-3 pr-4">
                        {isRevoked ? (
                          <Badge tone="bad">revoked</Badge>
                        ) : isExpired ? (
                          <Badge tone="bad">expired</Badge>
                        ) : isExhausted ? (
                          <Badge tone="warn">exhausted</Badge>
                        ) : (
                          <Badge tone="good">active</Badge>
                        )}
                      </td>
                      <td className="py-3 text-right">
                        <Button
                          disabled={revokeInvite.isPending || isRevoked}
                          onClick={() => revokeInvite.mutate(inv.id)}
                          type="button"
                          variant="danger"
                        >
                          Revoke
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
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
