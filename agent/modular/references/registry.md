# Registry and discovery

Read when registering the Application or calling an external gRPC service. Source:
`packages/core/node.go` and `packages/registry`.

## Identity

`core.ProcessIdentity` is immutable-by-construction runtime metadata: Name,
Version, optional InstanceID, and copied Metadata. Business Modules do not own
runtime identities. `core.NewServiceNodeFromProcess(identity, transports...)`
creates the registration node and uses InstanceID when supplied; otherwise it
derives a stable ID from the Application name plus transports.

Generated config maps these values from `Application.InstanceID` and
`Application.Metadata`. Set a unique InstanceID for each replica; transport-
derived IDs are suitable only when one instance owns a given address/port.

One node may publish HTTP and gRPC together. Always take transport values from
constructed servers because they pre-bind and may resolve `Port=0`.

## Registration

`registry.Registrar` registers/unregisters a `*core.ServiceNode`.
`registry.Discovery` gets and watches nodes by registered name. Application passes
the node through unchanged and requires a node when Registrar is configured.
It registers only after Endpoint readiness and unregisters before Endpoint
shutdown.

Registrar construction belongs in `wireModules` or another user-owned section
of `cmd/<application>`. Leave `moduleAssembly.Registrar` nil when registration
is unnecessary; when configured, main passes both it and the ServiceNode to
Application without changing library lifecycle code.

Consul writes one record per transport. Its record ID includes base node ID,
protocol, address, and port, preventing collisions between same-protocol
listeners. Health path is transport-specific. K8s implements Discovery only;
Deployment and Service resources own registration.

## gRPC targets

Register `registry.NewGRPCResolverBuilder(discovery)` and dial
`registry.BuildConsulTarget(serviceName)`, which returns
`consul:///serviceName`. The resolver publishes only `protocol == "grpc"`
addresses. Use `NewGRPCResolverBuilderWithScheme` for non-Consul schemes.

Module-to-module calls use local Go contracts and do not use discovery. RPC
clients and their standard generated stubs are adapters for external systems;
timeouts, retries, and resilience policy are explicit Application concerns.
