# Research: Admin-Driven Reverse Reconcile

This document records the investigation findings, prior art, design analysis, and
cross-check results that underpin `spec.md` and `plan.md`. It is the authoritative
reference for anyone implementing this feature or debugging its behavior.

The original design documents from which this research was synthesized are preserved
below in condensed form. For full detail on any section, refer to the linked sources.

---

## 1. Admin operations catalog

### 1.1 Methodology

A discovery agent identified 62 candidate admin operations on VMs accessible through
traditional vCenter interfaces (VMODL1/SOAP/VAPI/vCenter UI/ESXi). Each was evaluated
for:
- Whether it is genuinely admin-centric (infra persona, not DevOps).
- Whether there is operational value in keeping it on the traditional interface.
- What the appropriate reverse-reconcile decision should be.

### 1.2 Verdict categories

| Verdict | Meaning |
|---------|---------|
| `KEEP_ADMIN_ONLY` | Operation is infra-admin only; DevOps has no need to invoke it |
| `DUAL_SURFACE` | Has both admin and DevOps use cases (e.g., vMotion driven by DRS or by admin request) |
| `CONSUMER_ONLY` | Should be exposed only through the Kubernetes API; revert if admin does it |
| `OUT_OF_SCOPE` | Framework does not react (e.g., ESXi-direct, BIOS settings) |

### 1.3 Per-op decision table (§6.4 of design)

| Op (catalog ID) | INFRA | VENDOR | ADMIN |
|---|---|---|---|
| Power off / on / suspend / reset (ADM-01..03, 53) | (n/a) | (n/a) | ADOPT (`spec.powerState`); requires Event Manager subscriber |
| Snapshot create / remove / revert (ADM-04..06) | (n/a) | OBSERVE (Unmanaged) | OBSERVE (Unmanaged) |
| Consolidate / consolidation-needed (ADM-07, 51) | (n/a) | (n/a) | OBSERVE (info condition) |
| vMotion (ADM-08) | OBSERVE (update `status.vsphereObserved.host`) | (n/a) | OBSERVE |
| Storage vMotion (ADM-09, 50) | OBSERVE (update `status.vsphereObserved`) | (n/a) | OBSERVE + `StorageClassDrift` condition; never REVERT (SDRS ping-pong) |
| Folder reparent (ADM-10, 44) | (n/a) | (n/a) | REVERT (`MoveIntoFolder_Task` back to namespace folder) |
| Resource Pool reassignment (ADM-11, 45) | (n/a) | (n/a) | REVERT (move back to namespace RP) |
| Cross-VC migration (ADM-59) | (n/a) | (n/a) | LOST |
| Host maintenance / datastore decom (ADM-61, 62) | OBSERVE (info condition) | (n/a) | (n/a) |
| NIC disconnect (ADM-12) | (n/a) | (n/a) | OBSERVE if `admin-managed-nics` annotation present; else REVERT |
| NIC backing change (ADM-13) | (n/a) | (n/a) — NSX acts via tags only, not backing | OBSERVE if `admin-managed-nics`; else REVERT |
| NIC add / remove (ADM-14) | (n/a) | (n/a) | OBSERVE if `admin-managed-devices` lists key; else REVERT |
| Tag changes (ADM-20, 37) | OUT OF SCOPE | OUT OF SCOPE | OUT OF SCOPE — vSphere tags are admin-only; not surfaced to consumer persona |
| Disk add / remove (ADM-15) | (n/a) | OBSERVE (no status mutation if `backup-proxy=true`); else OBSERVE as Classic | OBSERVE as Classic if non-PVC; REVERT if PVC-backed disk admin-removed |
| CD-ROM attach (ADM-16) | (n/a) | (n/a) | OBSERVE if `admin-managed-devices`; else REVERT |
| SPBM profile change (ADM-46) | OBSERVE | (n/a) | OBSERVE + `StorageClassDrift` condition |
| Rename (ADM-17) | (n/a) | (n/a) | REVERT (re-set `config.name`) |
| Custom Attributes (ADM-19, 38) | (n/a) | (n/a) | OBSERVE (`status.vsphereObserved.customAttributes[]`) |
| Direct CPU/mem reconfigure (ADM-21) | (n/a) | (n/a) | REVERT (class invariant) |
| vTPM add/remove (ADM-22) | (n/a) | (n/a) | REVERT (class invariant) |
| Encryption rekey/decrypt (ADM-23) | (n/a) | (n/a) | REVERT (EncryptionClass invariant) |
| HW version upgrade (ADM-29) | (n/a) | (n/a) | OBSERVE if observed ≥ `spec.minHardwareVersion`; else REVERT |
| Boot options (ADM-30) | (n/a) | (n/a) | REVERT |
| ManagedBy change (ADM-58) | (n/a) | (n/a) | REVERT (re-set `ManagedByExtensionKey`); if fails → `VirtualMachineLost` + Critical alert |
| Unregister (ADM-26, 43) | (n/a) | (n/a) | LOST |
| Destroy (ADM-27) | (n/a) | (n/a) | LOST |
| Mark as template / VM (ADM-28, 42) | (n/a) | (n/a) | REVERT (`MarkAsVirtualMachine`) |
| FT (ADM-31) | OBSERVE (`status.vsphereObserved.faultTolerance`); evict secondary MoRef on role-swap | (n/a) | OBSERVE |
| HA override (ADM-32) | (n/a) | (n/a) | OBSERVE (`status.vsphereObserved.haProtection`) |
| HA failover (ADM-33) | OBSERVE (info condition + `LastHAFailoverTime`) | (n/a) | (n/a) |
| DRS rules (ADM-35) | (n/a) | (n/a) | OBSERVE; `AffinityDrift` if rule not authored by `vmop-vmg-<uid>-` prefix |
| Scheduled tasks (ADM-41) | (n/a) | (n/a) | OBSERVE + `ScheduledTaskActive` condition |
| ManagedBy change (ADM-58) | (n/a) | (n/a) | REVERT; Critical alert if fails |
| Direct ESXi (ADM-60) | (n/a) | (n/a) | OBSERVE; periodic resync is detection mechanism |

---

## 2. Source classification heuristics

The classifier produces `Source ∈ {INFRA, VENDOR, ADMIN, UNKNOWN}` from 8 heuristics
applied in order:

1. **Leave event** → `INFRA`.
2. **DRS vMotion** — `DrsVmMigratedEvent` or `DrsVmPoweredOnEvent` in paired-event cache → `INFRA`.
3. **SDRS** — `VmRelocatedEvent` with principal matching `InfraPrincipalPatterns` regex list → `INFRA(SDRS)`.
4. **HA** — `VmDasBeingResetEvent` / `VmFailoverEvent` in cache → `INFRA(HA)`.
5. **WCP-internal** — principal matches WCP patterns (`^wcp-.*$`, `^wcpsvc@.*$`, etc.) → `INFRA`.
6. **Vendor-driven** — principal matches `vmoperator-reverse-reconcile-vendors` ConfigMap → `VENDOR`.
7. **Empty-principal placement** — `PrincipalName == ""` AND `Path ∈ {summary.runtime.host, config.files.vmPathName, resourcePool}` → `INFRA` (DRS/SDRS/DPM).
8. **Default** → `ADMIN`.

Safe-default: `UNKNOWN` + power-state-only batch → OBSERVE (never ADOPT without a corroborating event).

**Why tag-based vendor classification was removed (CC3-07):** vSphere tags do not carry a
`created_by` field. More importantly, VM Operator maintains the invariant that vSphere
tags applied by admins are not surfaced to the DevOps/consumer persona. Since tags are
admin-only and the only §6.4 decision for tag changes is OBSERVE, a tag poller would add
cost (~500 API calls per 5-min interval at 40k VMs) for zero actionable signal. Vendor
classification relies entirely on `Event.UserName` principal matching (heuristics #3–#6).

---

## 3. Scale analysis (40,000 VMs)

### 3.1 Property Collector load

- Baseline today: ~15 property paths × 40k VMs = 600k watched path-instances.
- New paths added (when `AdminReverseReconcile=true`): ~12 new paths.
- New total: ~27 paths × 40k VMs = 1.08M watched path-instances (1.8× baseline).
- vCenter memory overhead: ~46 MB additional (100 bytes/entry × 480k new entries).
- No polling overhead — property collector is push-based (fires only on change).

**No separate cluster/host container view**: A rev-1 design proposed watching
`HostSystem` and `ClusterComputeResource` in separate container views. This was dropped
because: (a) 1 host change → fan-out to N resident VMs; (b) `configurationEx.rule` is a
large frequently-changing blob; (c) all necessary host/cluster signals are already
observable via per-VM paths (`summary.runtime.host`, `runtime.dasVmProtection`) or Event
Manager events (`EnteredMaintenanceModeEvent`).

### 3.2 Expected event rates from new paths (40k VM fleet)

| Property path | Expected rate |
|---|---|
| `config.managedBy` | ~0/day (only on operator upgrade or hostile takeover) |
| `config.template` | ~0–1/day (extremely rare on production VMs) |
| `config.flags.changeTrackingEnabled` | ~10/day (VADP backup enable/disable) |
| `runtime.dasVmProtection` | ~5/day (HA enable/disable on cluster) |
| `datastore` | ~20/day (storage vMotion, SDRS) |
| `config.version` | ~0/day (planned maintenance only) |
| `config.bootOptions` | ~0/day (extremely rare) |
| `disabledMethod` | ~5/day (VADP may toggle) |
| `customValue` | ~10/day (monitoring/CMDB writes) |
| `config.changeVersion` | ~100/day (fires on every ReconfigVM) |
| **Total new event rate** | **~150–200 events/day** (negligible vs ~1k–2k/day from existing paths) |

Worst-case burst: VADP backup window on 10k VMs = ~40k property notifications over 4
hours = ~2.8/sec. Handled by existing `MaxObjectUpdates=100` batching in `watcher.go`.

### 3.3 K8s API load from the decision engine

| Scenario | K8s API calls/day |
|---|---|
| Steady-state (echo cache hits) | 0 |
| DRS active (200 host changes/day) | 200 status patches |
| VADP backup (10k VMs, 4-hour cycle) | 10k status patches/cycle |
| Admin power-off burst (100 VMs) | 100 spec + 100 status patches |
| First-enable shadow window (40k VMs, 1hr) | 40k status-only patches staggered at ~11/sec |

---

## 4. Key design decisions and rationale

### 4.1 Adopt-into-spec semantics

When an admin changes a `KEEP_ADMIN_ONLY` property, the controller mutates `vm.Spec` to
match the observed state. Trade-off analysis:

| Aspect | Adopt-into-spec | Separate `adminOverride` field | Pause-on-drift only |
|---|---|---|---|
| API churn | Low | High (parallel spec) | None |
| Controller complexity | Medium | High (two-track reconcile) | Low |
| Consumer mental model | Spec drifts under their feet | Spec preserved but reconcile yields | Spec preserved, VM stops converging |
| **Chosen** | **Yes** | No | No |

Mitigations for spec drift: `last-adopt-source` annotation (TTL 5 min), `VirtualMachineSpecAdopted` K8s Event, `status.adminActivity[]` audit ring.

### 4.2 Single feature flag, no sub-flags

The admin persona controls the feature (on/off). Infra-admin adjusts operational knobs
via ConfigMap without restarting the operator. Sub-flags would add configuration-matrix
complexity for no operational benefit — the admin cannot toggle subsystems at runtime.

### 4.3 Event Manager subscriber required for power-state ADOPT

Power-state changes require corroborating HA/DRS event data to classify the source
correctly (INFRA vs ADMIN). Without the Event Manager subscriber (`subscribeEvents=true`),
power-state changes are OBSERVE-only. This is a deliberate safe default.

### 4.4 REVERT via `GenericEvent` injection

The reverse-reconcile controller does NOT call the vSphere API directly. REVERT is
implemented by injecting a `GenericEvent` into the standard `VirtualMachineReconciler`
queue, which re-applies `spec` to vSphere as part of its normal loop. This keeps the
decision engine purely as a nudge mechanism with no vSphere write path of its own.

---

## 5. Cross-check findings summary

Four parallel sub-agent reviewers (concurrency/batching, API/CRD, multi-actor/source
classification, rollout/observability) reviewed the design. A total of 44 CRITICAL/HIGH
issues were identified and resolved. Key resolutions:

| Issue | Resolution |
|---|---|
| CC1-01: Pause bypassed by property-collector batching | `AdminEventBatch` preserves batch boundary; pause evaluated from AFTER state of entire batch |
| CC1-07: `LastReconcileTime` does not exist in v1alpha6 | Use `*Synced` condition `LastTransitionTime` or `managedFields` timestamps for `tK8s` |
| CC2-02: `VirtualMachineProviderStatus` type collision | Renamed to `VirtualMachineVSphereObservedStatus`; exposed under `status.vsphereObserved` |
| CC2-04: Webhook bypass claim was false | New `IsReverseReconcileWrite()` helper with strict 3-condition check; `system:masters` explicitly excluded |
| CC2-05: Mutator stripping annotations races validation | Mutator skips strip for operator SA requests |
| CC3-01: HA failover heuristic requires EventSub | Power-state ADOPT requires corroborating event; OBSERVE-only without EventSub |
| CC3-02: HA failover invisible after restart | Event replay on startup (`eventReplayWindow=30m`) |
| CC3-06: Empty VADP allow-list breaks fleet | Default empty → VENDOR decisions degrade to OBSERVE + Warning |
| CC3-07: Tag `created_by` field does not exist | Tag-based vendor heuristic removed entirely; principal-name matching only |
| CC4: First-enable flood | `firstEnableShadowDuration` shadow window (default 1hr) |
| CC4: Missing idempotency token | `adoptKey = sha256(MoRef + setVersion + path + value)` stored in annotation |

Full issue-to-resolution traceability is in `01-design.md §16` (Cross-check findings map).

---

## 6. Race conditions and notable edge cases

| Case | Resolution |
|---|---|
| Pause + destructive op in same property-collector batch | Batch atomicity: pause wins; OBSERVE for entire batch |
| Admin destroys VM; K8s VM has finalizer | LOST condition; CR not deleted; consumer cleans up PVCs |
| DRS continuous vMotion (1+ moves/min) | INFRA → OBSERVE; echo cache suppresses redundant K8s patches |
| VADP backup cycle (snapshot every 4h) | VENDOR → OBSERVE; coalescing collapses 4 events to 1 ring entry |
| Two ADOPTs on same path within milliseconds | Optimistic-lock; only one wins; other retries with fresh fetch |
| Operator crash mid-ADOPT | Idempotency token prevents duplicate on restart |
| REVERT ping-pong (admin moves out, operator moves back, repeat) | Generalized detector: after N REVERTs in window → LOST condition |
| ManagedBy cleared + pause set in same batch | Pause wins → OBSERVE; ManagedBy REVERT queued for next non-paused reconcile |
| FT secondary VM floods watcher cache | Filter at §5.1.0 drops non-WCP MoRefs before aggregator |
| Operator's own REVERT mis-attributed as admin event | WCP SA principal → INFRA heuristic #5 → OBSERVE; echo cache hit; no feedback loop |

Full race catalog with 34 entries is in `01-design.md §12`.

---

## 7. Open design artifacts

The following detailed artifacts from the design phase are available for reference during
implementation:

- **Full design document**: `docs/design/admin-reverse-reconcile/01-design.md` (preserved in git history; this research.md synthesizes the key findings).
- **Operations analysis report**: `docs/design/admin-reverse-reconcile/00-admin-operations-analysis.md` (62-operation catalog with verdicts, watcher gaps, and detection-surface additions).
- **Story breakdown**: `docs/design/admin-reverse-reconcile/02-story-breakdown.md` (superseded by `tasks.md` in this directory; preserved in git history).
