# Implementation Plan: Admin-Driven Reverse Reconcile

- **Spec**: [`spec.md`](./spec.md)
- **Epic**: TBD
- **Date**: 2026-06-08

---

## Summary

Implement a reverse-reconcile framework that detects vSphere-side changes made by infra
admins and either adopts them into `vm.Spec` or reverts them, depending on whether they
are admin-centric operations (power state, quarantine) or consumer-invariant violations
(CPU/mem, encryption, folder placement). The framework extends the existing
`pkg/util/vsphere/watcher` property-collector pipeline, adds a new Event Manager
subscriber, and introduces a per-VM decision engine as a co-located controller inside the
existing `vm-operator` manager binary. Full design rationale, decision tables, and
cross-check findings are in `research.md`.

---

## Technical context

| Field | Value |
|-------|-------|
| **Language** | Go 1.22+ |
| **Primary dependencies** | `controller-runtime` v0.17, `govmomi` v0.36 |
| **API version(s) touched** | `v1alpha6` (additive only; no version bump) |
| **Modules touched** | `pkg/util/vsphere/watcher`, `services/vm-watcher`, `controllers/virtualmachine/`, `webhooks/virtualmachine/` |
| **New dependencies** | none beyond existing govmomi |
| **Testing** | Ginkgo v2 + Gomega; `vcsim` for integration; WCP Supervisor for E2E |
| **Feature flag** | `pkgcfg.Features.AdminReverseReconcile` (default `false`) |

---

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility | OK | All new fields are `+optional`; no existing fields removed; additive only |
| Thin controllers | OK | All vSphere calls pass through the existing provider abstraction; new decision logic lives in `pkg/util/vsphere/eventsub` and `controllers/virtualmachine/reverseReconcile/` |
| `status.observedGeneration` ownership | OK | Only the standard `VirtualMachineReconciler` writes `observedGeneration`; reverse-reconcile writes per-condition fields only |
| Feature flag default `false` | OK | `pkgcfg.Features.AdminReverseReconcile` defaults to `false`; zero behaviour change when off |
| E2E for cluster-observable changes | OK | Mandatory E2E tests for every ADOPT and REVERT decision path (tasks T-03 phase) |
| `+listType` / `+listMapKey` on all new lists | OK | `status.adminActivity` uses `+listType=atomic`; new list fields in `VirtualMachineVSphereObservedStatus` carry explicit markers |
| RBAC markers | OK | New `+kubebuilder:rbac` markers on the reverse-reconcile controller (see §Controller / webhook impact) |
| No internal URLs | OK | No Broadcom JIRA URLs, Confluence URLs, or internal prefixes in tracked files |

---

## Project structure

```
.sdd/specs/002-admin-driven-reconcile/
├── spec.md          — this feature's functional spec (what & why)
├── plan.md          — this file (how)
├── tasks.md         — ordered task checklist
├── research.md      — admin-operations analysis, cross-check findings, race catalog
└── model.md         — API types, conditions, annotations, status fields

pkg/util/vsphere/
├── watcher/
│   └── watcher.go           MODIFY — extend DefaultWatchedPropertyPaths(); fix
│                                     ObjectUpdateKindLeave disambiguation
└── eventsub/
    └── eventsub.go          NEW    — EventManager subscriber (goroutine,
                                     whitelist-filtered, paired-event cache)

services/vm-watcher/
└── vm_watcher_service.go    MODIFY — start/stop EventSubscriber alongside Watcher;
                                     start PeriodicResync goroutine

controllers/virtualmachine/reverseReconcile/
├── controller.go            NEW    — ReverseReconcileController (controller-runtime
│                                     Reconciler; co-deployed in existing Manager)
├── aggregator.go            NEW    — AdminEvent / AdminEventBatch types; buffered
│                                     aggregator channel
├── classifier.go            NEW    — ClassifySource() — 8 heuristics (INFRA/VENDOR/
│                                     ADMIN/UNKNOWN)
├── pause.go                 NEW    — PauseCheck, ImportWindowCheck, ShadowWindowCheck
├── decisions.go             NEW    — per-op handler dispatch table (§6.4 catalog)
├── guards.go                NEW    — InvariantGuards (class, storageclass, encryption)
├── conflict.go              NEW    — ConflictResolver (timestamp / last-writer-wins)
├── executor.go              NEW    — OBSERVE / ADOPT / REVERT / LOST execution paths;
│                                     echo cache; vendor-event coalescing; ping-pong
│                                     detector
└── metrics.go               NEW    — Prometheus metrics (vmoperator_reverse_reconcile_*)

api/v1alpha6/
└── virtualmachine_types.go  MODIFY — new condition constants; new status types
                                     (VirtualMachineVSphereObservedStatus,
                                     VirtualMachineAdminActivity, FTStatus, HAStatus);
                                     new annotation constants

webhooks/virtualmachine/
├── validation/
│   └── virtualmachine_validator.go  MODIFY — IsReverseReconcileWrite() helper;
│                                             immutability bypass branches; annotation
│                                             grammar validation (admin-managed-devices,
│                                             admin-managed-nics, backup-proxy)
└── mutation/
    └── virtualmachine_mutator.go    MODIFY — conditional annotation-strip (do NOT strip
                                              last-adopt-* on operator SA request)

config/rbac/
└── role.yaml                MODIFY — virtualmachines/status update; events create/patch;
                                     configmaps get/list/watch (for vendor ConfigMap)
```

---

## API / CRD strategy

**Additive only. No version bump.**

All new types are `+optional` fields on existing structs or new constants. The storage
version stays at `v1alpha6`. Conversion through the existing conversion-data annotation
is safe: new `MaxItems`-capped lists are bounded and do not overflow the annotation.

New types introduced (see `model.md` for field-by-field schema):

- `VirtualMachineVSphereObservedStatus` — surfaces observed vSphere state; exposed as
  `status.vsphereObserved`. Does NOT include vSphere tags (admin-only invariant).
- `VirtualMachineAdminActivity` — audit ring entry (max 10, FIFO).
- `VirtualMachineFTStatus`, `VirtualMachineHAStatus` — FT and HA status snapshots.
- New condition type constants (9 new conditions; see `model.md §2`).
- New annotation key constants (8 new annotations; see `model.md §3`).

---

## Controller / webhook impact

### New controller: `ReverseReconcileController`

- Registered in the **same `Manager`** as `VirtualMachineReconciler` (co-deployed;
  inherits leader election; shares the workqueue).
- Trigger source: buffered `AdminEventBatch` channel from the aggregator, plus periodic
  resync `GenericEvent`.
- `NeedLeaderElection() bool` returns `true`.
- Per-VM serialization: standard controller-runtime workqueue (one inflight per VM key).
- Does NOT call vSphere APIs directly; REVERT is a `GenericEvent` injection into the
  standard reconcile queue.

### New feature flag

`pkgcfg.Features.AdminReverseReconcile` — single bool, default `false`.
No sub-flags. All detection subsystems activate together under the master flag.
Operational knobs (resync interval, conflict-skew, retry limits, shadow window duration)
live in the `vmoperator-reverse-reconcile-config` ConfigMap (§8 of design doc; see
`model.md §4` for the full key table).

### Webhook changes

**Validation webhook** (`virtualmachine_validator.go`):
- New helper `IsReverseReconcileWrite(ctx, oldVM, newVM) bool`:
  - Returns `true` IFF (a) `IsVMOperatorServiceAccount(userInfo)`, (b) `last-adopt-source=vcenter` annotation present, AND (c) spec diff is within `adoptablePathWhitelist`.
  - `system:masters` / `kube-admin` / `PRIVILEGED_USERS` do NOT trigger this bypass — only the operator SA.
- Immutability-check branches on `spec.storageClass`, `spec.network.interfaces[*].mtu`, `spec.network.interfaces[*].macAddr` gain a bypass gated on `IsReverseReconcileWrite`.
- Annotation grammar validation for `admin-managed-devices`, `admin-managed-nics`, `backup-proxy`.

**Mutation webhook** (`virtualmachine_mutator.go`):
- Do NOT strip `last-adopt-*` annotations on requests from the operator SA (the
  validator must see them to activate the bypass).
- Strip stale `last-adopt-*` annotations on non-operator user requests.

### New RBAC markers on the controller

```go
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
```

---

## Detection layer

Three sources feed the unified `AdminEventBatch` aggregator channel:

1. **Property Collector watcher** (extended `pkg/util/vsphere/watcher`):
   - ~12 new property paths added when `AdminReverseReconcile=true`; none added when
     `false` (zero baseline impact).
   - `ObjectUpdateKindLeave` disambiguation fixed: synchronous `RetrieveProperties`
     follow-up → `LeaveKind` (Unregistered / Destroyed / XvcMoved / Ambiguous).
   - Filter: drop any `ObjectUpdate` whose MoRef is not in the `status.uniqueID` index.
   - Scale: 27 total paths × 40k VMs = 1.08M watched path-instances (1.8× baseline);
     ~150–200 new events/day at fleet scale (see `research.md §scale`).

2. **Event Manager subscriber** (`pkg/util/vsphere/eventsub`; optional):
   - Enabled by `subscribeEvents=true` in the tuning ConfigMap.
   - Polls `EventManager.ReadNextEvents` with a whitelist of ~30 event types.
   - Populates a `pairedEventCache` (TTL=60s) consumed by the source classifier.
   - Required for power-state ADOPT (§6.2.3 of design). Without it, power-state changes
     are OBSERVE-only.
   - On startup / leader handoff: replays events within `eventReplayWindow` (default 30m).

3. **Periodic full-property resync**:
   - Every `resyncIntervalSeconds` (default 1800): `RetrievePropertiesEx` for each VM;
     diff against watcher cache; emit synthetic `AdminEventBatch` for drift.
   - Staggered by `hash(MoRef) % interval` → ~22 VMs/sec at 40k VMs.
   - Backstop for ESXi-direct ops and missed property-collector events.

---

## Decision engine

The decision engine pipeline for each `AdminEventBatch`:

1. **Source classification** — 8 heuristics (Leave → INFRA; DRS events → INFRA; SDRS principal → INFRA; HA events → INFRA; WCP service account → INFRA; VADP vendor principal → VENDOR; empty-principal placement → INFRA; else → ADMIN). Safe default: UNKNOWN + power-state → OBSERVE.
2. **Pause / suppression check** — `PauseVMExtraConfigKey=True`, `PausedVMLabelKey`, `PauseAnnotation`, import/restore/failover annotation window, first-enable shadow window → all force OBSERVE.
3. **Per-op decision** — handler dispatch table keyed by property path / event leaf-type; implements the ADMIN/INFRA/VENDOR columns from the operations catalog.
4. **Invariant guards** — class invariant, encryption class, storage-class immutability, `minHardwareVersion` monotonicity, webhook approval, ExtraConfig reserved-prefix guard.
5. **Conflict resolution** — timestamp-based last-writer-wins with `conflictSkewSeconds` (default 30s) tolerance; `tK8s` from `*Synced` condition `LastTransitionTime`.
6. **Execution** — OBSERVE (status + ring + event), ADOPT (idempotency token + optimistic-lock patch + retry), REVERT (`GenericEvent` injection), LOST (condition + event + preserve CR).

Supporting mechanisms: vendor-event coalescing (collapse backup-window events in ring); status echo cache (suppress redundant K8s patches on DRS churn); ping-pong detector (escalate to LOST after N REVERTs in a window).

---

## Test strategy

- **Unit**: ≥ 85% branch coverage on `classifier.go`, `decisions.go`, `conflict.go`,
  `executor.go`, `IsReverseReconcileWrite`. All 8 classifier heuristics, all §6.4
  decision rows, all conflict-resolver branches, webhook bypass negatives.
- **Integration (vcsim)**: batch atomicity (CC1-01); Leave disambiguation; ManagedBy
  REVERT; `admin-managed-devices` annotation; `backup-proxy` annotation; shadow window.
- **E2E** (mandatory per `e2e-sync-with-changes.mdc`):
  - P0 minimum: `power_state`, `pause_batch`, `managedby_change`, `lost_destroy`,
    `webhook_bypass`, `shadow_window`.
  - P1 follow-up: `ha_failover`, `storage_drift`, `backup_proxy`, `import_window`,
    `concurrent_change`, `drs_host_change`, `vadp_snapshot`.
  - All test files under `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`.

---

## Rollout / migration

- **Feature flag**: `pkgcfg.Features.AdminReverseReconcile` default `false`.
- **v1 alpha**: flag off; new API types and conditions ship as additive CRD changes.
- **v1 beta**: flag on in dev/test; OBSERVE-only shadow window; validate classification on real workloads.
- **v1 GA**: flag on in production; tuning ConfigMap sets `subscribeEvents=true`.
- **First-enable shadow window**: all decisions OBSERVE for `firstEnableShadowDuration`
  (default 1 hour) after first `false → true` flag transition; prevents flood of
  ADOPT/REVERT against pre-existing VMs.
- **Rollback**: flip flag off; in-flight ADOPTs persist (already committed); standard
  reconciler resumes normal operation; status fields preserved (audit trail).
- **Schema upgrade**: additive CRD only; existing `VirtualMachine` objects round-trip
  without errors.

---

## Complexity tracking

| Violation | Why needed | Simpler alternative rejected because |
|-----------|------------|--------------------------------------|
| `spec` mutated by controller (not consumer) | Admin operations must not be immediately overridden; "pause-on-drift" and "adminOverride field" alternatives both leave the consumer's mental model broken (VM stops converging or spec is split) | See design §3 trade-off table; adopt-into-spec was chosen because it keeps a single source of truth and is reversible by the consumer re-patching spec |
| `IsReverseReconcileWrite` validation webhook bypass | The operator SA must be able to patch immutable fields (storageClass, network MAC/MTU) during adoption | Could be avoided by having a completely separate spec path (adminOverride), but that was the more complex alternative already rejected above |
| `last-adopt-*` annotations NOT stripped by mutator for operator SA | Mutator runs before validator; stripping prevents the bypass from firing | The alternative (strip and re-add in validator) is not possible because validators are read-only |
