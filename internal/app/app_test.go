package app

import (
	"errors"
	"testing"

	"github.com/rivo/tview"

	"dbtui/internal/config"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{":conn", "conn"},
		{"conn", "conn"},
		{" conn ", "conn"},
		{":q", "q"},
		{"", ""},
		{":tables extra", "tables"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseCommand(tt.in); got != tt.want {
				t.Fatalf("ParseCommand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatStatusBar(t *testing.T) {
	t.Run("nil active connection", func(t *testing.T) {
		got := FormatStatusBar(nil)
		if got == "" {
			t.Fatalf("expected non-empty status text for nil ActiveConnection")
		}
	})

	t.Run("disconnected", func(t *testing.T) {
		got := FormatStatusBar(&ActiveConnection{Status: StatusDisconnected})
		if got == "" {
			t.Fatalf("expected non-empty status text")
		}
	})

	t.Run("connected includes name, type, and green dot", func(t *testing.T) {
		ac := &ActiveConnection{
			Conn:   config.Connection{Name: "prod-db", Type: config.Postgres},
			Status: StatusConnected,
		}
		got := FormatStatusBar(ac)
		for _, want := range []string{"prod-db", "postgres", "green"} {
			if !contains(got, want) {
				t.Fatalf("FormatStatusBar() = %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("error status includes the error message", func(t *testing.T) {
		ac := &ActiveConnection{
			Conn:      config.Connection{Name: "prod-db", Type: config.Postgres},
			Status:    StatusError,
			LastError: errors.New("connection refused"),
		}
		got := FormatStatusBar(ac)
		if !contains(got, "connection refused") {
			t.Fatalf("FormatStatusBar() = %q, want it to contain the error text", got)
		}
	})
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFindConnection(t *testing.T) {
	cfg := &config.ConfigFile{Connections: []config.Connection{
		{Name: "a", Type: config.Postgres},
		{Name: "b", Type: config.Redis},
	}}

	t.Run("found", func(t *testing.T) {
		c, err := FindConnection(cfg, "b")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Type != config.Redis {
			t.Fatalf("got type %v, want redis", c.Type)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := FindConnection(cfg, "missing")
		if err == nil {
			t.Fatalf("expected error for missing connection")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := FindConnection(nil, "a")
		if err == nil {
			t.Fatalf("expected error for nil config")
		}
	})
}

func TestApp_NavStack_GoBack(t *testing.T) {
	a := NewApp(&config.ConfigFile{}, nil, nil)
	a.RegisterPage("one", tview.NewBox())
	a.RegisterPage("two", tview.NewBox())
	a.RegisterPage("three", tview.NewBox())

	a.SwitchToPage("one")
	a.SwitchToPage("two")
	a.SwitchToPage("three")

	if !a.GoBack() {
		t.Fatalf("GoBack() = false, want true (stack should have entries)")
	}
	front, _ := a.Pages.GetFrontPage()
	if front != "two" {
		t.Fatalf("after one GoBack(), front page = %q, want %q", front, "two")
	}

	if !a.GoBack() {
		t.Fatalf("GoBack() = false, want true")
	}
	front, _ = a.Pages.GetFrontPage()
	if front != "one" {
		t.Fatalf("after two GoBack()s, front page = %q, want %q", front, "one")
	}

	if a.GoBack() {
		t.Fatalf("GoBack() = true with an empty stack, want false")
	}
}
