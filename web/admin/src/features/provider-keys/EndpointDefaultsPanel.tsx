import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, Save } from 'lucide-react';
import { apiFetch } from '../../api/client';
import type { ListResponse, ProviderSetting } from '../../api/types';
import { Button, Field } from '../../components/ui';
import { defaultQuotaMode, quotaModeLabel } from './EndpointEditorSheet';

const PROVIDERS = ['grok', 'tavily', 'firecrawl'] as const;

export function EndpointDefaultsPanel() {
  const [open, setOpen] = useState(false);
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ['provider-settings'],
    queryFn: () => apiFetch<ListResponse<ProviderSetting>>('/admin/api/provider-settings'),
  });

  return (
    <section className="rounded-md border border-zinc-200 bg-white">
      <button
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-2 px-4 py-3 text-left text-sm font-medium text-zinc-900"
        onClick={() => setOpen((v) => !v)}
        type="button"
      >
        <span>New endpoint defaults</span>
        {open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
      </button>
      {open ? (
        <div className="grid gap-4 border-t border-zinc-200 px-4 py-4">
          <p className="text-xs text-zinc-500">
            Defaults affect future endpoints only. Changing a default never updates existing endpoints or live traffic.
          </p>
          {PROVIDERS.map((provider) => {
            const baseURL = settings.data?.items?.find((s) => s.provider === provider)?.base_url ?? '';
            return (
              <DefaultsRow
                key={provider}
                baseURL={baseURL}
                onSaved={() => void qc.invalidateQueries({ queryKey: ['provider-settings'] })}
                provider={provider}
              />
            );
          })}
        </div>
      ) : null}
    </section>
  );
}

function DefaultsRow({
  provider,
  baseURL,
  onSaved,
}: {
  provider: string;
  baseURL: string;
  onSaved: () => void;
}) {
  const [value, setValue] = useState(baseURL);
  useEffect(() => {
    setValue(baseURL);
  }, [baseURL]);

  const save = useMutation({
    mutationFn: (base_url: string) =>
      apiFetch(`/admin/api/provider-settings/${provider}`, {
        method: 'PATCH',
        body: JSON.stringify({ base_url }),
      }),
    onSuccess: onSaved,
  });

  return (
    <div className="grid gap-2 md:grid-cols-[120px_1fr_auto] md:items-end">
      <div>
        <div className="text-sm font-medium capitalize text-zinc-900">{provider}</div>
        <p className="text-xs text-zinc-500">Quota: {quotaModeLabel(defaultQuotaMode(provider))} (read-only)</p>
      </div>
      <Field
        label="Default base URL for new endpoints"
        onChange={(event) => setValue(event.target.value)}
        value={value}
      />
      <Button
        disabled={save.isPending || !value.trim()}
        onClick={() => save.mutate(value.trim())}
        type="button"
        variant="secondary"
      >
        <Save size={16} />
        Save
      </Button>
    </div>
  );
}
