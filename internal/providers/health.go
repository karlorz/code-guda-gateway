package providers

import (
	"strconv"
	"time"
)

type HealthStatus string

const (
	HealthHealthy    HealthStatus = "healthy"
	HealthMissingKey HealthStatus = "missing_key"
	HealthDisabled   HealthStatus = "disabled"
	HealthCooldown   HealthStatus = "cooldown"
	HealthUnused     HealthStatus = "unused"
	HealthDegraded   HealthStatus = "degraded"
)

type HealthItem struct {
	Provider                 string       `json:"provider"`
	BaseURL                  string       `json:"base_url"`
	KeyCount                 int          `json:"key_count"`
	EnabledKeyCount          int          `json:"enabled_key_count"`
	CooldownKeyCount         int          `json:"cooldown_key_count"`
	Status                   HealthStatus `json:"status"`
	Reasons                  []string     `json:"reasons"`
	LastUsedAt               *string      `json:"last_used_at,omitempty"`
	LastSuccessAt            *string      `json:"last_success_at,omitempty"`
	LastEventAt              *string      `json:"last_event_at,omitempty"`
	LastEventSource          *string      `json:"last_event_source,omitempty"`
	LastEventStatusClass     *string      `json:"last_event_status_class,omitempty"`
	LastEventHTTPStatus      *int         `json:"last_event_http_status,omitempty"`
	LastEventMessageRedacted *string      `json:"last_event_message_redacted,omitempty"`
}

func BuildHealth(settings *SettingsRepo, keys *KeyRepo) ([]HealthItem, error) {
	var out []HealthItem
	now := time.Now().UTC()
	for _, provider := range []string{ProviderGrok, ProviderTavily, ProviderFirecrawl} {
		baseURL, err := settings.GetBaseURL(provider)
		if err != nil {
			return nil, err
		}
		list, err := keys.List(provider)
		if err != nil {
			return nil, err
		}
		item := HealthItem{Provider: provider, BaseURL: baseURL, Reasons: []string{}}
		for _, k := range list {
			if k.ArchivedAt != nil {
				continue
			}
			item.KeyCount++
			if k.Enabled {
				item.EnabledKeyCount++
			}
			if k.CooldownUntil != nil {
				if t, err := time.Parse(time.RFC3339Nano, *k.CooldownUntil); err == nil && t.After(now) {
					item.CooldownKeyCount++
				}
			}
			item.LastUsedAt = laterString(item.LastUsedAt, k.LastUsedAt)
			item.LastSuccessAt = laterString(item.LastSuccessAt, k.LastSuccessAt)
			applyLatestKeyEvent(&item, k)
		}
		latestEventFailed := item.LastEventStatusClass != nil && *item.LastEventStatusClass != "2xx"
		switch {
		case item.KeyCount == 0:
			item.Status = HealthMissingKey
			item.Reasons = append(item.Reasons, "no provider keys")
		case item.EnabledKeyCount == 0:
			item.Status = HealthDisabled
			item.Reasons = append(item.Reasons, "no enabled provider keys")
		case item.EnabledKeyCount == item.CooldownKeyCount:
			item.Status = HealthCooldown
			item.Reasons = append(item.Reasons, "enabled keys are cooling down")
		case latestEventFailed:
			item.Status = HealthDegraded
			item.Reasons = append(item.Reasons, degradedEventReason(item))
		case item.LastUsedAt == nil && item.LastSuccessAt == nil:
			item.Status = HealthUnused
			item.Reasons = append(item.Reasons, "no recent usage")
		default:
			item.Status = HealthHealthy
		}
		out = append(out, item)
	}
	return out, nil
}

func applyLatestKeyEvent(item *HealthItem, k DisplayProviderKey) {
	latest := laterString(item.LastEventAt, k.LastEventAt)
	if latest != k.LastEventAt {
		return
	}
	item.LastEventAt = k.LastEventAt
	item.LastEventSource = k.LastEventSource
	item.LastEventStatusClass = k.LastEventStatusClass
	item.LastEventHTTPStatus = k.LastEventHTTPStatus
	item.LastEventMessageRedacted = k.LastEventMessageRedacted
}

func degradedEventReason(item HealthItem) string {
	reason := "latest provider event failed"
	if item.LastEventSource != nil && *item.LastEventSource != "" {
		reason += ": " + *item.LastEventSource
	}
	if item.LastEventHTTPStatus != nil {
		reason += " http " + strconv.Itoa(*item.LastEventHTTPStatus)
	}
	if item.LastEventMessageRedacted != nil && *item.LastEventMessageRedacted != "" {
		reason += " - " + *item.LastEventMessageRedacted
	}
	return reason
}

func laterString(a, b *string) *string {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b > *a {
		return b
	}
	return a
}
