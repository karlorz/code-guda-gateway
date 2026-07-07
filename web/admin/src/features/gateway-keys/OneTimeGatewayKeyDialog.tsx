import { useEffect, useState } from 'react';
import { Button, Dialog } from '../../components/ui';

export function OneTimeGatewayKeyDialog({ rawKey, onClose }: { rawKey: string; onClose: () => void }) {
  const [saved, setSaved] = useState(false);

  useEffect(() => () => setSaved(false), []);

  return (
    <Dialog title="Gateway key created" onClose={() => undefined}>
      <div className="grid gap-4">
        <input className="h-10 rounded-md border border-zinc-300 px-3 font-mono text-sm" readOnly value={rawKey} />
        <label className="flex items-center gap-2 text-sm">
          <input checked={saved} onChange={(event) => setSaved(event.target.checked)} type="checkbox" />
          I have saved this key
        </label>
        <div className="flex justify-end">
          <Button disabled={!saved} onClick={onClose} type="button">
            Done
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
