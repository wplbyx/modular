# Registry and discovery

Read when wiring Process discovery or extracting a module. Source:
`packages/core/node.go` and `packages/registry`.

## Identity

`core.ProcessIdentity` is immutable-by-construction Process metadata: Name,
Version, optional InstanceID, and copied Metadata. Business Modules do not own
runtime identities. `core.NewServiceNodeFromProcess(identity, transports...)`
creates the registration node and uses InstanceID when supplied; otherwise it
derives a stable ID from Process name plus transports.

Generated config maps these values from `Application.InstanceID` and
`Application.Metadata`. Set a unique InstanceID for each replica; transport-
derived IDs are suitable only when one instance owns a given address/port.

One node may publish HTTP and gRPC together. Always take transport values from
constructed servers because they pre-bind and may resolve `Port=0`.

## Registration

`registry.Registrar` registers/unregisters a `*core.ServiceNode`.
`registry.Discovery` gets and watches nodes by Process name. Application passes
the node through unchanged and requires a node when Registrar is configured.
It registers only after Endpoint readiness and unregisters before Endpoint
shutdown.

`wiring.Contribution.Registrar` is the scaffold-once composition seam. Leave it
nil for a local monolith. An extracted Process may return a configured Consul
Registrar without editing managed cmd code.

Consul writes one record per transport. Its record ID includes base node ID,
protocol, address, and port, preventing collisions between same-protocol
listeners. Health path is transport-specific. K8s implements Discovery only;
Deployment and Service resources own registration.

## gRPC targets

Register `registry.NewGRPCResolverBuilder(discovery)` and dial
`registry.BuildConsulTarget(processName)`, which returns
`consul:///processName`. The resolver publishes only `protocol == "grpc"`
addresses. Use `NewGRPCResolverBuilderWithScheme` for non-Consul schemes.

Local module wiring injects a generated Port implementation directly and uses
no Registrar. After extraction, the consumer owns a Remote Adapter around the
standard generated gRPC client and resolves the provider Process. Registrar,
timeouts, retries, and resilience policy are explicit Process concerns; moving
a module does not infer them from architecture configuration.
