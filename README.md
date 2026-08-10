<div align="center">
    <h1><code>component-model</code></h1>
    <p>A capability-gated WebAssembly Component Model plugin for the <a href="https://github.com/wago-org/wago">Wago</a> runtime.</p>
</div>

<p align="center">
    <a href="https://github.com/wago-org/component-model/actions/workflows/ci.yml"><img src="https://github.com/wago-org/component-model/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="https://go.dev/"><img src="https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg" alt="Go >= 1.22"></a>
    <a href="https://github.com/wago-org/wago"><img src="https://img.shields.io/badge/wago-%3E%3D0.1.0-6E56CF.svg" alt="Wago >= 0.1.0"></a>
</p>

<details>
<summary>Table of Contents</summary>

- [Overview](#overview)
- [Installation](#installation)
- [Concepts](#concepts)
- [Usage](#usage)
  - [Running a component](#running-a-component)
  - [Providing host imports](#providing-host-imports)
  - [Depending on the runtime service](#depending-on-the-runtime-service)
- [API](#api)
  - [Runtime and instances](#runtime-and-instances)
  - [WIT types and host imports](#wit-types-and-host-imports)
  - [Resources](#resources)
  - [Async calls](#async-calls)
  - [Compile cache](#compile-cache)
- [Security](#security)
- [Compatibility](#compatibility)
- [Testing](#testing)
- [Architecture](#architecture)
- [Contributing](#contributing)
- [License](#license)
- [Contact](#contact)

</details>

## Overview

`component-model` is an optional [Wago](https://github.com/wago-org/wago) plugin for
decoding, linking, and executing WebAssembly Components. Core Wago remains a compact
core-Wasm runtime; applications that never select this plugin do not retain the
component implementation in their Go dependency graph.

What you get out of the box:

- **Component execution**: decode real component binaries, compile their embedded core
  modules, resolve nested instantiation graphs, and call typed exports.
- **Canonical ABI**: lift and lower primitive and composite WIT values, strings, lists,
  variants, results, options, flags, resources, streams, and futures.
- **Typed host linking**: implement component imports with Go functions and an explicit
  WIT type vocabulary.
- **Safe composition**: component-world plugins such as WASI consume a versioned service
  instead of receiving core-runtime authority themselves.
- **Explicit lifecycle**: component instances, resources, async calls, compile caches,
  and the underlying engine handle have defined ownership and shutdown paths.

The plugin ID is `wago-org/component-model`. It publishes the typed service
`wago-org/component-model/runtime/v1`.

> **Stability:** experimental (`v0.0.0`). The API and supported Component Model surface
> may change without notice.

## Installation

If you have the [`wago`](https://github.com/wago-org/wago) CLI installed:

```sh
wago pkg install github.com/wago-org/component-model
```

or use [`go get`](https://pkg.go.dev/cmd/go#hdr-Get_packages_and_dependencies):

```sh
go get github.com/wago-org/component-model
```

Select the plugin in your project's `wago.json`:

```json
{
  "$schema": "https://wago.sh/v0/schema.json",
  "plugins": {
    "wago-org/component-model": "^0.0.0"
  }
}
```

Component execution requires one privileged plugin capability: `runtime.core`. Record
the reviewed authority and exact version in `wago-lock.json`:

```json
{
  "plugins": {
    "wago-org/component-model": {
      "version": "0.0.0",
      "requiredCapabilities": ["runtime.core"],
      "capabilities": {
        "runtime.core": true
      }
    }
  }
}
```

Generated Wago hosts blank-import the conventional registration package:

```go
import _ "github.com/wago-org/component-model/register"
```

## Concepts

| Term | Meaning |
| --- | --- |
| **Plugin** | The `component.Extension` installed under `wago-org/component-model`. It receives the narrow core-engine capability and provides one service. |
| **Service** | `component.RuntimeService`, the typed `wago-org/component-model/runtime/v1` dependency used by WASI and other component-world plugins. |
| **Runtime** | A `*component.Runtime` bound to one Wago runtime. It instantiates components but cannot register plugins, inspect policy, or control the Wago runtime lifecycle. |
| **Instance** | A live component graph with typed exports, embedded core instances, resources, and explicit `Close`. |
| **Option** | Host imports, WIT signatures, resource mappings, host state, or a compile cache supplied to one instantiation. |

The ownership chain is **Wago runtime → component service → component instance →
resources and calls**. Closing the Wago runtime revokes the engine capability and typed
service references.

## Usage

### Running a component

`Enable` installs the registered plugin with its required engine grant and returns its
runtime-scoped service:

```go
package main

import (
	"context"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
)

func run(ctx context.Context, componentBytes []byte) error {
	rt := wago.NewRuntime()
	defer rt.Close()

	components, err := component.Enable(rt)
	if err != nil {
		return err
	}
	instance, err := components.Instantiate(ctx, componentBytes)
	if err != nil {
		return err
	}
	defer instance.Close(ctx)

	_, err = instance.Call(ctx, "example:app/run#run")
	return err
}
```

World plugins supply the imports a component expects. For example, the separate
[`wago-org/wasi`](https://github.com/wago-org/wasi) plugin owns WASI filesystem,
network, clocks, random, environment, and process policy; this repository grants none
of that authority.

### Providing host imports

`WithImport` covers primitive and top-level composite signatures. A host function
receives lifted Go values and returns lifted values; returning a Go error traps the
guest call.

```go
echo := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
	return []component.Value{args[0]}, nil
}

stringType := component.PrimitiveDesc{Prim: "string"}
instance, err := components.Instantiate(ctx, componentBytes,
	component.WithImport(
		"example:host/echo@1.0.0",
		"echo",
		echo,
		[]component.TypeDesc{stringType},
		[]component.TypeDesc{stringType},
	),
)
```

For nested WIT types, build one `TypeTable` and pass its `FuncDesc` and `Resolver`
together:

```go
types := component.NewTypeTable()
result := types.Result(types.List(component.Prim("string")), component.Prim("u32"))
signature := types.Func([]component.TypeRef{component.Prim("string")}, result)

option := component.WithImportCustom(
	"example:host/catalog@1.0.0",
	"lookup",
	lookup,
	signature,
	types.Resolver(),
)
```

Interface matching ignores the patch component of an `@x.y.z` version, while the
Canonical ABI signature is still checked structurally.

### Depending on the runtime service

Plugins that provide component worlds should require `RuntimeService`; they should not
request `runtime.core` themselves:

```go
type Extension struct {
	components *plugin.Ref[component.Service]
}

func (e *Extension) Register(reg *wago.Registry) (err error) {
	e.components, err = plugin.Require(reg, component.RuntimeService)
	return err
}

func (e *Extension) instantiate(ctx context.Context, wasm []byte, opts ...component.Option) (*component.Instance, error) {
	components, err := e.components.Get()
	if err != nil {
		return nil, err
	}
	return components.Instantiate(ctx, wasm, opts...)
}
```

Manifest loading orders the provider before consumers automatically. Programmatic hosts
install the provider first; service resolution is type-checked and transactional.

## API

### Runtime and instances

- `Enable(rt)` installs the plugin and returns its `*Runtime`.
- `FromRuntime(rt)` resolves an already-installed service and fails if it is absent or
  invalid.
- `Runtime.Instantiate` decodes, compiles, links, and instantiates a component.
- `Instance.Call` invokes a top-level export by name.
- `Instance.CallExport` invokes a member of an exported component instance.
- `Instance.Close` releases the component graph and its retained resources.

Call arguments and results use the Canonical ABI's Go shapes: integer and floating-point
scalars, `string`, `[]byte`, `[]component.Value`, `VariantValue`, `ResultValue`, and
`uint32` resource representations.

### WIT types and host imports

`PrimitiveDesc`, `RecordDesc`, `VariantDesc`, `ListDesc`, `TupleDesc`, `FlagsDesc`,
`EnumDesc`, `OptionDesc`, `ResultDesc`, `OwnDesc`, `BorrowDesc`, `StreamDesc`, and
`FutureDesc` form the public WIT type vocabulary. `TypeTable` provides constructors for
nested signatures and keeps their type references paired with the correct resolver.

Use `WithImport` for ordinary signatures and `WithImportCustom` when a function contains
nested composites.

### Resources

Resource-bearing host interfaces use explicit tags:

- `WithResourceTag` maps an imported WIT resource to a host tag.
- `WithResourcesHook` exposes the instance's checked handle table to the host.
- `WithHostResourceDtor` registers cleanup for owned host representations.
- `Instance.DropResource` explicitly drops a guest-visible resource handle.

Tags and handles are instance-scoped. Cross-type, stale, and invalid handle operations
fail instead of resolving to an unrelated host representation.

### Async calls

`WithAsyncImport` registers an async-lowered import. `Instance.CallAsync` starts an
export and returns a `PendingCall`; the host completes imports through `AsyncCall`, then
awaits or cancels the export through `PendingCall.Await` or `PendingCall.Cancel`.

The async API is experimental. Call completion, cancellation, streams, futures, and
waitables remain bounded by the component instance lifecycle.

### Compile cache

`CompileCache` reuses compiled embedded core modules across repeated instantiations of
the same component bytes:

```go
cache := component.NewCompileCache()
defer cache.Close(ctx)

instance, err := components.Instantiate(ctx, componentBytes,
	component.WithCompileCache(cache),
)
```

A cache belongs to exactly one Wago runtime. It is safe for concurrent use, but must be
closed after every instance using it has closed.

## Security

- **Narrow authority**: `runtime.core` exposes only compilation, instantiation, and host
  function references. It does not expose `*wago.Runtime`, extension registration,
  policy, hooks, or arbitrary lifecycle control.
- **Revocable access**: the core-engine handle is inactive before transactional commit
  and revoked during runtime shutdown.
- **Typed composition**: missing services, duplicate providers, type mismatches, and
  ungranted capabilities reject plugin activation before runtime mutation.
- **Checked guest data**: component decoding, Canonical ABI layout, guest memory ranges,
  resource handles, and lifted value shapes are validated.
- **No ambient host authority**: this plugin does not expose files, sockets, environment,
  clocks, randomness, or process control. World plugins must receive those capabilities
  explicitly.
- **Explicit ownership**: instances, resources, pending calls, and compile caches have
  close or cancellation paths; service references fail closed after shutdown.

The plugin runs as compiled Go code in the host process. Wago's plugin capability model
limits access through its APIs; it is not a sandbox for untrusted Go source. Audit and
pin every plugin compiled into a host.

## Compatibility

| Axis | Support |
| --- | --- |
| Wago engine | `>= 0.1.0`; the current development module pins the reviewed Wago revision in `go.mod`. |
| Go toolchain | `>= 1.22` |
| Plugin ID | `wago-org/component-model` |
| Service API | `wago-org/component-model/runtime/v1` |
| Stability | Experimental |

Identity and catalog metadata live in [`wago.json`](./wago.json). Exact dependency
versions and reviewed capability grants belong in the consuming project's
`wago-lock.json`.

## Testing

```sh
go test ./...
go test -race ./...
go vet ./...
```

The checked-in suite is self-contained and does not require `wasm-tools` at test time.
It covers component decoding, Canonical ABI layout and values, typed host imports,
nested composition, resources, async builtins, compile-cache reuse, capability denial,
typed service resolution, and shutdown revocation.

## Architecture

- **`plugin.go`** — plugin identity, capability request, versioned service, and the
  runtime-scoped execution facade.
- **`component.go`**, **`host.go`**, **`types.go`**, **`typetable.go`** — public component
  instances, host-linking options, WIT types, values, and signature construction.
- **`internal/binary/`** — component binary decoding and semantic type/instantiation
  graphs.
- **`internal/abi/`** — Canonical ABI layout plus value lift/lower and memory access.
- **`internal/engine/`** — adapter from the narrow Wago core-engine interface.
- **`internal/instance/`** — graph instantiation, calls, composition, resources, async
  tasks, streams, futures, and lifecycle ownership.
- **`register/`** — blank-import shim for Wago-generated plugin hosts.
- **`wago.json`** — plugin catalog metadata.

The deep boundary is the versioned `Service`: Wago owns core-Wasm execution, this plugin
owns the Component Model and Canonical ABI, and world plugins own guest-visible host
policy. Keeping those layers separate preserves dead-code elimination and prevents a
WASI or application-world plugin from inheriting runtime authority.

## Contributing

Contributions are welcome! Please:

- Run `go test -race ./...` and `go vet ./...` before opening a pull request.
- Add focused decoder, ABI, lifecycle, and fail-closed tests for new Component Model
  behavior.
- Keep world policy out of this repository; WASI and application-specific interfaces
  belong in plugins that consume `RuntimeService`.
- Do not widen `runtime.core` or bypass the typed service boundary for convenience.
- Follow standard Go formatting (`gofmt`) and conventional commit messages.

## License

This project is distributed under the [Apache License 2.0](./LICENSE). See
[`NOTICE`](./NOTICE) for the provenance of the original decoder and Canonical ABI work.
Work on this project is done out of passion—if you want to support it financially, you
can donate through [GitHub Sponsors](https://github.com/sponsors/JairusSW).

## Contact

Please file issues at [GitHub Issues](https://github.com/wago-org/component-model/issues).
To chat, join the [Wago Discord](https://wago.sh/discord).

- **GitHub:** [https://github.com/wago-org/](https://github.com/wago-org/)
- **Website:** [https://wago.sh/](https://wago.sh/)
- **Discord:** [https://wago.sh/discord](https://wago.sh/discord)
