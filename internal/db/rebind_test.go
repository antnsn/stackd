package db

import "testing"

func TestRebind(t *testing.T) {
	orig := driverName
	t.Cleanup(func() { driverName = orig })

	tests := []struct {
		name   string
		driver string
		query  string
		want   string
	}{
		{"sqlite unchanged", "sqlite", "SELECT * FROM t WHERE a=? AND b=?", "SELECT * FROM t WHERE a=? AND b=?"},
		{"postgres numbered", "postgres", "SELECT * FROM t WHERE a=? AND b=?", "SELECT * FROM t WHERE a=$1 AND b=$2"},
		{"postgres none", "postgres", "SELECT 1", "SELECT 1"},
		{"postgres single", "postgres", "INSERT INTO t VALUES (?)", "INSERT INTO t VALUES ($1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driverName = tt.driver
			if got := Rebind(tt.query); got != tt.want {
				t.Fatalf("Rebind(%q) with driver %q = %q, want %q", tt.query, tt.driver, got, tt.want)
			}
		})
	}
}
