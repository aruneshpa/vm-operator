# KubeVM autoscaling implementation plan

**Status**: Draft architecture, dependent contracts unresolved. **Epic**: TBD.

The full architectural rationale is [design.md](design.md). Implement against [spec.md](spec.md) after its gates are resolved, using [tasks.md](tasks.md).

## Modules and boundaries

The inspected bootstrap hosts first-party KubeVM under `external/kubevm` with separate API and controller modules; its README anticipates a repository move. These are source landmarks, not authorization to edit vendored external APIs in this checkout. Q1 identifies the actual owning source repository before implementation. Keep generic APIs at their existing low dependency floor; generic controller code must not import the VM Operator root module.

Proposed generic package responsibilities are `api/<version>/` for set/autoscaler types, `pkg/workload/` for pair instantiation/membership, `pkg/autoscaling/` for pure decisions and metric validation, `internal/contract/` for versioned provider observations, and thin set/autoscaler controller packages beside the existing generic VM controller. Final paths follow the owning module's rules. Extend the generic VM controller only where UID binding, readiness projection and cleanup require it.

VM Operator integration stays in its KubeVM linkage/admission packages, provider telemetry under `pkg/providers/vsphere/`, and manager/config/packaging registration. Do not put generic group controllers directly under root `controllers/` or provider calls in generic core. VM Operator additions follow JoinContext, feature flags through pkgcfg, deferred patch/named return, observedGeneration and Ready conventions; generic packages use their own injected config without a reverse dependency.

## Delivery sequence

1. Settle Q1 naming, ownership, template capability and crash-safe pair protocol. Define CRDs, schema/admission, conversion strategy and `/scale` mappings together.
2. Implement generic set membership and bounded pair creation/deletion, including UID collision protection, partial-create recovery, expectations and finalizer behavior. Ship its E2E coverage with observable behavior.
3. Settle CPU telemetry and application readiness contracts; add optional capabilities to providers. Qualify memory semantics before adding memory support. Extend vSphere measurement/adaptation only inside its provider boundary.
4. Implement the provider-neutral sample reader and bounded watch/cache lifecycle. Build pure policy logic with injected clock, then the thin autoscaler controller and concurrency-safe target claim/write admission.
5. Register the generic controllers once in the existing embedding manager, gated off by default. Add least-privilege GVK-specific RBAC and installer lifecycle support. Benchmark the proposed status transport before enabling at production scale.
6. Prove two-provider conformance, Supervisor behavior, upgrade/disable/rollback, resource budgets and workload retirement guarantees. Record support limits before enablement.

## Security and lifecycle

Generic set creation must not become a confused deputy for provider object creation. Admission requires caller permission for the generic set and approved provider templates/namespace references; provider webhooks enforce tenant-specific access to networks, images, secrets and classes. The generic controller's service account gets only supported namespaced GVK permissions, not wildcard unstructured creation. UID-bound mutual links and owner checks reject preexisting/unrelated objects. Provider credentials never enter generic API/status or generic telemetry code.

Feature disablement prevents new scaling/creation while retaining deletion processing and CRDs. The installer must not reuse a feature-toggle path that deletes workload CRDs. Rollback requires compatibility with persisted APIs and finalizers; preserve conservative protection windows on history loss.

## Verification

Pure tests cover normalization, ratio rounding, maxima, invalid telemetry, window coverage, stabilization and restart protection with an injected clock. Envtest covers schemas, admission, RBAC/SAR behavior, two racing claims, main-resource versus `/scale` races, lost create responses, restart between pair halves, name reuse, deletion/create races and finalizer stalls. Follow owning-module test conventions; VM Operator tests follow repository memory rules.

Every observable slice includes E2E. Provider conformance uses vSphere plus a fake contract provider for deterministic failure injection and at least a second real provider implementation for portability qualification; a fake alone cannot prove memory equivalence. Functional E2E verifies application readiness/cleanup rather than treating InfrastructureReady as sufficient. Load tests measure API writes and storage, manager RSS/CPU, queues and backend calls at agreed Q4 loads, including 10,000 VMs only if chosen as a qualification target. No support scale is inferred from the arithmetic example.

No implementation or runtime verification is performed by this design change. No constitutional exception is proposed; unresolved ticket references remain draft placeholders until merge readiness.
