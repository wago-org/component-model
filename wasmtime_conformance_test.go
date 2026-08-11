package component_test

import (
	"context"
	_ "embed"
	"strings"
	"testing"

	"github.com/wago-org/component-model"
	"github.com/wago-org/wago"
)

//go:embed testdata/wasmtime/string_ptr_oob.wasm
var wasmtimeStringPointerOutOfBounds []byte

//go:embed testdata/wasmtime/string_length_overflow.wasm
var wasmtimeStringLengthOverflow []byte

//go:embed testdata/wasmtime/string_realloc_oob.wasm
var wasmtimeStringReallocOutOfBounds []byte

//go:embed testdata/wasmtime/post_return_scalars.wasm
var wasmtimePostReturnScalars []byte

//go:embed testdata/wasmtime/post_return_trap.wasm
var wasmtimePostReturnTrap []byte

//go:embed testdata/wasmtime/post_return_string.wasm
var wasmtimePostReturnString []byte

// Ported from Wasmtime's component-model string pointer bounds regression.
// Instantiation executes the sender's start function, which attempts to pass a
// string whose guest pointer is outside its linear memory.
func TestWasmtimeStringPointerOutOfBounds(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}

	instance, err := components.Instantiate(context.Background(), wasmtimeStringPointerOutOfBounds)
	if instance != nil {
		_ = instance.Close(context.Background())
	}
	if err == nil {
		t.Fatal("instantiated component with out-of-bounds string pointer")
	}
	if message := strings.ToLower(err.Error()); !strings.Contains(message, "string") ||
		(!strings.Contains(message, "bound") && !strings.Contains(message, "overflow")) {
		t.Fatalf("out-of-bounds string error = %q", err)
	}
}

// Ported from Wasmtime's string length overflow regression. Canonical ABI
// adapters must reject malicious lengths before pointer arithmetic wraps.
func TestWasmtimeStringLengthOverflow(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}
	instance, err := components.Instantiate(context.Background(), wasmtimeStringLengthOverflow)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer instance.Close(context.Background())

	_, err = instance.Call(context.Background(), "call", uint32(1<<31))
	if err == nil {
		t.Fatal("accepted overflowing string length")
	}
	if message := strings.ToLower(err.Error()); !strings.Contains(message, "string") ||
		(!strings.Contains(message, "bound") && !strings.Contains(message, "overflow")) {
		t.Fatalf("overflowing string length error = %q", err)
	}
}

// Ported from Wasmtime's realloc bounds regression. A guest realloc function
// returning memory outside its linear memory must trap before any host access.
func TestWasmtimeStringReallocOutOfBounds(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}
	instance, err := components.Instantiate(context.Background(), wasmtimeStringReallocOutOfBounds)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer instance.Close(context.Background())

	_, err = instance.Call(context.Background(), "call")
	if err == nil {
		t.Fatal("accepted realloc result outside guest memory")
	}
	if message := strings.ToLower(err.Error()); !strings.Contains(message, "string") ||
		(!strings.Contains(message, "bound") && !strings.Contains(message, "overflow")) {
		t.Fatalf("out-of-bounds realloc error = %q", err)
	}
}

// Ported from Wasmtime's post-return scalar coverage. This proves the adapter
// passes the exact flattened result values to each post-return function.
func TestWasmtimePostReturnReceivesScalarResults(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}
	instance, err := components.Instantiate(context.Background(), wasmtimePostReturnScalars)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer instance.Close(context.Background())

	tests := []struct {
		name string
		want component.Value
	}{
		{"i32", uint32(1)},
		{"i64", uint64(2)},
		{"f32", float32(3)},
		{"f64", float64(4)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := instance.Call(context.Background(), test.name)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(got) != 1 || got[0] != test.want {
				t.Fatalf("result = %#v, want [%#v]", got, test.want)
			}
		})
	}
}

// Ported from Wasmtime's post-return trap regression. Once guest code traps
// during post-return, the Component Model forbids all subsequent entry.
func TestWasmtimePostReturnTrapPoisonsInstance(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}
	instance, err := components.Instantiate(context.Background(), wasmtimePostReturnTrap)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer instance.Close(context.Background())

	if _, err := instance.Call(context.Background(), "call"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unreachable") {
		t.Fatalf("first call error = %v, want unreachable trap", err)
	}
	if _, err := instance.Call(context.Background(), "call"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cannot enter") {
		t.Fatalf("second call error = %v, want poisoned instance", err)
	}
}

// Ported from Wasmtime's indirect-result post-return coverage. The adapter
// must finish lifting the string before passing its return-area pointer to the
// guest cleanup function.
func TestWasmtimePostReturnString(t *testing.T) {
	runtime := wago.NewRuntime()
	defer runtime.Close()
	components, err := component.Enable(runtime)
	if err != nil {
		t.Fatalf("enable component plugin: %v", err)
	}
	instance, err := components.Instantiate(context.Background(), wasmtimePostReturnString)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer instance.Close(context.Background())

	got, err := instance.Call(context.Background(), "get")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("result = %#v, want [\"hello world\"]", got)
	}
}
