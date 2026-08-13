package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wago-org/component-model/internal/abi"
	"github.com/wago-org/component-model/internal/binary"
	api "github.com/wago-org/component-model/internal/engine"
)

type testFuncDef struct{ params, results []api.ValueType }

func (d testFuncDef) ParamTypes() []api.ValueType  { return d.params }
func (d testFuncDef) ResultTypes() []api.ValueType { return d.results }

type testFunction struct {
	def  testFuncDef
	call func(context.Context, []uint64) error
}

func (f *testFunction) Definition() api.FunctionDefinition { return f.def }
func (f *testFunction) Call(ctx context.Context, args ...uint64) ([]uint64, error) {
	stack := append([]uint64(nil), args...)
	if err := f.CallWithStack(ctx, stack); err != nil {
		return nil, err
	}
	return stack[:len(f.def.results)], nil
}
func (f *testFunction) CallWithStack(ctx context.Context, stack []uint64) error {
	if f.call != nil {
		return f.call(ctx, stack)
	}
	return nil
}

type testMemory struct{ buf []byte }

func (m *testMemory) Size() uint32 { return uint32(len(m.buf)) }
func (m *testMemory) Read(off, n uint32) ([]byte, bool) {
	if uint64(off)+uint64(n) > uint64(len(m.buf)) {
		return nil, false
	}
	return m.buf[off : off+n], true
}

type testModule struct {
	mem   api.Memory
	funcs map[string]api.Function
	close func()
}

func (*testModule) Name() string                                                   { return "test" }
func (m *testModule) Memory() api.Memory                                           { return m.mem }
func (m *testModule) ExportedFunction(name string) api.Function                    { return m.funcs[name] }
func (*testModule) ExportedFunctionDefinitions() map[string]api.FunctionDefinition { return nil }
func (*testModule) ExportedMemory(string) api.Memory                               { return nil }
func (*testModule) ExportedGlobal(string) api.Global                               { return nil }
func (m *testModule) Close(context.Context) error {
	if m.close != nil {
		m.close()
	}
	return nil
}

func TestReallocTrapPoisonsInstance(t *testing.T) {
	realloc := &testFunction{def: testFuncDef{params: []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, results: []api.ValueType{api.ValueTypeI32}}, call: func(context.Context, []uint64) error {
		return errors.New("realloc trap")
	}}
	main := &testFunction{}
	mod := &testModule{mem: &testMemory{buf: make([]byte, 64)}, funcs: map[string]api.Function{"run": main, "cabi_realloc": realloc}}
	be := &boundExport{mod: mod, funcName: "run", fd: binary.FuncDesc{Params: []binary.FuncParam{{Name: "s", Type: binary.TypeRef{Primitive: "string"}}}}}
	finalizeBoundExport(be, nil, nil, nil, 0)
	in := &Instance{resources: newHandleTable()}
	if _, err := in.invoke(context.Background(), be, "run", []abi.Value{"hello"}); err == nil {
		t.Fatal("expected realloc trap")
	}
	if _, err := in.invoke(context.Background(), &boundExport{}, "later", nil); err == nil || !strings.Contains(err.Error(), "cannot enter component instance") {
		t.Fatalf("second call error = %v, want poisoned-instance error", err)
	}
}

func TestGuestResultLiftTrapPoisonsInstance(t *testing.T) {
	main := &testFunction{
		def: testFuncDef{results: []api.ValueType{api.ValueTypeI32}},
		call: func(_ context.Context, stack []uint64) error {
			stack[0] = 0xd800 // invalid Unicode scalar value
			return nil
		},
	}
	mod := &testModule{funcs: map[string]api.Function{"run": main}}
	be := &boundExport{
		mod:      mod,
		funcName: "run",
		fd: binary.FuncDesc{Results: binary.FuncResults{Named: []binary.FuncResult{{
			Name: "value",
			Type: binary.TypeRef{Primitive: "char"},
		}}}},
	}
	finalizeBoundExport(be, nil, nil, nil, 0)
	in := &Instance{resources: newHandleTable()}
	if _, err := in.invoke(context.Background(), be, "run", nil); err == nil {
		t.Fatal("expected invalid guest result to trap")
	}
	if _, err := in.invoke(context.Background(), be, "later", nil); err == nil || !strings.Contains(err.Error(), "cannot enter component instance") {
		t.Fatalf("second call error = %v, want poisoned-instance error", err)
	}
}

func TestStaticResultShapeErrorDoesNotPoisonInstance(t *testing.T) {
	main := &testFunction{}
	mod := &testModule{funcs: map[string]api.Function{"run": main}}
	bad := &boundExport{
		mod:      mod,
		funcName: "run",
		fd: binary.FuncDesc{Results: binary.FuncResults{Named: []binary.FuncResult{{
			Name: "value",
			Type: binary.TypeRef{Primitive: "char"},
		}}}},
	}
	finalizeBoundExport(bad, nil, nil, nil, 0)
	in := &Instance{resources: newHandleTable()}
	if _, err := in.invoke(context.Background(), bad, "bad", nil); err == nil {
		t.Fatal("expected static result-shape error")
	}

	good := &boundExport{mod: mod, funcName: "run"}
	finalizeBoundExport(good, nil, nil, nil, 0)
	if _, err := in.invoke(context.Background(), good, "good", nil); err != nil {
		t.Fatalf("later valid call failed after static result error: %v", err)
	}
}

func TestCanonicalLiftOptionsRequireGraphPath(t *testing.T) {
	for _, kind := range []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07} {
		comp := &binary.Component{Canons: []binary.Canon{{Kind: binary.CanonKindLift, Opts: []binary.CanonOpt{{Kind: kind}}}}}
		if !needsGraphPath(comp) {
			t.Fatalf("canon lift option %#x did not require graph path", kind)
		}
	}
}

func TestImportKeysUseComponentModelVersionCompatibility(t *testing.T) {
	tests := []struct {
		a, b  string
		equal bool
	}{
		{"wasi:io/streams@0.2.3", "wasi:io/streams@0.2.12", true},
		{"wasi:io/streams@0.2.6-rc.1", "wasi:io/streams@0.2.12+build.7", true},
		{"wasi:io/streams@0.2.3", "wasi:io/streams@0.3.0", false},
		{"example:pkg/api@1.1.0", "example:pkg/api@1.9.7", true},
		{"example:pkg/api@1.1.0", "example:pkg/api@2.0.0", false},
		{"example:pkg/api@0.0.5", "example:pkg/api@0.0.6", false},
		{"example:pkg/api@not-semver", "example:pkg/api@also-bad", false},
		{"example:pkg/api@1.2.3-01", "example:pkg/api@1.9.0", false},
	}
	for _, tc := range tests {
		a := mkImportKey(tc.a, "run")
		b := mkImportKey(tc.b, "run")
		if got := a == b; got != tc.equal {
			t.Errorf("mkImportKey(%q) == mkImportKey(%q) is %v, want %v (%q vs %q)", tc.a, tc.b, got, tc.equal, a.iface, b.iface)
		}
	}
}

func TestDuplicateCompatibleImportRegistrationIsRejected(t *testing.T) {
	fn := func(context.Context, []abi.Value) ([]abi.Value, error) { return nil, nil }
	cfg := newConfig([]Option{
		WithImport("wasi:io/streams@0.2.3", "run", fn, nil, nil),
		WithImport("wasi:io/streams@0.2.12", "run", fn, nil, nil),
	})
	if cfg.registrationErr == nil {
		t.Fatal("compatible duplicate registration was accepted")
	}
}

func TestNonUTF8CanonicalEncodingIsRejected(t *testing.T) {
	comp := &binary.Component{Canons: []binary.Canon{{Kind: binary.CanonKindLift, Opts: []binary.CanonOpt{{Kind: 0x01}}}}}
	if err := validateCanonicalStringEncodings(comp); err == nil {
		t.Fatal("UTF-16 canonical option was accepted")
	}
}

func TestAsyncExecutionLeaseExcludesOtherCalls(t *testing.T) {
	in := &Instance{}
	if !in.claimAsyncExecution() {
		t.Fatal("first async execution lease was rejected")
	}
	if in.claimAsyncExecution() {
		t.Fatal("second async execution lease was accepted")
	}
	if in.syncCallMu.TryLock() {
		in.syncCallMu.Unlock()
		t.Fatal("synchronous execution overlapped async lease")
	}
	in.releaseAsyncExecution()
	if !in.syncCallMu.TryLock() {
		t.Fatal("execution lease remained held after release")
	}
	in.syncCallMu.Unlock()
}

func TestCallOfAsyncExportWaitsForExecutionLease(t *testing.T) {
	in := &Instance{mayEnter: true}
	if !in.claimAsyncExecution() {
		t.Fatal("could not claim async execution lease")
	}
	be := &boundExport{asyncCallback: true, funcName: "run"}
	done := make(chan error, 1)
	go func() {
		_, err := in.invoke(context.Background(), be, "run", nil)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Call returned while async lease was held: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	in.releaseAsyncExecution()
	if err := <-done; err == nil {
		t.Fatal("Call unexpectedly succeeded")
	}
}

func TestCallAsyncCompletionQueuesEvenBeforeHostCallbackReturns(t *testing.T) {
	in := &Instance{sched: &sched{}}
	p := &PendingCall{in: in, done: make(chan struct{})}
	in.amu.Lock()
	in.pending = p
	in.amu.Unlock()
	in.asyncActive.Store(true)
	st := newSubtask()
	st.resolveFn = func([]abi.Value) error {
		t.Fatal("completion applied off-driver")
		return nil
	}
	ac := &st.ac
	ac.in, ac.st = in, st
	ac.inCall.Store(true)
	if err := ac.Resolve(nil); err != nil {
		t.Fatal(err)
	}
	in.amu.Lock()
	queued := len(in.mailbox)
	in.amu.Unlock()
	if queued != 1 {
		t.Fatalf("queued completions = %d, want 1", queued)
	}
}

func TestLateAsyncCompletionReturnsClosedError(t *testing.T) {
	in := &Instance{sched: &sched{}}
	p := &PendingCall{in: in, done: make(chan struct{})}
	st := newSubtask()
	ac := &st.ac
	ac.in, ac.st = in, st
	p.calls = append(p.calls, ac)
	in.pending = p
	in.asyncActive.Store(true)
	in.finishAsync(p, nil, errCallAsyncCancelled)
	if err := ac.Resolve(nil); !errors.Is(err, errAsyncCallClosed) {
		t.Fatalf("late Resolve error = %v, want ErrCallClosed", err)
	}
}

func TestConcurrentAsyncResolvesAreOneShot(t *testing.T) {
	in := &Instance{sched: &sched{}}
	p := &PendingCall{in: in, done: make(chan struct{})}
	in.pending = p
	in.asyncActive.Store(true)
	st := newSubtask()
	ac := &st.ac
	ac.in, ac.st = in, st
	errs := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			errs <- ac.Resolve(nil)
		}()
	}
	close(start)
	var successes, failures int
	for range 2 {
		if err := <-errs; err != nil {
			failures++
		} else {
			successes++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("Resolve results: %d successes, %d failures; want 1 and 1", successes, failures)
	}
}

func TestPendingCallHasSingleDriver(t *testing.T) {
	in := &Instance{}
	p := &PendingCall{in: in, done: make(chan struct{})}
	if err := p.claimDriver(); err != nil {
		t.Fatal(err)
	}
	if err := p.claimDriver(); !errors.Is(err, errAsyncDriverActive) {
		t.Fatalf("second driver claim error = %v, want driver-active error", err)
	}
	p.releaseDriver()
	if err := p.claimDriver(); err != nil {
		t.Fatalf("driver claim after release: %v", err)
	}
	p.releaseDriver()
}

func TestStalePendingCallCannotClearNewAsyncCall(t *testing.T) {
	in := &Instance{}
	old := &PendingCall{in: in, done: make(chan struct{}), closed: true}
	close(old.done)
	current := &PendingCall{in: in, done: make(chan struct{})}
	in.pending = current
	in.asyncActive.Store(true)
	in.finishAsync(old, nil, errCallAsyncCancelled)
	if !in.asyncActive.Load() || in.pending != current {
		t.Fatal("stale finish changed the current async call")
	}
}

func TestCloseCancelsAsyncLeaseBeforeLocking(t *testing.T) {
	in := &Instance{resources: newHandleTable(), sched: &sched{}}
	if !in.claimAsyncExecution() {
		t.Fatal("could not claim async execution lease")
	}
	p := &PendingCall{in: in, done: make(chan struct{}), lease: true}
	in.pending = p
	in.asyncActive.Store(true)
	if err := in.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Fatal("Close did not cancel pending call")
	}
}

func TestSynchronousInvocationsAreSerialized(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var active, maximum atomic.Int32
	fn := &testFunction{call: func(context.Context, []uint64) error {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return nil
	}}
	mod := &testModule{funcs: map[string]api.Function{"run": fn}}
	be := &boundExport{mod: mod, funcName: "run"}
	finalizeBoundExport(be, nil, nil, nil, 0)
	in := &Instance{resources: newHandleTable()}
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := in.invoke(context.Background(), be, "run", nil); done <- err }()
	}
	<-started
	select {
	case <-started:
		t.Fatal("second synchronous call entered before the first returned")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	<-started
	release <- struct{}{}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent calls = %d, want 1", got)
	}
}

func TestHostImportErrorRestoresExecutionMarkers(t *testing.T) {
	wantErr := errors.New("host failed")
	in := &Instance{resources: newHandleTable()}
	hi := &hostImport{fn: func(context.Context, []abi.Value) ([]abi.Value, error) {
		return nil, wantErr
	}}
	fn, _, _, err := buildHostWrapper(in, "test:pkg/host", "fail", hi, in.resources, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertPanicsWith(t, wantErr.Error(), func() {
		fn.Call(context.Background(), &testModule{}, nil)
	})
	if got := in.inHostCall; got != 0 {
		t.Fatalf("inHostCall = %d after host error, want 0", got)
	}
}

func TestHostImportPanicRestoresExecutionMarkers(t *testing.T) {
	in := &Instance{resources: newHandleTable()}
	hi := &hostImport{fn: func(context.Context, []abi.Value) ([]abi.Value, error) {
		panic("host panicked")
	}}
	fn, _, _, err := buildHostWrapper(in, "test:pkg/host", "panic", hi, in.resources, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertPanicsWith(t, "host panicked", func() {
		fn.Call(context.Background(), &testModule{}, nil)
	})
	if got := in.inHostCall; got != 0 {
		t.Fatalf("inHostCall = %d after host panic, want 0", got)
	}
}

func TestAsyncHostImportErrorRestoresExecutionMarkers(t *testing.T) {
	wantErr := errors.New("async host failed")
	in := &Instance{resources: newHandleTable(), sched: &sched{}}
	var call *AsyncCall
	hi := &hostImport{asyncFn: func(_ context.Context, _ []abi.Value, ac *AsyncCall) error {
		call = ac
		return wantErr
	}}
	fn, _, _, err := buildAsyncHostWrapper(in, "test:pkg/host", "fail", hi, in.resources, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertPanicsWith(t, wantErr.Error(), func() {
		fn.Call(context.Background(), &testModule{}, make([]uint64, 1))
	})
	if got := in.inHostCall; got != 0 {
		t.Fatalf("inHostCall = %d after async host error, want 0", got)
	}
	if call == nil {
		t.Fatal("async host call was not invoked")
	}
	if call.inCall.Load() {
		t.Fatal("AsyncCall remained marked in-call after async host error")
	}
}

func TestAsyncHostImportPanicRestoresExecutionMarkers(t *testing.T) {
	in := &Instance{resources: newHandleTable(), sched: &sched{}}
	var call *AsyncCall
	hi := &hostImport{asyncFn: func(_ context.Context, _ []abi.Value, ac *AsyncCall) error {
		call = ac
		panic("async host panicked")
	}}
	fn, _, _, err := buildAsyncHostWrapper(in, "test:pkg/host", "panic", hi, in.resources, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertPanicsWith(t, "async host panicked", func() {
		fn.Call(context.Background(), &testModule{}, make([]uint64, 1))
	})
	if got := in.inHostCall; got != 0 {
		t.Fatalf("inHostCall = %d after async host panic, want 0", got)
	}
	if call == nil {
		t.Fatal("async host call was not invoked")
	}
	if call.inCall.Load() {
		t.Fatal("AsyncCall remained marked in-call after async host panic")
	}
}

func assertPanicsWith(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("did not panic; want panic containing %q", want)
		}
		if !strings.Contains(fmt.Sprint(got), want) {
			t.Fatalf("panic = %v, want text containing %q", got, want)
		}
	}()
	fn()
}

func TestHandleTableCloseDrainsHostResources(t *testing.T) {
	table := newHandleTable()
	var calls atomic.Int32
	wantErr := errors.New("dtor failed")
	table.registerHostDtor(7, func(context.Context, uint32) error {
		if calls.Add(1) == 1 {
			return wantErr
		}
		return nil
	})
	table.add(7, 10, true)
	table.add(7, 11, true)
	table.add(7, 12, false)
	if err := table.close(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("close error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("destructor calls = %d, want 2", got)
	}
	if err := table.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("destructor calls after repeated close = %d, want 2", got)
	}
}

func TestInstanceCloseDrainsResourcesBeforeModules(t *testing.T) {
	var order []string
	table := newHandleTable()
	table.registerHostDtor(1, func(context.Context, uint32) error {
		order = append(order, "resource")
		return nil
	})
	table.add(1, 9, true)
	in := &Instance{resources: table, closers: []api.Module{&testModule{close: func() { order = append(order, "module") }}}}
	if err := in.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "resource" || order[1] != "module" {
		t.Fatalf("close order = %v", order)
	}
}

func TestDropOwnedRunsTypedDestructorBeforeConsumingHandle(t *testing.T) {
	table := newHandleTable()
	wantErr := errors.New("destructor failed")
	fail := true
	table.registerDtor(7, func(context.Context, uint32) error {
		if fail {
			return wantErr
		}
		return nil
	})
	h := table.NewOwn(7, 42)
	if _, _, _, err := table.dropOwnedWithDtor(context.Background(), h); !errors.Is(err, wantErr) {
		t.Fatalf("drop error = %v, want destructor error", err)
	}
	if rep, err := table.Rep(7, h); err != nil || rep != 42 {
		t.Fatalf("handle after destructor error = (%d, %v), want live rep 42", rep, err)
	}
	fail = false
	typeIdx, rep, guestDtor, err := table.dropOwnedWithDtor(context.Background(), h)
	if err != nil || typeIdx != 7 || rep != 42 || !guestDtor {
		t.Fatalf("successful drop = (type %d, rep %d, guest %v, %v)", typeIdx, rep, guestDtor, err)
	}
	if _, err := table.Rep(7, h); err == nil {
		t.Fatal("handle remained live after successful destructor")
	}
}

func TestDropResourceGuestDestructorTrapPoisonsAndRetainsHandle(t *testing.T) {
	table := newHandleTable()
	table.registerDtor(7, func(context.Context, uint32) error { return errors.New("guest trap") })
	h := table.NewOwn(7, 42)
	in := &Instance{resources: table}
	if err := in.DropResource(context.Background(), "diagnostic:only/type", "resource", h); err == nil {
		t.Fatal("expected destructor trap")
	}
	if !in.poisoned.Load() {
		t.Fatal("guest destructor trap did not poison instance")
	}
	if rep, err := table.Rep(7, h); err != nil || rep != 42 {
		t.Fatalf("handle after trap = (%d, %v), want live rep 42", rep, err)
	}
}

func TestCompileCacheCloseIsTerminalAndClearsMetadata(t *testing.T) {
	cache := NewCompileCache()
	comp := &binary.Component{}
	cache.byComp["component"] = comp
	cache.byABI[comp] = map[uint32]*boundExportABI{0: {}}
	cache.byPlan[comp] = &graphPlan{}
	if err := cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cache.closed || len(cache.byComp) != 0 || len(cache.byABI) != 0 || len(cache.byPlan) != 0 {
		t.Fatalf("closed cache retained metadata: closed=%v comp=%d abi=%d plan=%d", cache.closed, len(cache.byComp), len(cache.byABI), len(cache.byPlan))
	}
	if _, err := cache.getOrDecode(nil); !errors.Is(err, errCompileCacheClosed) {
		t.Fatalf("getOrDecode after Close = %v, want closed error", err)
	}
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCompileCacheCloseCannotRaceMetadataRepopulation(t *testing.T) {
	cache := NewCompileCache()
	comp := &binary.Component{}
	start := make(chan struct{})
	done := make(chan struct{}, 32)
	for i := range 32 {
		go func(idx int) {
			<-start
			cache.abiFor(comp, uint32(idx), func() *boundExportABI { return &boundExportABI{} })
			done <- struct{}{}
		}(i)
	}
	close(start)
	if err := cache.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 32 {
		<-done
	}
	if len(cache.byABI) != 0 {
		t.Fatalf("closed cache was repopulated with %d ABI entries", len(cache.byABI))
	}
}

func TestHostStateFactoryIsPerConfig(t *testing.T) {
	type state struct{ n int }
	key := struct{}{}
	opt := WithHostStateFactory(key, func() any { return &state{} })
	a := newConfig([]Option{opt}).hostState[key].(*state)
	b := newConfig([]Option{opt}).hostState[key].(*state)
	if a == b {
		t.Fatal("factory returned shared state")
	}
}

func TestOptionsFactoryBuildsCoherentPerInstanceBundle(t *testing.T) {
	type state struct{ resources *handleTable }
	key := struct{}{}
	opt := WithOptionsFactory(func() []Option {
		s := &state{}
		return []Option{
			WithHostState(key, s),
			withResourcesHook(func(resources *handleTable) { s.resources = resources }),
		}
	})
	a := NewHarness([]Option{opt})
	b := NewHarness([]Option{opt})
	sa := a.cfg.hostState[key].(*state)
	sb := b.cfg.hostState[key].(*state)
	if sa == sb || sa.resources == sb.resources || sa.resources != a.resources || sb.resources != b.resources {
		t.Fatal("option factory did not keep fresh state and resource hooks coherent")
	}
}

func TestAsyncLowerAliasUsesNestedExportType(t *testing.T) {
	makeParent := func(async bool) *binary.Component {
		nested := &binary.Component{
			Types:              []binary.Type{{Descriptor: binary.FuncDesc{Async: async}}},
			TypeSpace:          []binary.TypeSpaceEntry{{Kind: binary.TypeSpaceDef, Def: 0}},
			Canons:             []binary.Canon{{Kind: binary.CanonKindLift, TypeIdx: 0}},
			ComponentFuncSpace: []binary.ComponentFuncSpaceEntry{{Kind: binary.ComponentFuncFromCanonLift, Canon: 0}},
			Exports:            []binary.Export{{Name: "f", ExternType: 0x01, ExternIndex: 0}},
		}
		return &binary.Component{
			NestedComponents:       []*binary.Component{nested},
			Instances:              []binary.Instance{{Kind: 0x00, ComponentIdx: 0}},
			ComponentInstanceSpace: []binary.ComponentInstanceSpaceEntry{{Kind: binary.ComponentInstanceFromDefinition, Instance: 0}},
			Aliases:                []binary.AliasDef{{Sort: 0x01, TargetKind: 0x00, InstanceIdx: 0, Name: "f"}},
			ComponentFuncSpace:     []binary.ComponentFuncSpaceEntry{{Kind: binary.ComponentFuncFromAlias, Alias: 0}},
			Canons:                 []binary.Canon{{Kind: binary.CanonKindLower, FuncIdx: 0, Opts: []binary.CanonOpt{{Kind: 0x06}}}},
		}
	}
	if err := validateAsyncCanonOptsAgreeWithTypes(makeParent(false)); err == nil {
		t.Fatal("sync alias accepted by async lower")
	}
	if err := validateAsyncCanonOptsAgreeWithTypes(makeParent(true)); err != nil {
		t.Fatalf("async alias rejected: %v", err)
	}
}

func TestUnavailableDeclaredResourceDestructorFails(t *testing.T) {
	err := resourceDtor(func() api.Function { return nil })(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v", err)
	}
}
