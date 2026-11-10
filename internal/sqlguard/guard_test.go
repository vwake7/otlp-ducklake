package sqlguard

import "testing"

func TestPrepareAcceptsBoundedSelect(t *testing.T) {
	got, err := Prepare(`SELECT * FROM logs WHERE timestamp >= '2024-01-01' AND timestamp < '2024-01-02'`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SELECT * FROM logs WHERE timestamp >= '2024-01-01' AND timestamp < '2024-01-02' LIMIT 1000"; got != want {
		t.Fatalf("got %s", got)
	}
}

func TestPrepareBetween(t *testing.T) {
	_, err := Prepare(`WITH x AS (SELECT * FROM logs) SELECT * FROM x WHERE timestamp BETWEEN TIMESTAMP '2024-01-01' AND TIMESTAMP '2024-01-02' LIMIT 10`, 1000)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrepareCapsLimit(t *testing.T) {
	got, err := Prepare(`SELECT body FROM logs WHERE timestamp >= 'a' AND timestamp < 'b' LIMIT 5000`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got != `SELECT body FROM logs WHERE timestamp >= 'a' AND timestamp < 'b' LIMIT 1000` {
		t.Fatalf("got %s", got)
	}
}

func TestPrepareKeepsSmallerLimit(t *testing.T) {
	got, err := Prepare(`SELECT body FROM logs WHERE timestamp >= 'a' AND timestamp < 'b' LIMIT 25`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got != `SELECT body FROM logs WHERE timestamp >= 'a' AND timestamp < 'b' LIMIT 25` {
		t.Fatalf("got %s", got)
	}
}

func TestPrepareAcceptsUnboundedSelect(t *testing.T) {
	got, err := Prepare(`SELECT * FROM logs`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got != `SELECT * FROM logs LIMIT 1000` {
		t.Fatalf("got %s", got)
	}
}

func TestPrepareRejects(t *testing.T) {
	cases := []string{
		`INSERT INTO logs VALUES (1)`,
		`SELECT 1; SELECT 2`,
		`UPDATE logs SET body = '' WHERE timestamp >= 'a' AND timestamp < 'b'`,
		`SELECT * FROM logs WHERE timestamp >= 'a' AND timestamp < 'b'; DROP TABLE logs`,
	}
	for _, sql := range cases {
		if _, err := Prepare(sql, 1000); err == nil {
			t.Fatalf("accepted %s", sql)
		}
	}
}
