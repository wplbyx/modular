# Layering and assembly

Read when designing module interfaces, wiring modules or resources, or auditing
dependencies. A Business Module owns a bounded context and its data writes. One
Application owns deployment, shared configuration, transports, and lifecycle.

## Two assembly levels

| Level | Responsibility | Inputs and outputs |
| --- | --- | --- |
| `cmd/<application>` | Outermost composition root: create shared Resources, connect modules in DAG order, mount entrypoints, and configure Application lifecycle | Application config and shared Providers in; assembled Application out |
| `modules/<module>/bootstrap.go` | Local composition root: construct module-owned adapters, use cases, and domain objects | Module Config and narrow Dependencies in; typed Module capabilities and mounting hooks out |

Bootstrap is assembly code, not business behavior. Its position at the module
root lets it import both `internal` use cases and `infrastructure` adapters.
Those packages do not import the module root. Business workflows, transaction
decisions, and failure recovery belong to the initiating use case, not either
composition root. Application itself only orchestrates lifecycle.

## Two interfaces

- **Business contract**: `modules/<provider>/contract` contains narrow public Go
  interfaces, Command/Query/Result values, and deliberately shared events.
  Siblings import only this contract, with a declared architecture DAG edge.
- **Assembly interface**: the module root exposes `Config`, `Dependencies`,
  `Module`, and `New(cfg Config, deps Dependencies) (*Module, error)` to cmd.
  Technical types such as `core.Provider[*gorm.DB]` are appropriate here.
  Exported parameters and results must be usable by cmd without importing
  module `internal` types. Expose business capabilities as contract interfaces;
  keep implementation fields private.

`Dependencies` contains only capabilities this module actually needs. Bootstrap
normally creates its private adapters; add an explicit replacement option only
when a real use case requires it. Modules do not accept an Application, a
service locator, or the entire application resource collection. A declared DAG
edge does not invent a contract method or constructor field: those need a known
use case.

Constructors connect objects and validate configuration. Repositories retain
Providers; constructing a module must not call `Value()` before Resource Setup,
start background tasks, execute migrations, or begin consuming messages. Return
necessary mounting hooks, migration callbacks, or lifecycle objects to cmd for
Application-managed execution. Shared Resources remain Application-owned.

The generated empty Module is only an assembly shell, not implemented business
behavior. Add fields and hooks only when needed; no universal module lifecycle
interface is required. Existing scaffold-once constructors remain user-owned.

## Module internals

- `internal/app`: use cases, private business inputs/results, and consumer-owned outbound ports, including
  repository, publisher, external-client, and use-case transaction interfaces.
- `internal/domain`: aggregates and policies when business invariants justify
  them; simple CRUD does not require an artificial domain model.
- Use cases implement public `contract` interfaces directly when needed; public
  Command/Query/Result types remain in `contract` and need not be duplicated.
- `infrastructure`: module-owned persistence, HTTP, and messaging adapters.
  Protocol DTOs and mappings live alongside their respective adapters.

Adapters may import their own module's internal ports. Siblings cannot import
the provider root, implementation, ORM models, or tables. Public contracts
should be understandable without the provider's persistence or transport
mechanisms. A cross-module ACID flow needs an explicit project-specific
transaction participation mechanism; calling contracts alone is not atomic.

## Interface design and review

Use [interface design](interface-design.md) when defining or reviewing business
intent, contract guarantees, request adaptation, strategies, or outbound ports.
It provides decision criteria, not a fixed interface taxonomy. This document
owns placement and assembly; review both assembly levels alongside the business
contracts and critical collaboration flows.

## Example: customer and order

The following excerpts assume real customer lookup and order placement use
cases. They illustrate assembly, not default generated business code; omitted
constructors and contracts belong to the application.

```go
// modules/order/bootstrap.go (imports omitted)
type Dependencies struct {
    DB        core.Provider[*gorm.DB]
    Customers customercontract.Reader
}

type Module struct {
    Placer contract.Placer
    http   *orderhttp.Handler
}

func New(cfg Config, deps Dependencies) (*Module, error) {
    repo := ordergorm.NewRepository(deps.DB) // retains Provider
    placer := app.NewPlacer(repo, deps.Customers)
    return &Module{
        Placer: placer,
        http:   orderhttp.NewHandler(placer),
    }, nil
}

func (m *Module) RegisterHTTP(routes *gin.RouterGroup) {
    m.http.Register(routes)
}
```

```go
// cmd/<application>/modules.go: inside application assembly
customers, err := customer.New(cfg.Customer, customer.Dependencies{DB: db})
if err != nil {
    return assembly, fmt.Errorf("assemble customer: %w", err)
}
orders, err := order.New(cfg.Order, order.Dependencies{
    DB: db, Customers: customers.Reader,
})
if err != nil {
    return assembly, fmt.Errorf("assemble order: %w", err)
}
// Collect registration callbacks here; mount them on the shared HTTP server.
// Application sets up DB before the server handles requests.
```

Changing order's internal repository wiring changes its bootstrap, not cmd.
Cmd knows module capabilities and their connections; order owns its use-case
sequence and consistency guarantees. Application owns when they start and stop.
