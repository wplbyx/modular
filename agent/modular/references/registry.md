# Registry & Discovery

Service registration and discovery. Read when wiring service discovery or single-to-micro switch. Source: `packages/registry/`.

## Table of contents

- [ServiceNode](#servicenode)
- [Registrar and Discovery](#registrar-and-discovery)
- [Consul registry](#consul-registry)
- [K8s registry](#k8s-registry)
- [gRPC resolver](#grpc-resolver)

## ServiceNode

`core.ServiceNode` (`packages/core/node.go`): `Name`, `Version`, `ID` (auto-generated), `Transports []Transport`, `Metadata`. `core.Transport`: `Protocol`, `Address`, `Port`, `HealthPath`. One Application = one ServiceNode.

`core.NewServiceNode(name, version, transports...)` builds a node and auto-generates a deterministic `ID` from name + transports. `core.NormalizeHost(host)` turns `0.0.0.0`/`::`/empty into `127.0.0.1` and strips IPv6 brackets - use it when building transports for registration. `core.GenerateID(parts...)` is exposed for custom IDs.

A node may carry multiple transports (e.g. HTTP + gRPC) - this is how a single node publishes both protocols.

## Registrar and Discovery

Two interfaces in `packages/registry/adapter.go`:

- `Registrar`: `Register(ctx, *core.ServiceNode) error` / `Unregister(ctx, *core.ServiceNode) error`.
- `Discovery`: `GetService(ctx, serviceName) ([]*core.ServiceNode, error)` / `Watch(ctx, serviceName) (<-chan []*core.ServiceNode, error)`.

Application passes the node to the registrar verbatim - it does not transform or interpret it. Both are optional: pass them via `app.WithServiceNode(node)` and `app.WithRegistrar(reg)`. If either is nil, registration is skipped.

## Consul registry

`registry.NewConsulRegistry(addr string) (*Registry, error)` - implements BOTH `Registrar` and `Discovery`. Registers ONE Consul record per Transport: the ID gets a protocol suffix (`transportID(node.ID, t.Protocol)`). So a single node with HTTP+gRPC produces two Consul entries, both under `Name` = node.Name, tagged `version=...` and `protocol=...`, each with its own health check derived from `Transport.HealthPath`. Register, unregister, service lookup, and watch calls pass the caller context into the Consul SDK; watch backs off on errors and exits promptly on context cancellation.

## K8s registry

`packages/registry/registry_k8s.go` implements `Discovery` only. `Register`/`Unregister` are no-ops: in K8s, the Deployment+Service does registration; discovery reads via SharedInformerFactory. `Watch` removes its event handler on context cancellation before closing the channel. Endpoints without `TargetRef` are supported; node IDs fall back to `serviceName-IP-port`.

## gRPC resolver

`registry.NewGRPCResolverBuilder(discovery Discovery) resolver.Builder` - register it with `grpc/resolver` so `grpc.Dial` can resolve service names. The default scheme is `"consul"`; use `registry.NewGRPCResolverBuilderWithScheme("k8s", discovery)` for another discovery backend. It watches the discovery channel and updates addresses, filtering transports to `protocol == "grpc"` only.

Target format: build a target string so the resolver picks the right service. Use `registry.BuildTarget(scheme, serviceName)` or `registry.BuildConsulTarget(serviceName)`. `BuildConsulTarget("svc")` returns `consul:///svc` (the resolver reads `target.URL.Host` or `.Path` as the service name). Dialing that target with the resolver registered yields gRPC addresses for all `grpc` transports of that service.

## Wiring for single vs micro

- **Single topology**: no Registrar. Cross-domain gRPC clients dial `127.0.0.1:<port>` directly.
- **Micro topology**: each service registers via Consul (or relies on K8s Discovery). Cross-domain gRPC clients dial `consul:///<serviceName>` with `registry.NewGRPCResolverBuilder(consulRegistry)` registered, or a custom scheme from `NewGRPCResolverBuilderWithScheme`. Build the ServiceNode from config transports; the Registrar handles per-transport records automatically.
