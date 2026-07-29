# Modular Infrastructure

This context defines the identities and identifiers shared by modular applications.

## Language

**Service Node**:
A running application instance described by its service name, version, instance ID, and transports for registration and discovery.
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
