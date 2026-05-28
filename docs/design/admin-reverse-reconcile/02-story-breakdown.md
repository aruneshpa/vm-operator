# Admin Reverse-Reconcile — Story Breakdown

**Design reference:** [`01-design.md`](01-design.md)  
**Analysis reference:** [`00-admin-operations-analysis.md`](00-admin-operations-analysis.md)  
**Status:** DRAFT rev 2 — ready for JIRA filing  

Stories are grouped into tracks that can progress largely in parallel once Track 0 is complete. Each story carries enough detail for any team member to implement it independently.

---

## Conventions

- **AC** = Acceptance criteria (numbered, testable).
- **Depends on:** story IDs that must be complete first.
- **Test:** primary test artifact (unit / intg / e2e).
- **Priority** uses milestone labels:
  - **P0 (v1 GA)** — Must-have for initial production enablement. Covers: power-state ADOPT, snapshot OBSERVE, placement OBSERVE/REVERT, NIC/disk guards, lifecycle-removal (LOST/REVERT), the decision engine core, and the single feature flag.
  - **P1 (v1.1)** — High value, but can ship in the first follow-up release. Covers: Event Manager subscriber (enables power-state ADOPT), annotation grammar validation, E2E tests, soak tests, snapshot status model, vendor ConfigMap.
  - **P2 (v2)** — Deferred. Covers: latency-sensitivity lift to first-class spec, cross-VC recovery.

---

## Track 0 — Foundation (serial prerequisite for all other tracks)

### F-01 — Feature flag + tuning ConfigMap wiring | P0

**Goal:** Add the single `Features.AdminReverseReconcile` bool to the operator's `FeatureStates` and implement the tuning ConfigMap reader (`vmoperator-reverse-reconcile-config`) per §8.

**Scope:**
- Add `AdminReverseReconcile bool` to `config/v1alpha3/feature_states_types.go` (or equivalent). Default `false`.
- Implement a ConfigMap watcher for `vmoperator-reverse-reconcile-config` in the operator namespace. On startup and on live-reload (via informer), parse all §8.2 keys into a typed `ReverseReconcileConfig` struct with sensible defaults (see §8.2 table). Validation: reject unknown keys; log warnings for out-of-range values.
- No sub-flags (`WatchPower`, `WatchPlacement`, etc.) — see §8.1 rationale.
- Gate all new code in subsequent stories behind `Features.AdminReverseReconcile`.

**AC:**
1. `make generate` succeeds; new field appears in deepcopy.
2. Operator starts with flag disabled → nothing registers, no ConfigMap watch.
3. Operator starts with flag enabled → ConfigMap loaded (or defaults applied); startup log emits `admin-reverse-reconcile: enabled`.
4. ConfigMap live-reload: change `resyncIntervalSeconds` → operator picks up within 30 s without restart.
5. Unit test: assert each default value.

**Test:** unit, `config/` package.

---

### F-02 — API extension: conditions, status fields, annotations | P0

**Goal:** Introduce all new API types, conditions, and annotations from §7 into the v1alpha6 API package.

**Scope (follow §7 exactly):**

*New conditions:*
`VirtualMachineReverseReconcileActive`, `VirtualMachineAdminPaused`, `VirtualMachineAdminManagedDevicesInvalid`, `VirtualMachineLost`, `VirtualMachineHAProtection`, `AffinityDrift`, `StorageClassDrift`, `ReverseReconcileQuarantined`, `VirtualMachineReverseReconcileDegraded`.

*New status types:*
- `AdminActivitySource` enum, `AdminActivityDecision` enum.
- `VirtualMachineAdminActivity` struct with `+kubebuilder:validation` markers per §7.2.
- `VirtualMachineVSphereObservedStatus` struct (includes `Tags`, `CustomAttributes`, `DisabledMethods`, `RootSnapshots`, `FTStatus`, `HAStatus`, `LastAdminActivityTime`).
- `VirtualMachineFTStatus`, `VirtualMachineHAStatus`.

*Add to `VirtualMachineStatus`:*
- `AdminActivity []VirtualMachineAdminActivity` (`+listType=atomic`, `MaxItems=10`).
- `VSphereObserved *VirtualMachineVSphereObservedStatus`.

*New annotation constants:* `LastAdoptSource`, `LastAdoptEventUID`, `LastAdoptTime`, `PausedBySource`, `PausedByPrincipal`, `BackupProxy`, `ManagedDevices`, `ManagedNICs`.

**AC:**
1. `make generate && make manifests` succeed.
2. All `+kubebuilder:validation` markers correct (`MaxItems`, `Enum`, `Required`).
3. `+listType`/`+listMapKey` markers on all new list fields.
4. No collision with existing `VirtualMachineProviderStatus` (new struct is `VirtualMachineVSphereObservedStatus`).
5. Deep-copy generated; existing API tests pass.

**Test:** unit — `go vet ./api/...`.  
**Depends on:** F-01.

---

## Track 1 — Detection layer

### D-01 — Watcher path extensions + Leave-event handling | P0

**Goal:** Extend `DefaultWatchedPropertyPaths()` and fix `ObjectUpdateKindLeave` handling per §5.1.

**Scope:**

*New paths (added when `AdminReverseReconcile=true`):*
- `config.managedBy`, `config.template`, `config.flags.changeTrackingEnabled`, `runtime.dasVmProtection`, `datastore`, `config.version`, `config.bootOptions`, `config.flags.vbsEnabled`, `config.latencySensitivity.level`, `disabledMethod`, `customValue`, `config.changeVersion`.
- Paths already watched today (verify, no duplication): `summary.runtime.host`, `summary.runtime.powerState`, `resourcePool`, `parent`, `config.extraConfig`, `config.hardware.device`, `summary.config.name`.

*Leave-event handling (§5.1.2):*
- Fix `ObjectUpdateKindLeave` in `watcher.go` (~line 573): (1) `RetrieveProperties` for BiosUUID/InstanceUUID disambiguation, (2) return `LeaveKind` (Unregistered/Destroyed/XvcMoved/Reparented/Ambiguous).

*Filter precondition (§5.1.0):*
- Drop any `ObjectUpdate` whose MoRef is not in the `status.uniqueID` index (non-WCP-managed VM).

**AC:**
1. `DefaultWatchedPropertyPaths()` returns full extended set when `AdminReverseReconcile=true`; returns only existing paths when `false`.
2. Leave-event vcsim test: unregister → `Unregistered`; destroy → `Destroyed`; ambiguous → `Ambiguous`.
3. Non-WCP MoRef dropped before aggregator.
4. Existing watcher tests pass.

**Test:** unit — `watcher_test.go`; intg — vcsim.  
**Depends on:** F-01.

---

### D-02 — Event Manager subscriber | P1

**Goal:** Implement the `EventSubscriber` per §5.2 to receive real-time vSphere events and correlate them with property-change batches.

**Scope:**
- New type `EventSubscriber` in `controllers/virtualmachine/reverseReconcile/event_subscriber.go`.
- Calls `EventManager.ReadNextEvents` (polling) with the §5.2 event whitelist (~30 event types).
- For each event: extract `CreatedTime`, `UserName` (principal), `LeafType`, VM MoRef.
- Feed into `pairedEventCache` (per VM, per leaf-type, TTL=60s) shared with source classifier.
- Guarded by `subscribeEvents` in tuning ConfigMap. Does not start when `false` (default).
- On startup / leader handoff: replay events within `eventReplayWindow` from vCenter's persistent event log (§6.2.4).

**AC:**
1. `subscribeEvents=false` → subscriber not started.
2. `VmPoweredOffEvent` received → entry in `pairedEventCache[MoRef][VmPoweredOff]`.
3. Cache entry expires after `PairWindow`.
4. Event replay on startup populates cache (vcsim: pre-populate event log; assert cache after leader election).
5. Reconnects after VC connectivity loss.
6. Table-driven principal extraction test for INFRA/VENDOR/ADMIN patterns.

**Test:** unit (mock Event Manager); intg (vcsim).  
**Depends on:** F-01, D-01.

**Why P1:** Power-state ADOPT (a P0 requirement) can function without EventSub — it just falls back to OBSERVE-only for unclassifiable power changes (§6.2.3). EventSub enables the full ADOPT path. Practically, v1 GA deployments will enable `subscribeEvents=true` via ConfigMap, but the code can ship in the first follow-up while the core framework lands first.

---

### ~~D-03 — CIS tag poller~~ — REMOVED (rev 2)

> **Removed.** vSphere tags are admin-only and not surfaced to the DevOps/consumer persona per VM Operator design invariant. The TagPoller added cost (500 API calls per 5-min interval at 40k VMs) with zero actionable signal — the only §6.4 decision for tag changes was OBSERVE, and vendor source classification is already handled by principal-name matching in EventSub. See §5.3 in the design doc for full rationale.

---

### D-04 — Periodic full-property resync | P0

**Goal:** Implement the periodic resync described in §5.1.4.

**Scope:**
- Goroutine in the controller's `Start()` method.
- Every `resyncIntervalSeconds` (default 1800): `RetrievePropertiesEx` for each managed VM; diff against watcher cache; emit `AdminEventBatch` for any drift.
- Stagger by `hash(MoRef) % interval`. At 40k VMs / 1800s = ~22 VMs/sec (see §5.5.5).
- Aggressive startup resync fires once at leader election time.
- Does NOT run if cache is stale (last success > 2× interval) → emit `Degraded{Reason=StaleCache}`.

**AC:**
1. After simulated missed event (disconnect/reconnect), resync emits the missed `AdminEventBatch`.
2. Resync at configured interval (fast interval in test).
3. Startup resync within 10 s of leader election.
4. Stale cache → `Degraded` condition, no resync.

**Test:** intg (vcsim: disconnect, modify properties, reconnect, assert resync detects change).  
**Depends on:** D-01.

---

## Track 2 — Decision engine

### E-01 — Source classifier | P0

**Goal:** Implement `ClassifySource(batch AdminEventBatch) (Source, Principal, err)` per §6.2.

**Scope:**
All 9 heuristics in order (§6.2.2). Safe-default: UNKNOWN + power-state → OBSERVE. Empty vendor allow-list → all VENDOR heuristics degrade to OBSERVE.

**AC:**
1. All 9 heuristics covered by table-driven unit tests.
2. DRS `DrsVmMigratedEvent` → `INFRA`.
3. Empty principal + system event → `INFRA`.
4. Empty vendor allow-list: VADP principal → falls to ADMIN; Warning Event emitted.
5. No event in paired cache, no known principal → `UNKNOWN`; power-state → OBSERVE.

**Test:** unit — `classifier_test.go`.  
**Depends on:** D-02 (soft: classifier works without EventSub — events simply won't be in the paired cache; heuristics #6–9 still function).

---

### E-02 — Pause and suppression check | P0

**Goal:** Implement §6.3 pause check, import-window guard, and shadow-window guard.

**Scope:**
- Batch atomicity (CC1-01): pause evaluated from AFTER state of `AdminEventBatch`.
- Import-window: OBSERVE if import/restore/failover annotation present within `importWindowExpiry`.
- Shadow-window: OBSERVE for `firstEnableShadowDuration` after first enablement.
- Auto-pause flip: VENDOR/INFRA destructive ops set `PausedVMLabelKey=vendor|infra`.

**AC:**
1. Pause=True → OBSERVE.
2. Pause + power-off in same batch → OBSERVE (CC1-01).
3. Import-window active → OBSERVE.
4. Shadow-window active → OBSERVE + warning event.

**Test:** unit; intg.  
**Depends on:** E-01.

---

### E-03 — Per-op decision table | P0

**Goal:** Implement the `Decide(op, source, vm) Decision` function for every §6.4 catalog entry.

**Scope:**
- Handler dispatch table: `(PropertyPath → HandlerFunc)` and `(EventLeafType → HandlerFunc)`.
- All ADMIN/INFRA/VENDOR/UNKNOWN columns from §6.4.
- `backup-proxy=true` → OBSERVE + no status mutation for disk ops.
- Storage vMotion → OBSERVE + `StorageClassDrift`; never REVERT.
- FT secondary → evict `watcher.Cache` on role-swap.

**AC:**
1. Table-driven test for every §6.4 row.
2. Power state: ADMIN → ADOPT; INFRA → OBSERVE; UNKNOWN (no EventSub) → OBSERVE.
3. Folder reparent → REVERT.
4. ManagedBy cleared → REVERT + Critical.
5. Storage vMotion → OBSERVE only.

**Test:** unit.  
**Depends on:** E-01, E-02, F-02.

---

### E-04 — Invariant guards | P0

**Goal:** Implement §6.5 invariant guard layer (runs after per-op decision, before mutation).

**Scope:**
Guards #1–8 from §6.5. Key: `IsReverseReconcileWrite` webhook bypass wired in; ExtraConfig reserved-prefix guard; import-window pass-through.

**AC:**
1. ADOPT of `spec.storageClass` → `InvariantViolationDowngrade`; downgraded to OBSERVE.
2. REVERT when `ReconfigVM` is disabled → `Degraded` condition.
3. ADOPT of `guestinfo.*` → rejected; OBSERVE + event.
4. Webhook rejection → OBSERVE fallback.

**Test:** unit; intg.  
**Depends on:** E-03.

---

### E-05 — Conflict resolver | P0

**Goal:** Implement §6.6 timestamp-based last-writer-wins conflict resolution.

**Scope:**
- `tVC` from property collector or Event Manager.
- `tK8s` from `*Synced` condition `LastTransitionTime` → `managedFields` → `creationTimestamp` fallback.
- Skew tolerance from `conflictSkewSeconds`.

**AC:**
1. `tVC >> tK8s` → ADOPT.
2. `tK8s >> tVC` → OBSERVE.
3. Within skew → `ConcurrentAdminConsumerChange` event; ADOPT.
4. Undefined `tK8s` → fallback → ADOPT.

**Test:** unit.  
**Depends on:** F-02.

---

### E-06 — Decision executor (ADOPT / REVERT / OBSERVE / LOST) | P0

**Goal:** Implement the four execution paths from §6.7.

**Scope:**
- OBSERVE: status + condition + ring buffer + K8s event.
- ADOPT: idempotency token, `MergeFromWithOptimisticLock`, split retry by error class, TTL removal of adopt annotations.
- REVERT: `GenericEvent` into standard reconcile queue; ping-pong detector.
- LOST: condition set; CR not deleted.
- Supporting: vendor-event coalescing, status echo cache.

**AC:**
1. ADOPT: idempotency token prevents duplicate; Conflict retries ≤ `adoptionRetryMax`; Forbidden → OBSERVE.
2. REVERT: GenericEvent on VM key; no direct vSphere call.
3. LOST: condition set; CR preserved.
4. Vendor coalescing: 10 events → 1 ring entry.
5. Echo cache: unchanged status → no K8s PATCH.
6. Ping-pong: 3 REVERTs → LOST.

**Test:** unit; intg (vcsim ADOPT end-to-end).  
**Depends on:** E-03, E-04, E-05, F-02.

---

## Track 3 — Controller wiring

### C-01 — ReverseReconcile controller registration | P0

**Goal:** Create `ReverseReconcileController`, register with the controller-manager, wire to aggregator.

**Scope:**
- New file `controllers/virtualmachine/reverseReconcile/controller.go`.
- `controller-runtime` `Reconciler`. Trigger: `GenericEvent` from aggregator + periodic resync.
- Same `Manager` as `VirtualMachineReconciler` (shared leader election).
- Pipeline: `pairedEventCache → ClassifySource → PauseCheck → InvariantGuards → ConflictResolver → Decide → Execute`.
- Aggregator: buffered channel, `adminEventQueueSize`.

**AC:**
1. Flag off → controller not registered.
2. Flag on → controller starts; workqueue depth metric.
3. Two events for same VM → processed sequentially.
4. Channel overflow → drop + metric + resync recovers.

**Test:** intg (vcsim: admin power-off → spec adopts `PoweredOff`).  
**Depends on:** E-06, D-01, D-04.

---

### C-02 — RBAC and vCenter permissions | P0

**Goal:** Add K8s RBAC markers and vCenter privilege self-check.

**Scope:**
- `+kubebuilder:rbac` markers from §10.1 on controller.
- Startup self-check `CheckVCenterPrivileges()` → `Degraded` condition if missing.
- `make manifests` generates new RBAC rules.

**AC:**
1. `make manifests` → new `ClusterRole` rules.
2. Missing VC privilege → `Degraded` condition; OBSERVE-only mode.
3. `virtualmachines/status update` permitted.

**Test:** unit (mock AuthorizationManager).  
**Depends on:** C-01.

---

## Track 4 — Webhook changes

### W-01 — Validation webhook: `IsReverseReconcileWrite` bypass | P0

**Goal:** Implement the `IsReverseReconcileWrite` helper and wire it per §7.4.1.

**Scope:**
- Returns true IFF: (a) `IsVMOperatorServiceAccount`, (b) `last-adopt-source=vcenter`, (c) diff within `adoptablePathWhitelist`.
- Bypass does NOT fire for `system:masters` / `kube-admin` / `PRIVILEGED_USERS`.

**AC:**
1. Operator SA + annotation + whitelisted path → accepted.
2. `system:masters` + same → rejected.
3. Operator SA + no annotation → rejected.
4. Operator SA + annotation + non-whitelisted path → rejected.

**Test:** unit — `webhooks/virtualmachine/validation/`.  
**Depends on:** F-02.

---

### W-02 — Mutation webhook: adopt-annotation lifecycle | P0

**Goal:** Implement annotation-strip behavior per §7.4.2.

**Scope:**
- Operator SA introducing `last-adopt-*` → NOT stripped.
- Non-operator user with stale annotations → stripped.

**AC:**
1. Operator SA adds `last-adopt-source` → preserved.
2. User `kubectl edit` passes through stale annotation → stripped.

**Test:** unit.  
**Depends on:** W-01.

---

### W-03 — Annotation grammar validation | P1

**Goal:** Validating-webhook entry for `admin-managed-devices`, `admin-managed-nics`, `backup-proxy` per §7.4.3.

**Scope:**
- Regex validation: `^[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*$`, max length 1024. No wildcards.
- `backup-proxy`: must be literal `"true"`.

**AC:**
1. `admin-managed-devices: "4096,4097"` → accepted.
2. `admin-managed-devices: "*"` → rejected.
3. `backup-proxy: "false"` → rejected.

**Test:** unit.  
**Depends on:** F-02.

---

### W-04 — `paused` label value extension | P0

**Goal:** Extend `PausedVMLabelKey` values to include `vendor` and `infra` per §7.4.4.

**Scope:**
- Validation webhook: allow `admin|devops|both|vendor|infra`.
- `vendor`/`infra` accepted from operator SA only.
- Standard reconciler behavior unchanged (only checks `admin|devops|both`).

**AC:**
1. Operator SA sets `PausedVMLabelKey=vendor` → accepted.
2. Regular user sets `PausedVMLabelKey=vendor` → rejected.
3. Standard reconciler ignores `vendor`/`infra` (no behavioral change).

**Test:** unit.  
**Depends on:** F-02.

---

## Track 5 — Observability

### O-01 — Prometheus metrics | P0

**Goal:** Register all §9.3 metrics under `vmoperator_reverse_reconcile_`.

**Scope:**
`events_total`, `adoption_conflicts_total`, `invariant_violations_total`, `lost_vms_total`, `resync_duration_seconds`, `pending_events_queue_depth`, `aggregator_drops_total`, `echo_cache_hits_total`, `vendor_coalesce_total`.

**AC:**
1. Metrics registered when flag is on.
2. After one ADOPT: `events_total{source=vcenter-admin,kind=power_state,decision=ADOPT}` = 1.
3. After queue overflow: `aggregator_drops_total` incremented.

**Test:** unit.  
**Depends on:** C-01.

---

### O-02 — Audit ring buffer maintenance | P0

**Goal:** Ensure `status.adminActivity[]` ring is bounded, coalesced, and crash-safe.

**Scope:**
- Bounded to `auditRingSize` (default 10); oldest evicted.
- Vendor coalesce: 10 events → 1 entry with `Count=10`.
- Crash recovery: ring persisted in `status`; no duplicates from idempotency token.

**AC:**
1. `auditRingSize+1` events → ring has `auditRingSize` entries.
2. 10 vendor events in coalesce window → 1 entry.
3. Crash + restart → ring consistent.

**Test:** unit.  
**Depends on:** E-06.

---

## Track 6 — Rollout / migration

### M-01 — First-enablement shadow window | P0

**Goal:** Implement §11.2 shadow window to prevent first-enable flood.

**Scope:**
- On `false → true` transition: record `firstEnableTime` in ConfigMap `vmoperator-reverse-reconcile-state`.
- For `firstEnableShadowDuration`, all decisions = OBSERVE.
- After expiry: normal decisions.
- Re-enable after disable: do NOT reset `firstEnableTime`.

**AC:**
1. First enable → OBSERVE for window duration.
2. After window → normal decisions.
3. Re-enable → no re-apply.
4. `Degraded{Reason=ShadowWindow}` condition during window.

**Test:** unit; intg.  
**Depends on:** C-01.

---

### M-02 — Upgrade compatibility validation | P0

**Goal:** Verify CRD changes are additive; no storage-version bump needed.

**Scope:**
- `make generate && make manifests` → compare CRD YAML.
- JSON round-trip test (old struct ↔ new struct).
- Confirm no `+kubebuilder:storageversion` conflict.

**AC:**
1. No `required` fields added to existing objects.
2. Round-trip test passes.
3. PR description confirms no storage-version bump.

**Test:** unit (round-trip); CI manifest diff.  
**Depends on:** F-02.

---

## Track 7 — Tests

### T-01 — Unit test foundation | P0

**Goal:** ≥ 85% branch coverage on: `watcher.go` new paths, `classifier.go`, `decisions.go`, `conflict_resolver.go`, `IsReverseReconcileWrite`.

**AC:**
1. `go test ./controllers/virtualmachine/reverseReconcile/... -v` passes.
2. Coverage ≥ 85% on listed files.
3. All 9 classifier heuristics, all §6.4 decision rows, all §6.6 conflict branches tested.

**Depends on:** E-01 through E-06, W-01 through W-03.

---

### T-02 — Integration test suite (vcsim) | P0

**Goal:** Implement all vcsim integration tests from §13.2.

**Scope:** One test file per scenario group:
- Batch atomicity (CC1-01), Leave disambiguation, ManagedBy REVERT, `admin-managed-devices`, `backup-proxy`, shadow window.

**AC:**
1. All scenarios pass with vcsim.
2. `go test -timeout 5m`.
3. Tagged `//go:build integration`.

**Depends on:** C-01, T-01.

---

### T-03 — E2E test suite | P1

**Goal:** Implement E2E tests from §13.3 under `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`.

**v1 GA minimum (P0 subset):**
- `power_state.go`, `pause_batch.go`, `managedby_change.go`, `lost_destroy.go`, `webhook_bypass.go`, `shadow_window.go`.

**v1.1 (P1):**
- `ha_failover.go`, `storage_drift.go`, `backup_proxy.go`, `import_window.go`, `concurrent_change.go`, `drs_host_change.go`, `vadp_snapshot.go`.

**AC:**
1. Ginkgo label pattern per `test/e2e/README.md`.
2. Shared helpers from `lib/vmoperator`.
3. v1 GA tests pass in CI E2E pipeline.

**Depends on:** T-02, all Tracks 0–6.

---

### T-04 — Soak / chaos tests | P1

**Goal:** Implement §13.4 soak tests.

**Scope:**
- 10k events/min via vcsim → no drops, no stall.
- Kill pod mid-ADOPT → idempotency token recovery.
- 1k VMs + hourly DRS → echo cache prevents churn.

**Depends on:** T-02.

---

## Track 8 — Supplementary stories

### SST-01 — `VirtualMachineSnapshot` status model extension | P1

**Goal:** Extend `VirtualMachineSnapshotReference` with `MoRef`, `CreateTime`, `Description` (prerequisite for ADM-16 OBSERVE path).

**Depends on:** F-02.

---

### SST-02 — Vendor allow-list ConfigMap bootstrap | P1

**Goal:** Author default `vmoperator-reverse-reconcile-vendors` ConfigMap and wire loading into source classifier.

**Scope:**
- Helm chart default with example vendor principal-name patterns (Veeam, Commvault, Veritas, Cohesity, Zerto).
- Absent ConfigMap → Warning event; VENDOR decisions degrade to OBSERVE.
- Live-reload via watch.

**AC:**
1. Absent → Warning; VENDOR → OBSERVE.
2. Present with 1 vendor principal entry → correct classification.
3. Live-reload within 30 s.

**Depends on:** E-01.

---

## Priority summary

| Priority | Stories | Count | Theme |
|---|---|---|---|
| **P0 (v1 GA)** | F-01, F-02, D-01, D-04, E-01, E-02, E-03, E-04, E-05, E-06, C-01, C-02, W-01, W-02, W-04, O-01, O-02, M-01, M-02, T-01, T-02 | **21** | Core framework: feature flag, API types, property-collector detection, decision engine (all ops), webhooks, metrics, shadow window, unit+intg tests. Power-state is OBSERVE-only without EventSub (P1) — but placement REVERT, lifecycle LOST, ManagedBy REVERT, snapshot OBSERVE, and the full decision table all work. |
| **P1 (v1.1)** | D-02, W-03, T-03, T-04, SST-01, SST-02 | **6** | Event Manager subscriber (unlocks power-state ADOPT), annotation grammar validation, E2E tests, soak tests, snapshot status model, vendor ConfigMap. |
| **P2 (v2)** | — | **0** | Latency-sensitivity lift, cross-VC recovery (tracked as open questions, not stories yet). |

**Revised P0 scope rationale:** The design says "v1 focuses on power-state, snapshot, placement, NIC, hardware-class, and lifecycle-removal categories." After re-evaluation:

- **Power-state**: Property collector detects changes (already watched); the decision engine processes them. Without EventSub, power-state changes are OBSERVE-only (safe default per §6.2.3). With EventSub (P1), they become ADOPT. This is acceptable for v1 GA — the consumer sees the power-state change in status and conditions, and the admin can still override.
- **Snapshot**: OBSERVE path works with property collector alone. SST-01 (snapshot status model) is P1 because the existing `rootSnapshot`/`snapshot` paths already provide basic signal.
- **Placement**: vMotion/folder/RP detection works via property collector. REVERT for folder/RP works immediately. Storage vMotion is OBSERVE-only.
- **NIC/Disk**: `config.hardware.device` is already watched. `admin-managed-devices` annotation grammar (W-03) is P1 because the core guard logic (REVERT if not in annotation list) works with the annotation; validation of malformed values is a polish item.
- **Hardware-class**: CPU/mem reconfig is detected via `config.hardware.device` (already watched). REVERT via class invariant guard is P0.
- **Lifecycle-removal**: LOST (destroy/unregister) requires the Leave-event fix (D-01), which is P0.
- **Event Manager subscriber (D-02)**: Moved to P1 because it's an optional enhancer — the framework is fully functional without it, just more conservative (OBSERVE instead of ADOPT for power-state).

---

## Story dependency map

```
Track 0 (Foundation)  F-01 → F-02
                               │
         ┌─────────────────────┼───────────────┐
         │                     │               │
Track 1  D-01                D-02 (P1)       D-03 (REMOVED)
(Detect)   └─ D-04              │
         │                     │
Track 2  E-01 (soft dep on D-02)
(Decision)  └─ E-02 ← E-01
                └─ E-03 ← E-01,E-02
                    └─ E-04 ← E-03
                        └─ E-05 ← F-02
                            └─ E-06 ← E-03,E-04,E-05
         │
Track 3  C-01 ← E-06,D-01,D-04
(Controller)  └─ C-02 ← C-01
         │
Track 4  W-01 ← F-02
(Webhooks)  ├─ W-02 ← W-01
            ├─ W-03 ← F-02 (P1)
            └─ W-04 ← F-02
         │
Track 5  O-01 ← C-01
(Metrics)   └─ O-02 ← E-06
         │
Track 6  M-01 ← C-01
(Rollout)   └─ M-02 ← F-02
         │
Track 7  T-01 ← E-01..E-06,W-01..W-03
(Tests)     ├─ T-02 ← C-01,T-01
            ├─ T-03 ← T-02, all Tracks (P1)
            └─ T-04 ← T-02 (P1)
         │
Track 8  SST-01 ← F-02 (P1)
(Supp.)     └─ SST-02 ← E-01 (P1)
```

**Total stories: 27** (21 P0, 6 P1, 0 P2)  
**P0 critical path:** F-01 → F-02 → D-01 → E-01 → E-02 → E-03 → E-04 → E-05 → E-06 → C-01 → T-02
