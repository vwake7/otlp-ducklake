package lake

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestPairsToMap(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(pairsMacro); err != nil {
		t.Fatal(err)
	}
	var got string
	err = db.QueryRow(`SELECT pairs_to_map('[{"k":"service.name","v":"api"}]')::VARCHAR`).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || got == "{}" {
		t.Fatalf("map %q", got)
	}
	err = db.QueryRow(`SELECT pairs_to_map('[]')::VARCHAR`).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
}
