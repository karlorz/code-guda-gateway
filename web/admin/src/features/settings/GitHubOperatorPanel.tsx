import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '../../api/client';
import { Button, Panel } from '../../components/ui';

type GitHubSettings = {
  enabled: boolean;
  client_id: string;
  secret_configured: boolean;
  allowed_user_ids: string[];
  callback_url: string;
  available: boolean;
};

export function GitHubOperatorPanel() {
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: ['github-operator'],
    queryFn: () => apiFetch<GitHubSettings>('/admin/api/settings/github-operator'),
  });
  const [enabled, setEnabled] = useState(false);
  const [clientId, setClientId] = useState('');
  const [secret, setSecret] = useState('');
  const [ids, setIds] = useState('');
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (query.data) {
      setEnabled(query.data.enabled ?? false);
      setClientId(query.data.client_id ?? '');
      setIds((query.data.allowed_user_ids ?? []).join(', '));
    }
  }, [query.data]);
  const save = useMutation({
    mutationFn: () => apiFetch<GitHubSettings>('/admin/api/settings/github-operator', {
      method: 'PATCH',
      body: JSON.stringify({
        enabled, client_id: clientId.trim(), client_secret: secret,
        allowed_user_ids: ids.split(/[\s,]+/).filter(Boolean),
      }),
    }),
    onSuccess: (data) => {
      qc.setQueryData(['github-operator'], data);
      setSecret('');
      setSaved(true);
    },
  });
  const error = (save.error ?? query.error) as Error | null;
  return (
    <Panel title="GitHub operator sign-in">
      <p className="mb-3 max-w-3xl text-sm text-zinc-600">
        Add GitHub as another way to sign in on the MCP consent page. Only the GitHub accounts listed below can approve connections. You still review and approve each client's permissions. This does not grant admin-console access.
      </p>
      <ol className="mb-4 max-w-3xl list-decimal space-y-2 pl-5 text-sm text-zinc-600">
        <li>Create a dedicated <a className="underline" href="https://github.com/settings/applications/new" target="_blank" rel="noreferrer">GitHub OAuth app</a>. Set its homepage to this gateway and use this authorization callback URL:
          <code className="mt-2 block w-fit break-all rounded bg-zinc-100 px-2 py-1">{query.data?.callback_url || 'Configure the gateway OAuth issuer first.'}</code>
        </li>
        <li>Enter the app's client ID and secret. Add the numeric GitHub user IDs allowed to act as operators. Find an account's <code>id</code> at <code>https://api.github.com/users/USERNAME</code>; use the ID, not the username.</li>
      </ol>
      <div className="grid max-w-xl gap-3" onChange={() => setSaved(false)}>
        <label className="flex items-center gap-2 text-sm font-medium">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enable GitHub operator sign-in
        </label>
        <label className="grid gap-1 text-sm">
          <span className="font-medium">GitHub client ID</span>
          <input className="rounded border border-zinc-300 px-3 py-2" value={clientId} onChange={(e) => setClientId(e.target.value)} autoComplete="off" />
        </label>
        <label className="grid gap-1 text-sm">
          <span className="font-medium" id="github-secret-label">GitHub client secret</span>
          <input className="rounded border border-zinc-300 px-3 py-2" type="password" aria-labelledby="github-secret-label" aria-describedby="github-secret-help" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" placeholder={query.data?.secret_configured ? 'Configured — leave blank to keep' : 'Enter client secret'} />
          <span id="github-secret-help" className="text-xs text-zinc-500">Stored encrypted and never shown again. Leave blank to keep it for the same client ID. Changing the client ID requires its new secret.</span>
        </label>
        <label className="grid gap-1 text-sm">
          <span className="font-medium">Allowed GitHub user IDs</span>
          <input className="rounded border border-zinc-300 px-3 py-2" value={ids} onChange={(e) => setIds(e.target.value)} placeholder="Numeric IDs, separated by commas" autoComplete="off" />
        </label>
      </div>
      {error ? <p className="mt-2 text-sm text-red-600">{error.message}</p> : null}
      {saved ? <p role="status" className="mt-2 text-sm text-zinc-700">GitHub operator settings saved. The existing operator password remains available.</p> : null}
      <div className="mt-3">
        <Button type="button" disabled={query.isPending || query.isError || query.data?.available !== true || save.isPending} onClick={() => save.mutate()}>Save GitHub sign-in</Button>
      </div>
    </Panel>
  );
}
