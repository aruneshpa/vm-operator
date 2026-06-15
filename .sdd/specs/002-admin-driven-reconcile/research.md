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

## 5. Cross-check issue-resolution map

Four parallel sub-agent reviewers (concurrency/batching, API/CRD, multi-actor/source
classification, rollout/observability) reviewed the design. A total of 44 CRITICAL/HIGH
issues were identified and resolved.

| Issue ID | Title | Severity | Resolved in |
|---|---|---|---|
| CC1-01 | Pause check bypassed when pause+op arrive in same property-collector batch | CRITICAL | Batch atomicity: `AdminEventBatch` preserves batch boundary; pause evaluated from AFTER state of entire batch (R1) |
| CC1-07 | `LastReconcileTime` does not exist in v1alpha6 API | CRITICAL | Use `*Synced` condition `LastTransitionTime` or `managedFields` timestamps for `tK8s` |
| CC1-05 | Leave + re-register race before disambiguation | HIGH | BiosUUID/InstanceUUID check on `RetrieveProperties` follow-up before emitting `LeaveKind` (R11) |
| CC1-10 | REVERT races in-flight standard reconcile | HIGH | REVERT = `GenericEvent` injection only; standard reconciler owns all vSphere writes |
| CC1-11 | Operator SA not on INFRA allow-list → ADOPT our own REVERT | HIGH | Classifier heuristic #5: WCP SA patterns (`wcp-*`, `wcpsvc@*`) always → INFRA |
| CC1-12 | Optimistic-lock retry outrunning controller-runtime cache | HIGH | Patch built from fresh `client.MergeFrom` on refetch; never from stale watch cache |
| CC1-13 | Channel-driven controller has no per-key serialization path | HIGH | Co-deployed in same Manager; standard controller-runtime workqueue provides per-VM-key serialization (CC4-04) |
| CC1-15 | Container-view churn causes dead events for removed zones | HIGH | §5.1.0 filter drops non-wcp-managed MoRefs; EventSub stops with leader on handoff |
| CC1-18 | `cource` buffer-100 overflow stalls REVERT loop | HIGH | `GenericEvent` via buffered channel; aggregator channel `AdminEventQueueSize` default 4096 |
| CC2-02 | `VirtualMachineProviderStatus` type collision with existing field | CRITICAL | Renamed to `VirtualMachineVSphereObservedStatus`; exposed as `status.vsphereObserved` |
| CC2-04 | Webhook bypass claim "existing privileged-user check covers it" is false | CRITICAL | New `IsReverseReconcileWrite()` helper: 3-condition check (operator SA + `last-adopt-source=vcenter` + diff in whitelist); `system:masters` explicitly excluded |
| CC2-05 | Mutator strips adopt annotation before validator sees it — bypass never fires | CRITICAL | Mutator skips strip for operator SA requests; annotation lives until TTL |
| CC2-06 | New RBAC for `virtualmachines/status` update not enumerated | HIGH | Explicit `+kubebuilder:rbac` markers added; see `plan.md §New RBAC markers` |
| CC2-07 | Retry loop doesn't distinguish `Conflict` from webhook `Forbidden` | HIGH | Retry split by error class: `Conflict` → refetch + retry; `Forbidden` → emit event + stop |
| CC2-09 | Reverse-reconcile writes `status.observedGeneration` — lies to consumers | HIGH | `observedGeneration` NOT written by reverse-reconcile; per-condition field used instead |
| CC2-12 | §10 claims no new vCenter privileges but EventSub needs them | MEDIUM | Full privilege table + startup self-check per source; see `plan.md §vCenter permissions` |
| CC2-13 | Auto-set `PausedVMLabelKey=admin` rejected by webhook for non-privileged SA | MEDIUM | Controller runs as vm-operator SA (privileged); webhook allows label set from operator SA |
| CC2-15 | New status list fields missing `+listType`/`+listMapKey` markers | HIGH | Explicit markers + `MaxItems` caps on every new list; see `model.md` |
| CC2-16 | Import/restore/failover annotation window not respected by reverse-reconcile | MEDIUM | Import-window OBSERVE rule: all decisions OBSERVE while import/restore annotation is present |
| CC3-01 | HA failover heuristic unusable when Event Manager subscriber is off (default) | CRITICAL | Power-state ADOPT requires corroborating HA event; without EventSub → OBSERVE only |
| CC3-02 | HA failover invisible after operator restart / leader switch | CRITICAL | Event replay on startup (`eventReplayWindow=30m`) + `ResyncUncorroborated` source |
| CC3-03 | DRS event type set undefined; manual migrate misclassifiable | HIGH | Classifier uses leaf-type matching only (`VmMigratedEvent.drsFault != nil`) |
| CC3-04 | SDRS principal pattern brittle across VC versions / VCF | HIGH | Explicit configurable regex list for SDRS principal patterns |
| CC3-06 | VADP allow-list bootstrap undefined; default empty breaks fleet-wide backup | HIGH | Default empty allow-list; safe default = VENDOR decisions degrade to OBSERVE + Warning |
| CC3-07 | Tag `created_by` field does not exist | HIGH | Tag-based vendor heuristic removed; `(Category.Name, Tag.Name)` patterns used instead |
| CC3-08 | DRS host-churn thundering herd on controller restart | HIGH | Status echo cache seeded from existing status on startup; suppresses redundant K8s patches |
| CC3-09 | VADP backup window floods `status.adminActivity[]` ring | MEDIUM | Vendor-event coalescing with `VendorCoalesceWindow` |
| CC3-10 | Unmanaged snapshot model lacks identity | HIGH | Prerequisite: extend `VirtualMachineSnapshotReference` with MoRef/CreateTime/Description (task SST-02) |
| CC3-11 | ADM-13 VENDOR column misattributes NSX as NIC-backing changer | HIGH | VENDOR column removed for ADM-13; NSX only acts via tags |
| CC3-12 | `LastHAFailoverTime` referenced but never defined | MEDIUM | `VirtualMachineHAStatus` fully defined including `LastFailoverTime` in `model.md` |
| CC3-13 | Cluster `dasVmConfig` watch O(rules×VMs) cross-reference storm | MEDIUM | Removed; per-VM `summary.runtime.dasVmProtection` used instead |
| CC3-14 | vCLS shadow VMs not explicitly filtered | MEDIUM | Explicit `config.managedBy.extensionKey == wcp` filter at entry point |
| CC3-16 | FT secondary VM floods `watcher.Cache` and corrupts on role-swap | MEDIUM | Drop secondary at entry filter; evict cache on role-swap (R31) |
| CC3-18 | Backup-proxy VMs (hot-add transport) not addressed | HIGH | ADM-15 VENDOR row: no status mutation if `backup-proxy=true` annotation set; see `model.md §3` |
| CC3-20 | Pause label misattributed to admin when set by VADP vendor | MEDIUM | Label value space extended: value encodes source (`admin`, `vendor`, `infra`) |
| CC3-21 | SDRS storage ping-pong has no detector | MEDIUM | REVERT disabled for storage vMotion; OBSERVE + `StorageClassDrift` condition only (R28 generalized) |
| CC3-22 | `spec.affinity` vs DRS rule comparison is structurally undefined | HIGH | ADM-35: AffinityDrift uses VMGroup rule-name prefix, not raw `spec.affinity` |
| CC4-01 | First-enable resync flood from N existing VMs | CRITICAL | `firstEnableShadowDuration` shadow window (default 1hr); see `plan.md §Rollout` |
| CC4-02 | Empty `VADPVendorAllowList` breaks fleet-wide backup on Day 1 | CRITICAL | Empty default; all VENDOR decisions degrade to OBSERVE without allow-list |
| CC4-03 | Crash mid-ADOPT — no idempotency token | HIGH | `sha256(MoRef + setVersion + path + value)` stored as `last-adopt-event-uid` annotation |
| CC4-04 | Leader election scope of reverse-reconcile controller undefined | HIGH | Co-deployed in same Manager; shared lease; required for cache coherence |

---

## 6. Race conditions and notable edge cases

| Case | Resolution |
|---|---|
| **R1** Pause + destructive op in same property-collector batch | Batch atomicity: pause wins; OBSERVE for entire batch |
| **R2** Watcher loses connection mid-`WaitForUpdatesEx`; misses updates | Periodic resync (`resyncIntervalSeconds`) covers the gap; fresh `RetrieveProperties` on restart |
| **R3** Admin renames VM; new name collides with another K8s VM in same namespace | REVERT issues `ReconfigVM_Task` with original name; if revert fails, surface `RenameRevertFailed` event |
| **R4** Admin powers off VM; consumer patches `spec.powerState=PoweredOn` within skew window | `ConcurrentAdminConsumerChange` warning; ADOPT (admin newer); consumer's intent re-applies on next standard reconcile |
| **R5** DRS continuously moves a VM (1+ moves/min) | INFRA(DRS) → OBSERVE; status echo cache suppresses redundant K8s patches |
| **R6** VADP snapshot create/remove every ~4 hours | VENDOR → OBSERVE; coalescing collapses 4-event window to 1 ring entry |
| **R7** Admin power-off during consumer's clone-from-snapshot reconcile | ADOPT writes `spec.powerState=PoweredOff`; admin runbook: assert `PauseVMExtraConfigKey=True` before destructive ops |
| **R8** Two parallel admin ops on same VM (vMotion + ReconfigVM) | Both land in aggregator as separate batches; controller processes serially via per-key workqueue |
| **R9** Admin destroys VM; K8s VM has finalizer | LOST condition; CR NOT deleted; finalizer remains; consumer runs `kubectl delete vm` |
| **R10** Cross-VC migration: destination VC tries to adopt the new MoRef | Destination MoRef has no `status.uniqueID` index match → dropped; stays unrecognized until admin runs `ImportVM` |
| **R11** Leave event races `RetrieveProperties` disambiguation: VM re-registered before follow-up call | BiosUUID match → brief unregistered+re-registered pair; mismatch → `LeaveAmbiguous` |
| **R12** `watcher.Cache` keyed by MoRef holds stale entry after MoRef reuse (rare Destroy + re-Register) | On `Enter` for known MoRef whose cached BiosUUID mismatches K8s VM's, evict entry and proceed as fresh `Enter` |
| **R13** `ManagedBy` cleared by hostile admin (ADM-58) | REVERT re-sets `ManagedByExtensionKey`; if REVERT fails (DisableMethods block), set `VirtualMachineLost{Reason=ManagedByLost}` + Critical event |
| **R14** Scheduled task fires nightly to power off VM; consumer wants it always on | Each fire detected as ADM-01 and ADOPTED; consumer must remove the scheduled task in vSphere; `ScheduledTaskActive` condition surfaces conflict |
| **R15** Leader switch during active reconcile | Event replay up to `EventReplayWindow` runs before any decision; periodic resync fires aggressively at election time |
| **R16** Property collector returns null for a deleted optional property | Treated as REMOVE; handler classifies per op (e.g., `config.managedBy=null` → REVERT) |
| **R17** `customValue` change includes new field definition not yet cached | Look up `CustomFieldsManager.availableField[].key` on first observation; cache locally by field-key integer |
| **R18** Admin enables HA "no restart" then host fails; consumer sees PoweredOff with no host change | `VmDasResetFailedEvent` or `NotEnoughResourcesToStartVmEvent` triggers INFRA if EventSub on; without EventSub: UNKNOWN → OBSERVE; `VirtualMachineHAProtection{ProtectionState=unprotected}` set when `dasVmProtection.dasProtected=false` |
| **R19** VC client invalid on startup; EventSub / Watcher all fail | Existing `IsInvalidLogin`/`IsNotAuthenticatedError` retry loop; periodic resync only when cache is warm |
| **R20** Burst of 1000 admin ops (admin reorganizes 1000 VMs into new folder) | Aggregator channel depth 4096 (`AdminEventQueueSize`); drops counted in `aggregator_drops_total`; periodic resync recovers dropped events |
| **R21** 100k VMs with CD-ROM connect/disconnect on reboot | Per-op handler for CD-ROM connect/disconnect is OBSERVE-only → no K8s write; echo cache suppresses redundant observations |
| **R22** Admin runs `MarkAsTemplate` on powered-on VM | vSphere rejects (template requires power-off); if admin first powers off then templates: PoweredOff → ADOPT; template flip → REVERT |
| **R23** `lookupNamespacedName` returns wrong VM if MoRefs collide across VC restarts | Existing `status.uniqueID` index protection; no new MoRef-keyed caches |
| **R24** Two ADOPTs on same path within milliseconds | `MergeFromWithOptimisticLock`; only one wins; other retries with fresh fetch + re-evaluation |
| **R25** Controller crashes after ADOPT but before status/condition write | Idempotency token (`last-adopt-event-uid = sha256(MoRef+setVersion+path+value)`) ensures re-processing same batch is no-op; status write is idempotent |
| **R26** VADP vendor SA mis-allow-listed → classified as ADMIN | With empty default allow-list: snapshot ops are OBSERVE; disk/NIC/method ops emit Warning but do not REVERT |
| **R27** Consumer and admin patch `spec.powerState` in same microsecond | Same end-state in common case; timestamp comparison resolves; audit ring records concurrent change with source attribution |
| **R28** REVERT ping-pong on any op (generalized: folder reparent, RP reassignment, storage relocate) | Track revert count per `(VM, op)` in `RevertPingPongWindow` (default 5min); after `RevertPingPongMax=3` reverts → `VirtualMachineLost{Reason=<Op>RevertPingPong}`; counter resets after 1hr |
| **R29** EventSub delivers `VmReconfiguredEvent` BEFORE paired property change | Paired-event cache `PairWindow=60s`; event cached until property change arrives; if property arrives first, cache miss falls through to principal-based heuristics 4-9 |
| **R30** SDRS storage ping-pong (SDRS moves VM back after operator's corrective relocate) | REVERT for storage vMotion disabled; OBSERVE + `StorageClassDrift` condition only; admin must resolve SPBM policy conflict manually |
| **R31** FT secondary VM floods `watcher.Cache` | Secondary MoRef dropped at entry filter (MoRef != `status.uniqueID`); secondary NOT stored in `watcher.Cache` |
| **R32** Operator-initiated REVERT mis-attributed as new admin event | Standard reconciler runs as vm-operator SA; classifier heuristic #5 → INFRA → OBSERVE; echo cache hit → no K8s write; no feedback loop |
| **R33** First-enable shadow window expires during high-burst backup | Shadow window checked on every decision (wall-clock from persisted ConfigMap); VENDOR events still OBSERVE after expiry; no spec flood |
| **R34** `config.managedBy` + `config.extraConfig[pause]` both change in one batch: managedBy cleared + pause set | Batch processing: pause check runs first → OBSERVE; ManagedBy handler queues REVERT-on-next-batch; while pause is True, REVERT for ManagedBy suppressed; `ManagedByExtensionLost` condition set regardless |

---

## 7. Open design artifacts

The following detailed artifacts from the design phase are available for reference during
implementation:

- **Full design document**: `admin-driven-reconcile-backup/01-design.md` (preserved in this repo; this research.md synthesizes the key findings).
- **Operations analysis report**: `admin-driven-reconcile-backup/00-admin-operations-analysis.md` (62-operation catalog with verdicts, watcher gaps, and detection-surface additions).
- **Story breakdown**: `admin-driven-reconcile-backup/02-story-breakdown.md` (superseded by `tasks.md` in this directory).

---

## 8. Per-op handler stubs (illustrative)

Pseudocode for the most representative handlers.
Full implementation lives in `controllers/virtualmachine/reverseReconcile/handlers_*.go`.

### Power state (ADM-01..03)

```go
func handlePowerState(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    obs := e.NewValue.(string)
    switch obs {
    case "poweredOff", "poweredOn", "suspended":
        return Decision{Kind: ADOPT, SpecPath: "powerState", NewValue: vmopv1ToAPIPowerState(obs)}
    }
    return Decision{Kind: OBSERVE}
}
```

### Folder reparent (ADM-10/44)

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

### ManagedBy change (ADM-58)

```go
func handleManagedBy(ctx context.Context, e *AdminEvent, vm *vmopv1.VirtualMachine) Decision {
    observed := e.NewValue.(vimtypes.ManagedByInfo)
    if observed.ExtensionKey == ManagedByExtensionKey && observed.Type == ManagedByExtensionType {
        return Decision{Kind: OBSERVE}
    }
    return Decision{
        Kind:           REVERT,
        RevertReason:   "ManagedByExtensionLost",
        Severity:       Critical,
        AlertOnFailure: LostManagedBy,
    }
}
```

### Leave event

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
        return Decision{Kind: OBSERVE}
    default:
        return Decision{Kind: LOST, LostReason: "AmbiguousLeave", PreserveUniqueID: true}
    }
}
```

---

## 9. Glossary

| Term | Definition |
|---|---|
| **ADOPT** | Mutate `vm.Spec` to match observed vSphere state |
| **REVERT** | Inject a `GenericEvent` to trigger the standard reconciler to re-apply spec to vSphere |
| **OBSERVE** | Update status / condition / event only; do not touch spec or vSphere |
| **LOST** | The vSphere VM has disappeared; surface a `VirtualMachineLost` condition |
| **INFRA-driven** | Source = DRS, SDRS, HA, FDM, vCLS, DPM, etc.; non-human automation |
| **VENDOR-driven** | Source = VADP / NSX / SRM / backup-vendor service account |
| **ADMIN-driven** | Source = human admin via vSphere UI / SDK / SOAP / VAPI |
| **Catalog ID (ADM-NN)** | Identifier from `00-admin-operations-analysis.md` (62-op catalog) |
| **MoRef** | `vim.ManagedObjectReference` |
| **Leave event** | Property Collector `ObjectUpdateKindLeave` for a VM MoRef |
| **Paired-event cache** | In-memory TTL cache correlating EventSub `VmReconfiguredEvent` with the corresponding Property Collector change for source classification |
| **Echo cache** | In-memory cache of last-written status values; suppresses redundant K8s patches when the observed vSphere value has not changed since the last write |
| **Shadow window** | Period after feature-flag `false → true` transition during which all decisions are OBSERVE; prevents flood of ADOPT/REVERT against pre-existing drift |
| **Idempotency token** | `sha256(MoRef + setVersion + path + value)` stored as `last-adopt-event-uid` annotation; prevents duplicate ADOPT on operator restart |
| **Ping-pong** | Repeated REVERT cycle where an admin keeps making a change and the operator keeps undoing it; detected and escalated to LOST |
