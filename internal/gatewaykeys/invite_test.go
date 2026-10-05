package gatewaykeys_test

import (
	"path/filepath"
	"testing"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
)

func TestInviteCodes_Lifecycle(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "invite_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	svc := gatewaykeys.NewInviteService(st.DB())

	// 1. Create with agent_label_bind
	exp := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	inv, err := svc.Create("test-agent", 2, exp)
	if err != nil {
		t.Fatalf("Create invite: %v", err)
	}
	if inv.ID == 0 || inv.Code == "" || inv.AgentLabelBind != "test-agent" || inv.MaxRedemptions != 2 {
		t.Fatalf("unexpected invite fields: %+v", inv)
	}

	// 2. Redeem with wrong bind fails
	_, err = gatewaykeys.RedeemTx(st.DB(), inv.Code, "wrong-agent")
	if err != gatewaykeys.ErrInviteLabelBound {
		t.Fatalf("redeem wrong bind err = %v, want ErrInviteLabelBound", err)
	}

	// 3. Redeem 1 with correct bind succeeds
	r1, err := gatewaykeys.RedeemTx(st.DB(), inv.Code, "test-agent")
	if err != nil {
		t.Fatalf("redeem 1: %v", err)
	}
	if r1.RedemptionCount != 1 {
		t.Fatalf("redemption count = %d, want 1", r1.RedemptionCount)
	}

	// 4. Redeem 2 succeeds
	r2, err := gatewaykeys.RedeemTx(st.DB(), inv.Code, "test-agent")
	if err != nil {
		t.Fatalf("redeem 2: %v", err)
	}
	if r2.RedemptionCount != 2 {
		t.Fatalf("redemption count = %d, want 2", r2.RedemptionCount)
	}

	// 5. Redeem 3 fails (exhausted)
	_, err = gatewaykeys.RedeemTx(st.DB(), inv.Code, "test-agent")
	if err != gatewaykeys.ErrInviteExhausted {
		t.Fatalf("redeem 3 err = %v, want ErrInviteExhausted", err)
	}

	// 6. Test expired invite
	pastExp := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	expiredInv, err := svc.Create("", 5, pastExp)
	if err != nil {
		t.Fatalf("create expired invite: %v", err)
	}
	_, err = gatewaykeys.RedeemTx(st.DB(), expiredInv.Code, "any-agent")
	if err != gatewaykeys.ErrInviteExpired {
		t.Fatalf("redeem expired err = %v, want ErrInviteExpired", err)
	}

	// 7. Test revoked invite
	revokedInv, err := svc.Create("", 5, exp)
	if err != nil {
		t.Fatalf("create revokable invite: %v", err)
	}
	if err := svc.Revoke(revokedInv.ID); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}
	_, err = gatewaykeys.RedeemTx(st.DB(), revokedInv.Code, "any-agent")
	if err != gatewaykeys.ErrInviteRevoked {
		t.Fatalf("redeem revoked err = %v, want ErrInviteRevoked", err)
	}

	// 8. List invites
	list, err := svc.List()
	if err != nil {
		t.Fatalf("List invites: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
}

func TestRevokeUnused(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "revoke_unused_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	svc := gatewaykeys.NewService(st.DB())

	// Create 4 keys directly via DB to control timestamps
	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -35).Format(time.RFC3339Nano)
	recentTime := now.AddDate(0, 0, -5).Format(time.RFC3339Nano)

	// Key 1: Old created, never used -> should be revoked
	_, err = st.DB().Exec(`
		INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at, last_used_at)
		VALUES ('old-never-used', 'gsk_111', 'fp1', 'h1', 1, ?, NULL)`, oldTime)
	if err != nil {
		t.Fatalf("insert key 1: %v", err)
	}

	// Key 2: Old created, old last_used -> should be revoked
	_, err = st.DB().Exec(`
		INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at, last_used_at)
		VALUES ('old-used-long-ago', 'gsk_222', 'fp2', 'h2', 1, ?, ?)`, oldTime, oldTime)
	if err != nil {
		t.Fatalf("insert key 2: %v", err)
	}

	// Key 3: Old created, recently used -> should NOT be revoked
	_, err = st.DB().Exec(`
		INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at, last_used_at)
		VALUES ('old-recently-used', 'gsk_333', 'fp3', 'h3', 1, ?, ?)`, oldTime, recentTime)
	if err != nil {
		t.Fatalf("insert key 3: %v", err)
	}

	// Key 4: Recently created, never used -> should NOT be revoked
	_, err = st.DB().Exec(`
		INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at, last_used_at)
		VALUES ('new-never-used', 'gsk_444', 'fp4', 'h4', 1, ?, NULL)`, recentTime)
	if err != nil {
		t.Fatalf("insert key 4: %v", err)
	}

	revokedCount, err := svc.RevokeUnused(30)
	if err != nil {
		t.Fatalf("RevokeUnused: %v", err)
	}
	if revokedCount != 2 {
		t.Fatalf("revokedCount = %d, want 2", revokedCount)
	}

	// Verify key 1 is revoked
	var e1 int
	var r1 *string
	_ = st.DB().QueryRow(`SELECT enabled, revoked_at FROM gateway_keys WHERE name = 'old-never-used'`).Scan(&e1, &r1)
	if e1 != 0 || r1 == nil {
		t.Errorf("key 1 not revoked: enabled=%d, revoked_at=%v", e1, r1)
	}

	// Verify key 3 is still enabled
	var e3 int
	var r3 *string
	_ = st.DB().QueryRow(`SELECT enabled, revoked_at FROM gateway_keys WHERE name = 'old-recently-used'`).Scan(&e3, &r3)
	if e3 != 1 || r3 != nil {
		t.Errorf("key 3 should still be enabled: enabled=%d, revoked_at=%v", e3, r3)
	}

	// Verify key 4 is still enabled
	var e4 int
	var r4 *string
	_ = st.DB().QueryRow(`SELECT enabled, revoked_at FROM gateway_keys WHERE name = 'new-never-used'`).Scan(&e4, &r4)
	if e4 != 1 || r4 != nil {
		t.Errorf("key 4 should still be enabled: enabled=%d, revoked_at=%v", e4, r4)
	}
}
