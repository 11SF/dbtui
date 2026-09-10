package main

import "testing"

func TestIsDestructiveRedisCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"FLUSHDB", "FLUSHDB", true},
		{"lowercase flushall", "flushall", true},
		{"DEL with key", "DEL somekey", true},
		{"UNLINK multiple keys", "UNLINK a b c", true},
		{"GET is safe", "GET somekey", false},
		{"SET is safe", "SET x 1", false},
		{"no substring false-positive on DEL", "DELAY_SOMETHING", false},
		{"empty string", "", false},
		{"whitespace only", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDestructiveRedisCommand(tc.command); got != tc.want {
				t.Errorf("IsDestructiveRedisCommand(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestRunKVCommand_NoActiveConnection_ReturnsError(t *testing.T) {
	a, _ := newTestApp(t)
	_, err := a.RunKVCommand(0, "GET foo", false)
	if err == nil {
		t.Fatal("want error with no active connection, got nil")
	}
}

func TestRunKVCommand_DestructiveUnconfirmed_NeedsConfirm(t *testing.T) {
	a, _ := newTestApp(t)
	res, err := a.RunKVCommand(0, "FLUSHDB", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.NeedsConfirm {
		t.Fatal("want NeedsConfirm = true for an unconfirmed destructive command")
	}
	if res.Result != nil {
		t.Fatalf("want no result when confirmation is needed, got %v", res.Result)
	}
}
