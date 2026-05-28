# Admin Reverse-Reconcile Framework — Design

Status: DRAFT (post-cross-check, revision 1)
Owner: vm-operator core

> **Revision history**
> - r0: Initial draft (pre-cross-check).
> - r1: Folded findings from 4 parallel cross-check sub-agents. See `§16` for an explicit issue→resolution map. All CRITICALs and HIGHs are addressed in §§5–11; MEDIUMs are either in-line or recorded as `§15` open questions.
Companion docs: [`00-admin-operations-analysis.md`](00-admin-operations-analysis.md), [`02-story-breakdown.md`](02-story-breakdown.md)

---

## 1. Summary

Post-VCFA-migration, traditional vSphere VMs become VM Service VMs reconciled by `vm-operator`. Two personas coexist: a Kubernetes-native **consumer (DevOps)** and an **infrastructure admin** who continues to act on the same VMs through vCenter UI / VMODL1 / SOAP / VAPI / ESXi.

This design introduces a **reverse-reconcile framework** that:

1. **Detects** admin-initiated changes on the vSphere side through the existing `pkg/util/vsphere/watcher` (extended), a new **Event Manager** subscriber, and a **periodic full-property** backstop.
2. **Classifies** every detected change as `INFRA_DRIVEN` (DRS/HA/SDRS/vCLS), `VENDOR_DRIVEN` (VADP, NSX, SRM), or `ADMIN_DRIVEN` (human).
3. **Decides** per operation whether to **observe**, **adopt into `spec`**, or **revert**, based on the catalog in [`00-admin-operations-analysis.md §4`](00-admin-operations-analysis.md#4-consolidated-catalog).
4. **Coexists** with the existing `PauseVMExtraConfigKey` / `PausedVMLabelKey` admin pause mechanism, which serves as the admin's explicit break-glass to suppress reconciliation entirely.

The framework is delivered behind a feature flag (`AdminReverseReconcile`) and lands incrementally. v1 focuses on power-state, snapshot, placement, NIC, hardware-class, and lifecycle-removal categories. v2 picks up tag-driven NSX flows, cross-vCenter migration, and FT/HBR/scheduled tasks.

---

## 2. Goals and non-goals

### 2.1 Goals

- Detect admin operations on managed VMs through a layered detection surface; close the four detection gaps enumerated in [`00 §6`](00-admin-operations-analysis.md#6-required-detection-surface-additions).
- Maintain **a single source of truth per intent**: the Kubernetes `VirtualMachine` spec for consumer intent; observed vSphere state for admin-imposed reality on `KEEP_ADMIN_ONLY` axes.
- Adopt observed state into `vm.Spec` (per the parent decision in this thread) when, and only when, all invariants hold (class, storage class, encryption class, immutable fields).
- Revert admin changes that violate the consumer invariants (CPU/mem, encryption, boot options, rename, vTPM).
- Tolerate non-human admin actors (DRS, SDRS, HA, FDM, vCLS) without spec churn.
- Cooperate with VADP backup vendors without removing or rewriting their snapshots.
- Provide first-class observability: conditions, events, status echoes, metrics.

### 2.2 Non-goals

- **No new admin API surface.** The framework reads existing vSphere/VAPI APIs and writes only to the Kubernetes API.
- **No new consumer admin operations.** "Quarantine", "isolate", "force-restart with reason" remain admin-only — invoked from vCenter UI, not Kubernetes.
- **No automatic re-registration on a destination vCenter** (cross-VC migration). Surface the loss; require the consumer to re-create via `ImportVM` on the destination Supervisor.
- **No real-time detection of direct ESXi shell ops.** Covered by the periodic backstop only.
- **No general-purpose CRD adoption controller.** Scope is limited to `VirtualMachine` (v1alpha6+).
- **No backward-compat for sub-v1alpha6 APIs.** This framework is v1alpha6+ only.

---

## 3. Core design decision: adopt-into-spec

> **Decision** (locked in with the parent agent before drafting): when the controller observes an admin-driven change that is allowed by the invariants, it **mutates `vm.Spec` to match the observed state**.

This is a deliberate departure from the Kubernetes norm of "spec = user intent, status = observed". Rationale and trade-offs:

| Aspect | Adopt-into-spec | Separate `adminOverride` field | Pause-on-drift only |
|---|---|---|---|
| API churn | Low (no new fields) | High (parallel spec) | None |
| Controller complexity | Medium (one decision tree) | High (two-track reconcile) | Low |
| Consumer mental model | Spec drifts under their feet | Spec preserved, but reconcile yields | Spec preserved, VM stops converging |
| Audit trail | Annotation + Event (we mint these) | Built-in via parallel field | Built-in via condition |
| Race vs. consumer | Last-writer-wins resolvable | Both paths must merge | Hard divergence |
| **Chosen** | **Yes** | No | No |

The drawback (spec drifts under the consumer) is mitigated by:

- Mandatory **annotation `vmoperator.vmware.com/last-adopt-source=vcenter`** with timestamp and event UID on every adoption patch.
- A new **`VirtualMachineSpecAdopted` Event** emitted to the K8s audit log.
- A new condition `VirtualMachineSpecAdopted{Status,Reason,Message,LastTransitionTime}` reflecting the most recent adoption.
- A **read-only audit endpoint** (`status.adminActivity[]`, ring buffer of the last 10 admin operations) so consumers can see what happened without trawling events.

---

## 4. High-level architecture

```
        ┌────────────────────────────────────────────────────────┐
        │  vCenter / ESXi                                        │
        │                                                        │
        │   ┌────────────┐  ┌────────────┐                       │
        │   │ Property   │  │ Event      │                       │
        │   │ Collector  │  │ Manager    │                       │
        │   └─────┬──────┘  └─────┬──────┘                       │
        └─────────┼───────────────┼──────────────────────────────┘
                  │               │
        ┌─────────▼───────────────▼──────────────────────────────┐
        │  Detection layer (services/vm-watcher)                 │
        │                                                        │
        │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐   │
        │  │ Watcher      │ │ EventSub     │ │ Periodic     │   │
        │  │ (existing,   │ │ (new)        │ │ Resync (new) │   │
        │  │  extended)   │ │              │ │              │   │
        │  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘   │
        │         │                │                │            │
        │  ┌──────▼────────────────▼────────────────▼─────┐     │
        │  │  Result aggregator  (Kind, Source,           │     │
        │  │   Path, Value, VCChangeTime, MoRef→NS/Name)  │     │
        │  └──────────────────┬───────────────────────────┘     │
        └─────────────────────┼─────────────────────────────────┘
                              │
                              ▼
                ┌─────────────────────────────────────────────────────┐
                │  Reverse-Reconcile Controller (NEW)                 │
                │                                                     │
                │  1. Source classifier (INFRA / VENDOR / ADMIN)      │
                │  2. Pause check (PauseVMExtraConfigKey / Label)     │
                │  3. Per-op decision (catalog table)                 │
                │  4. Invariant guard (class / crypto / storage)      │
                │  5. Conflict resolver (timestamp / generation)      │
                │  6. Patch: spec / status / condition / event        │
                │  7. Audit ring buffer (status.adminActivity[])      │
                └────────────────────┬────────────────────────────────┘
                                     │
                                     ▼
                ┌─────────────────────────────────────────────────────┐
                │  Existing VirtualMachine controller                 │
                │  (reconciles spec → vSphere; unchanged)             │
                └─────────────────────────────────────────────────────┘
```

### 4.1 Component responsibilities

| Component | Responsibility | Status |
|---|---|---|
| **Watcher** (`pkg/util/vsphere/watcher`) | Property-collector signal on the VM container view | EXISTS; needs extensions in §5 |
| **EventSub** (`pkg/util/vsphere/eventsub`) | Event Manager subscription on a fixed event-type whitelist | NEW |
| **Periodic resync** (extension to existing watcher) | Full property re-fetch every N minutes per VM to backstop ESXi-direct ops | NEW |
| **Result aggregator** | Single typed channel of `AdminEvent` going to the controller | NEW |
| **Reverse-Reconcile Controller** (`controllers/virtualmachine/reverseReconcile`) | Decision engine in §5 and §6 | NEW |
| **VirtualMachine controller** (`controllers/virtualmachine/virtualmachine`) | Unchanged; consumes any spec patch the reverse-reconcile controller produces | EXISTS |

---

## 5. Detection layer

### 5.1 Watcher extensions

#### 5.1.0 Filter precondition: drop non-vm-operator-managed VMs at the watcher boundary

The watcher container view today is folder-scoped (per-Zone), which already excludes vCLS shadow VMs (`<datacenter>/vm/.vCLS/`) for the VM watcher. The new **cluster/host watcher** in §5.1.3 reaches per-VM via cross-reference and MUST apply an explicit filter before forwarding `AdminEvent`s:

```text
emit AdminEvent iff
    vm.config.managedBy.extensionKey == ManagedByExtensionKey  // i.e., wcp-managed
```

Implementation: add `config.managedBy` to `DefaultWatchedPropertyPaths()` (already in §6.1 below) AND wire a per-event filter in the aggregator (§5.4) that drops events whose `MoRef` is not present in the index `status.uniqueID` → namespace/name.

This also defends against `ADM-58` recovery: if `config.managedBy` is observed cleared, the aggregator emits the event (so the controller can REVERT) but stops forwarding subsequent property changes for the same MoRef until the claim is restored (so we do not adopt drift on a VM that another extension now owns).

#### 5.1.1 Property paths

Append the paths from [`00 §6.1`](00-admin-operations-analysis.md#61-new-property-paths-for-defaultwatchedpropertypaths) to `DefaultWatchedPropertyPaths()`. All additional paths are added when `Features.AdminReverseReconcile=true` and removed when the flag is `false`. There are no per-subsystem toggles — see §8.1 rationale.

#### 5.1.2 Leave-event handling (`F1`)

`watcher.go` `onUpdate` today does:

```go
if oui.Kind != vimtypes.ObjectUpdateKindLeave { … }
```

Replace with:

```go
switch oui.Kind {
case vimtypes.ObjectUpdateKindEnter, vimtypes.ObjectUpdateKindModify:
    // existing logic
case vimtypes.ObjectUpdateKindLeave:
    // disambiguate via a synchronous follow-up:
    // 1. RetrieveProperties on the moref. If "not found" → Destroyed or xVC-moved.
    // 2. Else check parent → if outside watched container view → Reparented.
    // 3. Else → still here, ignore.
    // For each case, emit Result{LeaveKind: ...}.
}
```

The aggregator forwards leave events with kind: `Unregistered`, `Reparented`, `Destroyed`, `XvcMoved`, or `Ambiguous` (when the follow-up RetrieveProperties races and we cannot determine — fall back to `LeaveAmbiguous` which the controller surfaces as a degraded condition).

#### 5.1.3 Host and cluster signals — no separate watcher

**Previous design (rev 1)** proposed a separate container-view on `ClusterComputeResource` and `HostSystem`. This is **removed in rev 2** due to scale concerns (see §5.5):

- A Supervisor may have 1,000+ hosts. Watching `runtime.inMaintenanceMode`, `runtime.connectionState`, `runtime.powerState` on every host creates a second container-view + property filter whose per-host events must be cross-referenced against all resident VMs — a fan-out problem (1 host change → N VM lookups).
- `ClusterComputeResource.configurationEx.rule` is a large, frequently-changing blob in clusters with DRS rule churn.
- The data we need is already observable per-VM:
  - `summary.runtime.host` changes → watcher detects host changes (DRS, maintenance evacuations).
  - `summary.runtime.dasVmProtection` → per-VM HA protection state.
  - Event Manager events (`EnteredMaintenanceModeEvent`, `DrsVmMigratedEvent`) → provide host/cluster context with the admin principal.

**What replaces it:**
- Per-VM property paths (already in §5.1.1): `summary.runtime.host`, `summary.runtime.dasVmProtection`, `resourcePool`.
- Event Manager subscriber (§5.2): subscribes to `EnteredMaintenanceModeEvent`, `EnteringMaintenanceModeEvent`, `ExitMaintenanceModeEvent` — these carry the host MoRef so the decision engine can attribute the event to infrastructure.
- The decision engine classifies `summary.runtime.host` changes paired with DRS/HA events as INFRA (§6.2.2 heuristics #1–#4).

> **CC3-13 resolution (unchanged):** `dasVmConfig` at cluster scope was already removed in rev 1 due to O(rules×VMs) fan-out. Per-VM `summary.runtime.dasVmProtection` is sufficient.

> **CC3-15 resolution (revised):** Host `runtime.powerState` (DPM standby detection) is now derived from Event Manager (`EnteredStandbyModeEvent` → INFRA) rather than a host property watcher. If Event Manager subscriber is off (`subscribeEvents=false` in ConfigMap), DPM standby is detected at next periodic resync via the per-VM `summary.runtime.host` change.

#### 5.1.4 Periodic resync (`F5`)

Add a `resyncIntervalSeconds` (default 1800, configurable via the tuning ConfigMap §8.2). For each managed VM, a background goroutine in `vm-watcher` calls `RetrieveProperties` for the *full* watched property set, diffs against the watcher's cache, and emits synthetic `AdminEvent` for any unexplained drift. This is the only defense against direct-ESXi shell ops (`ADM-60`) and against missed updates due to property-collector loss.

To bound load: stagger VMs by hash of `MoRef` % `ResyncInterval`. At 25k VMs and 30-minute interval, that's ~14 VMs/sec — well within VC capacity.

### 5.2 Event Manager subscriber (`F2`)

New package: `pkg/util/vsphere/eventsub`.

Subscribe to a whitelist of event types via `EventHistoryCollector` with filter `EventFilterSpec{EventTypeId: […]}`. Initial whitelist (covers v1):

- Lifecycle: `VmRemovedEvent`, `VmRenamedEvent`, `VmRegisteredEvent`, `VmReconfiguredEvent`, `VmTemplateAddedEvent`, `VmUpgradedEvent`, `VmReloadedEvent`
- HA / FT: `VmDasBeingResetEvent`, `VmDasResetEvent`, `VmDasResetFailedEvent`, `VmFailoverEvent`, `VmFaultToleranceStateChangedEvent`, `NotEnoughResourcesToStartVmEvent`
- Placement: `VmMigratedEvent`, `VmEmigratingEvent`, `VmRelocatedEvent`, `DrsRuleViolationEvent`, `DrsRuleComplianceEvent`, `EntityMovedEvent`
- Authorization: `PermissionAddedEvent`, `PermissionUpdatedEvent`, `PermissionRemovedEvent`, `RoleUpdatedEvent`
- Scheduled tasks: `ScheduledTaskCreatedEvent`, `ScheduledTaskStartedEvent`, `ScheduledTaskFailedEvent`
- Console / guest: `VmAcquiredMksTicketEvent`, `VmAcquiredTicketEvent`, `VmGuestRebootEvent`, `VmGuestShutdownEvent`, `VmGuestStandbyEvent`
- Alarms: `AlarmActionTriggeredEvent`
- Storage: `VmInaccessibleEvent`, `VmOrphanedEvent`, `DatastoreRemovedOnHostEvent`
- Host maintenance: `EnteredMaintenanceModeEvent`, `EnteringMaintenanceModeEvent`

The collector reads events with a small batch size, page-by-page; the **principal name** on the event (`Event.UserName`) is essential for distinguishing human admin from a service account (vpxd, FDM, DRS).

`Event.UserName` heuristics for source classification:
- `vpxd-extension-...`, `wcp-...`, `vsphere.local\machine-*` → INFRA
- Backup-vendor service accounts (configurable allow-list per deployment) → VENDOR
- Other authenticated users → ADMIN

### 5.3 ~~VAPI tagging poller~~ — REMOVED (rev 2)

> **Removed.** VM Operator maintains the invariant that vSphere tags applied by admins are not surfaced to the DevOps/consumer persona. Since tags are admin-only (no consumer action depends on them) and the only §6.4 decision for tag changes is OBSERVE, there is no reverse-reconcile action to take. Vendor source classification (NSX, SRM, backup vendors) is already handled by principal-name matching in the Event Manager subscriber (§6.2.2 heuristics #3–#7). A TagPoller would add cost (~500 API calls per 5-min interval at 40k VMs) for zero actionable signal.

### 5.4 The unified `AdminEvent` type

```go
package adminop

type AdminEvent struct {
    // Identity
    MoRef        vimtypes.ManagedObjectReference
    Namespace    string  // resolved via watcher index
    Name         string

    // What
    Kind         EventKind     // PropertyChanged | LeaveEvent | VCEvent | Resync
    LeaveKind    LeaveKind     // when Kind=LeaveEvent
    Path         string        // property path or VC event-type-id
    OldValue     any
    NewValue     any

    // When
    VCChangeTime time.Time     // event/property-collector timestamp
    DetectedAt   time.Time     // local clock

    // Who
    PrincipalName string        // from EventManager; empty for property-only signals
    Source        Source        // Infra | Vendor | Admin | Unknown
}
```

The aggregator multiplexes from Watcher / EventSub / Resync onto a single buffered channel. The reverse-reconcile controller consumes from it.

### 5.5 Scale analysis (40,000 VMs)

This section quantifies the impact of the new detection surfaces on a Supervisor with **40,000 managed VMs** across ~1,000 hosts and ~50 clusters. The baseline is the existing VM watcher with ~15 property paths per VM.

#### 5.5.1 Property Collector load

**Baseline (today):**
- 15 property paths × 40,000 VMs = 600,000 watched path-instances.
- vCenter property collector tracks these via a single container view + property filter. Steady-state update rate depends on workload; typical: ~2–5 updates/sec for power state, guest heartbeat, snapshot changes across the fleet.

**With `AdminReverseReconcile=true` (rev 2):**
- Additional paths added: `config.managedBy`, `config.template`, `config.flags.changeTrackingEnabled`, `runtime.dasVmProtection`, `datastore`, `config.version`, `config.bootOptions`, `config.flags.vbsEnabled`, `config.latencySensitivity.level`, `disabledMethod`, `customValue`, `config.changeVersion` = **~12 new paths**.
- Paths already watched today (no additional cost): `summary.runtime.host`, `summary.runtime.powerState`, `resourcePool`, `parent`, `config.extraConfig`, `config.hardware.device`, `summary.config.name`, `config.hardware.numCPU` (via `config.hardware.device`).
- New total: ~27 property paths × 40,000 VMs = **1,080,000 watched path-instances** (1.8× baseline).

**vCenter Property Collector capacity:**
- The property collector is a server-side subscription mechanism. Adding paths to an existing filter does NOT add per-VM polling overhead — the collector pushes only when a tracked property changes. The cost is:
  - Memory: vCenter maintains a per-path change-tracking entry per VM. At ~100 bytes/entry, 480,000 new entries ≈ **46 MB** additional VC memory. Negligible.
  - Event delivery: only fires when a watched property actually changes. The new paths are low-churn (see §5.5.2).

**No cluster/host container view (rev 2):** The rev 1 design proposed a second container view on `ClusterComputeResource` (50 objects × ~2 paths) and `HostSystem` (1,000 objects × 3 paths). This was dropped (see §5.1.3) because:
- Host property changes from DRS/DPM/maintenance generate fan-out: 1 host event → must cross-reference N VMs on that host. At 40 VMs/host × 1,000 hosts, this adds a lookup storm with no additional signal beyond what per-VM `summary.runtime.host` already provides.
- Cluster `configurationEx.rule` is a serialized blob that changes on every DRS rule modification. In a 50-cluster deployment with active rule management, this generates frequent large property-change notifications with no per-VM relevance.
- All necessary host/cluster signals are either already covered by per-VM paths or by Event Manager events.

#### 5.5.2 Chatter analysis — expected event rates

| Property path | Change trigger | Expected rate at 40k VMs | Notes |
|---|---|---|---|
| `summary.runtime.powerState` | Power on/off/suspend | ~50/day (admin-driven) | Already watched today. DRS/HA add transient bursts. |
| `summary.runtime.host` | vMotion, DRS, HA failover | ~200–1000/day | Already watched today. DRS-active clusters are bursty. |
| `config.hardware.device` | Disk/NIC/CD-ROM add/remove, VADP hot-add | ~100/day (VADP-dominated) | Already watched today. VADP backup cycles drive most churn. |
| `config.extraConfig` | Guest customization, backup metadata, pause key | ~500/day | Already watched today. VKS pod churn contributes but is filtered by `ignoredExtraConfigKeys`. |
| `resourcePool` | RP reassignment (rare), RP migration during upgrade | ~5/day | Already watched today. |
| `parent` (folder) | Folder reparent (rare) | ~1/day | Already watched today. |
| `rootSnapshot` / `snapshot` | Snapshot create/remove | ~200/day (VADP 4-hour cycles × subset of VMs) | Already watched today. |
| `summary.config.name` | VM rename | ~1/day | Already watched today. |
| **NEW** `config.managedBy` | Extension registration change | ~0/day (near-zero) | Only changes during operator upgrade or hostile takeover. |
| **NEW** `config.template` | Mark-as-template (rare) | ~0–1/day | Extremely rare on production VMs. |
| **NEW** `config.flags.changeTrackingEnabled` | CBT toggle (VADP, admin) | ~10/day | Toggled during backup enable/disable. |
| **NEW** `runtime.dasVmProtection` | HA config change on cluster | ~5/day | Changes when HA is enabled/disabled on cluster or per-VM override applied. |
| **NEW** `datastore` | Storage vMotion, SDRS | ~20/day | SDRS in active environments; otherwise rare. |
| **NEW** `config.version` | VM hardware upgrade | ~0/day | Planned maintenance only. |
| **NEW** `config.bootOptions` | Boot order change | ~0/day | Extremely rare. |
| **NEW** `config.flags.vbsEnabled` | VBS toggle | ~0/day | Extremely rare. |
| **NEW** `config.latencySensitivity.level` | Latency sensitivity change | ~0/day | Extremely rare. |
| **NEW** `disabledMethod` | Method disable/enable | ~5/day | VADP may toggle; admin anti-lockout rare. |
| **NEW** `customValue` | Custom attribute set/change | ~10/day | Monitoring/CMDB integration writes. |
| **NEW** `config.changeVersion` | Any ReconfigVM | ~100/day | Fires on every reconfig; used as a cheap dirty-check. |

**Total new event rate from added paths: ~150–200 events/day** across the entire 40,000-VM fleet. This is negligible compared to the existing ~1,000–2,000 events/day from already-watched paths.

**Worst case (bursty):** A fleet-wide VADP backup window touching 10,000 VMs with `config.flags.changeTrackingEnabled` + `config.hardware.device` + `snapshot` changes: ~40,000 property-change notifications in a 4-hour window = ~2.8/sec. The existing property collector handles this; the watcher's `MaxObjectUpdates=100` batching (line 439 of `watcher.go`) and the aggregator's buffered channel (4,096 depth) absorb the burst without backpressure.

#### 5.5.3 Event Manager subscriber load

The Event Manager subscriber (when enabled via `subscribeEvents=true` in ConfigMap) polls `EventHistoryCollector.ReadNextEvents` with batch size 100. At 40,000 VMs:
- Typical event rate: ~500–2,000 events/day (lifecycle + HA + DRS + auth combined).
- Burst: HA failover of 100 VMs = 200 events (BeingReset + Reset per VM) in ~30 seconds.
- The subscriber processes events in a single goroutine; at 2,000 events/day the CPU cost is negligible.
- Memory: paired-event cache holds at most `40,000 VMs × 3 event slots × 200 bytes ≈ 24 MB`. Entries expire after `PairWindow` (60s).

#### 5.5.4 Periodic resync load

At `resyncIntervalSeconds=1800` (30 min) with 40,000 VMs:
- Staggered by `hash(MoRef) % interval`: ~22 VMs/sec.
- Each resync is a single `RetrieveProperties` call for the full watched-path set of one VM. At 27 paths, the response is ~2–5 KB.
- Sustained throughput: 22 calls/sec × 5 KB = ~110 KB/sec to vCenter. Well within VC capacity.
- Cache diff: compare against the watcher's in-memory cache. If no change → no `AdminEvent` emitted. If change → single `AdminEvent` per changed path.
- Expected resync-diff rate: <0.1% of VMs per cycle (most VMs have no unexplained drift). At 40,000 VMs: ~40 resync-diffs per 30-min cycle = ~0.02/sec. Negligible.

#### 5.5.5 K8s API load from the decision engine

The primary concern is status writes:

| Scenario | K8s API calls/day | Notes |
|---|---|---|
| Steady-state (all OBSERVE, no drift) | 0 | Echo cache suppresses redundant writes. |
| DRS active (200 host changes/day) | 200 status patches | First occurrence per VM updates `status.VSphereObserved.host`; subsequent same-host events are echo-cached. |
| VADP backup (10k VMs, 4h cycle) | 10,000 status patches / cycle | One per VM for snapshot add; coalesced for subsequent snapshot events in the same window. |
| Admin power-off burst (100 VMs) | 100 spec patches + 100 status patches | ADOPT path. Retry adds ~10% overhead (optimistic-lock conflicts). |
| First-enable shadow window (40k VMs) | 40,000 status-only patches over 1 hour | Shadow window forces OBSERVE; patches are staggered by resync hash. ~11/sec. Manageable for K8s API server (comparable to a large deployment rollout). |

**Conclusion:** The detection layer adds negligible load to vCenter. The K8s API load is dominated by the first-enable shadow window and VADP backup cycles, both of which are bounded and transient. Steady-state load is near-zero for well-behaved environments.

---

## 6. Decision engine

The decision engine is the new package `controllers/virtualmachine/reverseReconcile`. It is a Kubernetes controller that subscribes to the aggregator channel (not to `VirtualMachine` watch events).

### 6.1 Top-level flow

For each `AdminEvent e`:

```
1. Resolve VM CR: get VirtualMachine in (e.Namespace, e.Name).
   - If not found → drop (VM is being deleted on K8s side; defer to delete reconcile).
   - If !vm.Status.UniqueID == e.MoRef.Value → drop (stale MoRef; index recovery in progress).

2. Source classification (§6.2): set e.Source = INFRA | VENDOR | ADMIN | UNKNOWN.

3. Pause check (§6.3):
   - If pause is asserted, only update status / condition / audit ring buffer.
   - Skip spec mutation.

4. Per-op decision (§6.4): look up handler in the per-op table.
   - Handlers return one of: OBSERVE | ADOPT | REVERT | LOST.

5. Invariant guard (§6.5):
   - If handler returns ADOPT, run invariant checks. If any fail, downgrade to REVERT (and emit warning event).

6. Conflict resolution (§6.6):
   - If handler returns ADOPT and a concurrent consumer spec change is detected, run timestamp comparison.
   - If consumer wins, downgrade to OBSERVE (and emit warning event).

7. Apply mutation (§6.7):
   - OBSERVE: patch status / condition / audit ring buffer / emit Event.
   - ADOPT: patch spec via strategic merge with `MergeFromWithOptimisticLock`; on conflict retry up to 3 times.
   - REVERT: emit Event; the VirtualMachine controller's next reconcile will re-apply spec → vSphere.
   - LOST: set `VirtualMachineLost` condition; surface in audit; do NOT delete CR.

8. Audit ring buffer: append to status.adminActivity[] (cap at 10 entries; FIFO eviction).
```

### 6.2 Source classification

The classifier produces `Source ∈ {INFRA, VENDOR, ADMIN, UNKNOWN}`. Decisions in §6.4 are sensitive to this; mis-classification at this layer is the highest-impact failure mode.

#### 6.2.1 Inputs

Each `AdminEvent` carries (at least) `Path | EventType | LeaveKind`, `MoRef`, `PrincipalName` (may be empty), and `VCChangeTime`. A short-lived **paired-event cache** keyed by `(MoRef, EventClass)` correlates property-collector observations with Event Manager events that landed within `PairWindow` (default 60s; 5 min for HA-class events).

#### 6.2.2 Heuristics, in order

1. **HA-driven** — `EventType ∈ HAEventLeafTypes` OR a property change on `summary.runtime.{host,powerState,bootTime}` paired in the cache with one of:
   - `VmDasBeingResetEvent`, `VmDasResetEvent`, `VmDasResetFailedEvent`, `VmFailoverEvent`, `NotEnoughResourcesToStartVmEvent`
   ⇒ `INFRA(HA)`.

2. **DRS-driven** — `EventType ∈ DRSEventLeafTypes` (exact leaf-type match, not parent class):
   - `DrsVmMigratedEvent`, `DrsVmPoweredOnEvent`, `DrsResourceConfiguredEvent`, `DrsRuleViolationEvent`, `DrsRuleComplianceEvent`, `DrsSoftRuleViolationEvent`
   ⇒ `INFRA(DRS)`.
   > **CC3-03 resolution:** `VmMigratedEvent` (parent class) is NOT in the set; that event is emitted for manual admin migrations and must remain attributed to its principal.

3. **SDRS-driven** — `Path ∈ {config.files.vmPathName, config.vmStorageObjectId}` paired with `VmRelocatedEvent` whose `PrincipalName` matches one of the SDRS principal patterns:
   - default regex set: `^vpxd-extension(-.*)?$`, `^vpxd-extension-vcf-vps-.*$`, `^system@vsphere\\.local$`
   - configurable via `infraPrincipalPatterns` in the tuning ConfigMap (§8.2)
   ⇒ `INFRA(SDRS)`.
   > **CC3-04 resolution:** Patterns explicitly listed; deployments override via the feature flag list.

4. **System-event (FDM/vCLS/DPM)** — `PrincipalName == ""` (empty) AND the paired event is in `SystemEmptyPrincipalEventTypes`:
   - `HostShutdownEvent`, `EnteredStandbyModeEvent` (DPM), `VmDasResetEvent` (FDM)
   ⇒ `INFRA`.
   > **CC3-05 resolution:** Replaces the (fictitious) `FDM` / `vCLS` literal principal matches with an empty-principal + paired-event check.

5. **WCP-internal** — `PrincipalName` matches the WCP patterns in `infraPrincipalPatterns` (default includes `^wcp-.*$`, `^wcpsvc@.*$`) ⇒ `INFRA`. (vm-operator's own writes round-trip via the watcher; this prevents adopting our own ADOPT.)

6. **Vendor-driven** — `PrincipalName` matches a pattern in the `AdminReverseReconcileVendors` ConfigMap (see CC3-06 resolution below) ⇒ `VENDOR`.

7. **Empty-principal placement** — `PrincipalName == ""` and `Path ∈ {summary.runtime.host, config.files.vmPathName, resourcePool}` ⇒ `INFRA` (predominantly DRS/SDRS/DPM-driven; emit a Warning Event if the change does NOT match a recent INFRA event to make unusual non-DRS placement visible).

8. **Default** ⇒ `ADMIN`.

> **CC3-07 resolution (rev 2):** The tag-based vendor classification heuristic has been removed along with the TagPoller. vSphere tags do not carry a `created_by` field and are not surfaced to the consumer persona. Vendor classification relies entirely on `Event.UserName` principal matching (heuristics #3–#6).

#### 6.2.3 Safe-default behavior

For events classified `UNKNOWN`:
- `Path ∈ {summary.runtime.powerState, runtime.powerState}` and there is no paired HA event ⇒ DOWNGRADE the decision to **OBSERVE** (never ADOPT). Emit `PowerStateChangeUnclassified` Warning Event.
- All other `UNKNOWN` events ⇒ treat as `ADMIN`, with a `Source=Unknown` audit-ring entry.

> **CC3-01 resolution:** Power-state ADOPT requires either an INFRA-paired event OR a non-empty `ADMIN` principal. Without the Event Manager subscriber (`subscribeEvents=true` in the tuning ConfigMap), power state cannot be ADOPTED; it can only be OBSERVED. This is enforced at startup: if `subscribeEvents=false` (or ConfigMap absent), the operator emits a Warning condition `VirtualMachineReverseReconcileDegraded{Reason=EventSubRequired}` cluster-wide. Power-state detection still works (property collector), but all decisions are capped at OBSERVE.

#### 6.2.4 Operator-restart event replay

On startup AND after leader-election handoff:
1. The EventSub MUST issue an initial `EventManager.QueryEvents` with a lookback of `eventReplayWindow` (default 30 min, max 24 h; configurable in tuning ConfigMap §8.2).
2. Until the replay completes, the source classifier marks every event `Source=ResyncUncorroborated` and any matching decision is forced to OBSERVE (never ADOPT/REVERT).
3. After replay completes, `Source=ResyncUncorroborated` is downgraded to the classifier's normal output for new events.

> **CC3-02 resolution:** This addresses the post-restart HA-failover misattribution. Audit-ring entries during the replay window carry the `ResyncUncorroborated` source so operators can tell that the classification is best-effort.

### 6.3 Pause / suppression check

The framework formalizes the existing pause primitives PLUS the import/restore/failover suppression.

#### 6.3.1 Property-collector batch atomicity (CC1-01)

`WaitForUpdatesEx` returns updates as `ObjectUpdate.ChangeSet[]` (a per-MoRef batch of property changes within a single `set.Version`). The aggregator MUST preserve this batch boundary all the way to the decision engine:

```go
type AdminEventBatch struct {
    MoRef        moRef
    SetVersion   string
    VCChangeTime time.Time
    Changes      []AdminEvent  // ordered by property path
    // The cache snapshot AFTER applying all Changes:
    AfterExtraConfig map[string]string  // resolved view
    AfterLabels      map[string]string  // K8s side (operator reads CR before processing)
}
```

The controller processes an `AdminEventBatch` as one transaction: the pause check operates on `AfterExtraConfig[PauseVMExtraConfigKey]`. If pause is `True`, no decision in the batch escalates above OBSERVE. This closes the race where, within a single `set.Version`, the admin set `pause=True` AND power-off in the same `ReconfigVM_Task`; without batch atomicity, the controller could process the power-off (using the BEFORE pause state) and ADOPT it before the pause took effect.

If a batch contains `pause=True` and `pause=False` (transient flip): the AFTER state is whichever value is last in `ChangeSet[]` for the same key. vSphere does not document ordering guarantees within a batch — if both values are observed in the same batch, the controller must treat the batch as `pause=True` (conservative: assume the admin meant to pause).

#### 6.3.2 Pause signals

| Signal | Effect |
|---|---|
| `PauseVMExtraConfigKey="True"` observed on vSphere VM | Decision engine restricts to OBSERVE; also writes a label to advertise the pause (see §6.3.3). |
| `PausedVMLabelKey=devops` / `devops+admin` on K8s VM | OBSERVE only. |
| `PauseAnnotation=true` on K8s VM | OBSERVE only. |
| `ImportedVMAnnotation` / `RestoredVMAnnotation` / `FailedOverVMAnnotation` on K8s VM | OBSERVE only, for the lifetime of the annotation OR up to `ImportWindowExpiry` (default 30 min, configurable). See CC2-16. |

#### 6.3.3 Label attribution by source (CC3-20)

When the controller observes `PauseVMExtraConfigKey` flipping `False→True`, it sets:

| `AfterExtraConfig` pause flip principal | Label value | Additional annotation |
|---|---|---|
| `Source == INFRA` | `infra` | — |
| `Source == VENDOR` | `vendor` | `vmoperator.vmware.com/paused-by-source: vendor`, `…/paused-by-principal: <name>` |
| `Source == ADMIN` | `admin` | `…/paused-by-principal: <name>` |

> **CC3-20 resolution:** A backup vendor that sets the pause key during a backup window will be advertised as `vendor`, not `admin`, so monitoring dashboards do not page on routine backup activity.

This requires the **`paused` label values** to be extended in the existing API (`api/v1alpha6` already allows `admin|devops|both`; we add `vendor|infra`). The validation webhook (existing privileged-user gate) must allow these new values from the operator SA.

#### 6.3.4 Operator service account requirement (CC2-13)

The reverse-reconcile controller's write to `PausedVMLabelKey` is currently rejected by the validation webhook unless the writer is privileged (`virtualmachine_validator.go:2715-2728`). The controller MUST therefore run under the existing vm-operator SA, so `IsVMOperatorServiceAccount → IsPrivilegedAccount → true`. This is enforced by:

- Documenting in §10 RBAC that the reverse-reconcile controller is co-deployed with the existing controllers in the same `Deployment`.
- A startup self-check: attempt a dry-run `LabelPatch` against a known-test resource; if rejected, set cluster-wide condition `VirtualMachineReverseReconcileDegraded{Reason=ControllerNotPrivileged}` and refuse to start the decision engine.

### 6.4 Per-op decision table

The controller maintains a registry keyed by `(EventKind, Path | EventType | LeaveKind)`. Each entry is a function `(*AdminEvent, *VirtualMachine) → Decision`.

Decisions for the catalog from [`00 §4`](00-admin-operations-analysis.md#4-consolidated-catalog) — only the action shown, full implementation in §A.1:

| Op (catalog ID) | INFRA | VENDOR | ADMIN |
|---|---|---|---|
| Power off / on / suspend / reset (ADM-01..03, 53) | (n/a) | (n/a) | ADOPT (`spec.powerState`) |
| Snapshot create / remove / revert (ADM-04..06) | (n/a) | OBSERVE (Unmanaged) | OBSERVE (Unmanaged) |
| Consolidate / consolidation-needed (ADM-07, 51) | (n/a) | (n/a) | OBSERVE (info condition) |
| vMotion (ADM-08) | OBSERVE (update `status.host`, `status.zone`) | (n/a) | OBSERVE (Zone label re-eval) |
| Storage vMotion (ADM-09, 50) | OBSERVE (update `status.storage`) | (n/a) | **OBSERVE + `StorageClassDrift` condition only** (revised per CC3-21: never REVERT — SDRS would ping-pong us). Operator-initiated relocate may be added as an explicit `kubectl` action in v2. |
| Folder reparent (ADM-10, 44) | (n/a) | (n/a) | REVERT (`MoveIntoFolder_Task` back to namespace folder) |
| Resource Pool reassignment (ADM-11, 45) | (n/a) | (n/a) | REVERT (move back to namespace RP) |
| Cross-VC migration (ADM-59) | (n/a) | (n/a) | LOST |
| Host maintenance / datastore decom (ADM-61, 62) | OBSERVE (info condition) | (n/a) | (n/a) |
| NIC disconnect (ADM-12) | (n/a) | (n/a) | OBSERVE if `admin-managed-nics` annotation present; else REVERT |
| NIC backing change (ADM-13) | (n/a) | (n/a) — NSX does NOT change NIC backing; it changes effective DFW via tags (ADM-37) | OBSERVE if `admin-managed-nics`; else REVERT |
| NIC add / remove (ADM-14) | (n/a) | (n/a) | OBSERVE if `admin-managed-devices` lists this key; else REVERT |
| Tag changes (ADM-20, 37) | (n/a) | (n/a) | **OUT OF SCOPE** — vSphere tags are admin-only; not surfaced to consumer persona per VM Operator design invariant. No detection, no status update. |
| Disk add / remove (ADM-15) | (n/a) | OBSERVE without status mutation if `vmoperator.vmware.com/backup-proxy=true` annotation present (hot-add transport mode); else OBSERVE as Classic | OBSERVE as Classic if non-PVC; REVERT if PVC-backed disk admin-removed |
| CD-ROM attach (ADM-16) | (n/a) | (n/a) | OBSERVE if `admin-managed-devices`; else REVERT |
| SPBM profile change (ADM-46) | OBSERVE | (n/a) | OBSERVE + StorageClassDrift condition |
| Rename (ADM-17) | (n/a) | (n/a) | REVERT (re-set `config.name`) |
| Annotation (ADM-18) | (n/a) | (n/a) | OBSERVE (do not stomp; surface in `status.provider.notes`) |
| Custom Attributes / Fields (ADM-19, 38) | (n/a) | (n/a) | OBSERVE (`status.provider.customAttributes[]`) |
| Direct CPU/mem reconfigure (ADM-21) | (n/a) | (n/a) | REVERT (class invariant) |
| vTPM add/remove (ADM-22) | (n/a) | (n/a) | REVERT (class invariant) |
| Encryption rekey/decrypt (ADM-23) | (n/a) | (n/a) | REVERT (EncryptionClass invariant) |
| HW version upgrade (ADM-29) | (n/a) | (n/a) | OBSERVE if observed ≥ `spec.minHardwareVersion`; else REVERT |
| Boot options (ADM-30) | (n/a) | (n/a) | REVERT |
| Latency sensitivity (ADM-47) | (n/a) | (n/a) | OBSERVE + Drift condition (v2: lift to first-class spec field) |
| NUMA / cpuAffinity (ADM-48) | (n/a) | (n/a) | OBSERVE + Drift condition |
| vApp / OVF env (ADM-49) | (n/a) | (n/a) | OBSERVE + BootstrapDrift condition |
| Tools settings (ADM-52) | (n/a) | (n/a) | OBSERVE |
| VMX flags (ADM-54) | (n/a) | (n/a) | OBSERVE + FlagsDrift condition |
| Disable console (ADM-24) | (n/a) | (n/a) | OBSERVE (do not stomp `RemoteDisplay.*`) |
| Disable methods (ADM-25, 39) | (n/a) | OBSERVE (VADP common) | OBSERVE + DisabledMethods condition |
| RBAC change (ADM-40) | (n/a) | (n/a) | OBSERVE (event-only audit) |
| MKS ticket / guest ops (ADM-55, 56) | (n/a) | (n/a) | OBSERVE (event-only audit) |
| ManagedBy change (ADM-58) | (n/a) | (n/a) | REVERT (re-set `ManagedByExtensionKey`); if revert fails, emit Critical alert and SUSPEND further reverse-reconcile (we may be losing the VM) |
| Unregister (ADM-26, 43) | (n/a) | (n/a) | LOST (do not auto-re-register in v1) |
| Destroy (ADM-27) | (n/a) | (n/a) | LOST |
| Mark as template / VM (ADM-28, 42) | (n/a) | (n/a) | REVERT (`MarkAsVirtualMachine`); if VM is in use, REVERT immediately |
| FT (ADM-31) | OBSERVE (`status.faultTolerance`); evict secondary MoRef from `watcher.Cache` on role-swap per CC3-16 | (n/a) | OBSERVE |
| HA override (ADM-32) | (n/a) | (n/a) | OBSERVE (`status.haProtection`) |
| HA failover (ADM-33) | OBSERVE (info condition + LastHAFailoverTime) | (n/a) | (n/a) |
| Replication (ADM-34) | (n/a) | OBSERVE | OBSERVE |
| DRS rules (ADM-35) | (n/a) | (n/a) | OBSERVE; AffinityDrift defined as: "DRS rule R mentions VM V, AND R was NOT authored by `VirtualMachineGroup` (identified by the operator's rule-name prefix `vmop-vmg-<vmgroup-uid>-`), AND V is in `spec.affinity` scope" (CC3-22). Surface as `VirtualMachineAffinityDrift{Reason=ForeignDrsRule, Foreign=<rulename>}`. |
| Cluster VM Overrides (ADM-36) | (n/a) | (n/a) | OBSERVE |
| Scheduled tasks (ADM-41) | (n/a) | (n/a) | OBSERVE + ScheduledTaskActive condition |
| Triggered alarm action (ADM-57) | OBSERVE | (n/a) | OBSERVE |
| Direct ESXi (ADM-60) | (n/a) | (n/a) | OBSERVE; the resync backstop is the detection mechanism |

### 6.5 Invariant guards

Before any ADOPT, verify:

1. **Class invariant**: For any spec change touching CPU/memory/devices that come from a `VirtualMachineClass`, refuse adoption — the class is the source of truth. Force REVERT.
2. **EncryptionClass invariant**: Same for crypto.
3. **StorageClass immutability**: Already enforced by webhook; for sVMotion observations, only update status. If observed datastore does not match the SPBM policy bound to the StorageClass, emit `StorageClassDrift` condition and queue a corrective relocate.
4. **MinHardwareVersion monotonicity**: For `UpgradeVM_Task` observations, accept observed value if `observed >= spec.minHardwareVersion`; otherwise REVERT.
5. **Webhook approval**: The validation webhook today (`virtualmachine_validator.go`) has no bypass for the immutability rules touched by ADOPT (`spec.storageClass`, network MTU/MAC, etc.). A new helper `IsReverseReconcileWrite(ctx, vm)` (CC2-04) must be added that returns true iff ALL of: (a) `IsVMOperatorServiceAccount(ctx.UserInfo)`, (b) the request carries the `last-adopt-source: vcenter` annotation, AND (c) the spec diff is contained within the `adoptablePathWhitelist` (tuning ConfigMap §8.2). Specific immutability checks (current lines 2454-2469, 2490+, 2540) gain a new "bypass if `IsReverseReconcileWrite`" branch. The `system:masters` / `IsKubeAdmin` paths must NOT trigger the bypass — explicitly tested by negative unit tests.
6. **Cardinality safety**: If adoption would result in `spec.network.interfaces=[]` for a powered-on VM, refuse — that is a quarantine that consumer should not own.
7. **ExtraConfig reserved prefixes (CC2-14)**: ADOPT MUST NOT target `spec.advanced.extraConfig` keys whose prefix matches `systemReservedExtraConfigPrefixes` (currently `vmservice.*`, `guestinfo.*`, etc.) nor exact keys in `systemReservedExtraConfigKeys`. Such observations are downgraded to OBSERVE + `ReservedExtraConfigSkipped` event. Any ADOPT into `extraConfig` MUST use strategic merge keyed by `key` (`+listType=map +listMapKey=key`), never a list replacement.
8. **Import window respect (CC2-16)**: If the VM has `ImportedVMAnnotation`, `RestoredVMAnnotation`, OR `FailedOverVMAnnotation`, force decision = OBSERVE for ALL paths until the annotation is removed (by the import/restore/failover controller) OR `ImportWindowExpiry` elapses (default 30 min). Surface `VirtualMachineImportWindowActive=True` condition.

If any invariant fails, downgrade ADOPT → REVERT and emit an `InvariantViolationDowngrade` Event so the operator's behaviour is observable.

### 6.6 Conflict resolution (timestamp / generation)

For any DUAL_SURFACE op where ADOPT is the default decision, resolve the race between admin (vCenter) and consumer (K8s).

#### 6.6.1 Sources of truth

- `tVC` = `e.VCChangeTime`. For property-collector signals this is the propagation timestamp from the `WaitForUpdatesEx` set; for Event Manager signals it is `Event.CreatedTime`. Both are vCenter wall-clock.
- `tK8s` per adopted path:
  - **Preferred**: `LastTransitionTime` of the most-relevant `*Synced` condition for that path. For `spec.powerState`, this is `VirtualMachinePowerStateSynced`. For `spec.className`-derived hardware, this is `VirtualMachineClassConfigurationSynced`. Each condition's `LastTransitionTime` is updated by the standard controller after a successful reconcile of that subsystem.
  - **Fallback**: server-side-apply `metadata.managedFields[]` `.fieldsV1` entry timestamp for the relevant field path. Only available when the consumer used SSA (`kubectl apply --server-side` or controller-runtime patches with `FieldManager`). If neither SSA nor a `*Synced` condition is available, `tK8s` is undefined → see §6.6.3.

> **CC1-07 resolution:** The previously-referenced `vm.Status.LastReconcileTime` does not exist in v1alpha6. Use the `*Synced` condition's `LastTransitionTime` instead (these conditions are already standard and per-subsystem). Add a new condition `VirtualMachineBootOptionsSynced` if BootOptions needs ADOPT (currently CONSUMER_ONLY, so deferred).

#### 6.6.2 Decision

- `tVC > tK8s + skew` → ADOPT (admin is newer; consumer's intent already converged).
- `tK8s > tVC + skew` → REVERT (consumer just expressed intent that contradicts admin; next reconcile re-applies spec).
- `|tVC - tK8s| ≤ skew` → emit `ConcurrentAdminConsumerChange` Event; default ADOPT but ALSO set `VirtualMachineConcurrentChangeDetected{Status=True,Reason=AdminAndConsumerWithinSkew}` for the next reconcile cycle. Consumer can re-apply to unambiguously win.

Skew tolerance: `conflictSkewSeconds` (default 30s; tuning ConfigMap §8.2).

#### 6.6.3 Undefined-`tK8s` fallback

When `tK8s` is undefined (no `*Synced` condition for the path, no SSA managedFields entry):

- Treat as `tK8s == VM.creationTimestamp` (the consumer has never "touched" this field).
- This always yields `tVC > tK8s + skew` → ADOPT, which is the desired safe default for a field the consumer is not actively managing.

#### 6.6.4 Special cases

- For `OUT_OF_SCOPE` ops (folder reparent, vTPM add/remove, encryption decrypt of class-managed VMs): no conflict to resolve; operator always reverts.
- For `PausedVMLabelKey ∈ {admin, vendor, infra}`: admin/vendor/infra wins — no timestamp check; decision degrades to OBSERVE for the duration of the pause.

### 6.7 Apply mutation

#### 6.7.0 Vendor-event coalescing (CC3-09)

A single VADP backup window emits ≥4 events per VM (snapshot.add, disk.add, disk.remove, snapshot.remove). Without coalescing, the 10-entry `status.adminActivity[]` ring is dominated by backup noise.

The decision engine coalesces consecutive `Source=VENDOR` events with `Path ∈ {snapshot, rootSnapshot, config.hardware.device}` for the same `(MoRef, PrincipalName)` arriving within `VendorCoalesceWindow` (default 30 min) into a single ring entry:

```
AdminActivityEntry{
    Operation:     "VENDOR_BACKUP_WINDOW",
    Source:        "VENDOR",
    PrincipalName: <vendor SA>,
    Decision:      "OBSERVE",
    Path:          "<n events coalesced: paths…>",
    Message:       "Backup window started at <t0>; n events; latest at <tn>",
}
```

Metric `vmoperator_reverse_reconcile_vendor_coalesce_total{principal}` counts coalesces. Status fields (e.g. `status.rootSnapshots[]` for Unmanaged refs) update on every event; only the audit ring is coalesced.

#### 6.7.0.5 Status echo cache (CC3-08)

The reverse-reconcile controller maintains a process-local cache:

```go
type StatusEchoCache map[string]string  // key = "<ns>/<name>:<path>", value = sha256(payload)
```

Used to short-circuit redundant status patches when a property re-fires with no change (typical for DRS host churn or VKS pod churn that propagates through `config.hardware.device`). Behavior:

- On startup AND on leader-election handoff: seed the cache from existing `vm.Status.*` fields via a one-time list-then-fill pass (avoids the thundering herd from CC3-08).
- On `AdminEvent`: compute `sha256(NewValue)`; if it matches the cache entry for `(ns/name, path)`, **drop the event before any K8s API call**, increment metric `vmoperator_reverse_reconcile_echo_cache_hits_total`.
- On status write success: update the cache to the new hash.

The cache is bounded by total #VMs × #watched-paths; at 25k VMs × ~25 paths × 64-byte SHA = ~40 MiB. Acceptable.

#### 6.7.X Execution paths

Three execution paths:

#### 6.7.1 OBSERVE

- Update `status.*` fields per op.
- Update conditions via `pkgcond.MarkTrue/MarkFalse`.
- Append to `status.adminActivity[]` ring buffer.
- Emit `Event` via `pkgrecord`.

No spec patch. No retry needed (server-side resource version conflicts are retried by client-go's standard machinery).

#### 6.7.2 ADOPT

- **Idempotency token (CC4)**: compute `adoptKey = sha256(MoRef + setVersion + path + newValue)`. Persist this key as the value of the `last-adopt-event-uid` annotation. On controller restart, if an incoming `AdminEvent` would generate the same `adoptKey` already present in the annotation, skip the ADOPT (it succeeded) and proceed straight to status/event/audit-ring (which may also already have an entry — see CC4 in §16).
- **Patch construction**: build a strategic-merge patch keyed by the adopted path. Base it on a fresh `client.MergeFrom(oldObj)` containing ONLY the adopted path's old/new values — do NOT carry unrelated diffs from a stale cache fetch (CC2-07).
- **Optimistic lock**: `MergeFromWithOptimisticLock{}` — fails fast on `resourceVersion` conflict, no silent overwrite.
- **Annotations on the patch**:
  - `vmoperator.vmware.com/last-adopt-source: vcenter`
  - `vmoperator.vmware.com/last-adopt-event-uid: <adoptKey>`
  - `vmoperator.vmware.com/last-adopt-time: <RFC3339>`
- **Retry, split by error class (CC2-07)**:
  - `IsConflict(err)` (HTTP 409, resourceVersion mismatch) → re-fetch + rebuild + retry, up to `AdoptionRetryMax` (default 3) with `50ms * attempt` backoff.
  - `IsForbidden(err)` / `IsInvalid(err)` (HTTP 403 / 422, webhook reject) → do NOT retry; emit `VirtualMachineSpecAdoptionRejected{Reason=<field path from API error>}` Warning Event AND set `VirtualMachineSpecAdoptionRejected` condition; downgrade decision to OBSERVE for this event; record in audit ring.
  - Any other error → exponential backoff, capped at 3 attempts.
- On success, emit `VirtualMachineSpecAdopted` Event with the diff.
- **`status.observedGeneration` is NOT written by reverse-reconcile (CC2-09)**. That field is owned by the standard `VirtualMachine` controller and signals "I converged on the latest spec". If we wrote it here, consumers polling `observedGeneration == generation` would conclude the VM is reconciled before the standard controller has executed. Instead, the per-condition `observedGeneration` field on the `VirtualMachineSpecAdopted` condition (metav1.Condition carries its own) is the appropriate signal.

Important: ADOPT must NOT touch `status` in the same patch (different sub-resource). Status mutations are a separate API call. The status writer contract (CC2-10) requires using the project's `patch.NewHelper`/`helper.Patch` flow; raw `client.Update(...).Status()` on `VirtualMachine` is disallowed and enforced by a CI lint.

##### 6.7.2.1 Mutator interaction (CC2-05)

The mutating webhook must NOT strip `last-adopt-*` annotations on the same admission request that introduces them. Today's mutator order is mutating → validating; stripping before validation means the validator's `IsReverseReconcileWrite()` bypass cannot fire. The mutator MUST:

- Skip stripping when `ctx.UserInfo` is the operator service account.
- Allow the annotation to persist on the object until the controller's next reconcile, then strip via a normal patch (not in the mutator).
- TTL on the annotation: 5 minutes (configurable). After TTL, the next K8s reconcile of the VM removes it.

#### 6.7.3 REVERT

- The operator does NOT directly call the vSphere API to undo the admin change.
- Instead, it triggers a normal reconcile of the existing VirtualMachine controller, which re-applies `spec` to `vSphere` as part of its standard loop.
- Mechanism: emit an event via `cource.FromContextWithBuffer(ctx, "VirtualMachine", 100) <- event.GenericEvent{Object: vm}` (the same channel `vm-watcher` already uses).
- The VirtualMachine controller's reconciler observes spec-vs-actual drift on the relevant property and corrects it.
- Emit `VirtualMachineAdminChangeReverted` Event.

This decoupling is critical: the reverse-reconcile controller is a **decision engine** that nudges the standard reconcile pipeline; it does not have its own provider-API client.

#### 6.7.4 LOST

- Set `VirtualMachineConditionCreated=False, Reason=<DestroyedOutOfBand|UnregisteredOutOfBand|MigratedToOtherVC>`.
- Set new condition `VirtualMachineLost=True, Reason, Message`.
- Clear `status.uniqueID` ONLY for `DestroyedOutOfBand` (so MoRef cache doesn't reattach to a recycled ID). For `UnregisteredOutOfBand` and `MigratedToOtherVC`, retain `status.uniqueID` (it's needed for re-registration / lookup).
- Do NOT delete the K8s VM CR.
- Do NOT delete bound PVCs (CSI handles their lifecycle on actual disk loss).
- Emit `VirtualMachineLost` Event with a human-readable cause.

---

## 7. API impact (v1alpha6)

### 7.1 New conditions

| Condition | Status | Reason values |
|---|---|---|
| `VirtualMachineSpecAdopted` | True after adoption; False after `OperationDuration` elapses | `PowerStateAdopted`, `SnapshotsObserved`, `PlacementObserved`, `DevicesObserved`, ... |
| `VirtualMachineLost` | True if MoRef is destroyed / unregistered / xVC-moved | `DestroyedOutOfBand`, `UnregisteredOutOfBand`, `MigratedToOtherVC` |
| `VirtualMachineStorageClassDrift` | True if observed datastore violates StorageClass→SPBM mapping | `DatastoreNotPolicyCompliant` |
| `VirtualMachineAffinityDrift` | True if cluster DRS rules contradict `spec.affinity` | `DrsRuleConflict` |
| `VirtualMachineBootstrapDrift` | True if `config.vAppConfig` diverges from spec-implied bootstrap | `VAppConfigChanged` |
| `VirtualMachineFlagsDrift` | True if `config.flags` diverges from defaults / class | `VBSToggled`, `MonitorTypeChanged`, ... |
| `VirtualMachineDisabledMethods` | True if `disabledMethod` is non-empty | `DisabledMethods` |
| `VirtualMachineScheduledTaskActive` | True if a vCenter scheduled task targets this VM | `ScheduledTask` |
| `VirtualMachineConcurrentChangeDetected` | Transient — set during conflict, cleared on next reconcile | `AdminAndConsumerWithinSkew` |
| `VirtualMachineInfraDrift` | Composite umbrella — True if any of the above are True | sums sub-reasons |

All conditions are managed via the existing `pkg/conditions` helpers.

### 7.2 New status fields

> **CC2-02 resolution:** `VirtualMachineProviderStatus` already exists in `api/v1alpha6/virtualmachine_types.go` with `{CreationTimestamp}`. The new struct uses a different name (`VirtualMachineVSphereObservedStatus`) and is exposed under `status.vsphereObserved` to avoid silently dropping the existing `Provider.CreationTimestamp` field.

> **CC2-15 resolution:** All new list fields have explicit `+listType`/`+listMapKey` markers.

```go
type VirtualMachineStatus struct {
    // ... existing fields, including the existing Provider field, UNCHANGED ...

    // AdminActivity is a ring buffer of the last AuditRingSize admin operations
    // detected on this VM through reverse-reconcile.
    // +optional
    // +listType=atomic
    // +kubebuilder:validation:MaxItems=10
    AdminActivity []AdminActivityEntry `json:"adminActivity,omitempty"`

    // VSphereObserved exposes vSphere-side metadata observed by the
    // reverse-reconcile framework. Read-only; not used by the controller for
    // spec reconciliation.
    // +optional
    VSphereObserved *VirtualMachineVSphereObservedStatus `json:"vsphereObserved,omitempty"`

    // FaultTolerance reflects vSphere Fault Tolerance configuration if any.
    // +optional
    FaultTolerance *VirtualMachineFTStatus `json:"faultTolerance,omitempty"`

    // HAProtection reflects vSphere HA protection state.
    // +optional
    HAProtection *VirtualMachineHAStatus `json:"haProtection,omitempty"`
}

// +kubebuilder:validation:Enum=INFRA;VENDOR;ADMIN;UNKNOWN
type AdminActivitySource string

// +kubebuilder:validation:Enum=OBSERVE;ADOPT;REVERT;LOST
type AdminActivityDecision string

type AdminActivityEntry struct {
    // Operation is the catalog ID (e.g. "ADM-01") or stable short code
    // (e.g. "VENDOR_BACKUP_WINDOW" for coalesced entries).
    // +kubebuilder:validation:Required
    Operation        string                  `json:"operation"`
    // Source classification.
    // +kubebuilder:validation:Required
    Source           AdminActivitySource     `json:"source"`
    // Decision the framework took.
    // +kubebuilder:validation:Required
    Decision         AdminActivityDecision   `json:"decision"`
    // PrincipalName is the vCenter user/service account that performed the op,
    // when known.
    // +optional
    PrincipalName    string                  `json:"principalName,omitempty"`
    // Time the operation was detected by the operator.
    // +kubebuilder:validation:Required
    DetectedAt       metav1.Time             `json:"detectedAt"`
    // VCChangeTime as reported by the property collector / Event Manager.
    // +kubebuilder:validation:Required
    VCChangeTime     metav1.Time             `json:"vcChangeTime"`
    // Path of the change (property path, event type id, or symbolic name).
    // +kubebuilder:validation:Required
    Path             string                  `json:"path"`
    // Short human-readable message.
    // +optional
    Message          string                  `json:"message,omitempty"`
}

type VirtualMachineVSphereObservedStatus struct {
    // Notes echoes config.annotation, if set by an admin.
    // +optional
    Notes              string                 `json:"notes,omitempty"`

    // CustomAttributes echoes vSphere CustomFields/customValue entries.
    // +optional
    // +listType=map
    // +listMapKey=key
    // +kubebuilder:validation:MaxItems=50
    CustomAttributes   []KeyValuePair         `json:"customAttributes,omitempty"`

    // ResourcePoolPath is the vSphere resource pool the VM is in.
    // +optional
    ResourcePoolPath   string                 `json:"resourcePoolPath,omitempty"`

    // FolderPath is the vSphere folder the VM is in.
    // +optional
    FolderPath         string                 `json:"folderPath,omitempty"`

    // DatastoreURL is the home datastore URL.
    // +optional
    DatastoreURL       string                 `json:"datastoreUrl,omitempty"`

    // DisabledMethods is the set of method names disabled via
    // AuthorizationManager.DisableMethods on this VM.
    // +optional
    // +listType=set
    // +kubebuilder:validation:MaxItems=50
    DisabledMethods    []string               `json:"disabledMethods,omitempty"`
}

type VirtualMachineFTStatus struct {
    // State is the observed Fault Tolerance state of this VM
    // (e.g. "notConfigured", "starting", "running", "needSecondary").
    // +optional
    State            string         `json:"state,omitempty"`
    // SecondaryHost is the host of the FT secondary, when configured.
    // +optional
    SecondaryHost    string         `json:"secondaryHost,omitempty"`
}

type VirtualMachineHAStatus struct {
    // ProtectionState is the observed DAS protection state
    // ("protected", "unprotected", "disabled").
    // +optional
    ProtectionState  string         `json:"protectionState,omitempty"`
    // LastFailoverTime records the most recent HA-driven restart of this VM.
    // +optional
    LastFailoverTime *metav1.Time   `json:"lastFailoverTime,omitempty"`
    // LastFailoverHost is the host the VM was restarted on by HA.
    // +optional
    LastFailoverHost string         `json:"lastFailoverHost,omitempty"`
}
```

> **CC2-03 resolution:** `Source` and `Decision` use typed string enums with `+kubebuilder:validation:Enum`. Required fields carry `+kubebuilder:validation:Required`. `VirtualMachineFTStatus` and `VirtualMachineHAStatus` are now fully defined.

> **CC2-08 resolution:** All new list fields carry `MaxItems` caps to keep the v1alpha5/v1alpha6 conversion-data annotation under the 256 KB ceiling. Worst-case payload: `10 audit entries × ~500 bytes + 50 customAttrs × ~200 bytes + 100 tags × ~50 bytes + 50 disabledMethods × ~30 bytes ≈ 21 KB`, well within budget.

### 7.3 New annotations

| Annotation | Set by | Value grammar | Purpose |
|---|---|---|---|
| `vmoperator.vmware.com/last-adopt-source: vcenter` | reverse-reconcile | literal `vcenter` | Marks the most recent spec patch as adoption-driven; consumed by webhook (§7.4) |
| `vmoperator.vmware.com/last-adopt-event-uid: <uid>` | reverse-reconcile | 64-char hex sha256 | Idempotency token (CC4) |
| `vmoperator.vmware.com/last-adopt-time: <RFC3339>` | reverse-reconcile | RFC3339 timestamp | Audit trail |
| `vmoperator.vmware.com/paused-by-source: vendor\|admin\|infra` | reverse-reconcile | enum | Attributes the source of an automatic pause flip (CC3-20) |
| `vmoperator.vmware.com/paused-by-principal: <principal>` | reverse-reconcile | RFC-3986 unreserved chars, max 256 | Principal name that triggered the pause |
| `vmoperator.vmware.com/admin-managed-devices: <keys>` | privileged user | regex `^[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*$`, max length 1024 | Comma-separated `VirtualDevice.key` values to exclude from reconcile. **Wildcards NOT supported.** Invalid values trigger `VirtualMachineAdminManagedDevicesInvalid=True` and the annotation is treated as absent (fail closed). |
| `vmoperator.vmware.com/admin-managed-nics: <ifnames>` | privileged user | regex `^[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*$`, max length 1024 | Same, by interface name. Wildcards NOT supported. |
| `vmoperator.vmware.com/backup-proxy: "true"` | privileged user | literal `true` | Marks a VM as a backup-vendor proxy (hot-add transport mode). Disk attach/detach observed on this VM is OBSERVE-only with NO status mutation (CC3-18). |

> **CC2-11 resolution:** Each annotation has a documented grammar enforced by a new validating-webhook entry. Malformed values fail closed (annotation treated as absent + a Drift condition fires).

### 7.4 Webhook changes

> **CC2-04 / CC2-05 resolution:** the previous text contained two incorrect claims — that the existing privileged-user gate already enables ADOPT bypass, and that the mutator can safely strip adopt annotations. Both are corrected below.

#### 7.4.1 Validation webhook

`webhooks/virtualmachine/validation/virtualmachine_validator.go`:

- Add a new helper `IsReverseReconcileWrite(ctx, oldVM, newVM) bool` that returns true ONLY if ALL hold:
  1. `IsVMOperatorServiceAccount(ctx.UserInfo)` (NOT the broader `IsPrivilegedAccount`; `system:masters` / `IsKubeAdmin` / `PRIVILEGED_USERS` MUST NOT bypass).
  2. The new object carries `vmoperator.vmware.com/last-adopt-source: vcenter`.
  3. The spec diff (`oldVM.Spec` → `newVM.Spec`) is entirely within `adoptablePathWhitelist` (tuning ConfigMap §8.2).
- Specific immutability checks gain a new bypass branch keyed off `IsReverseReconcileWrite`:
  - `spec.storageClass` (current line ~2466).
  - `spec.network.interfaces[*].mtu` (current line ~2490+).
  - `spec.network.interfaces[*].macAddr` (current line ~2540).
  - Any other field listed in `AdoptablePathWhitelist`.
- For `OUT_OF_SCOPE` ops (folder reparent — but this is implemented via a vSphere call, not a spec patch — so no validator change needed; vTPM / encryption — these are CONSUMER_ONLY, reverted via the standard reconciler), NEVER permit a spec patch that would violate the class/encryption invariants. Reject with a clear error.
- Negative unit tests: a request from `system:masters` / `kube-admin` / a `PRIVILEGED_USERS` entry carrying a forged `last-adopt-source: vcenter` annotation MUST still be rejected on the same immutability fields.

#### 7.4.2 Mutation webhook

`webhooks/virtualmachine/mutation/virtualmachine_mutator.go`:

- Do NOT strip `last-adopt-*` annotations on the SAME admission request that introduces them (the validator runs after the mutator; stripping would defeat the §7.4.1 bypass).
- For subsequent updates by non-operator users that still carry stale `last-adopt-*` annotations (e.g., a user runs `kubectl edit` and saves the unmodified annotations through), strip them.
- The reverse-reconcile controller separately removes the annotation 5 minutes after writing it (TTL via the next reconcile of the VM). This keeps annotations bounded without racing the validator.

#### 7.4.3 Annotation grammar validation

A new validating-webhook entry rejects malformed values for `admin-managed-devices`, `admin-managed-nics`, and `backup-proxy` per the grammars in §7.3.

#### 7.4.4 `paused` label value extension

The `PausedVMLabelKey` value space, today restricted to `admin|devops|both`, is extended to `admin|devops|both|vendor|infra`. The privileged-user gate on label writes is unchanged; the reverse-reconcile controller (which runs as the vm-operator SA per §6.3.4) writes these new values.

---

## 8. Configuration

### 8.1 Feature flag

There is exactly **one** feature flag:

| Flag | Type | Default | Effect |
|---|---|---|---|
| `Features.AdminReverseReconcile` | bool | `false` | Master switch. When `true`, all detection sources, the decision engine, and the new webhook paths are active. |

**Rationale (rev 2):** The previous design had 8 boolean sub-flags (`WatchPower`, `WatchPlacement`, etc.). These sub-flags are not toggleable at runtime by the admin persona — they only change via operator deployment config, which is infra-admin owned. Having half-a-dozen flags adds configuration-matrix complexity with no operational benefit; the admin persona turns the feature on or off — not fine-grained subsystems. Instead, the Event Manager subscriber (the only optional detection subsystem) is controlled by a **tuning knob** in a ConfigMap (§8.2), which the operator reads at startup and on live-reload without requiring a restart or feature-gate bump.

When `AdminReverseReconcile=false`:
- No additional property paths are added to `DefaultWatchedPropertyPaths()`.
- Event Manager subscriber and periodic resync do not start.
- The reverse-reconcile controller does not register.
- Webhook bypass (`IsReverseReconcileWrite`) always returns `false`.
- All new status fields, conditions, and annotations are inert (not written to).

### 8.2 Tuning ConfigMap (`vmoperator-reverse-reconcile-config`)

A namespace-scoped ConfigMap read by the operator at startup and on live-reload (via a watch). All keys have sensible defaults; the ConfigMap is optional — the operator falls back to built-in defaults when absent.

| Key | Type | Default | Effect |
|---|---|---|---|
| `subscribeEvents` | bool | `false` | Enables Event Manager subscriber. Required for power-state ADOPT (see §6.2.3). When `false`, power-state changes are OBSERVE-only. |
| `periodicResync` | bool | `true` | Enables periodic full-property resync (§5.1.4). |
| `resyncIntervalSeconds` | int | `1800` | Resync interval (30 min). |
| `conflictSkewSeconds` | int | `30` | Last-writer-wins skew tolerance. |
| `adoptionRetryMax` | int | `3` | Spec adoption retry count on optimistic-lock conflict. |
| `auditRingSize` | int | `10` | `status.adminActivity[]` max length. |
| `adminEventQueueSize` | int | `4096` | Aggregator channel buffer depth. |
| `infraPrincipalPatterns` | []string (JSON) | `["^vpxd-extension(-.*)?$", "^vpxd-extension-vcf-vps-.*$", "^system@vsphere\\\\.local$", "^wcp-.*$", "^wcpsvc@.*$"]` | Principal name regexes classified as INFRA (CC3-04, CC3-05). |
| `adoptablePathWhitelist` | []string (JSON) | `["spec.powerState", "spec.minHardwareVersion"]` (v1; expanded incrementally) | Spec paths that ADOPT may modify; enforced by §7.4.1 webhook bypass. |
| `eventReplayWindow` | duration | `30m` | Event Manager lookback on startup / leader handoff (CC3-02). |
| `vendorCoalesceWindow` | duration | `30m` | Coalescing window for VENDOR backup events (CC3-09). |
| `importWindowExpiry` | duration | `30m` | OBSERVE-only window after Imported/Restored/FailedOverVMAnnotation (CC2-16). |
| `revertPingPongWindow` | duration | `5m` | Window for ping-pong detection on any REVERT op (CC3-21). |
| `revertPingPongMax` | int | `3` | Number of reverts within window before escalation to LOST. |
| `revertPingPongCooldown` | duration | `1h` | Cooldown before ping-pong counter resets. |
| `firstEnableShadowDuration` | duration | `1h` | On first enablement, ALL decisions degrade to OBSERVE for this duration (CC4). |

### 8.3 Vendor allow-list ConfigMap (`vmoperator-reverse-reconcile-vendors`)

A separate ConfigMap (may reside in the same namespace) for vendor/VADP principal patterns. This is intentionally separate so it can be shipped as a Helm chart default and overridden per deployment.

| Key | Type | Default | Effect |
|---|---|---|---|
| `vendorPrincipals` | []string (JSON) | `[]` (empty) | Principal name regexes classified as VENDOR. Until populated, ALL VENDOR-class decisions degrade to OBSERVE and emit a Warning Event (CC3-06). |

---

## 9. Observability

### 9.1 Conditions

See §7.1.

### 9.2 Kubernetes Events

| Event | Reason | Severity |
|---|---|---|
| `VirtualMachineSpecAdopted` | per op | Normal |
| `VirtualMachineAdminChangeReverted` | per op | Normal |
| `VirtualMachineSpecAdoptionConflict` | `OptimisticLockFailed` | Warning |
| `InvariantViolationDowngrade` | `<Invariant>` | Warning |
| `ConcurrentAdminConsumerChange` | `WithinSkewWindow` | Warning |
| `VirtualMachineLost` | per LeaveKind | Warning |
| `ManagedByExtensionChanged` | `Hostile` | Critical (paging) |

### 9.3 Metrics

Prometheus metrics under prefix `vmoperator_reverse_reconcile_`:

- `events_total{source,kind,decision}` (counter)
- `adoption_conflicts_total{path}` (counter)
- `invariant_violations_total{invariant}` (counter)
- `lost_vms_total{cause}` (counter)
- `resync_duration_seconds{stage}` (histogram)
- `pending_events_queue_depth` (gauge)
- `aggregator_drops_total{source,reason}` (counter)

### 9.4 Audit ring buffer

`status.adminActivity[]` exposes the last 10 events. Larger retention belongs in the K8s event stream or external SIEM via the operator's existing event sinks.

---

## 10. RBAC, leader election, and security

### 10.1 Kubernetes RBAC (CC2-06)

The reverse-reconcile controller is deployed in the SAME `Deployment` and `Manager` as the existing `VirtualMachine` controller, runs under the SAME vm-operator ServiceAccount, and inherits the manager's leader election (`NeedLeaderElection() == true` is automatic for controller-runtime managers). It needs the following RBAC additions, expressed as `+kubebuilder:rbac` markers on the controller:

```go
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch  // for AdminReverseReconcileVendors
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete  // for leader election
```

Without these, the first status write fails with `forbidden` and the framework is silently broken.

### 10.2 Leader election scope (CC4)

The reverse-reconcile controller MUST share the leader election lease with the `vm-watcher` service. Today `vm-watcher` is leader-elected (`Service.NeedLeaderElection() returns true`). Co-locating the reverse-reconcile controller in the same Manager guarantees:

- A single process holds the watcher state AND the decision engine state, so the §6.7.0.5 status-echo cache and the §6.2.1 paired-event cache are coherent.
- On leader handoff, BOTH the watcher and the decision engine restart together; the §6.2.4 event replay covers both.

### 10.3 vCenter permissions (CC2-12)

§10 previously claimed "no new vSphere permissions needed". That is incorrect. The new detection surfaces require:

| Capability | vCenter privilege |
|---|---|
| Property Collector on cluster / host MOs (§5.1.3) | `System.View` on the relevant Folder/Cluster |
| Event Manager subscription (§5.2) | `Global.Diagnostics` (read events) |
| Reading `PermissionAddedEvent` / `RoleUpdatedEvent` | additional `Authorization.ModifyPermissions` read on some VC builds |
| CIS Tagging read (§5.3) | `InventoryService.Tagging.ObjectAttachable` |
| RetrieveProperties for Leave disambiguation (§5.1.2) | already covered by `System.View` |

A **startup self-check** runs one representative API call per source. On failure: do NOT abort startup (other sources may be working); set cluster-wide condition `VirtualMachineReverseReconcileDegraded{Reason=MissingVCPrivilege, Source=<name>}`. The condition lives on a sentinel object (e.g., a `VirtualMachineReverseReconcileState` per-zone — to be defined; for v1, simpler: emit a per-source Warning Event on every startup and let operators look at logs).

### 10.4 Webhook bypass narrowing

Already detailed in §7.4. Summary: bypass requires `IsVMOperatorServiceAccount` AND `last-adopt-source=vcenter` annotation AND diff-in-whitelist. `system:masters` / `IsKubeAdmin` / `PRIVILEGED_USERS` do NOT bypass.

### 10.5 Annotation editing authority

The new annotations `admin-managed-devices`, `admin-managed-nics`, `backup-proxy` are editable only by privileged users (existing `CheckAnnotation*` gate). The `last-adopt-*` annotations are editable only by the operator SA (the mutator strips user-supplied versions).

---

## 11. Migration / upgrade / rollout

### 11.1 Phased rollout

1. **v0 (current)** — no framework. Admin actions are sometimes detected (power state via existing watcher), often invisible (placement, hardware drift, leave events).
2. **v1 alpha** — `Features.AdminReverseReconcile=false` (default). The new CRD fields (§7.2), new conditions (§7.1), and new annotations (§7.3) ship as additive API changes. Watcher property-path extensions land but are gated behind the single feature flag.
3. **v1 beta** — `Features.AdminReverseReconcile=true` in dev/test deployments. Decision engine emits OBSERVE only (shadow window, §11.2). Validates classification accuracy on real workloads. Tuning ConfigMap (`subscribeEvents=false`) keeps the blast radius small.
4. **v1 GA** — `Features.AdminReverseReconcile=true` in production. Tuning ConfigMap sets `subscribeEvents=true` (required for power-state ADOPT per CC3-01). Leave-event handling enabled.
5. **v2** — Latency-sensitive lift to first-class spec, optional cross-VC auto-recovery.

### 11.2 First-enablement shadow window (CC4)

On first enablement against an existing cluster with N VMs already running, the §5.1.4 periodic resync will fire for all N VMs and detect "drift" for every property an admin has changed historically. Without mitigation, this is a flood of ADOPT/REVERT events.

The framework MUST observe a **first-enable shadow window** of `firstEnableShadowDuration` (default 1 hour; tuning ConfigMap §8.2) after the master flag transitions from `false → true` on a given operator pod. During the shadow window:

- All decisions degrade to OBSERVE.
- Status fields (`adminActivity[]`, `vsphereObserved`, `faultTolerance`, `haProtection`) ARE populated.
- Conditions ARE set.
- NO spec writes, NO REVERT signals to the standard reconciler.

After the shadow window ends, the framework promotes from OBSERVE-only to the full §6.4 decision table. The transition is logged with a single `VirtualMachineReverseReconcileShadowWindowEnded` cluster Event.

Persistence: the start time of the shadow window is recorded in a cluster-scoped ConfigMap (`vm-operator-reverse-reconcile-state`) so a pod restart during the window does not reset the clock.

### 11.3 Rollback

Flipping the master flag off reverts behavior to v0 immediately for new events. In-flight ADOPT spec patches remain (already committed); the standard reconciler operates as before.

Status fields populated during the feature-on period remain in the API (no migration of `status.adminActivity[]` on rollback). This is intentional — the audit trail is valuable even after disable.

### 11.4 CRD compatibility

CRD changes are additive only (new optional fields, new conditions, new annotations). Conversion to v1alpha1..v1alpha5 round-trips through the existing conversion-data annotation; the §7.2 `MaxItems` caps keep this safe per CC2-08.

---

## 12. Race conditions and corner cases

This section documents every known race, ordering hazard, and edge case. Items marked **(NEW r1)** were added after the cross-check review.

| Case | Description | Resolution |
|---|---|---|
| **R1** | Property collector emits multiple changes within a single `set.Version` (e.g., `PauseVMExtraConfigKey=True` AND `powerState=PoweredOff` in same batch). | §6.3.1: aggregator preserves batch boundary as `AdminEventBatch`. Decision engine evaluates pause from the AFTER state of the whole batch. If both pause=True and pause=False appear in one batch, treat as pause=True (conservative). |
| **R2** | Watcher loses connection mid-`WaitForUpdatesEx`; misses updates. | §5.1.4 periodic resync covers the gap. On restart, fresh `RetrieveProperties` for all VMs in the container view (existing behavior). |
| **R3** | Admin renames a VM AND the new name collides with another K8s VM in the same namespace. | §6.4: REVERT issues `ReconfigVM_Task` with original name. If revert fails, surface `RenameRevertFailed` event; K8s VM is unchanged. |
| **R4** | Admin powers off VM; consumer immediately patches `spec.powerState=PoweredOn` within skew window. | §6.6: `ConcurrentAdminConsumerChange` warning; default ADOPT (admin newer); consumer's `PoweredOn` intent re-applies on next standard reconcile. Two transitions, but converges. |
| **R5** | DRS continuously moves a VM (1+ moves/min). | §6.2: INFRA(DRS); OBSERVE only. §6.7.0.5 echo cache prevents redundant K8s patches when host string is unchanged. |
| **R6** | VADP snapshot create/remove every ~4 hours. | §6.2: VENDOR; §6.4: OBSERVE as Unmanaged. §6.7.0 coalescing collapses the 4-event window to 1 ring entry. |
| **R7** | Admin power-offs during consumer's clone-from-snapshot reconcile. | ADOPT writes `spec.powerState=PoweredOff`. Consumer clone sees PoweredOff and pauses/errors cleanly. Admin runbook: assert `PauseVMExtraConfigKey=True` before any destructive op. |
| **R8** | Two parallel admin ops on same VM (vMotion + ReconfigVM). | Both land in the aggregator as separate `AdminEventBatch` items; controller processes serially via controller-runtime workqueue (one item per VM at a time). |
| **R9** | Admin destroys VM; K8s VM has finalizer. | LOST; condition set; CR NOT deleted; finalizer remains. Consumer runs `kubectl delete vm` to clean up PVCs. Existing finalizer logic handles VM-not-found gracefully. |
| **R10** | Cross-VC migration: destination VC vm-operator tries to adopt the new MoRef. | Destination MoRef has no `status.uniqueID` index match → destination watcher drops it. VM stays as unrecognized until admin runs `ImportVM` on destination Supervisor. |
| **R11** | Leave event races with `RetrieveProperties` disambiguation: VM is re-registered before the follow-up call completes. **(NEW r1)** | On disambiguation, check BiosUUID/InstanceUUID of the newly-found VM against the K8s `status.biosUUID`. Match → it was briefly unregistered and re-registered: emit `Unregistered` + `Registered` pair. Mismatch → `LeaveAmbiguous`. |
| **R12** | `watcher.Cache` keyed by MoRef holds stale entry after MoRef reuse (Destroy + re-Register with same value, rare). | On `Enter` for a known MoRef whose cached BiosUUID does not match the K8s VM's, evict the cache entry and proceed as fresh `Enter`. |
| **R13** | `ManagedBy` cleared by hostile admin (`ADM-58`). | §A.3: REVERT re-sets `ManagedByExtensionKey`. If REVERT fails (admin also used DisableMethods to block `ReconfigVM`), set `VirtualMachineLost{Reason=ManagedByLost}`, emit Critical event, SUSPEND all further reverse-reconcile. |
| **R14** | Scheduled task fires nightly to power off VM; consumer wants it always on. | Each fire is detected as ADM-01 PowerOff and ADOPTED. Consumer must remove the scheduled task at vSphere; `ScheduledTaskActive` condition surfaces the conflict. |
| **R15** | Leader switch during active reconcile. | On new-leader startup, §6.2.4 event replay (up to `EventReplayWindow`) runs before any decision is made. Periodic resync fires once aggressively at election time. |
| **R16** | Property collector returns null for a deleted optional property. | Treated as REMOVE. Handler classifies per op (e.g., `config.managedBy=null` → REVERT). |
| **R17** | `customValue` change includes a new field definition not yet cached. | Look up `CustomFieldsManager.availableField[].key` on first observation; cache locally keyed by field-key integer. |
| **R18** | Admin enables HA `dasVmConfig` "no restart" then host fails; consumer sees PoweredOff with no host change. | §6.2: `VmDasResetFailedEvent` or `NotEnoughResourcesToStartVmEvent` triggers INFRA classification if EventSub is on. Without EventSub: UNKNOWN → OBSERVE (power state never ADOPTED without corroborating HA event per §6.2.3). `VirtualMachineHAProtection{ProtectionState=unprotected}` is set when `dasVmProtection.dasProtected=false` is observed (pre-failure signal). |
| **R19** | VC client invalid on startup; EventSub / Watcher all fail. | Existing `IsInvalidLogin`/`IsNotAuthenticatedError` retry loop in `vm_watcher_service.go`. All new detection sources follow same retry. Periodic resync runs only when cache is warm; otherwise would generate spurious LOST on stale caches. |
| **R20** | Burst of 1000 admin ops (admin reorganizes 1000 VMs into a new folder). | Aggregator channel default depth 4096 (`AdminEventQueueSize`); drops counted in `aggregator_drops_total`; periodic resync recovers any dropped events. |
| **R21** | 100k VMs with CD-ROM connect/disconnect on reboot. | Existing watcher already sees `config.hardware.device` changes. Per-op handler for CD-ROM connect/disconnect is OBSERVE-only → no K8s write. Echo cache (§6.7.0.5) suppresses redundant observations. See §5.5 for full scale analysis. |
| **R22** | Admin runs `MarkAsTemplate` on powered-on VM. | vSphere rejects (template requires power-off). If admin first powers off then templates: PoweredOff → ADOPT; template flip → REVERT (`MarkAsVirtualMachine`); standard reconcile re-applies `spec.powerState`. Final state: VM, powered-on. |
| **R23** | `lookupNamespacedName` returns wrong VM if MoRefs collide across VC restarts. | Existing `status.uniqueID` index protection. No new MoRef-keyed caches added by this framework. |
| **R24** | Two ADOPTs on same path within milliseconds. **(NEW r1)** | `MergeFromWithOptimisticLock` ensures only one wins; other fails `Conflict`; §6.7.2 retry re-fetches and re-evaluates. End-state is the later change per the new fetch's timestamp comparison. |
| **R25** | Controller crashes after ADOPT but before status/condition write. **(NEW r1)** | Idempotency token (`last-adopt-event-uid` = `sha256(MoRef+setVersion+path+value)`) ensures re-processing the same `AdminEventBatch` is a no-op. Status/condition re-write is idempotent. Convergence preserved; audit ring may have a single entry gap (acceptable). |
| **R26** | VADP vendor SA mis-allow-listed → classified as ADMIN. | With empty default allow-list (CC3-06): snapshot ops are OBSERVE regardless. Disk/NIC/method ops emit Warning event but do not REVERT when `Source=VENDOR` is absent. After correct allow-list is populated, the warning stops. |
| **R27** | Consumer and admin patch `spec.powerState` in same microsecond. | Both intend same end-state in the common case; §6.6 timestamp comparison resolves; audit ring records concurrent change with source attribution. |
| **R28** | REVERT ping-pong on any op (e.g., folder reparent, RP reassignment, storage relocate). | **Generalized** (not just folder): track revert count per `(VM, op)` within `RevertPingPongWindow` (default 5 min). After `RevertPingPongMax` (default 3) reverts, set `VirtualMachineLost{Reason=<Op>RevertPingPong}`. Counter resets after `RevertPingPongCooldown` (1 hour). |
| **R29** | EventSub delivers `VmReconfiguredEvent` BEFORE the paired property change arrives. **(NEW r1)** | §6.2.1 paired-event cache has `PairWindow=60s`. If event arrives first, it is cached; when the property change arrives, it finds the cached event and is corroborated. If property change arrives first and no event appears within the window, source falls through to heuristics 4-9 (principal-based). |
| **R30** | SDRS storage ping-pong (SDRS moves VM back after operator's corrective relocate). **(NEW r1)** | §6.4 ADM-09/50: REVERT for storage vMotion is disabled; only `StorageClassDrift` condition is set (CC3-21). SDRS can operate freely; admin must resolve the SPBM policy conflict manually. |
| **R31** | FT secondary VM floods `watcher.Cache`. **(NEW r1)** | §6.1 step 1: secondary MoRef is dropped (MoRef != `status.uniqueID`). §5.1.0 filter drops it before reaching the aggregator. Secondary MoRef is NOT stored in `watcher.Cache`. |
| **R32** | Operator-initiated REVERT is mis-attributed as a new admin event. **(NEW r1)** | The standard reconciler runs as the vm-operator SA. §6.2.2 heuristic #5 classifies principal=`wcp-*`/`wcpsvc@*` as INFRA. The subsequent property-change after a REVERT is classified INFRA → OBSERVE → echo-cache hit → no K8s write. No feedback loop. |
| **R33** | First-enable shadow window expires during a high-burst backup. **(NEW r1)** | Shadow window is checked on every decision; expiry is wall-clock from the persisted ConfigMap start time. A burst of VENDOR events just before expiry are coalesced per §6.7.0. Immediately after expiry, VENDOR events are still OBSERVE (no ADOPT for VENDOR-classified ops). No spec flood. |
| **R34** | `config.managedBy` and `config.extraConfig[pause]` both change in one batch; managedBy is cleared + pause is set by the same admin. **(NEW r1)** | Batch processing: pause check runs first → OBSERVE only. ManagedBy handler queues a REVERT-on-next-batch. On next reconcile, if pause is still True, REVERT for ManagedBy is suppressed until pause clears. Set `ManagedByExtensionLost` condition regardless so humans can triage. |

---

## 13. Testing strategy

### 13.1 Unit tests

For each component:

- **Watcher extensions**: extend `pkg/util/vsphere/watcher` tests; add Leave-event disambiguation with vcsim; verify `ObjectUpdateKindLeave` → `RetrieveProperties` → `LeaveKind` output; verify BiosUUID mismatch path (R11).
- **Batch atomicity** (CC1-01): test that an `AdminEventBatch` with pause=True AND powerOff in same set-version results in OBSERVE for powerOff.
- **EventSub**: mock Event Manager; verify whitelist filtering, `CreatedTime` extraction, and principal-name extraction; verify `PairWindow` expiry.
- **Source classifier** (§6.2): table-driven tests for all 8 heuristics including: DRS leaf-type match (CC3-03), SDRS principal regex (CC3-04), empty-principal system events (CC3-05), empty allow-list safe-default (CC3-06).
- **Per-op decision**: full §6.4 catalog; every row including VENDOR vs ADMIN for NIC backing (CC3-11 — NSX never changes backing; verify ADMIN-only path).
- **Invariant guards**: each invariant; including webhook-reject recovery (CC2-07 split retry).
- **Conflict resolver**: table-driven tests for tVC>tK8s, tK8s>tVC, within-skew; undefined `tK8s` fallback (§6.6.3).
- **Idempotency token**: duplicate `AdminEventBatch` with same sha256 → no second ADOPT.
- **Annotation grammar**: validator accepts valid, rejects invalid (wildcard attempt), rejects unknown `backup-proxy` values.
- **Webhook bypass narrowing** (CC2-04): `system:masters` + forged `last-adopt-source` → still rejected; operator SA + annotation + whitelisted path → accepted.
- **Import window** (CC2-16): VM with `RestoredVMAnnotation` → all decisions OBSERVE.
- **Ping-pong detector**: generalized (R28); after N reverts within window → LOST condition.
- **vCLS filter** (CC3-14): events from MoRefs not in `status.uniqueID` index → dropped before controller.

### 13.2 Integration tests (`vcsim`)

Base pattern: start operator + vcsim → create VirtualMachine CR → wait ready → simulate admin op via govmomi → assert.

For each `KEEP_ADMIN_ONLY` op:
- Assert: correct condition set; audit ring entry; NO spec mutation; NO REVERT signal.

For each `DUAL_SURFACE` op:
- Sub-case INFRA: assert OBSERVE only.
- Sub-case VENDOR (with allow-list ConfigMap present): assert OBSERVE + coalesced ring entry.
- Sub-case ADMIN: assert ADOPT (or OBSERVE depending on the specific op).

For each `CONSUMER_ONLY` op:
- Assert: REVERT signal emitted → standard reconciler re-applies spec → vSphere matches spec.

For each `OUT_OF_SCOPE` op:
- Assert: vSphere action reversed; ping-pong detector fires after N attempts.

Additional specific intg tests:
- `AdminEventBatch` with pause + op in same set-version → OBSERVE only.
- `Leave` disambiguation via govmomi: Unregister → `VirtualMachineLost{Reason=UnregisteredOutOfBand}`.
- `Leave` disambiguation: Destroy → `VirtualMachineLost{Reason=DestroyedOutOfBand}`.
- `Leave` then re-register before disambiguation → BiosUUID check → correct `Unregistered+Registered` pair.
- `config.managedBy` cleared → REVERT → re-set → Critical event.
- `admin-managed-devices` annotation with valid value → admin-attached disk OBSERVE; with wildcard value → treated as absent.
- `backup-proxy=true` annotation → VADP hot-add disk OBSERVE with no status mutation.

### 13.3 E2E tests (per workspace rule `e2e-sync-with-changes.mdc`)

Under `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`:

| File | What it tests |
|---|---|
| `power_state.go` | Admin powers off via VC; assert spec adopts `PoweredOff`; standard reconciler drives power-on if spec reverts |
| `folder_reparent.go` | Admin moves VM out of namespace folder; assert `MoveIntoFolder_Task` revert; ping-pong detection after 3 reverts |
| `managedby_change.go` | Admin clears `config.managedBy`; assert REVERT + `ManagedByExtensionChanged` Critical event |
| `lost_destroy.go` | Admin runs `Destroy_Task`; assert `VirtualMachineLost{Reason=DestroyedOutOfBand}`; CR not deleted |
| `pause_check.go` | Admin sets `PauseVMExtraConfigKey=True` then powers off; assert OBSERVE only; label = `vendor\|admin` per principal |
| `pause_batch.go` | Pause + power-off in same `set.Version`; assert OBSERVE for the combined batch **(CC1-01)** |
| `vadp_snapshot.go` | Vendor SA creates/removes snapshot; assert OBSERVE (Unmanaged); coalesced ring entry; no CR-managed snapshot created |
| `drs_host_change.go` | govmomi vMotion; assert OBSERVE with `status.host` update; no spec mutation |
| `concurrent_change.go` | Admin + consumer change `spec.powerState` within skew window; assert `ConcurrentAdminConsumerChange` event |
| `webhook_bypass.go` | Operator SA ADOPT patch with correct annotation → accepted; `system:masters` with same annotation → rejected **(CC2-04)** |
| `import_window.go` | VM with `RestoredVMAnnotation`; admin op detected; assert OBSERVE only during import window **(CC2-16)** |
| `ha_failover.go` | Simulate HA failover (host failure + FDM restart); assert INFRA classification; `LastHAFailoverTime` set; no ADOPT of transient PoweredOff **(CC3-01)** |
| `storage_drift.go` | Admin storage vMotion to non-compliant datastore; assert `StorageClassDrift` condition; no REVERT **(CC3-21)** |
| `backup_proxy.go` | `backup-proxy=true` annotated VM; VADP hot-add disk; assert no status mutation **(CC3-18)** |
| `shadow_window.go` | Enable feature flag; assert all decisions are OBSERVE for `FirstEnableShadowDuration`; then normal decisions after **(CC4)** |

### 13.4 Soak / chaos

- Generate 10,000 admin events/min through vcsim; verify aggregator does not drop (or drops are counted and recovered by next resync).
- Kill the operator pod mid-ADOPT; assert idempotency token prevents duplicate adoption on restart.
- Kill the operator pod during Leave-event disambiguation; assert `AmbiguousLeave` is surfaced correctly.
- Simulate 1,000 VMs with hourly DRS vMotions; verify echo cache prevents K8s API churn.
- Enable VADP backup at scale with empty allow-list; verify Warning events emitted but no spec mutations; populate allow-list; verify VENDOR classification switches cleanly.

---

## 14. Risks

1. **Spec churn confuses GitOps users.** ADOPT writes to spec; `kubectl get vm -o yaml` shows a `spec.powerState` the consumer didn't author. Mitigated by `last-adopt-source` annotation (TTL 5 min), `VirtualMachineSpecAdopted` event, and audit ring. GitOps users with strict spec ownership must set `PauseAnnotation=true` on VMs that must not be reverse-reconciled.
2. **Decision-engine bugs revert legitimate consumer changes.** OBSERVE is the hardcoded fallback for unknown events; ADOPT/REVERT requires an explicit entry in the §6.4 table. New paths start in OBSERVE via `AdoptablePathWhitelist` even if the decision is ADOPT — the whitelist must explicitly permit each path.
3. **vCenter load.** Mitigated by feature sub-flags in §8; beta deployments verify volume before GA.
4. **Event Manager subscription firehose.** Conservative whitelist (~30 event types). Beta must measure event rate before GA.
5. ~~CIS tag poller~~ — Removed (rev 2). vSphere tags are admin-only; no consumer visibility.
6. **VADP vendor SA misclassification (empty allow-list).** Default allow-list is empty (CC3-06). All vendor SA operations degrade to OBSERVE until the `AdminReverseReconcileVendors` ConfigMap is populated. Deployments that skip this will see Warning events but no breakage.
7. **Ping-pong on OUT_OF_SCOPE ops.** Generalized detector (§8 `RevertPingPongMax=3`). Escalates to `VirtualMachineLost` condition rather than infinite fight.
8. **`ManagedBy` revert fights deliberate hand-off.** v1 always reverts. v2 adds an `abdicate` annotation path to explicitly release the VM from vm-operator management.
9. **Power-state ADOPT requires Event Manager subscriber.** Without `subscribeEvents=true` in the tuning ConfigMap, power state is OBSERVE only (§6.2.3). This is intentional. Deployments must enable the subscriber to get ADOPT behavior for power state.
10. **Audit retention.** K8s events default to 1-hour TTL. The `status.adminActivity[]` ring (10 entries) and the operator's event stream are the primary audit surfaces. Long-term compliance audit requires a SIEM sink (external to this design).

---

## 15. Open questions

These require team decisions. The design is structured to support any option.

| # | Question | Options | Recommendation |
|---|---|---|---|
| OQ-1 | **VADP vendor allow-list delivery.** How does a fresh deployment populate the `AdminReverseReconcileVendors` ConfigMap? | (a) Default empty + Helm values / (b) Shipped presets per vendor version / (c) Auto-discovery via vCenter `Extension` API | (b) Ship versioned presets as part of the operator Helm chart; deployments override. |
| OQ-2 | ~~Tag poll vs event subscription~~ | REMOVED (rev 2) | TagPoller removed — vSphere tags are admin-only; no consumer visibility. |
| OQ-3 | **Cross-VC migration auto-recovery.** v1 surfaces LOST. Should v2 auto-`ImportVM`? | (a) Yes, auto-discover on destination VC / (b) No, require manual `ImportVM` | (b) Manual; too many topology assumptions. Surface as LOST with a clear runbook. |
| OQ-4 | **`ManagedBy` abdicate path.** v2 feature: explicit release of a VM from vm-operator management. What is the signal? | (a) `PausedVMLabelKey=admin+abdicate` / (b) New annotation `vmoperator.vmware.com/abdicate: true` / (c) A new `VirtualMachineAbdicate` subresource | (b) Annotation; consistent with existing pattern. Out of scope for v1. |
| OQ-5 | **`spec.latencySensitivity` lift.** CC3-04 / DUAL_SURFACE verdict. Lift to first-class spec field in v2? | (a) Yes (requires API change) / (b) Keep as drift condition indefinitely | (a) in v2; the OBSERVE+drift condition is the v1 holding pattern. |
| OQ-6 | **vcsim coverage gaps.** `disabledMethod`, `customValue`, and some HA event types are not modeled in vcsim. What is the test strategy for these? | (a) Fake/mock adapters in unit tests only / (b) Real VC in CI for specific paths / (c) vcsim extension PRs | (a) for v1; (c) where feasible as follow-up. |
| OQ-7 | **Minimum supported VC version.** The event whitelist and tagging APIs reference VC 7.0+ event types. What is the floor? | (a) VC 7.0 U3 / (b) VC 8.0 GA | Define explicitly in the feature-flag documentation; startup self-check emits `VirtualMachineReverseReconcileDegraded{Reason=VCVersionInsufficient}` below floor. |

---

## 16. Cross-check issue-resolution map

The following table maps every CRITICAL and HIGH issue from the four cross-check reviewers to the section that resolves it. MEDIUM issues are either in-line (denoted by CC-tag in the edited section) or captured in §15.

| Issue ID | Title | Severity | Resolved in |
|---|---|---|---|
| CC1-01 | Pause check bypassed when pause+op arrive in same property-collector batch | CRITICAL | §6.3.1 (batch atomicity), R1 |
| CC1-07 | `LastReconcileTime` does not exist in v1alpha6 API | CRITICAL | §6.6.1 (use `*Synced` condition timestamps) |
| CC1-05 | Leave + re-register race before disambiguation | HIGH | §5.1.2 (BiosUUID check on disambiguation), R11 |
| CC1-10 | REVERT races in-flight standard reconcile | HIGH | §6.7.3 (REVERT = signal only; standard reconciler owns vSphere) |
| CC1-11 | Operator SA not on INFRA allow-list → ADOPT our own REVERT | HIGH | §6.2.2 heuristic #5 (WCP SA patterns classified as INFRA) |
| CC1-12 | Optimistic-lock retry outrunning controller-runtime cache | HIGH | §6.7.2 (patch built from fresh `client.MergeFrom` on refetch) |
| CC1-13 | Channel-driven controller has no per-key serialization path | HIGH | §10.2 (co-deployed in same Manager; standard workqueue per VM key) |
| CC1-15 | Container-view churn can cause dead events for removed zones | HIGH | §5.1.0 filter (drop non-wcp-managed), §10.2 (EventSub stops with leader) |
| CC1-18 | `cource` buffer-100 overflow stalls REVERT loop | HIGH | §6.7.3 uses `GenericEvent` directly via buffered channel; `AdminEventQueueSize` default 4096 for aggregator |
| CC2-02 | `VirtualMachineProviderStatus` type collision with existing field | CRITICAL | §7.2 (renamed to `VirtualMachineVSphereObservedStatus`, exposed as `status.vsphereObserved`) |
| CC2-04 | Webhook bypass claim "existing privileged-user check covers it" is false | CRITICAL | §6.5 #5, §7.4.1 (`IsReverseReconcileWrite` helper) |
| CC2-05 | Mutator strips adopt annotation before validator sees it — bypass never fires | CRITICAL | §6.7.2.1, §7.4.2 (SA-gated strip; annotation lives until TTL) |
| CC2-06 | New RBAC for `virtualmachines/status` update not enumerated | HIGH | §10.1 (explicit `+kubebuilder:rbac` markers) |
| CC2-07 | Retry loop doesn't distinguish `Conflict` from webhook `Forbidden` | HIGH | §6.7.2 (split retry by error class) |
| CC2-09 | Reverse-reconcile writes `status.observedGeneration` — lies to consumers | HIGH | §6.7.2 (observedGeneration NOT written; use per-condition field) |
| CC2-12 | §10 claims no new vCenter privileges but EventSub needs them | MEDIUM | §10.3 (privilege table + startup self-check) |
| CC2-13 | Auto-set `PausedVMLabelKey=admin` rejected by webhook for non-privileged SA | MEDIUM | §6.3.4 (controller runs as vm-operator SA), §7.4.4 |
| CC2-15 | New status list fields missing `+listType`/`+listMapKey` markers | HIGH | §7.2 (explicit markers + `MaxItems` caps on every new list) |
| CC2-16 | Import/restore/failover annotation window not respected by reverse-reconcile | MEDIUM | §6.3.2 (import-window OBSERVE rule), §6.5 #8 |
| CC3-01 | HA failover heuristic unusable when Event Manager subscriber is off (default) | CRITICAL | §6.2.3 (power-state ADOPT requires corroborating HA event; without EventSub → OBSERVE only) |
| CC3-02 | HA failover invisible after operator restart / leader switch | CRITICAL | §6.2.4 (event replay on startup + `ResyncUncorroborated` source) |
| CC3-03 | DRS event type set undefined; manual migrate misclassifiable | HIGH | §6.2.2 heuristic #2 (leaf-type matching only) |
| CC3-04 | SDRS principal pattern brittle across VC versions / VCF | HIGH | §6.2.2 heuristic #3 (explicit regex list + configurable) |
| CC3-06 | VADP allow-list bootstrap undefined; default empty breaks fleet-wide backup | HIGH | §5.3, §8 (empty default + ConfigMap-driven; safe-default = OBSERVE) |
| CC3-07 | Tag `created_by` field does not exist | HIGH | §6.2.2 heuristic #7 (`(Category.Name, Tag.Name)` patterns) |
| CC3-08 | DRS host-churn thundering herd on controller restart | HIGH | §6.7.0.5 (status echo cache seeded from existing status on startup) |
| CC3-09 | VADP backup window floods `status.adminActivity[]` ring | MEDIUM | §6.7.0 (vendor-event coalescing with `VendorCoalesceWindow`) |
| CC3-10 | Unmanaged snapshot model lacks identity; design assumes richer feature | HIGH | §6.4 note: prerequisite is extending `VirtualMachineSnapshotReference` with MoRef/CreateTime/Description (tracked as story SST-02 in `02-story-breakdown.md`) |
| CC3-11 | ADM-13 VENDOR column misattributes NSX as NIC-backing changer | HIGH | §6.4 ADM-13 row (VENDOR column removed; NSX only acts via tags) |
| CC3-12 | `LastHAFailoverTime` referenced but never defined | MEDIUM | §7.2 (`VirtualMachineHAStatus` fully defined including `LastFailoverTime`) |
| CC3-13 | Cluster `dasVmConfig` watch O(rules×VMs) cross-reference storm | MEDIUM | §5.1.3 (removed; per-VM `summary.runtime.dasVmProtection` used instead) |
| CC3-14 | vCLS shadow VMs not explicitly filtered | MEDIUM | §5.1.0 (explicit `config.managedBy.extensionKey == wcp` filter) |
| CC3-16 | FT secondary VM floods `watcher.Cache` and corrupts on role-swap | MEDIUM | §6.4 ADM-31 (drop secondary at §5.1.0 filter; evict cache on role-swap), R31 |
| CC3-18 | Backup-proxy VMs (hot-add transport) not addressed | HIGH | §6.4 ADM-15 VENDOR row (no status mutation if `backup-proxy=true`), §7.3 |
| CC3-20 | Pause label misattributed to admin when set by VADP vendor | MEDIUM | §6.3.3 (label value per source), §7.4.4 (value space extended) |
| CC3-21 | SDRS storage ping-pong has no detector | MEDIUM | §6.4 ADM-09 (REVERT disabled; OBSERVE + `StorageClassDrift` only), R28 generalized |
| CC3-22 | `spec.affinity` vs DRS rule comparison is structurally undefined | HIGH | §6.4 ADM-35 (AffinityDrift uses VMGroup rule-name prefix, not raw `spec.affinity`) |
| CC4-01 | First-enable resync flood from N existing VMs | CRITICAL | §11.2 (shadow window with `FirstEnableShadowDuration`) |
| CC4-02 | Empty `VADPVendorAllowList` breaks fleet-wide backup on Day 1 | CRITICAL | §8, §6.2 (empty default + all VENDOR decisions degrade to OBSERVE) |
| CC4-03 | Crash mid-ADOPT — no idempotency token | HIGH | §6.7.2 (sha256 idempotency token in `last-adopt-event-uid`) |
| CC4-04 | Leader election scope of reverse-reconcile controller undefined | HIGH | §10.2 (co-deployed in same Manager; shared lease) |

---

## Appendix A — Per-op handler stubs (illustrative)

Pseudocode for the most representative handlers. Full implementation in `controllers/virtualmachine/reverseReconcile/handlers_*.go`.

### A.1 Power state (ADM-01..03)

```go
func handlePowerState(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    obs := e.NewValue.(string)
    switch obs {
    case "poweredOff", "poweredOn", "suspended":
        // No invariant violations for power state.
        return Decision{Kind: ADOPT, SpecPath: "powerState", NewValue: vmopv1ToAPIPowerState(obs)}
    }
    return Decision{Kind: OBSERVE}
}
```

### A.2 Folder reparent (ADM-10/44)

```go
func handleParent(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    canonical := zoneFolderMoIDFor(vm)
    observed := e.NewValue.(MoRef)
    if observed == canonical {
        return Decision{Kind: OBSERVE}
    }
    return Decision{Kind: REVERT, RevertReason: "FolderOutsideNamespace", VimAction: "MoveIntoFolder", Target: canonical}
}
```

### A.3 ManagedBy change (ADM-58)

```go
func handleManagedBy(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    observed := e.NewValue.(vimtypes.ManagedByInfo)
    if observed.ExtensionKey == ManagedByExtensionKey && observed.Type == ManagedByExtensionType {
        return Decision{Kind: OBSERVE}
    }
    // Critical: someone is trying to steal this VM (or release us).
    return Decision{
        Kind:          REVERT,
        RevertReason:  "ManagedByExtensionLost",
        Severity:      Critical,
        AlertOnFailure: LostManagedBy,
    }
}
```

### A.4 Leave event

```go
func handleLeave(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    switch e.LeaveKind {
    case Unregistered:
        return Decision{Kind: LOST, LostReason: "UnregisteredOutOfBand", PreserveUniqueID: true}
    case Destroyed:
        return Decision{Kind: LOST, LostReason: "DestroyedOutOfBand", PreserveUniqueID: false}
    case XvcMoved:
        return Decision{Kind: LOST, LostReason: "MigratedToOtherVC", PreserveUniqueID: true}
    case Reparented:
        // Already handled by handleParent on the parent property change; this is the secondary signal.
        return Decision{Kind: OBSERVE}
    default:
        return Decision{Kind: LOST, LostReason: "AmbiguousLeave", PreserveUniqueID: true}
    }
}
```

---

## Appendix B — Glossary

| Term | Definition |
|---|---|
| **ADOPT** | Mutate `vm.Spec` to match observed vSphere state |
| **REVERT** | Trigger the standard reconcile to re-apply spec to vSphere |
| **OBSERVE** | Update status / condition / event only; do not touch spec or vSphere |
| **LOST** | The vSphere VM has disappeared; surface a `VirtualMachineLost` condition |
| **INFRA-driven** | Source = DRS, SDRS, HA, FDM, vCLS, DPM, etc.; non-human |
| **VENDOR-driven** | Source = VADP / NSX / SRM / backup-vendor service account |
| **ADMIN-driven** | Source = human admin via UI/SDK/SOAP/VAPI |
| **Catalog ID (ADM-NN)** | Identifier from [`00-admin-operations-analysis.md`](00-admin-operations-analysis.md) |
| **MoRef** | `vim.ManagedObjectReference` |
| **Leave event** | Property collector `ObjectUpdateKindLeave` |
| **Pause** | `PauseVMExtraConfigKey="True"` on vSphere VM OR `PausedVMLabelKey=admin*` on K8s VM |

---

End of design. All CRITICAL and HIGH issues from the Phase-3 cross-check review have been incorporated. See §16 for the full issue-to-resolution traceability map. Implementation stories are in [`02-story-breakdown.md`](02-story-breakdown.md).
