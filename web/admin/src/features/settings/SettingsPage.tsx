import { Link } from 'react-router-dom';
import { PageHeader, Panel } from '../../components/ui';

export function SettingsPage() {
  return (
    <div>
      <PageHeader
        description="Runtime information and guidance for endpoint creation defaults."
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
