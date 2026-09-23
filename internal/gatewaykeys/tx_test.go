package gatewaykeys_test

import (
	"path/filepath"
	"testing"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
)

func TestCreateTxAndDisableTx(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	svc := gatewaykeys.NewService(st.DB())

	// Test CreateTx in a transaction
	tx, err := st.DB().Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	raw, disp, err := gatewaykeys.CreateTx(tx, "tx-key-1")
	if err != nil {
		t.Fatalf("CreateTx: %v", err)
	}
	if disp.ID <= 0 || disp.Name != "tx-key-1" || !disp.Enabled {
		t.Fatalf("CreateTx disp invalid: %+v", disp)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify key works
	key, err := svc.Verify(raw)
	if err != nil || key == nil || key.Name != "tx-key-1" {
		t.Fatalf("Verify: key=%v err=%v", key, err)
	}

	// Test DisableTx in a transaction
	tx2, err := st.DB().Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}
	if err := gatewaykeys.DisableTx(tx2, disp.ID); err != nil {
		t.Fatalf("DisableTx: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit tx2: %v", err)
	}

	// Verify key is now disabled
	keyAfter, err := svc.Verify(raw)
	if err != gatewaykeys.ErrNotAuthorized || keyAfter != nil {
		t.Fatalf("Verify disabled: key=%v err=%v", keyAfter, err)
	}
}
