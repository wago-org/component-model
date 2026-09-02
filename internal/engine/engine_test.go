package engine

import (
	"testing"

	core "github.com/wago-org/wago"
)

func TestMemoryReadsCurrentWagoMemory(t *testing.T) {
	mem, err := core.NewMemory(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mem.Close(); err != nil {
			t.Errorf("close memory: %v", err)
		}
	}()

	copy(mem.UnsafeBytes()[7:], []byte("wago"))
	wrapped := &memory{mem: mem}
	if got, want := wrapped.Size(), uint32(1<<16); got != want {
		t.Fatalf("Size() = %d, want %d", got, want)
	}
	got, ok := wrapped.Read(7, 4)
	if !ok || string(got) != "wago" {
		t.Fatalf("Read(7, 4) = %q, %v; want %q, true", got, ok, "wago")
	}
	if _, ok := wrapped.Read(1<<16-1, 2); ok {
		t.Fatal("out-of-bounds Read succeeded")
	}
}
