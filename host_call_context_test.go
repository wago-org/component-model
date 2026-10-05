package component_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
)

//go:embed testdata/host_call_context.wasm
var hostCallContextComponent []byte

type hostContextValueKey struct{}

func TestHostImportReceivesActiveCallCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancellation"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			rt, ref := loadService(t, nil)
			defer rt.Close()
			instantiateCtx := context.WithValue(context.Background(), hostContextValueKey{}, "instance")
			observed := make(chan context.Context, 1)
			interrupted := make(chan error, 1)
			rescue := make(chan struct{})
			var rescueOnce sync.Once
			release := func() { rescueOnce.Do(func() { close(rescue) }) }
			defer release()
			fn := func(ctx context.Context, _ []component.Value) ([]component.Value, error) {
				observed <- ctx
				select {
				case <-ctx.Done():
					interrupted <- ctx.Err()
					return nil, ctx.Err()
				case <-rescue:
					return nil, nil
				}
			}
			err := ref.With(func(service component.Service) error {
				return service.WithInstance(instantiateCtx, hostCallContextComponent, func(in *component.Instance) error {
					var callCtx context.Context
					var cancel context.CancelFunc
					wantErr := error(context.Canceled)
					if deadline {
						callCtx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
						wantErr = context.DeadlineExceeded
					} else {
						callCtx, cancel = context.WithCancel(context.Background())
					}
					defer cancel()
					callCtx = context.WithValue(callCtx, hostContextValueKey{}, "call")
					done := make(chan error, 1)
					go func() { _, err := in.Call(callCtx, "run"); done <- err }()
					var hostCtx context.Context
					select {
					case hostCtx = <-observed:
					case err := <-done:
						return fmt.Errorf("call returned before host import: %v", err)
					}
					wantDeadline, wantHasDeadline := callCtx.Deadline()
					gotDeadline, gotHasDeadline := hostCtx.Deadline()
					if !deadline {
						cancel()
					}
					var hostErr error
					select {
					case hostErr = <-interrupted:
					case <-time.After(time.Second):
						release()
					}
					callErr := <-done
					if !errors.Is(hostErr, wantErr) {
						return fmt.Errorf("active host wait error = %v, want %v (call returned %v)", hostErr, wantErr, callErr)
					}
					if !errors.Is(callErr, wantErr) {
						return fmt.Errorf("component call error = %v, want %v", callErr, wantErr)
					}
					if gotHasDeadline != wantHasDeadline || !gotDeadline.Equal(wantDeadline) {
						return fmt.Errorf("host deadline = %v/%v, want %v/%v", gotDeadline, gotHasDeadline, wantDeadline, wantHasDeadline)
					}
					if got := hostCtx.Value(hostContextValueKey{}); got != "instance" {
						return fmt.Errorf("host context value = %v, want preserved instance value", got)
					}
					return nil
				}, component.WithImport("test:context/host", "wait", fn, nil, nil))
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHostImportContextExpiresAfterCallback(t *testing.T) {
	rt, ref := loadService(t, nil)
	defer rt.Close()
	var retained context.Context
	fn := func(ctx context.Context, _ []component.Value) ([]component.Value, error) {
		retained = ctx
		// Query concurrently to verify the bridge publishes one context safely.
		var queries sync.WaitGroup
		for i := 0; i < 8; i++ {
			queries.Add(1)
			go func() {
				defer queries.Done()
				ctx.Done()
				ctx.Deadline()
				ctx.Err()
			}()
		}
		queries.Wait()
		return nil, nil
	}
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), hostCallContextComponent, func(in *component.Instance) error {
			values, err := in.Call(context.Background(), "run")
			if err != nil || len(values) != 1 || values[0] != uint32(7) {
				return fmt.Errorf("successful host call = %v, %v", values, err)
			}
			select {
			case <-retained.Done():
			default:
				return errors.New("retained host context remained live after callback exit")
			}
			if err := retained.Err(); !errors.Is(err, context.Canceled) {
				return fmt.Errorf("retained context error = %v, want cancellation", err)
			}
			return nil
		}, component.WithImport("test:context/host", "wait", fn, nil, nil))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func BenchmarkHostImportCallContext(b *testing.B) {
	for _, query := range []bool{false, true} {
		name := "unused"
		if query {
			name = "queried"
		}
		b.Run(name, func(b *testing.B) {
			rt, ref := loadService(b, nil)
			defer rt.Close()
			fn := func(ctx context.Context, _ []component.Value) ([]component.Value, error) {
				if query {
					ctx.Done()
				}
				return nil, nil
			}
			err := ref.With(func(service component.Service) error {
				return service.WithInstance(context.Background(), hostCallContextComponent, func(in *component.Instance) error {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := in.Call(context.Background(), "run"); err != nil {
							return err
						}
					}
					b.StopTimer()
					return nil
				}, component.WithImport("test:context/host", "wait", fn, nil, nil))
			})
			if err != nil {
				b.Fatal(err)
			}
		})
	}
}
