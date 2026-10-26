# VM Service horizontal autoscaling

- **Status**: Draft
- **Created**: 2026-10-20
- **Feature branch**: `features/hpa-for-vm-service`
- **Fork**: `aruneshpa/vm-operator`
- **PR target**: `vmware-tanzu/vm-operator:main` (proposed; confirm before opening a PR)
- **Epic**: TBD — explicitly requested by the author; no epic exists yet.
- **Related artifacts**: [Plan](plan.md), [Tasks](tasks.md), [API model](model.md), [Research](research.md)

## Purpose and agreed scope

A VM Service user declares one autoscaling resource for an existing VirtualMachineReplicaSet. That resource defines CPU and/or memory targets, replica bounds, and scaling behavior. One autoscaling controller reads measurements, computes the desired replica count, and controls the ReplicaSet count. The ReplicaSet remains responsible for creating and deleting template-based VMs.

There are no native/external control modes, externally supplied replica recommendations, scaling-request resources, or competing workflow writers. Automation tools configure the same autoscaling resource. Metric sources supply measurements to its controller.

This draft specifies the first product slice for interchangeable replicas with an explicit readiness check and an acceptable existing deletion lifecycle. Customer-specific IPAM, DNS, domain join, and load-balancer integration remain dependencies of the workload. Their lifecycle requirements must be honored; implementing new integration adapters is not silently included in the core autoscaler.

## Goals

- **G1**: Users MUST be able to configure CPU, memory, or both on one autoscaling resource targeting one same-namespace ReplicaSet.
- **G2**: The controller MUST derive replica counts from the configured metrics and select the largest recommendation when multiple metrics are configured.
- **G3**: Scaling MUST respect user bounds, readiness, stabilization, and rate limits, with observable reasons when it cannot proceed.
- **G4**: Missing, invalid, incomplete, or stale telemetry MUST NOT be treated as zero or authorize scale-in.
- **G5**: Only one autoscaling authority MUST control a target; namespace authorization and target identity MUST survive deletion/recreation and concurrent updates.
- **G6**: Suspension, deletion, feature disablement, restart, upgrade, and rollback MUST preserve the documented capacity contract.
- **G7**: The feature MUST have bounded resource use and a measured support envelope before production enablement.
- **G8**: Existing VM lifecycle behavior MUST remain owned by the existing workload components, including template stamping and deletion cleanup.

## Non-goals

Vertical resizing; scaling Supervisor/ESXi capacity; rollout or template revision management; predictive/scheduled scaling; scale from zero; standalone VM or StatefulSet targets; arbitrary application metrics/queries; a second autoscaling API or workflow-driven count updates; general heterogeneous-replica capacity calculations; new application draining, IPAM, F5, or domain-join implementations.

The feature does not guarantee instantaneous running capacity, compensate for insufficient physical resources, or make applications horizontally scalable.

## Metric and workload contract

CPU represents aggregate utilization of a VM's configured virtual CPUs. The memory proposal is active guest physical memory as a percentage of configured memory, explicitly named active-memory utilization in user documentation. It MUST NOT be silently substituted for guest OS used memory. **Q1** below must settle this before the memory contract ships.

Metrics are averaged over equal-capacity eligible VMs after each VM's time-weighted measurement window is complete. Eligibility requires target ownership, matching membership, completed provisioning, powered-on state, and an explicit successful readiness check throughout the window. Actual CPU and memory capacity must match the target template's resolved capacity. An adopted or resized incompatible VM blocks scaling with an explanation.

Initial support assumes readiness accurately reflects ability to serve the application. A workload requiring additional pool admission or draining is not supported for automatic scale-in until its integration supplies a tested lifecycle contract; **Q3** governs that extension.

## User stories and acceptance criteria

### US1 — Declare autoscaling

As a DevOps user, I want to declare utilization targets and bounds without repeatedly changing replicas.

- **AC01**: One resource references an existing same-namespace ReplicaSet and its immutable identity, with one or two distinct CPU/memory targets, minimum and maximum replicas, scaling behavior, and suspension.
- **AC02**: Reject invalid or empty metric sets, duplicate metrics, percentages outside (0, 100], minimum below one, maximum below minimum, invalid timing/rate values, cross-namespace targets, and unsupported target kinds.
- **AC03**: The template remains in the ReplicaSet. Scaling creates replicas from that template without mutating existing VM configurations or introducing another VM owner.
- **AC04**: Active autoscaling requires an existing positive desired count and an explicit readiness check. Activating against zero desired replicas is rejected; users establish initial capacity before enabling autoscaling.
- **AC05**: Windows and Linux workloads use the same API and control algorithm. Automation blueprints and Day-2 actions configure this resource, not replica counts.

### US2 — Adjust capacity from measurements

As a workload owner, I want capacity to follow load without unstable repeated changes.

- **AC06**: For N converged eligible replicas, each valid metric recommends the ceiling of N multiplied by measured average divided by its target. Relative deviation within tolerance retains N.
- **AC07**: Choose the greatest per-metric recommendation. Four replicas at 90% CPU against 60% recommend six; memory recommending two does not override six.
- **AC08**: Apply bounds, stabilization, and scale rates. Under a 25% increase limit, the preceding example requests five first and reevaluates after convergence using new measurements.
- **AC09**: A metric-based change waits until desired and non-terminating owned counts agree, no deletion is outstanding, and all members meet eligibility. A starting/failed VM cannot cause repeated desired-count inflation.
- **AC10**: Scale-in uses the highest recommendation within the configured stabilization window, requires a full safe observation history, and honors an explicit scale-in disable setting.
- **AC11**: Scale limits count changes across their entire configured period, not per reconciliation. Repeated reconciliations and no-op updates do not reset cooldowns or increase the allowance.
- **AC12**: Raising minimum replicas can request capacity without utilization data once identity and lifecycle checks pass. Lowering maximum replicas may reduce desired capacity only when scale-in is enabled and lifecycle/rate constraints permit. Report temporary out-of-bounds capacity while suspended, pending, or scale-in disabled; never silently override these protections.

### US3 — Degrade safely

As an operator, I want outages and slow provisioning to retain existing capacity.

- **AC13**: Stale, missing, invalid, partial-window, or wrong-identity samples invalidate that metric. If all metrics are invalid, preserve desired count and report the reason.
- **AC14**: One completely valid metric can justify scale-out while another is unavailable; any unavailable configured metric prevents scale-in. Missing members within a metric do not count as zero.
- **AC15**: Reject samples spanning power cycles or capacity changes and rebuild the observation window. Old VM samples cannot apply to a new VM with a reused name.
- **AC16**: Quota, placement, image, storage, networking, startup, or deletion delays block further metric-based changes while unresolved. Startup timeout reports failure to converge; it does not authorize automatic deletion/replacement.
- **AC17**: After restart or leadership change, reconstruct membership and fresh samples, respect a conservative rate-limit cooldown, and accumulate the full scale-in history before deleting capacity. A crash between a successful count update and status persistence cannot repeat the same scale increment.

### US4 — Enforce ownership and provide manual control

As a tenant administrator, I want scaling to remain within my namespace and authority.

- **AC18**: Admission requires authority to scale the referenced target as well as create/update the autoscaler. Unauthorized or cross-namespace attempts have no scaling side effects.
- **AC19**: Concurrent attempts cannot create two autoscalers for one target. While active, other clients cannot change target replicas through any served target version or its scale interface.
- **AC20**: Target identity is immutable. Deleting/recreating a same-named ReplicaSet does not transfer the old policy. A parent controller that owns desired replica count is incompatible unless an explicit ownership handoff exists.
- **AC21**: Suspension stops initiating new changes; already accepted VM operations may finish. After suspension is acknowledged, users can scale manually. Resume revalidates current capacity and collects fresh observations.
- **AC22**: Deleting the autoscaler leaves the ReplicaSet and its last desired count intact. Deleting the target never causes the autoscaler to recreate it.
- **AC23**: Conflicting updates are recomputed against fresh target state. No stale calculation overwrites a concurrent change.

### US5 — Operate and upgrade the feature

As a Supervisor administrator, I want supported installation, troubleshooting, and upgrades.

- **AC24**: The feature defaults off and depends on workload-management capability. Disabled behavior cannot collect metrics or initiate count changes. Existing resources remain readable, suspendable, and deletable.
- **AC25**: A product-managed install/upgrade deploys all required components with namespace isolation, least privilege, health checks, and documented resource reservations. Tenant root access and ad hoc installation are not prerequisites.
- **AC26**: Status exposes observed policy generation, current/desired/eligible/pending replicas, timestamped measurements, raw/final recommendations, last scale, and Ready plus explanatory conditions. Successful count submission is distinguishable from VMs becoming ready.
- **AC27**: Provider outage degrades scaling without repeated process restarts. Collection, queues, caches, API writes, and event emission are bounded.
- **AC28**: Rolling upgrades and rollback preserve policy data and replica counts. No upgrade silently changes metric semantics or policy defaults; rollback to an unsupported binary requires suspension and an explicit manual-control handoff.
- **AC29**: Publish measured VM/policy/namespace limits, decision latency, vCenter load, and pod sizing before release. Additional standby pods are not advertised as throughput scaling.

## Decisions and implementation gates

The single-resource/single-controller model is agreed. The following unresolved decisions have owners and task gates; an implementation agent MUST NOT silently select a different contract.

| ID | Decision | Owner | Impact |
| --- | --- | --- | --- |
| Q1 | [NEEDS CLARIFICATION: confirm active/configured memory as the supported memory percentage, or specify guest-used semantics and prerequisites.] | Product and field engineering | Blocks memory API/collector behavior; CPU groundwork can proceed |
| Q2 | [NEEDS CLARIFICATION: confirm Supervisor release, packaging owner, capability key, vCenter privileges, and supported upgrade/downgrade versions.] | Supervisor platform and VM Service maintainers | Blocks production deployment and upgrade qualification; local manifests can be prepared |
| Q3 | [NEEDS CLARIFICATION: identify first-release workloads requiring IPAM/F5 lifecycle coordination and the existing readiness/drain contract they use.] | Field integration owner and VM Service maintainers | Blocks enablement for those integrations, not eligible stateless workloads |
| Q4 | [NEEDS CLARIFICATION: confirm maximum tested VM/policy scale and supported latency/resource budgets.] | Performance and platform owners | Blocks production enablement and publication of sizing guarantees |

Epic remains TBD by author instruction. Task tickets must be assigned before shipping/merging product work. This draft is ready for review and explicitly gated investigation, not unconditional production implementation.

