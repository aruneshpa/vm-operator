# KubeVM workload replication and horizontal autoscaling

**Status**: Draft architecture · **Created**: 2026-10-22 · **Epic**: TBD — author requested drafting without an epic.

Read [design.md](design.md) for the proposal, [plan.md](plan.md) for implementation boundaries, [model.md](model.md) for proposed contracts, and [tasks.md](tasks.md) for gated work. This proposal is separate from [spec 012](../012-vm-horizontal-autoscaling/spec.md).

## Goals

Users declare portable template-based replica groups and one CPU/memory autoscaling policy per group. Providers supply equivalent lifecycle and measurement contracts; generic controllers own replication and scaling. A Deployment is optional future rollout functionality, not a prerequisite. Initial groups contain interchangeable, homogeneous, same-provider VMs in one namespace.

## Non-goals

Rolling deployments, stateful membership, scale from zero, cross-provider scheduling/migration, vertical or host scaling, external replica recommendations, arbitrary queries, and new customer integration implementations. No in-place migration of existing VM Operator ReplicaSets is specified.

## User stories and acceptance criteria

### US1 — Replicate portable VMs

- **AC01**: Setting desired count creates that many generic VMs, each with a distinct, mutually linked infrastructure object and correct UID-based ownership. Restart at either half of creation cannot duplicate platform capacity or adopt another workload's object.
- **AC02**: Decreasing count deletes generic VMs through core/provider cleanup. In-progress deletion remains visible and bounded; blocked cleanup cannot trigger unlimited replacements.
- **AC03**: Templates and selectors are immutable for a set revision. Only owned, matching children count; unrelated labeled VMs are not adopted. No platform VM belongs to both a generic VM and a provider ReplicaSet.

### US2 — Autoscale from measurements

- **AC04**: One autoscaler declares CPU and/or memory targets and bounds, computes the largest metric recommendation, and writes only the generic target replica count. Measurements cannot supply a desired count. No Deployment is needed.
- **AC05**: Providers are interchangeable at the contract boundary. Generic code contains no provider SDK or provider-type imports. The same behavioral tests run against two conformant providers.
- **AC06**: CPU and memory definitions, windows, capacity and identity are explicit. Unsupported or invalid measurements produce an observable hold, never a zero sample. Memory acceptance requires Q2 resolution.
- **AC07**: A valid complete metric may allow scale-out when another is unavailable; incomplete configured telemetry never allows scale-in. No valid telemetry holds count, including bound edits. Tolerance, limits and stabilization apply across restarts.
- **AC08**: Scaling waits for membership/provisioning/deletion convergence and explicit application readiness for the sample window. Infrastructure readiness alone is insufficient. Startup failure stops progress with a reason rather than increasing capacity indefinitely.

### US3 — Safe authority and retirement

- **AC09**: Same-namespace authorization, immutable target UID binding, one autoscaler claim, and optimistic concurrency prevent competing writers and recreated-target adoption. Parent-resource and scale-subresource updates are both protected.
- **AC10**: Scale-in requires explicit opt-in and supported retirement guarantees. Suspension freezes writes and keeps the claim; policy deletion releases it without deleting VMs or resetting count.

### US4 — Operate and evolve

- **AC11**: Generic controllers have one elected authority for each ownership scope, including clusters with multiple providers. Unsupported capability versions hold scaling while existing VM lifecycle continues.
- **AC12**: Disablement retains resources/CRDs and cleanup capability. Upgrade/rollback preserves counts, ownership and rate/stabilization protections; incompatible downgrade is prevented by documented lifecycle procedures.
- **AC13**: Collection, queues, caches and concurrency are bounded. A measured support envelope covers backend/API load, manager resources, failure recovery and impact on normal VM operations.
- **AC14**: Status reports observed generation, Ready and actionable blocked reasons with events/metrics sufficient to distinguish missing telemetry, unsupported provider, failed provisioning and failed retirement.

## Unresolved contracts

- **Q1** [NEEDS CLARIFICATION: Final generic set name, owning module/repository, template instantiation and UID-link contract, API version.]
- **Q2** [NEEDS CLARIFICATION: Portable memory semantic, per-provider evidence, clock skew tolerance, telemetry capability schema and qualified publication load.]
- **Q3** [NEEDS CLARIFICATION: Application-readiness contract and supported workload retirement integrations.]
- **Q4** [NEEDS CLARIFICATION: Supervisor packaging owner, compatible releases, generic-controller host registration and measured resource/support envelope.]

The proposed directions are detailed in design.md. They are not claims that these capabilities already exist. CPU foundations can proceed after their own contracts are settled; this does not constitute completion of the requested CPU-and-memory feature.
