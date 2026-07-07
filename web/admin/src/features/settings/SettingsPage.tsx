import { Panel } from '../../components/ui';

export function SettingsPage() {
  return (
    <div>
      <h1 className="text-2xl font-semibold">Settings</h1>
      <Panel title="Runtime">
        <div className="grid gap-2 text-sm text-zinc-700">
          <div className="flex justify-between border-t border-zinc-200 py-3">
            <span>Admin base path</span>
            <strong>/admin</strong>
          </div>
          <div className="flex justify-between border-t border-zinc-200 py-3">
            <span>Deployment runtime</span>
            <strong>Go binary</strong>
          </div>
        </div>
      </Panel>
    </div>
  );
}
