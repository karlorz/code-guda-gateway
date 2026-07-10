import type { EndpointQuotaInput, ProviderKey, QuotaFlow, QuotaMode } from '../../api/types';
import { Button, Field, valueOf } from '../../components/ui';
import { X } from 'lucide-react';
import { FormEvent, useEffect, useState } from 'react';

export function defaultQuotaMode(provider: string): QuotaMode {
  if (provider === 'tavily' || provider === 'firecrawl') return 'endpoint_credentials';
  return 'disabled';
}

export function defaultQuotaFlow(provider: string): QuotaFlow {
  if (provider === 'tavily') return 'tavily_usage';
  if (provider === 'firecrawl') return 'firecrawl_credit_usage';
  return 'grok2api_admin';
}

export function quotaModeLabel(mode: QuotaMode | string | undefined): string {
  switch (mode) {
    case 'disabled':
      return 'Disabled';
    case 'endpoint_credentials':
      return 'Use inference URL and key';
    case 'separate_credentials':
      return 'Separate credentials';
    default:
      return mode || '—';
  }
}

export type EndpointEditorSheetProps = {
  mode: 'create' | 'edit';
  endpoint?: ProviderKey | null;
  defaultBaseURL?: string;
  defaultURLByProvider?: Record<string, string>;
  onClose: () => void;
  onCreate: (input: {
    provider: string;
    name: string;
    base_url: string;
    key: string;
    quota: EndpointQuotaInput;
  }) => Promise<void> | void;
  onUpdate?: (input: {
    id: number;
    base_url: string;
    key?: string;
    quota: EndpointQuotaInput;
    quota_key?: string;
  }) => Promise<void> | void;
  pending?: boolean;
};

export function EndpointEditorSheet({
  mode,
  endpoint,
  defaultBaseURL = '',
  defaultURLByProvider = {},
  onClose,
  onCreate,
  onUpdate,
  pending = false,
}: EndpointEditorSheetProps) {
  const record = (endpoint ?? {}) as Record<string, unknown>;
  const existingProvider = valueOf<string>(record, 'Provider', 'provider', 'grok');
  const existingName = valueOf<string>(record, 'Name', 'name', '');
  const existingBaseURL = valueOf<string>(record, 'BaseURL', 'base_url', '');
  const existingQuotaMode = valueOf<QuotaMode>(record, 'QuotaMode', 'quota_mode', defaultQuotaMode(existingProvider));
  const existingQuotaFlow = valueOf<QuotaFlow>(record, 'QuotaFlow', 'quota_flow', defaultQuotaFlow(existingProvider));
  const existingQuotaBaseURL = valueOf<string>(record, 'QuotaBaseURL', 'quota_base_url', '') || '';
  const existingID = valueOf<number>(record, 'ID', 'id', 0);

  const [provider, setProvider] = useState(mode === 'edit' ? existingProvider : 'grok');
  const [name, setName] = useState(mode === 'edit' ? existingName : '');
  const [baseURL, setBaseURL] = useState(mode === 'edit' ? existingBaseURL : defaultBaseURL);
  const [inferenceKey, setInferenceKey] = useState('');
  const [quotaMode, setQuotaMode] = useState<QuotaMode>(mode === 'edit' ? existingQuotaMode : defaultQuotaMode('grok'));
  const [quotaFlow, setQuotaFlow] = useState<QuotaFlow>(mode === 'edit' ? existingQuotaFlow : defaultQuotaFlow('grok'));
  const [quotaBaseURL, setQuotaBaseURL] = useState(mode === 'edit' ? existingQuotaBaseURL : '');
  const [quotaKey, setQuotaKey] = useState('');
  const [quotaModeTouched, setQuotaModeTouched] = useState(mode === 'edit');

  // Apply provider defaults for base URL (create) and quota mode/flow until operator picks a mode.
  useEffect(() => {
    if (mode !== 'create') return;
    if (defaultBaseURL) {
      setBaseURL((current) => (current.trim() ? current : defaultBaseURL));
    }
  }, [defaultBaseURL, mode]);

  useEffect(() => {
    if (mode !== 'create') return;
    if (!quotaModeTouched) {
      setQuotaMode(defaultQuotaMode(provider));
      setQuotaFlow(defaultQuotaFlow(provider));
    } else {
      // Still align flow with provider when mode was touched but provider changes.
      setQuotaFlow(defaultQuotaFlow(provider));
    }
  }, [provider, quotaModeTouched, mode]);

  function handleProviderChange(next: string) {
    setProvider(next);
    if (mode !== 'create') return;
    const nextDefault = defaultURLByProvider[next] ?? defaultBaseURL ?? '';
    const prevDefaults = Object.values(defaultURLByProvider);
    setBaseURL((current) => {
      if (!current.trim() || prevDefaults.includes(current) || current === defaultBaseURL) {
        return nextDefault;
      }
      return current;
    });
    // Quota mode/flow defaults are owned by the provider useEffect above.
  }

  function handleQuotaModeChange(next: QuotaMode) {
    setQuotaModeTouched(true);
    setQuotaMode(next);
    if (next !== 'separate_credentials') {
      setQuotaBaseURL('');
      setQuotaKey('');
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const quota: EndpointQuotaInput = {
      mode: quotaMode,
      flow: quotaFlow,
    };
    if (quotaMode === 'separate_credentials') {
      if (quotaBaseURL.trim()) quota.base_url = quotaBaseURL.trim();
      if (quotaKey.trim()) quota.key = quotaKey;
    }

    if (mode === 'create') {
      if (!name.trim() || !baseURL.trim() || !inferenceKey.trim()) return;
      if (quotaMode === 'separate_credentials' && (!quota.base_url || !quota.key)) return;
      await onCreate({
        provider,
        name: name.trim(),
        base_url: baseURL.trim(),
        key: inferenceKey,
        quota,
      });
      return;
    }

    if (!onUpdate || !existingID) return;
    if (!baseURL.trim()) return;
    await onUpdate({
      id: existingID,
      base_url: baseURL.trim(),
      key: inferenceKey.trim() || undefined,
      quota: {
        mode: quota.mode,
        flow: quota.flow,
        base_url: quota.base_url,
      },
      quota_key: quotaKey.trim() || undefined,
    });
  }

  const title = mode === 'create' ? 'Create endpoint' : `Edit endpoint · ${existingName || existingID}`;
  const createReady =
    name.trim() &&
    baseURL.trim() &&
    inferenceKey.trim() &&
    (quotaMode !== 'separate_credentials' || (quotaBaseURL.trim() && quotaKey.trim()));
  const editReady = baseURL.trim();

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-zinc-950/30">
      <div
        aria-label={title}
        aria-modal="true"
        className="flex h-full w-full max-w-lg flex-col bg-white shadow-xl"
        role="dialog"
      >
        <div className="flex items-center justify-between border-b border-zinc-200 px-5 py-4">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button aria-label="Close" className="rounded p-1 hover:bg-zinc-100" onClick={onClose} type="button">
            <X size={18} />
          </button>
        </div>
        <form className="flex flex-1 flex-col overflow-y-auto" onSubmit={(e) => void submit(e)}>
          <div className="grid flex-1 gap-6 px-5 py-4">
            <section className="grid gap-3">
              <h3 className="text-sm font-semibold text-zinc-950">Identity</h3>
              <label className="grid gap-1 text-sm font-medium text-zinc-700">
                <span>Provider</span>
                <select
                  className="h-9 rounded-md border border-zinc-300 bg-white px-3"
                  disabled={mode === 'edit'}
                  onChange={(event) => handleProviderChange(event.target.value)}
                  value={provider}
                >
                  <option value="grok">Grok</option>
                  <option value="tavily">Tavily</option>
                  <option value="firecrawl">Firecrawl</option>
                </select>
              </label>
              <Field
                disabled={mode === 'edit'}
                label="Name"
                onChange={(event) => setName(event.target.value)}
                value={name}
              />
              <p className="text-xs text-zinc-500">
                Endpoint name is an identifier only — it does not define routing priority.
              </p>
            </section>

            <section className="grid gap-3">
              <h3 className="text-sm font-semibold text-zinc-950">Inference route</h3>
              <Field label="Base URL" onChange={(event) => setBaseURL(event.target.value)} value={baseURL} />
              <Field
                autoComplete="off"
                label={mode === 'create' ? 'Key' : 'New inference key (optional rotate)'}
                onChange={(event) => setInferenceKey(event.target.value)}
                type="password"
                value={inferenceKey}
              />
              {mode === 'edit' ? (
                <p className="text-xs text-zinc-500">Existing secrets are never shown. Leave blank to keep the current inference key.</p>
              ) : null}
            </section>

            <section className="grid gap-3">
              <h3 className="text-sm font-semibold text-zinc-950">Quota source</h3>
              <label className="grid gap-1 text-sm font-medium text-zinc-700">
                <span>Quota mode</span>
                <select
                  className="h-9 rounded-md border border-zinc-300 bg-white px-3"
                  onChange={(event) => handleQuotaModeChange(event.target.value as QuotaMode)}
                  value={quotaMode}
                >
                  <option value="disabled">Disabled</option>
                  <option value="endpoint_credentials">Use inference URL and key</option>
                  <option value="separate_credentials">Separate credentials</option>
                </select>
              </label>
              <label className="grid gap-1 text-sm font-medium text-zinc-700">
                <span>Quota flow</span>
                <select
                  className="h-9 rounded-md border border-zinc-300 bg-white px-3"
                  onChange={(event) => setQuotaFlow(event.target.value as QuotaFlow)}
                  value={quotaFlow}
                >
                  <option value="grok2api_admin">grok2api_admin</option>
                  <option value="tavily_usage">tavily_usage</option>
                  <option value="firecrawl_credit_usage">firecrawl_credit_usage</option>
                </select>
              </label>
              {quotaMode === 'separate_credentials' ? (
                <>
                  <Field
                    label="Quota base URL"
                    onChange={(event) => setQuotaBaseURL(event.target.value)}
                    value={quotaBaseURL}
                  />
                  <Field
                    autoComplete="off"
                    label={mode === 'create' ? 'Quota key' : 'New quota key (optional rotate)'}
                    onChange={(event) => setQuotaKey(event.target.value)}
                    type="password"
                    value={quotaKey}
                  />
                  {mode === 'edit' ? (
                    <p className="text-xs text-zinc-500">Quota secrets are never prefilled. Paste a new key only to rotate.</p>
                  ) : null}
                </>
              ) : null}
            </section>
          </div>
          <div className="flex justify-end gap-2 border-t border-zinc-200 px-5 py-4">
            <Button onClick={onClose} type="button" variant="secondary">
              Cancel
            </Button>
            <Button
              disabled={pending || (mode === 'create' ? !createReady : !editReady)}
              type="submit"
            >
              {mode === 'create' ? 'Create endpoint' : 'Save changes'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
