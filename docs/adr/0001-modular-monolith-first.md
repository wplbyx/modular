# Prefer an extractable modular monolith

modular v0.3 treats a Business Module as a static code boundary and a Process as the deployment boundary. The default is one Process with shared infrastructure and direct, protobuf-shaped Go ports; moving a module to another Process is an explicit extraction that replaces local adapters, removes declared blockers, and adds distributed consistency. This rejects configuration-only monolith-to-microservice switching because it makes every local change pay network, transaction, and reliability costs before extraction is justified.
