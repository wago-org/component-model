// Package component runs WebAssembly Component Model binaries through Wago.
//
// The package owns Component Model decoding, graph linking, Canonical ABI
// lift/lower, resources, and typed host imports. Core Wasm compilation and
// execution stay behind three reviewed Wago authorities. WASI and other world
// policy belong in plugins that consume Contract.
//
// A consuming plugin declares Contract in its PluginDefinition and acquires a
// typed reference during registration:
//
//	components, err := plugin.Require(reg, component.Contract)
//
// Calls stay inside both the contract lease and the component instance's
// lifetime:
//
//	err := components.With(func(service component.Service) error {
//		return service.WithInstance(ctx, componentWasm, func(in *component.Instance) error {
//			_, err := in.Call(ctx, "wasi:cli/run@0.2.3#run")
//			return err
//		})
//	})
//
// The service closes the instance before WithInstance returns. Neither the
// service nor the instance may be retained outside its callback.
package component

import (
	"github.com/wago-org/component-model/internal/abi"
	"github.com/wago-org/component-model/internal/instance"
)

// Instance is a live component instance. It is valid only during the
// Service.WithInstance callback that supplied it.
type Instance = instance.Instance

// PendingCall is a live CallAsync invocation, suspended awaiting external
// import completions. See Instance.CallAsync.
type PendingCall = instance.PendingCall

// Option configures one component instantiation.
type Option = instance.Option

// CompileCache amortizes component decoding and embedded core-module
// compilation across repeated Service.WithInstance calls. A cache belongs to
// one loaded Component Model provider and must be closed before that runtime.
type CompileCache = instance.CompileCache

// WithCompileCache reuses cache across component instantiations.
func WithCompileCache(cache *CompileCache) Option { return instance.WithCompileCache(cache) }

// NewCompileCache returns an empty compile cache. Close it before closing the
// Wago runtime that loaded the Component Model provider.
func NewCompileCache() *CompileCache { return instance.NewCompileCache() }

// Value is a component-level call value matching the Canonical ABI lifting of
// a WIT type.
type Value = abi.Value

// HostFunc implements a synchronous component import.
type HostFunc = instance.HostFunc

// AsyncHostFunc implements an async-lowered component import.
type AsyncHostFunc = instance.AsyncHostFunc

// AsyncCall is the completion handle supplied to an AsyncHostFunc.
type AsyncCall = instance.AsyncCall

// WithImport registers a synchronous component import.
func WithImport(iface, name string, fn HostFunc, params, results []TypeDesc) Option {
	return instance.WithImport(iface, name, fn, params, results)
}

// WithAsyncImport registers an async-lowered component import.
func WithAsyncImport(iface, name string, fn AsyncHostFunc, params, results []TypeDesc) Option {
	return instance.WithAsyncImport(iface, name, fn, params, results)
}
