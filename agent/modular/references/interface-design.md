# Interface design

Read when designing or reviewing business capabilities, use-case inputs and
results, outbound ports, or variable business rules. These are decision criteria,
not mandatory interface categories, layers, or directories. Use
[layering](layering.md) for placement and the two assembly levels.

## Start with responsibility and intent

Identify the business intent, data owner, and invariants before choosing methods.
Design a capability that keeps its rules inside the owning module. A caller
should not need implementation knowledge to use it safely.

Separate operations when their permissions, state transitions, or success
guarantees differ. Changing an email and deactivating an account usually deserve
distinct operations; editing a nickname and biography may share one operation.
This does not prescribe action-based HTTP paths or prohibit ordinary updates.

A use case expresses a complete intent, not necessarily one ACID transaction.
Distinguish accepting work, committing local state, and completing the final
business effect. For example, a successful refund request may mean a durable
request exists, while settlement remains pending.

Keep internal use cases private; expose capabilities through `contract` when
siblings need them. A use case need not have a dedicated Go interface. Prefer
functions or concrete types when they express the behavior without an unnecessary
indirection; extract interfaces for public contracts or real dependency seams.

### Example: reserve inventory

Exposing `GetAvailable` and `SetAvailable` makes callers own the read/modify/write
race. An illustrative business capability instead looks like:

```go
type Reserver interface {
	Reserve(ctx context.Context, cmd ReserveCommand) (Reservation, error)
}
```

Define whether success means a committed reservation, how concurrent requests
avoid overselling, and whether retries can duplicate reservations. If idempotency
is promised, define its key scope, conflicting payload behavior, and retention
period. These are project decisions, not defaults inferred from the method name.

## Keep request adaptation separate from business protection

Inbound adapters parse protocols, validate representation, authenticate identity,
and explicitly map request DTOs to application inputs. Request DTOs do not use
ORM models. Business contracts must not require transport or persistence
knowledge; do not copy identical types at every internal call merely to create
another layer.

Use cases enforce business authorization and preconditions across their supported
entrypoints. Domain objects or policies may express the rules. Admission checks
in transport middleware do not replace authorization over a particular resource.

### Example: cancel an order

- The HTTP adapter validates the order ID and obtains a trusted actor identity.
- The use case checks whether that actor may cancel this order.
- The order's rule determines whether its current state permits cancellation.

A message or scheduled-task adapter supplies its own trusted caller context and
enters the same business protection. An actor ID claimed in an untrusted request
is not proof of identity. State-dependent checks and the resulting write must
preserve the invariant under concurrency, not merely pass an earlier check.

## Extract strategies for actual variation

Stable branches can remain ordinary code. Extract a strategy when rules genuinely
vary, need replacement, or compose independently. If bootstrap can select a
policy from configuration, inject that policy directly; every policy need not
implement `Supports`.

When selection depends on each business request, keep selection in business code.
Specify what happens when multiple policies match, none matches, or policies
combine. Make priority and composition order explicit instead of relying on
registration order. A strategy can itself be pure domain computation; these
roles do not require separate layers.

## Define outbound needs and delivery guarantees

The consuming use case defines the narrow outbound capability it needs, rather
than reproducing a third-party SDK or anticipating every possible backend.
An adapter is replaceable only if it preserves the port's behavior, including
errors, consistency, and side effects. Changing databases or payment providers
may require revisiting those guarantees.

For event publication, specify whether success means acceptance into memory,
durable storage, or acknowledgement by a broker. `Publish` alone does not ensure
eventual consistency. Reliable workflows may need a transactionally linked
Outbox, retries, idempotent consumption, and recovery; choose these from actual
delivery requirements. Modular's local EventBus remains best-effort.

## Prefer deterministic computation for pure rules

Calculations without I/O can use functions, value objects, or concrete domain
services. Make facts affecting the answer explicit where practical: time,
exchange rates, random values, and relevant configuration. Introduce a strategy
interface only when the calculation really needs interchangeable behavior.

### Example: currency conversion

```text
Use case gets an exchange-rate quote through an outbound port
    -> supplies amount, rate, and rounding rule to a pure calculation
    -> persists or returns the result
```

The use case can check quote freshness; the calculation can be tested against
exact rounding cases without a clock or network. A currency conversion operation
does not itself require a `CurrencyConverter` interface.

## Contract evidence and review

Document inputs, outputs, success guarantees, errors, and side effects with the
Go contract. Add idempotency, concurrency, cancellation, transaction, and event
timing semantics when they affect safe use. A cancellation or timeout does not
by itself prove that no effect occurred. Keep simple operations concise rather
than filling a universal contract template.

Review these questions:

1. Must a caller read the implementation to use the capability safely?
2. Does the interface leak an internal mechanism or transfer its rules to callers?
3. Are guarantees and failure handling clear when multiple capabilities compose?
4. Do tests establish promised behavior, rather than only mock invocation order?

Use public-entrypoint behavior tests and real adapter integration tests for
guarantees mocks cannot establish. Doctor/build and test presence establish
structural properties, not business correctness. Review high-risk implementation
details where needed; interface signatures alone do not contain every risk.
