package component

import (
	"context"

	"github.com/wago-org/component-model/internal/instance"
)

// This file is what a host implementation outside wazy registers through.
// It is the same surface wazy's own WASI 0.2 implementation uses -- not a
// reduced one -- so any interface WASI can implement, an embedder can.

// HandleTable is one instance's resource handle table. Obtain it with
// WithResourcesHook; use it to mint own<T>/borrow<T> handles that sit nested
// inside a composite result, which the engine's automatic top-level handle
// translation does not reach.
type HandleTable = instance.HandleTable

// WithImportCustom registers fn as the host implementation of iface/name
// with a hand-built signature -- the general form of WithImport, and the only
// one that can express a nested composite. Build fd and resolve from a
// TypeTable:
//
//	tbl := component.NewTypeTable()
//	fd := tbl.Func([]component.TypeRef{component.Prim("string")},
//		tbl.Result(tbl.List(component.Prim("string")), component.Prim("u32")))
//	opt := component.WithImportCustom("acme:api/host@1.0.0", "lookup", fn, fd, tbl.Resolver())
//
// iface is matched with its "@x.y.z" version suffix stripped, so one
// registration serves every patch version of an interface.
func WithImportCustom(iface, name string, fn HostFunc, fd FuncDesc, resolve Resolver) Option {
	return instance.WithImportCustom(iface, name, fn, fd, resolve)
}

// WithResourceTag declares that the resource `name`, exported by the imported
// interface `iface`, is the one this host tags as `tag` when minting handles.
//
// Required for any resource-bearing interface: the guest drops handles through
// a canon carrying the component binary's own type index, while the host mints
// them under a tag of its choosing. Without this mapping the two numberings
// disagree and the first drop trips the handle table's cross-type check.
func WithResourceTag(iface, name string, tag uint32) Option {
	return instance.WithResourceTag(iface, name, tag)
}

// WithResourcesHook registers a callback run once per instantiation, with
// that instance's HandleTable, before any host func executes. This is how a
// host implementation gets the table it needs to mint nested handles.
func WithResourcesHook(hook func(*HandleTable)) Option {
	return instance.WithResourcesHook(hook)
}

// WithHostResourceDtor registers the Go destructor run when the guest drops an
// owned handle of the host resource tagged `tag`.
func WithHostResourceDtor(tag uint32, fn func(ctx context.Context, rep uint32) error) Option {
	return instance.WithHostResourceDtor(tag, fn)
}

// WithHostState attaches an opaque, intentionally shared value to every
// Instance built with this option. Use WithHostStateFactory for mutable state
// that must be isolated per instantiation.
//
// Use a package-private key type, not a bare string, so two independent host
// implementations cannot collide:
//
//	type myHostKey struct{}
//	component.WithHostStateFactory(myHostKey{}, func() any { return newMyHost() })
//	h := inst.HostState(myHostKey{}).(*myHost)
func WithHostState(key, value any) Option {
	return instance.WithHostState(key, value)
}

// WithHostStateFactory creates a fresh value for each instantiation.
func WithHostStateFactory(key any, newState func() any) Option {
	return instance.WithHostStateFactory(key, newState)
}

// WithOptionsFactory creates a fresh, coherent option bundle for each
// instantiation.
func WithOptionsFactory(newOptions func() []Option) Option {
	return instance.WithOptionsFactory(newOptions)
}

// InstanceExports is re-exported on Instance itself; see
// instance.Instance.InstanceExports. Listed here so the host-implementation
// surface is documented in one place: it is how a host finds an exported
// interface whose version suffix it does not know in advance.
