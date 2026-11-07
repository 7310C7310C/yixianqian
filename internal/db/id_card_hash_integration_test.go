package db

import (
	"testing"
)

func TestIntegrationIDCardHashInUseLegacy(t *testing.T) {
	database, ctx := openPhotoQuotaDB(t)
	user := photoQuotaUser(t, ctx, database)
	legacy := "legacy-hash-" + randomDigits(t, 12)
	if _, err := database.pool.Exec(ctx,
		`UPDATE users SET id_card_hash = $2 WHERE id = $1`, user.ID, legacy); err != nil {
		t.Fatal(err)
	}
	inUse, err := database.IDCardHashInUse(ctx, "new-hash-not-present", legacy, 0)
	if err != nil || !inUse {
		t.Fatalf("legacy hash not detected: inUse=%v err=%v", inUse, err)
	}
	inUse, err = database.IDCardHashInUse(ctx, "new-hash-not-present", legacy, user.ID)
	if err != nil || inUse {
		t.Fatalf("excluded user still matched: inUse=%v err=%v", inUse, err)
	}
}
