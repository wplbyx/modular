# Modular Applications

This context defines the business and deployment identities used by modular applications.

## Language

**Business Module**:
A static code boundary that owns a cohesive set of business capabilities, rules, and data writes. It is independent of deployment topology.
_Avoid_: Service, svc, process

**Process**:
A deployable runtime that hosts one or more Business Modules and owns one Application lifecycle and one Service Node.
_Avoid_: Business Module, service

**Module Contract**:
The stable protobuf-shaped capability a Business Module exposes to another module. Local and remote adapters implement the same transport-independent Go port.
_Avoid_: HTTP handler, repository interface

**Surface**:
An external HTTP, gRPC, or event projection contributed by a Business Module to its hosting Process.
_Avoid_: Business Module, service identity

**Extraction Blocker**:
A declared dependency, such as a shared transaction or shared data write, that must be removed before a Business Module can move to another Process.
_Avoid_: Technical debt

**Local Notification**:
A best-effort, process-local notification that may be lost and is not a deployment contract.
_Avoid_: Integration Event

**Integration Event**:
A stable, durable fact exchanged across Process boundaries with retry and idempotency semantics.
_Avoid_: Domain Event, Local Notification

**Service Node**:
A running Process instance described by its name, version, instance ID, and transports for registration and discovery.
_Avoid_: Service identity, endpoint identity

**Business ID**:
An opaque identifier assigned to a business record or event; its encoding is not part of the business contract.
_Avoid_: Identity, Snowflake ID

**Request ID**:
The correlation identifier of one inbound request and its propagated work.
_Avoid_: Business ID, trace ID

**Snowflake Node ID**:
The leased numeric partition used by one Snowflake generator to prevent collisions with concurrent generators.
_Avoid_: Service node ID, instance identity, worker identity
