package providers

import (
	"fmt"
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
	DistinctBaseURLs         int          `json:"distinct_base_urls,omitempty"`
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
		settingsBaseURL, err := settings.GetBaseURL(provider)
		if err != nil {
			return nil, err
		}
		list, err := keys.List(provider)
		if err != nil {
			return nil, err
		}
		item := HealthItem{Provider: provider, Reasons: []string{}}
		// Track distinct base URLs among non-archived endpoints for health display
		// (settings base_url is creation default only, not live routing).
		distinct := map[string]struct{}{}
		enabledDistinct := map[string]struct{}{}
		for _, k := range list {
			if k.ArchivedAt != nil {
				continue
			}
			item.KeyCount++
			if k.BaseURL != "" {
				distinct[k.BaseURL] = struct{}{}
			}
			if k.Enabled {
				item.EnabledKeyCount++
				if k.BaseURL != "" {
					enabledDistinct[k.BaseURL] = struct{}{}
				}
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
		// Prefer enabled endpoints for display; fall back to all non-archived.
		displaySet := enabledDistinct
		if len(displaySet) == 0 {
			displaySet = distinct
		}
		item.DistinctBaseURLs = len(displaySet)
		item.BaseURL = healthBaseURLLabel(displaySet, settingsBaseURL, item.EnabledKeyCount, item.KeyCount)
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

// healthBaseURLLabel describes live routing URLs, not the settings creation default.
// Single distinct URL → that URL; mixed → "mixed (N endpoints)"; none → settings default.
func healthBaseURLLabel(distinct map[string]struct{}, settingsDefault string, enabledCount, keyCount int) string {
	switch len(distinct) {
	case 0:
		return settingsDefault
	case 1:
		for u := range distinct {
			return u
		}
	}
	n := enabledCount
	if n == 0 {
		n = keyCount
	}
	if n == 0 {
		n = len(distinct)
	}
	return fmt.Sprintf("mixed (%d endpoints)", n)
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
