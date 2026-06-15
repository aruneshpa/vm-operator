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
2. **Classifies** each change as infrastructure-driven (DRS/HA/SDRS/vCLS),
   vendor-driven (VADP, NSX, SRM), or admin-driven (human).
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
  reassignment and revert them to enforce tenant isolation.
- MUST detect `ManagedBy` extension key removal and revert it; emit a Critical event if
  the revert fails.
- MUST classify DRS/HA/SDRS/vCLS changes as infrastructure-driven and observe without
  spec mutation, so infra operations do not generate spurious drift events.
- MUST classify VADP backup-vendor operations as vendor-driven and observe without
  disrupting snapshot state.
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

### US5 — Infra admin: folder reparent is reverted

- **Given** a `VirtualMachine` CR managed in namespace folder `ns-folder`,
  **when** the infra admin moves the VM to a different vCenter folder,
  **then** the operator issues `MoveIntoFolder_Task` to restore the VM to `ns-folder`
  and emits a `VirtualMachineAdminChangeReverted` Event.
- **Given** the admin repeats the reparent more than 3 times in 5 minutes,
  **then** a `VirtualMachineLost{Reason=FolderRevertPingPong}` condition is set instead
  of continuing to revert.

### US6 — Consumer: concurrent admin and consumer change

- **Given** a consumer patches `spec.powerState` and an admin power-changes the same VM
  within the 30-second skew window,
  **then** a `ConcurrentAdminConsumerChange` Warning Event is emitted; the admin intent
  wins (adopt) but the consumer can re-apply to override.

### US7 — Infra admin: VADP backup snapshot cycle is not disrupted

- **Given** a VADP backup vendor creates and removes snapshots on a VM,
  **when** the vendor's principal name matches the `vmoperator-reverse-reconcile-vendors`
  ConfigMap allow-list,
  **then** the operator classifies the changes as `VENDOR_DRIVEN`, performs OBSERVE only,
  and does NOT create or delete any `VirtualMachineSnapshot` CR.
- **Given** the vendor ConfigMap is absent,
  **then** VENDOR-class decisions degrade to OBSERVE and a Warning Event is emitted.

### US8 — Operator: first-enable shadow window prevents flood

- **Given** a cluster with N existing VMs and the feature flag flipped `false → true`,
  **when** the first-enable shadow window is active (default 1 hour),
  **then** all decisions are OBSERVE-only; no `spec` patches and no REVERT signals are
  emitted; status fields and conditions ARE populated.

---

## Open questions

- [NEEDS CLARIFICATION: Should the Event Manager subscriber (`subscribeEvents`) default
  to `true` in v1 GA or remain `false` and be enabled via the tuning ConfigMap? The
  design currently defaults to `false` (requires explicit ConfigMap opt-in), which means
  power-state ADOPT is disabled until operators configure it. Confirm the intended v1 GA
  default with the product team.]

- [NEEDS CLARIFICATION: The `adoptablePathWhitelist` defaults to `["spec.powerState",
  "spec.minHardwareVersion"]`. What is the full v1 list? The decision should be recorded
  in `plan.md` and reflected in the ConfigMap default before GA.]
