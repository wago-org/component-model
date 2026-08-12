<div align="center">
  <h1><code>component-model</code></h1>
  <p>WebAssembly Component Model execution and Canonical ABI linking for Wago.</p>
</div>

<p align="center">
  <a href="https://github.com/wago-org/component-model/actions/workflows/ci.yml"><img src="https://github.com/wago-org/component-model/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg" alt="Go >= 1.22"></a>
</p>

`component-model` is Wago's optional Component Model runtime. It decodes
components, links their embedded core-module graph, implements Canonical ABI
lift and lower, and exposes typed component exports and host imports. Core-only
Wago programs do not link it.

The plugin ID is `github.com/wago-org/component-model`. It provides the typed
`github.com/wago-org/component-model/runtime` contract at major version 1.
There is no global registration or ambient runtime lookup.

The implementation currently covers Preview 2 component binaries, nested
composition, strings and composite WIT values, resources, typed host imports,
and experimental async tasks, futures, and streams. WASI policy stays in the
separate [`github.com/wago-org/wasi`](https://github.com/wago-org/wasi) plugin.

## Install

Add the plugin to a Wago project:

```sh
wago add github.com/wago-org/component-model
```

For Go development against its public types:

```sh
go get github.com/wago-org/component-model
```

The Wago installer resolves the full dependency and contract graph, then asks
the user to review the plugin's three exact authorities. The accepted versions,
definition digest, grants, scopes, and contract bindings live in
`wago-lock.json`.

Generated runtimes call `register.Providers()` explicitly. Importing the
package does not mutate a process-global registry:

```go
providers := component_register.Providers()
```

## Consume the service from another plugin

A component-world plugin declares both its package dependency and its contract
dependency. The package edge selects and versions the implementation. The
contract edge selects the typed API.

```go
var definition = wago.PluginDefinition{
    ID:          "github.com/acme/component-world",
    Name:        "Acme Component World",
    Version:     "0.1.0",
    Description: "Host policy for Acme components.",
    Stability:   wago.Experimental,
    Provenance: wago.PluginProvenance{
        Repository: "https://github.com/acme/component-world",
        License:    "Apache-2.0",
    },
    Requires: []wago.PluginRequirement{
        {ID: component.PluginID, Version: "^0.1.0"},
    },
    Consumes: []wago.ContractRequirement{
        {
            ID:    component.Contract.ID(),
            Major: component.Contract.Major(),
            Mode:  wago.ContractRequired,
        },
    },
}

type worldPlugin struct {
    components *plugin.Ref[component.Service]
}

func (p *worldPlugin) Register(reg *wago.Registrar) error {
    var err error
    p.components, err = plugin.Require(reg, component.Contract)
    return err
}
```

Calls use two nested callbacks. `Ref.With` leases the provider contract, and
`Service.WithInstance` owns one component instance through its callback:

```go
func (p *worldPlugin) run(ctx context.Context, wasm []byte) error {
    return p.components.With(func(components component.Service) error {
        return components.WithInstance(ctx, wasm, func(in *component.Instance) error {
            _, err := in.Call(ctx, "wasi:cli/run@0.2.3#run")
            return err
        })
    })
}
```

`WithInstance` closes the complete component graph before it returns. Do not
retain the service or instance outside its callback. During shutdown Wago
rejects new contract calls, waits for active callbacks and component cleanup,
then revokes the core handles. Consumers can still call the service from their
own `Stop` callback because Wago stops consumers before providers.

## Host imports

`WithImport` registers a synchronous host function with an explicit WIT
signature:

```go
echo := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
    return []component.Value{args[0]}, nil
}

stringType := component.PrimitiveDesc{Prim: "string"}
option := component.WithImport(
    "example:host/echo@1.0.0",
    "echo",
    echo,
    []component.TypeDesc{stringType},
    []component.TypeDesc{stringType},
)
```

Pass options after the callback:

```go
err := components.WithInstance(ctx, wasm, useInstance, option)
```

For nested WIT types, build a `TypeTable` and pass its `FuncDesc` and
`Resolver` to `WithImportCustom`. Resource-bearing interfaces use
`WithResourceTag`, `WithResourcesHook`, and `WithHostResourceDtor`. Async host
functions use `WithAsyncImport`, `Instance.CallAsync`, and `PendingCall`.

## Compile cache

`CompileCache` reuses component decoding and embedded core-module compilation
across repeated calls:

```go
cache := component.NewCompileCache()
defer cache.Close(context.Background())

err := components.WithInstance(
    ctx,
    wasm,
    useInstance,
    component.WithCompileCache(cache),
)
```

A cache belongs to one loaded Component Model provider. Close every instance
callback first, then close the cache, then close the Wago runtime.

## Authorities

The provider asks for three required authorities:

| Authority | Why it is needed |
| --- | --- |
| `core.module.compile` | Compile core Wasm modules embedded in a component. |
| `core.instance.instantiate` | Instantiate and own the core-module graph, within reviewed positive instance and memory limits. |
| `core.funcref.create` | Build typed host references for Canonical ABI bridges. |

These handles do not expose plugin registration, runtime policy, hooks, or
arbitrary runtime lifecycle control. A user may narrow the requested positive
instantiation limits. The published request allows 64 live core instances and
16 GiB of aggregate declared maximum memory across them. Memoryless linker
shims consume an instance slot but no memory budget. Components or concurrent
callbacks that exceed either reviewed limit fail closed. The plugin has no
configuration fields and rejects unknown configuration.

This authority model is an API boundary, not a sandbox for untrusted Go code.
Audit every plugin source and pin the exact release compiled into a host.

## Public surface

- `Service.WithInstance` scopes component execution and cleanup.
- `Instance.Call` and `Instance.CallExport` invoke typed component exports.
- `WithImport`, `WithImportCustom`, and `WithAsyncImport` define host imports.
- `TypeTable` and the descriptor types express WIT signatures.
- Resource options bind checked host resource tags, handles, and destructors.
- `CompileCache` reuses decode and JIT work for one provider lifetime.

Malformed component encodings, invalid type relationships, out-of-bounds
memory access, bad resource ownership transfers, missing imports, and
unsupported behavior return errors or named guest traps.

## Test

```sh
go test ./...
go test -race ./...
go vet ./...
```

The repository includes component decoder, Canonical ABI, composition,
resource, async, malformed-input, contract graph, strict-config, and shutdown
revocation tests. Test fixtures are checked in; `wasm-tools` is not required.

## License

Apache-2.0. See [NOTICE](./NOTICE) for provenance.
