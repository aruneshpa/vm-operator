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

Without the `virtualmachines/status` marker, the first status write fails with `forbidden`
and the framework is silently broken.

### Leader election scope

The `ReverseReconcileController` is co-deployed in the **same Manager** as the existing
`VirtualMachine` controller and shares the same leader election lease
(`NeedLeaderElection() == true` is automatic for controller-runtime managers). This is
required because:

- The status-echo cache (suppresses redundant K8s patches on DRS churn) and the
  paired-event cache (corroborates EventSub events with property-collector changes) both
  live in-process; they must be coherent with the watcher state.
- On leader handoff, both the watcher and the decision engine restart together; the event
  replay window (`eventReplayWindow=30m`) covers both, ensuring no events are missed.

Do **not** deploy `ReverseReconcileController` in a separate Manager or a separate
Deployment; this would break the cache coherence invariant (CC1-13, CC4-04).

### vCenter permissions

The detection layer requires the following vCenter privileges beyond those already held
by the vm-operator ServiceAccount:

| Capability | vCenter privilege |
|---|---|
| Property Collector on cluster/host MOs (periodic resync) | `System.View` on the relevant Folder/Cluster |
| Event Manager subscription | `Global.Diagnostics` (read events) |
| `PermissionAddedEvent` / `RoleUpdatedEvent` | `Authorization.ModifyPermissions` (read-only; some VC builds) |
| CIS Tagging read (vendor heuristic) | `InventoryService.Tagging.ObjectAttachable` |
| `RetrieveProperties` for Leave disambiguation | already covered by `System.View` |

A **startup self-check** runs one representative API call per detection source. On
failure: do NOT abort startup (other sources may be working). Instead, set a
`VirtualMachineReverseReconcileDegraded{Reason=MissingVCPrivilege, Source=<name>}`
condition and emit a per-source Warning Event so operators can triage without looking at
logs. Implemented in task C-02.

---

## Observability

### Kubernetes Events

| Event | Reason | Severity |
|---|---|---|
| `VirtualMachineSpecAdopted` | per-op (e.g. `PowerStateAdopted`) | Normal |
| `VirtualMachineAdminChangeReverted` | per-op (e.g. `FolderReparentReverted`) | Normal |
| `VirtualMachineSpecAdoptionConflict` | `OptimisticLockFailed` | Warning |
| `InvariantViolationDowngrade` | `<InvariantName>` | Warning |
| `ConcurrentAdminConsumerChange` | `WithinSkewWindow` | Warning |
| `VirtualMachineLost` | per LeaveKind (e.g. `DestroyedOutOfBand`) | Warning |
| `ManagedByExtensionChanged` | `Hostile` | Critical (paging) |
| `VirtualMachineReverseReconcileShadowWindowEnded` | `ShadowWindowExpired` | Normal |

All events are emitted via `record.EventRecorder`; the `Reason` field doubles as a structured key for alerting rules.

### Prometheus metrics

Prefix: `vmoperator_reverse_reconcile_`

| Metric | Type | Labels |
|---|---|---|
| `events_total` | Counter | `source`, `kind`, `decision` |
| `adoption_conflicts_total` | Counter | `path` |
| `invariant_violations_total` | Counter | `invariant` |
| `lost_vms_total` | Counter | `cause` |
| `resync_duration_seconds` | Histogram | `stage` |
| `pending_events_queue_depth` | Gauge | — |
| `aggregator_drops_total` | Counter | `source`, `reason` |

### Audit ring buffer

`status.adminActivity[]` exposes the last 10 events inline on the VM object.
Larger retention belongs in the Kubernetes event stream or an external SIEM via the
operator's existing event sinks. The ring is bounded by `MaxItems=10` and is populated
even during the first-enable shadow window (decisions are OBSERVE; conditions and ring
are still written). See `model.md §1.1` for the `AdminActivityEntry` schema.

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

---

## Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **Spec churn confuses GitOps users.** ADOPT writes to `spec`; `kubectl get vm -o yaml` shows a `spec.powerState` the consumer did not author. | `last-adopt-source` annotation (TTL 5 min), `VirtualMachineSpecAdopted` event, and audit ring. GitOps users with strict spec ownership must set `PauseAnnotation=true` on VMs that must not be reverse-reconciled. |
| R2 | **Decision-engine bugs revert legitimate consumer changes.** | OBSERVE is the hardcoded fallback for unknown events. ADOPT/REVERT requires an explicit entry in the per-op decision table. New paths start in OBSERVE via `adoptablePathWhitelist` even if the decision is ADOPT — the whitelist must explicitly permit each path. |
| R3 | **vCenter load.** New property paths and optional EventSub subscription. | Feature flag default `false`. Beta deployments measure event volume before GA. `subscribeEvents` defaults `false`. See `research.md §3` (scale analysis) for expected rates. |
| R4 | **Event Manager subscription firehose.** Conservative whitelist (~30 event types). | Beta must measure event rate before GA. `aggregator_drops_total` metric tracks overflow. |
| R5 | **VADP vendor SA misclassification with empty allow-list.** | Default allow-list is empty (CC3-06). All vendor SA operations degrade to OBSERVE until the `AdminReverseReconcileVendors` ConfigMap is populated. Deployments that skip this see Warning events but no breakage. |
| R6 | **Ping-pong on OUT_OF_SCOPE ops.** Admin keeps making a change the operator keeps reverting. | Generalized ping-pong detector (`RevertPingPongMax=3` within `RevertPingPongWindow=5min`). Escalates to `VirtualMachineLost` condition rather than infinite fight. |
| R7 | **`ManagedBy` revert fights deliberate hand-off.** v1 always reverts. | v2 adds an `abdicate` annotation path to explicitly release the VM from vm-operator management (OQ-4). Surface as Critical event and `VirtualMachineLost` condition to force human triage. |
| R8 | **Power-state ADOPT requires Event Manager subscriber.** | Without `subscribeEvents=true` in the tuning ConfigMap, power state is OBSERVE only. This is intentional. Deployments must opt in to get ADOPT behavior for power state. |
| R9 | **Audit retention.** Kubernetes events default to 1-hour TTL. | `status.adminActivity[]` ring (10 entries) and the operator's event stream are the primary audit surfaces. Long-term compliance audit requires a SIEM sink (external to this design). |
| R10 | **First-enable flood.** All pre-existing VM drift is detected simultaneously on first enable. | `firstEnableShadowDuration` (default 1 hour) shadow window: all decisions OBSERVE; status/conditions populated; no spec writes or REVERT signals. See `plan.md §Rollout` and task C-03. |
