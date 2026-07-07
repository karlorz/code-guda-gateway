package cooldown

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter_Seconds(t *testing.T) {
	d, ok := ParseRetryAfter("120", time.Now())
	if !ok {
		t.Fatal("expected ok")
	}
	if d != 120*time.Second {
		t.Fatalf("dur = %v, want 120s", d)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Second)
	header := future.UTC().Format(http.TimeFormat)
	d, ok := ParseRetryAfter(header, now)
	if !ok {
		t.Fatal("expected ok")
	}
	if d < 89*time.Second || d > 91*time.Second {
		t.Fatalf("dur = %v, want ~90s", d)
	}
}

func TestParseRetryAfter_Absent(t *testing.T) {
	_, ok := ParseRetryAfter("", time.Now())
	if ok {
		t.Fatal("expected not ok")
	}
}

func TestParseRetryAfter_Capped(t *testing.T) {
	d, ok := ParseRetryAfter("86400", time.Now())
	if !ok {
		t.Fatal("expected ok")
	}
	if d != MaxRetryAfter {
		t.Fatalf("dur = %v, want cap %v", d, MaxRetryAfter)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	_, ok := ParseRetryAfter("garbage", time.Now())
	if ok {
		t.Fatal("expected not ok")
	}
}

func defaultS() Settings { return DefaultSettings() }

func TestPolicyForStatus_429(t *testing.T) {
	dur, reason, apply, retry := PolicyForStatus(http.StatusTooManyRequests, defaultS())
	if !apply || !retry || reason != "rate_limited" || dur != DefaultRateLimitCooldown {
		t.Fatalf("got dur=%v reason=%q apply=%v retry=%v", dur, reason, apply, retry)
	}
}

func TestPolicyForStatus_Transient(t *testing.T) {
	for _, code := range []int{
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		dur, reason, apply, retry := PolicyForStatus(code, defaultS())
		if !apply || !retry || reason != "transient" || dur != DefaultTransientCooldown {
			t.Fatalf("status %d: dur=%v reason=%q apply=%v retry=%v", code, dur, reason, apply, retry)
		}
	}
}

func TestPolicyForStatus_Credential(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		dur, reason, apply, retry := PolicyForStatus(code, defaultS())
		if !apply || !retry || reason != "credential_error" || dur != DefaultCredentialCooldown {
			t.Fatalf("status %d: dur=%v reason=%q apply=%v retry=%v", code, dur, reason, apply, retry)
		}
	}
}

func TestPolicyForStatus_Success(t *testing.T) {
	_, reason, apply, retry := PolicyForStatus(http.StatusOK, defaultS())
	if apply || retry || reason != "" {
		t.Fatalf("apply=%v retry=%v reason=%q", apply, retry, reason)
	}
}

func TestPolicyForStatus_Other4xx(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusNotFound} {
		_, reason, apply, retry := PolicyForStatus(code, defaultS())
		if apply || retry || reason != "" {
			t.Fatalf("status %d: apply=%v retry=%v reason=%q", code, apply, retry, reason)
		}
	}
}
