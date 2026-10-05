package component_test

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"testing"

	component "github.com/wago-org/component-model"
)

//go:embed testdata/initialization_resource_failure.wasm
var initializationResourceFailure []byte

func TestGraphInitializationFailureClosesHostResources(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		name := "successful-cleanup"
		if cleanupFails {
			name = "cleanup-error"
		}
		t.Run(name, func(t *testing.T) {
			rt, ref := loadService(t, nil)
			defer rt.Close()
			setupErr := errors.New("initializer setup failed")
			cleanupErr := errors.New("resource cleanup failed")
			const iface = "test:initialization/resources"
			const tag uint32 = 700
			root := t.TempDir()
			var files []*os.File
			var dropped []uint32
			opts := []component.Option{
				component.WithResourceTag(iface, "resource", tag),
				component.WithImport(iface, "acquire", func(context.Context, []component.Value) ([]component.Value, error) {
					file, err := os.CreateTemp(root, "resource-")
					if err != nil {
						return nil, err
					}
					files = append(files, file)
					t.Cleanup(func() { _ = file.Close() })
					return []component.Value{uint32(len(files))}, nil
				}, nil, []component.TypeDesc{component.OwnDesc{ResourceType: tag}}),
				component.WithImport(iface, "fail", func(context.Context, []component.Value) ([]component.Value, error) { return nil, setupErr }, nil, nil),
				component.WithHostResourceDtor(tag, func(ctx context.Context, rep uint32) error {
					if ctx.Err() != nil {
						t.Errorf("resource cleanup context canceled: %v", ctx.Err())
					}
					dropped = append(dropped, rep)
					err := files[rep-1].Close()
					if cleanupFails && rep == 1 {
						return errors.Join(err, cleanupErr)
					}
					return err
				}),
			}
			entered := false
			err := ref.With(func(service component.Service) error {
				return service.WithInstance(context.Background(), initializationResourceFailure, func(*component.Instance) error { entered = true; return nil }, opts...)
			})
			if entered {
				t.Fatal("callback ran after initialization failure")
			}
			if !errors.Is(err, setupErr) {
				t.Fatalf("initialization error = %v, want setup error", err)
			}
			if len(files) != 2 {
				t.Fatalf("acquired resources = %d, want 2", len(files))
			}
			if len(dropped) != 2 || dropped[0] == dropped[1] {
				t.Errorf("destructed resources = %v, want both resources exactly once", dropped)
			}
			for _, file := range files {
				if _, statErr := file.Stat(); statErr == nil || file.Fd() != ^uintptr(0) {
					t.Errorf("resource %s after failed initialization = %v, want closed", filepath.Base(file.Name()), statErr)
				}
			}
			if cleanupFails && !errors.Is(err, cleanupErr) {
				t.Errorf("initialization error = %v, want joined cleanup error", err)
			}
		})
	}
}
