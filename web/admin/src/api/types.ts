export type SessionResponse = { authenticated: true; csrf_token: string };
export type ListResponse<T> = { items: T[]; page?: { limit: number; offset: number }; filter?: Record<string, string> };
export type PagedItems<T> = { items: T[]; page: { limit: number; offset: number; total: number } };

export type GatewayKey = {
  ID?: number;
  id?: number;
  Name?: string;
  name?: string;
  Prefix?: string;
  prefix?: string;
  Fingerprint?: string;
  fingerprint?: string;
  Enabled?: boolean;
  enabled?: boolean;
  CreatedAt?: string;
  created_at?: string;
  LastUsedAt?: string;
  last_used_at?: string;
  RevokedAt?: string;
  revoked_at?: string;
};

export type GatewayKeyCreateResponse = { key: GatewayKey; raw_key: string };

export type QuotaMode = 'disabled' | 'endpoint_credentials' | 'separate_credentials';
export type QuotaFlow = 'grok2api_admin' | 'tavily_usage' | 'firecrawl_credit_usage';

export type EndpointQuotaInput = {
  mode: QuotaMode;
  flow: QuotaFlow;
  base_url?: string;
  key?: string;
};

export type ProviderKey = {
  ID?: number;
  id?: number;
  Provider?: string;
  provider?: string;
  Name?: string;
  name?: string;
  BaseURL?: string;
  base_url?: string;
  KeyPrefix?: string;
  key_prefix?: string;
  Fingerprint?: string;
  fingerprint?: string;
  Enabled?: boolean;
  enabled?: boolean;
  ArchivedAt?: string;
  archived_at?: string;
  CooldownUntil?: string;
  cooldown_until?: string;
  cooldown_reason?: string;
  LastFailedAt?: string;
  last_failed_at?: string;
  LastEventAt?: string;
  last_event_at?: string;
  LastEventSource?: string;
  last_event_source?: string;
  last_used_at?: string;
  last_success_at?: string;
  last_error_at?: string;
  last_error_status?: number;
  QuotaMode?: QuotaMode;
  quota_mode?: QuotaMode;
  QuotaFlow?: QuotaFlow;
  quota_flow?: QuotaFlow;
  QuotaBaseURL?: string | null;
  quota_base_url?: string | null;
  QuotaKeyConfigured?: boolean;
  quota_key_configured?: boolean;
  QuotaKeyPrefix?: string | null;
  quota_key_prefix?: string | null;
  QuotaKeyFingerprint?: string | null;
  quota_key_fingerprint?: string | null;
};

export type ProviderSetting = { provider: string; base_url: string };
export type ProviderHealth = {
  provider: string;
  base_url: string;
  status: string;
  reasons: string[];
  key_count: number;
  enabled_key_count: number;
  cooldown_key_count: number;
  last_event_at?: string;
  last_event_source?: string;
  last_event_status_class?: string;
  last_event_http_status?: number;
  last_event_message_redacted?: string;
};

export type ProviderQuota = {
  provider: string;
  provider_key_id?: number;
  available: boolean;
  source: string;
  used?: number;
  limit_value?: number;
  remaining?: number;
  checked_at?: string;
  expires_at?: string;
  message_redacted?: string;
  details?: Record<string, unknown>;
};

export type ProviderKeyQuota = {
  provider_key_id: number;
  provider: string;
  available: boolean;
  source: string;
  used?: number;
  limit_value?: number;
  remaining?: number;
  checked_at?: string;
  expires_at?: string;
  message_redacted?: string;
  details?: Record<string, unknown>;
};

/** Operational quota display independent of inference pool status. */
export type QuotaOperationalState = 'ok' | 'disabled' | 'not_configured' | 'not_refreshed' | 'unavailable';

export type RefreshAllKeyQuotasResult = {
  provider: string;
  attempted: number;
  succeeded: number;
  failed: number;
  skipped_cooldown?: number;
  skipped_disabled: number;
  skipped_archived?: number;
  skipped_not_configured?: number;
};

export type ProviderPoolRow = {
  key: ProviderKey;
  status: 'available' | 'cooling' | 'disabled' | 'archived' | 'not_refreshed';
  quota?: ProviderKeyQuota;
};

export type ProviderPool = {
  provider: string;
  summary: {
    provider: string;
    key_count: number;
    enabled_key_count: number;
    available_key_count: number;
    cooling_key_count: number;
    refreshed_key_count: number;
    known_remaining?: number;
  };
  items: ProviderPoolRow[];
  page: { limit: number; offset: number; total: number };
};

export type UsageDaily = {
  Day?: string;
  day?: string;
  Provider?: string;
  provider?: string;
  RouteFamily?: string;
  route_family?: string;
  StatusClass?: string;
  status_class?: string;
  RequestCount?: number;
  request_count?: number;
};

export type AuditEvent = {
  ID?: number;
  id?: number;
  OccurredAt?: string;
  occurred_at?: string;
  ActorKind?: string;
  actor_kind?: string;
  Action?: string;
  action?: string;
  TargetKind?: string;
  target_kind?: string;
  TargetID?: string;
  target_id?: string;
  DetailRedacted?: string;
  detail_redacted?: string;
};

export type ProxyAttempt = {
  id: number;
  occurred_at?: string;
  request_id: string;
  provider: string;
  route_family: string;
  path: string;
  attempt_index: number;
  provider_key_id?: number;
  provider_key_name?: string;
  provider_key_fingerprint?: string;
  upstream_status?: number;
  status_class: string;
  reason?: string;
  cooldown_until?: string;
  terminal: boolean;
  message_redacted?: string;
};

export type ProxyDebugAttemptsSetting = { enabled: boolean };

export type DisplayTimezoneSetting = { timezone: string; source: 'stored' | 'host' };

