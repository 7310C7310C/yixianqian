package db

import "testing"

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	if err := VerifyMigrationFiles(); err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	list, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i, m := range list {
		if m.version <= 0 || m.name == "" || m.body == "" {
			t.Fatalf("invalid migration %d: %+v", i, m)
		}
		if i > 0 && list[i-1].version >= m.version {
			t.Fatalf("migrations not strictly ordered at %d", i)
		}
	}
}
