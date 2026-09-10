package logging

import (
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestRingBuffer_CapsAtMaxLines(t *testing.T) {
	rb := NewRingBuffer(500)
	for i := 0; i < 600; i++ {
		rb.Log(fmt.Sprintf("line-%d", i))
	}

	got := rb.Lines()
	if len(got) != 500 {
		t.Fatalf("Lines() returned %d lines, want 500", len(got))
	}
	if got[0] != "line-100" {
		t.Fatalf("Lines()[0] = %q, want %q (oldest 100 should have been dropped)", got[0], "line-100")
	}
	if got[len(got)-1] != "line-599" {
		t.Fatalf("Lines()[last] = %q, want %q", got[len(got)-1], "line-599")
	}
}

func TestRingBuffer_ConcurrentWrites_NoRace(t *testing.T) {
	rb := NewRingBuffer(500)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				rb.Log(fmt.Sprintf("g%d-%d", g, i))
			}
		}(g)
	}
	wg.Wait()

	got := rb.Lines()
	if len(got) != 500 {
		t.Fatalf("Lines() returned %d lines, want 500 (50 goroutines * 20 lines = 1000 written, capped at 500)", len(got))
	}
}

func TestRingBuffer_ImplementsIOWriter(t *testing.T) {
	rb := NewRingBuffer(500)
	var _ io.Writer = rb

	n, err := rb.Write([]byte("first\nsecond\nthird"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len("first\nsecond\nthird") {
		t.Fatalf("Write() n = %d, want %d", n, len("first\nsecond\nthird"))
	}

	got := rb.Lines()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("Lines() = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("Lines()[%d] = %q, want %q", i, got[i], w)
		}
	}
}
