# Proposed KubeVM API and provider contracts

**Status**: Draft, not a served schema. **Epic**: TBD. Q1–Q3 in [spec.md](spec.md) gate final types and manifests.

## Generic ReplicaSet (working name)

Proposed group `kube-vm.io`; choose the served version under Q1. The roadmap's `VirtualMachineSet` and this proposal's `VirtualMachineReplicaSet` are one proposed kind, not two APIs.

| Field | Contract |
|---|---|
| `spec.replicas` | Nonnegative integer; `/scale` maps here. Autoscaling requires minimum one. |
| `spec.selector` | Immutable, matches template labels; controller ownership determines membership. |
| `spec.template` | Immutable portable VM template excluding a concrete infrastructureRef. Includes declared application-readiness requirements. |
| `spec.infrastructureTemplateRef` | Immutable same-namespace provider template identity, UID and supported instantiation contract version. |
| `status.replicas` | Observed non-terminating owned generic VMs, including provisioning members. |
| `status.readyReplicas` | Members meeting declared application readiness and lifecycle prerequisites. |
| `status.terminatingReplicas` | Outstanding deletion work; not silently counted as available capacity. |
| `status.selector` | Serialized selector for Scale status. |
| `status.observedGeneration`, conditions | Current reconciliation generation and Ready/blocked reasons. |

Reject template payloads that include concrete instance IDs, copied owner references/status, or an infrastructureRef to a shared live object. Generic admission validates portable structure; provider admission validates the opaque template payload and allowed tenant references.

## Generic VirtualMachineAutoscaler

One proposed `kube-vm.io` resource: required same-namespace `scaleTargetRef` with kind/name/UID, `minReplicas`, `maxReplicas`, nonempty unique CPU/memory target list, behavior and suspension fields. Only generic sets are accepted initially. Targets are integer percentages in 1–100; min is at least one and max is at least min. Target identity is immutable. There are no modes or external recommendation fields.

Defaults: evaluate every 30s, use complete 60s measurement windows no older than 90s, tolerance 10%, up to 25% increase (at least one) per 60s, one decrease per 300s, 300s downscale stabilization. Scale-in opt-in defaults false. Enforce rate limits against recorded recent scale events, not repeated percentages of a newly increased count.

Status includes observed generation, target UID, current/desired replicas, per-metric validity/coverage, last action, bounded protection history, and Ready/ScalingActive/ScalingLimited conditions. Conditions communicate computed state rather than gating the reconcile flow themselves. Bind claims to target UID, and use optimistic-lock writes to `/scale`. Admission validates the caller's target scale permission and all count-write paths. Claim acquisition and count-write interleavings require an integration proof before shipping; an annotation alone is not a concurrency guarantee.

## Provider template extension

A provider template advertises target group/kind, compatible served version and an immutable `spec.template` payload under a versioned instantiation capability. Generic set logic copies only allowed template fields into a fresh object, supplies the concrete name and mutual link, and sets controller ownership to the generic VM UID. Provider controllers resolve portable intent as they do today. Providers without this capability remain valid for manually created VM pairs but cannot back generic sets.

## Optional provider observation extensions

Proposed `status.utilization` contains a versioned report, bound generic UID/generation, infrastructure generation, timestamp/window, per-metric semantic ID, numerator/denominator, capacity and validity/reason. Provider object UID comes from metadata and is part of the reader's cache key. CPU represents total configured vCPU utilization. Memory semantic is gated by Q2; no ambiguous generic 'used memory' field is finalized here.

Application readiness needs a separate versioned observation with observed generation, readiness value, transition/observation times and freshness requirements (Q3). Today's InfrastructureReady/Ready mapping is unchanged. All observations are read-only inputs to generic policy logic; providers cannot choose replica counts.

Capability discovery must distinguish machine lifecycle, template instantiation, telemetry and application readiness, with explicit compatible contract versions. REST preferred-version resolution alone is insufficient. New fields on published APIs require compatibility review, appropriate version/conversion and regenerated manifests; silently relying on unknown fields being preserved is unacceptable.
