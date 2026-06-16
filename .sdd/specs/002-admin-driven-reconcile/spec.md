# Feature Specification: Admin-Driven Reverse Reconcile

- **Feature branch**: `feature/admin-driven-reconcile`
  - **Fork**: `aruneshpa/vm-operator`
  - **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-06-08
- **Status**: In Progress (Design complete; implementation not yet started)
- **Epic**: TBD
- **Design docs**: see `research.md`, `plan.md`, and `model.md` in this directory

---

## Summary

Post-VCFA-migration, traditional vSphere VMs become VM Service VMs reconciled by
`vm-operator`. Two personas coexist on the same cluster:

- **Consumer (DevOps)**: operates VMs exclusively through the Kubernetes API surface
  exposed by `vm-operator` (VCFA UI plugin, `kubectl`, Supervisor).
- **Infrastructure admin**: continues to act on the same VMs through vCenter UI,
  VMODL1/SOAP/VAPI, or ESXi to perform maintenance, quarantine, and infra operations.

Today `vm-operator` has no mechanism to detect or react to admin-side vSphere changes.
This creates two failure modes:
1. Admin quarantines a VM (powers it off, restricts NIC access) → operator immediately
   reverts the change to match the Kubernetes desired state.
2. Admin performs a maintenance operation (vMotion, storage migration, HA failover) →
   operator is unaware and may log spurious errors or report incorrect placement status.

This feature introduces an **admin-driven reverse-reconcile framework** that:
1. **Detects** admin-initiated vSphere changes through the extended property-collector
   watcher, an Event Manager subscriber, and a periodic full-property backstop.
2. **Classifies** each change as infrastructure-driven (DRS/HA/SDRS/vCLS) or
   admin-driven (human or backup vendor — vendor distinction not in v1; see §v1 scope).
3. **Decides** per operation whether to observe (status only), adopt into `vm.Spec`
   (reverse-reconcile desired state), or revert (re-assert the consumer's intent).
4. **Coexists** with the existing `PauseVMExtraConfigKey` / `PausedVMLabelKey` break-glass
   mechanism.

---

## Goals

- MUST detect admin-initiated power state changes (power off, on, suspend) and adopt
  them into `spec.powerState` so the operator does not immediately re-power the VM.
- MUST detect VM destruction or unregistration out-of-band and surface a
  `VirtualMachineLost` condition without deleting the Kubernetes CR.
- MUST detect admin-initiated folder reparent (out of namespace folder) and resource-pool
  reassignment and surface a `NamespacePlacementDrift` condition; does NOT revert (no
  reconcile code path exists for placement correction; action is deliberately admin-initiated).
- MUST detect `ManagedBy` extension key removal and revert it; emit a Critical event if
  the revert fails.
- MUST classify DRS/HA/SDRS/vCLS changes as infrastructure-driven and observe without
  spec mutation, so infra operations do not generate spurious drift events.
- MUST classify VADP backup-vendor operations as OBSERVE (snapshot create/remove/revert
  is OBSERVE for all sources; `backup-proxy=true` annotation prevents status mutation for
  hot-add disk transport).
- MUST emit structured Kubernetes Events, conditions, and an audit ring buffer
  (`status.adminActivity[]`) so consumers can observe every admin action.
- MUST guard all invariants: class-defined CPU/memory cannot be adopted; storage-class
  immutability is enforced; encryption class cannot be stripped.
- MUST be fully gated behind a single feature flag (`pkgcfg.Features.AdminReverseReconcile`
  defaulting to `false`) and produce no behaviour change when the flag is off.
- SHOULD apply an observe-only shadow window on first enablement to prevent a flood of
  ADOPT/REVERT events against existing VMs.
- SHOULD support a tuning ConfigMap for operational knobs (resync interval, conflict-skew
  window, retry limits) without requiring an operator restart or flag change.

---

## Non-goals

- **No new admin API surface.** The framework reads existing vSphere/VAPI APIs and writes
  only to the Kubernetes API.
- **No CIS tag poller.** vSphere tags are admin-only and not surfaced to the consumer
  persona per VM Operator's design invariant. There is no actionable reverse-reconcile
  decision for tag changes.
- **No automatic re-registration on a destination vCenter.** Cross-VC migration surfaces
  a `VirtualMachineLost` condition; the consumer re-creates the VM via `ImportVM`.
- **No real-time detection of direct ESXi shell operations.** The periodic property
  resync is the backstop for direct-host changes.
- **No general-purpose CRD adoption controller.** Scope is `VirtualMachine` (v1alpha6+)
  only.
- **No sub-v1alpha6 API support.** This framework is v1alpha6+ only.
- **No consumer-visible vSphere tags.** vSphere tags remain admin-only and are explicitly
  not echoed into `status.vsphereObserved`.

---

## User stories / acceptance criteria

### US1 — Infra admin: power-off a compromised VM

- **Given** a `VirtualMachine` CR with `spec.powerState=PoweredOn`,
  **when** the infra admin powers the VM off through vCenter UI and the Event Manager
  subscriber is enabled (`subscribeEvents=true` in the tuning ConfigMap),
  **then** `spec.powerState` is patched to `PoweredOff` within 60 seconds, a
  `VirtualMachineSpecAdopted` Event is emitted, and the `last-adopt-source=vcenter`
  annotation appears on the VM.
- **Given** the same scenario but the Event Manager subscriber is disabled,
  **then** `spec.powerState` is NOT adopted (OBSERVE only) and a
  `VirtualMachineReverseReconcileDegraded{Reason=EventSubRequired}` condition is set.

### US2 — Infra admin: VM is destroyed out-of-band

- **Given** a `VirtualMachine` CR,
  **when** the infra admin destroys the VM in vSphere (`Destroy_Task`),
  **then** a `VirtualMachineLost{Reason=DestroyedOutOfBand}` condition is set on the CR,
  the CR is NOT deleted, PVCs are NOT removed, and a Warning Event is emitted.
- **Given** the VM is unregistered (not destroyed),
  **then** the condition reason is `UnregisteredOutOfBand` and `status.uniqueID` is
  retained so re-registration can recover the mapping.

### US3 — Infra admin: vMotion and DRS placement

- **Given** a `VirtualMachine` CR with `status.host=host-A`,
  **when** DRS or a manual vMotion moves the VM to `host-B`,
  **then** `status.vsphereObserved.host` is updated to `host-B`, `spec` is NOT modified,
  no REVERT is triggered, and no spurious drift condition is set.

### US4 — Consumer: admin action is visible in status

- **Given** any admin-driven vSphere change has been processed,
  **then** `status.adminActivity[]` contains an entry with `source`, `decision`,
  `principalName`, `path`, and `timestamp` populated, and the ring is bounded to 10
  entries (FIFO eviction).

### US5 — Infra admin: folder reparent or resource-pool reassignment is surfaced

- **Given** a `VirtualMachine` CR managed in namespace folder `ns-folder`,
  **when** the infra admin moves the VM to a different vCenter folder,
  **then** a `NamespacePlacementDrift` condition is set on the CR, a Warning Event is
  emitted, and `status.vsphereObserved` reflects the new parent. The operator does NOT
  attempt to move the VM back.
- **Given** a `VirtualMachine` CR managed in namespace resource pool `ns-rp`,
  **when** the infra admin moves the VM to a different resource pool,
  **then** the same `NamespacePlacementDrift` condition and Warning Event are set. The
  operator does NOT attempt to move the VM back.

**Rationale:** vm-operator has no reconcile path that moves an existing VM into a
namespace folder or RP (placement happens once, at creation). A REVERT would require the
reverse-reconcile controller to call `MoveIntoFolder_Task` directly, violating the
design principle that only the standard reconciler makes vSphere writes. More
importantly, this is a deliberate admin action requiring elevated vCenter privilege — the
tenant isolation boundary is already a vCenter RBAC concern, not something vm-operator
can enforce by moving the object back. The `NamespacePlacementDrift` condition surfaces
the drift so humans can triage.

### US6 — Consumer: concurrent admin and consumer change

- **Given** a consumer patches `spec.powerState` and an admin power-changes the same VM
  within the 30-second skew window,
  **then** a `ConcurrentAdminConsumerChange` Warning Event is emitted; the admin intent
  wins (adopt) but the consumer can re-apply to override.

### US7 — Infra admin: VADP backup snapshot cycle is not disrupted

- **Given** a VADP backup vendor creates and removes snapshots on a VM (regardless of
  the vendor's principal name),
  **then** the operator performs OBSERVE only for snapshot create/remove/revert
  operations — it does NOT create or delete any `VirtualMachineSnapshot` CR, because
  snapshot operations are OBSERVE for all source classifications.
- **Given** a backup vendor uses hot-add transport and the VM has the
  `backup-proxy=true` annotation,
  **then** disk attach/detach events on that VM produce no status mutations
  (the `backup-proxy` annotation is the only vendor-awareness mechanism required).

### US9 — Infra admin: ManagedBy extension key is removed

- **Given** a `VirtualMachine` CR with `config.managedBy.extensionKey=vmoperator.vmware.com`,
  **when** an infra admin (or hostile actor) removes or overwrites the `ManagedBy` field
  via vCenter,
  **then** the operator immediately issues `ReconfigVM_Task` to restore
  `config.managedBy`, emits a `ManagedByExtensionChanged{Reason=Hostile}` Critical Event
  (page-worthy), and sets a `VirtualMachineAdminPaused` condition during the revert.
- **Given** the revert fails (e.g., `DisableMethods` is blocking `ReconfigVM`),
  **then** a `VirtualMachineLost{Reason=ManagedByLost}` condition is set and no further
  reverse-reconcile decisions are made for that VM until a human resolves it.

### US10 — Infra admin: direct hardware reconfigure is reverted (invariant guard)

- **Given** a `VirtualMachine` CR with a class-defined CPU count,
  **when** an infra admin directly reconfigures the VM's CPU or memory to a value that
  differs from the class definition (`spec.className`),
  **then** the operator triggers a REVERT (injects `GenericEvent` for the standard
  reconciler), emits an `InvariantViolationDowngrade` Warning Event, and sets an
  appropriate drift condition; the standard reconciler re-applies the class-defined
  values.
- **Given** an infra admin strips the encryption class or performs a `ReconfigVM` that
  would change storage-class assignment,
  **then** the operator triggers the same REVERT path; the immutable fields are
  restored on the next standard-reconcile pass.

### US8 — Operator: first-enable shadow window prevents flood

- **Given** a cluster with N existing VMs and the feature flag flipped `false → true`,
  **when** the first-enable shadow window is active (default 1 hour),
  **then** all decisions are OBSERVE-only; no `spec` patches and no REVERT signals are
  emitted; status fields and conditions ARE populated.

---

---

## v1 scope

To avoid a multi-quarter project, v1 ships only what is needed to eliminate the primary
admin-operator conflict and provide safe rollout. Everything else is deferred or dropped.

### In scope for v1

| Item | Priority | Why |
|---|---|---|
| Property-collector extension (~12 new paths) | P0 | Foundation for all detection |
| Periodic full-property resync (backstop) | P0 | ESXi-direct ops, missed events |
| Event Manager subscriber | P0 | Required for power-state ADOPT |
| Power-state ADOPT (US1) | P0 | #1 user pain: ping-pong on admin power-off |
| VM LOST detection (US2) | P0 | Confusing dangling CRs |
| DRS/HA OBSERVE without spec mutation (US3) | P0 | Spurious drift events |
| Folder + RP OBSERVE + `NamespacePlacementDrift` condition (US5) | P0 | No REVERT code path; deliberate admin action; surface drift for human triage |
| ManagedBy REVERT + Critical alert (US9) | P0 | Security |
| Shadow window on first enable (US8) | P0 | Safe rollout |
| Audit ring `status.adminActivity[]` (US4) | P0 | Observability |
| Basic conditions (9 new condition types) | P0 | Required by US1–US10 |
| `backup-proxy=true` annotation (US7) | P1 | Needed before GA if backup vendors present |
| Invariant guards: CPU/mem/encryption REVERT (US10) | P1 | Correctness; ops are rare |

### Deferred to v2

| Item | Reason for deferral |
|---|---|
| FT status fields (`VirtualMachineFTStatus`) | Observability only; no decisions; adds API surface |
| HA status fields (`VirtualMachineHAStatus.LastFailoverTime`) | Same — informational; no decisions |
| `AffinityDrift` condition (DRS rule drift) | Rarely actionable; no REVERT path |
| `StorageClassDrift` condition (SDRS/SPBM drift) | Rarely actionable; REVERT disabled anyway |
| Rename REVERT | Rare; low pain |
| `spec.latencySensitivity` lift to first-class spec field | API change; v2 only |
| Cross-VC migration auto-recovery (`ImportVM`) | Too many topology assumptions |
| `ManagedBy` abdicate annotation path | v2; v1 always reverts |

### Dropped (not deferred)

| Item | Reason |
|---|---|
| `AdminReverseReconcileVendors` ConfigMap | VENDOR classification collapsed into ADMIN (see below) |
| VENDOR source class in classifier | All VENDOR decisions are identical to ADMIN for the affected ops; snapshot/disk ops are OBSERVE regardless |
| Vendor-event ring coalescing | Cosmetic; ring still correct without it |

**Why VENDOR is collapsed into ADMIN:** The per-op decision table produces the same
outcome (OBSERVE) for every operation where a backup vendor is involved. The only
structural difference was audit-ring coalescing, which is cosmetic. Collapsing removes
the `AdminReverseReconcileVendors` ConfigMap, heuristic #6 in the classifier, the
vendor allow-list bootstrap problem (OQ-1), and 2 tasks. The `backup-proxy=true`
annotation handles the one VADP-specific case (hot-add disk transport) without requiring
vendor classification.

---

## Open questions

- [RESOLVED: **OQ-1 — VADP vendor allow-list delivery.** Dropped. VENDOR source class
  and `AdminReverseReconcileVendors` ConfigMap are not part of v1 scope. The
  `backup-proxy=true` annotation is the only VADP-awareness mechanism required.]

- [NEEDS CLARIFICATION: Should the Event Manager subscriber (`subscribeEvents`) default
  to `true` in v1 GA or remain `false` and be enabled via the tuning ConfigMap? The
  design currently defaults to `false` (requires explicit ConfigMap opt-in), which means
  power-state ADOPT is disabled until operators configure it. Confirm the intended v1 GA
  default with the product team.]

- [NEEDS CLARIFICATION: The `adoptablePathWhitelist` defaults to `["spec.powerState",
  "spec.minHardwareVersion"]`. What is the full v1 list? The decision should be recorded
  in `plan.md` and reflected in the ConfigMap default before GA.]

- [NEEDS CLARIFICATION: **OQ-3 — Cross-VC migration auto-recovery.** v1 surfaces LOST.
  Should v2 auto-`ImportVM` when the VM is discovered on a destination VC? Options:
  (a) yes, auto-discover on destination VC; (b) no, require manual `ImportVM`.
  Recommendation: (b) manual; too many topology assumptions. Surface as LOST with a clear
  runbook.]

- [NEEDS CLARIFICATION: **OQ-4 — `ManagedBy` abdicate path (v2).** Explicit release of
  a VM from vm-operator management. What is the signal? Options: (a) `PausedVMLabelKey=
  admin+abdicate`; (b) new annotation `vmoperator.vmware.com/abdicate: true`; (c) a new
  `VirtualMachineAbdicate` subresource. Recommendation: (b) annotation; consistent with
  existing pattern. Out of scope for v1.]

- [NEEDS CLARIFICATION: **OQ-5 — `spec.latencySensitivity` lift.** Should this be
  promoted to a first-class spec field in v2? Options: (a) yes (requires API change);
  (b) keep as a drift condition indefinitely. Recommendation: (a) in v2; the
  OBSERVE+drift condition is the v1 holding pattern.]

- [NEEDS CLARIFICATION: **OQ-6 — vcsim coverage gaps.** `disabledMethod`, `customValue`,
  and some HA event types are not modeled in vcsim. Test strategy for these paths?
  Options: (a) fake/mock adapters in unit tests only; (b) real VC in CI for specific
  paths; (c) vcsim extension PRs. Recommendation: (a) for v1; (c) as follow-up.]

- [NEEDS CLARIFICATION: **OQ-7 — Minimum supported VC version.** The event whitelist and
  tagging APIs reference VC 7.0+ event types. Options: (a) VC 7.0 U3; (b) VC 8.0 GA.
  Define explicitly in feature-flag documentation; startup self-check emits
  `VirtualMachineReverseReconcileDegraded{Reason=VCVersionInsufficient}` below floor.]
