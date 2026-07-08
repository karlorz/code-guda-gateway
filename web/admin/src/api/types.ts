export type SessionResponse = { authenticated: true; csrf_token: string };
export type ListResponse<T> = { items: T[]; page?: { limit: number; offset: number }; filter?: Record<string, string> };

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

export type ProviderKey = {
  ID?: number;
  id?: number;
  Provider?: string;
  provider?: string;
  Name?: string;
  name?: string;
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
  LastEventAt?: string;
  last_event_at?: string;
  LastEventSource?: string;
  last_event_source?: string;
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
};

export type ProviderQuota = {
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
