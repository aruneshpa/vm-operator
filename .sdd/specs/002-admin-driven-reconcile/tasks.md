# Tasks: Admin-Driven Reverse Reconcile

- **Spec**: [`spec.md`](./spec.md)
- **Plan**: [`plan.md`](./plan.md)
- **Epic**: TBD
- **Format**: `[T###] [P?] [Story?] [vmop-NNN?] Description — file paths`

Tasks marked `[P]` within a phase may run in parallel (different files, no dependency
between them). Each task that produces shipping code requires a `[vmop-NNN]` tag once
JIRA stories are filed under the epic.

Priority legend: **P0** = v1 GA must-have; **P1** = v1.1; all tasks below are P0 unless
noted.

---

## Phase 0 — Design review / architecture (no code)

- [ ] T001 Review `spec.md` with product and resolve both `[NEEDS CLARIFICATION]` items
- [ ] T002 File the JIRA epic; update `spec.md` and `plan.md` headers with `Epic: vmop-NNN`
- [ ] T003 Resolve `adoptablePathWhitelist` v1 GA contents; record decision in `plan.md`
      and in the tuning ConfigMap default

*Gate: T001–T003 must be complete before Phase 1 code work begins.*

---

## Phase 1 — Setup (scaffolding; parallel)

- [ ] T010 [P] Scaffold `pkg/util/vsphere/eventsub/` package skeleton —
      `pkg/util/vsphere/eventsub/eventsub.go`
- [ ] T011 [P] Scaffold `controllers/virtualmachine/reverseReconcile/` package skeleton —
      `controllers/virtualmachine/reverseReconcile/controller.go`
- [ ] T012 [P] Add `AdminReverseReconcile bool` to `pkgcfg.Features`; wire guard in
      `controllers/controllers.go` and `services/vm-watcher/vm_watcher_service.go` —
      `pkg/config/features.go`, `controllers/controllers.go`,
      `services/vm-watcher/vm_watcher_service.go`

---

## Phase 2 — API + tuning foundation (F-01, F-02)

Story F-01 — Feature flag + tuning ConfigMap wiring (P0):

- [ ] T020 [vmop-NNN] Implement `vmoperator-reverse-reconcile-config` ConfigMap reader
      with live-reload; populate `ReverseReconcileConfig` struct with all §8.2 defaults —
      `controllers/virtualmachine/reverseReconcile/config.go`
- [ ] T021 [vmop-NNN] Unit tests for ConfigMap parsing; assert each default value;
      assert live-reload updates the in-memory config —
      `controllers/virtualmachine/reverseReconcile/config_test.go`

Story F-02 — API extension (P0):

- [ ] T022 [vmop-NNN] Add 9 new condition type constants to `api/v1alpha6/virtualmachine_types.go`
      (`VirtualMachineReverseReconcileActive`, `VirtualMachineAdminPaused`,
      `VirtualMachineAdminManagedDevicesInvalid`, `VirtualMachineLost`,
      `VirtualMachineHAProtection`, `AffinityDrift`, `StorageClassDrift`,
      `ReverseReconcileQuarantined`, `VirtualMachineReverseReconcileDegraded`) —
      `api/v1alpha6/virtualmachine_types.go`
- [ ] T023 [vmop-NNN] Add new status types: `AdminActivitySource` enum,
      `AdminActivityDecision` enum, `VirtualMachineAdminActivity` struct,
      `VirtualMachineVSphereObservedStatus` struct, `VirtualMachineFTStatus`,
      `VirtualMachineHAStatus`; add `AdminActivity` and `VSphereObserved` fields to
      `VirtualMachineStatus` — `api/v1alpha6/virtualmachine_types.go`
- [ ] T024 [vmop-NNN] Add 8 new annotation key constants —
      `api/v1alpha6/virtualmachine_types.go`
- [ ] T025 [vmop-NNN] Run `make generate && make manifests`; verify deepcopy and CRD YAML
      correct; JSON round-trip test (old struct ↔ new struct) —
      `api/v1alpha6/zz_generated.deepcopy.go`,
      `config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml`
- [ ] T026 [vmop-NNN] Unit tests: `+kubebuilder:validation` markers correct; no collision
      with existing field names — `api/v1alpha6/virtualmachine_types_test.go`

*Gate: T025 must pass before Phase 3 begins.*

---

## Phase 3 — Detection layer (D-01, D-04)

Story D-01 — Watcher path extensions + Leave-event handling (P0):

- [ ] T030 [vmop-NNN] Extend `DefaultWatchedPropertyPaths()` with ~12 new paths gated on
      `AdminReverseReconcile=true`; verify no duplication with existing paths —
      `pkg/util/vsphere/watcher/watcher.go`
- [ ] T031 [vmop-NNN] Fix `ObjectUpdateKindLeave` arm: add synchronous
      `RetrieveProperties` disambiguation; return `LeaveKind` (Unregistered / Destroyed /
      XvcMoved / Reparented / Ambiguous) — `pkg/util/vsphere/watcher/watcher.go`
- [ ] T032 [vmop-NNN] Add §5.1.0 filter precondition: drop any `ObjectUpdate` whose MoRef
      is not in the `status.uniqueID` index — `pkg/util/vsphere/watcher/watcher.go`
- [ ] T033 [vmop-NNN] Unit + vcsim integration tests: Leave-event disambiguation (unregister
      → `Unregistered`; destroy → `Destroyed`; ambiguous → `Ambiguous`); non-WCP MoRef
      dropped; existing watcher tests still pass —
      `pkg/util/vsphere/watcher/watcher_test.go`

Story D-04 — Periodic full-property resync (P0):

- [ ] T034 [vmop-NNN] Implement periodic resync goroutine in controller `Start()`: stagger
      by `hash(MoRef) % interval`; aggressive startup resync at leader election; stale-cache
      guard — `controllers/virtualmachine/reverseReconcile/resync.go`
- [ ] T035 [vmop-NNN] vcsim integration test: disconnect watcher; modify VM properties;
      reconnect; assert resync emits the missed `AdminEventBatch` —
      `controllers/virtualmachine/reverseReconcile/resync_test.go`

---

## Phase 4 — Decision engine (E-01 through E-06)

Story E-01 — Source classifier (P0):

- [ ] T040 [vmop-NNN] Implement `ClassifySource()` with all 8 heuristics; safe-default for
      UNKNOWN + power-state — `controllers/virtualmachine/reverseReconcile/classifier.go`
- [ ] T041 [vmop-NNN] Table-driven unit tests for all 8 heuristics; empty vendor allow-list
      safe-default test — `controllers/virtualmachine/reverseReconcile/classifier_test.go`

Story E-02 — Pause and suppression check (P0):

- [ ] T042 [vmop-NNN] Implement batch-atomicity pause check (`AdminEventBatch`
      `AfterExtraConfig`); import-window guard; shadow-window guard; auto-pause flip with
      label attribution by source — `controllers/virtualmachine/reverseReconcile/pause.go`
- [ ] T043 [vmop-NNN] Unit tests: pause=True in batch → OBSERVE; pause+power-off in same
      batch → OBSERVE (CC1-01); shadow-window → OBSERVE; import-window → OBSERVE —
      `controllers/virtualmachine/reverseReconcile/pause_test.go`

Story E-03 — Per-op decision table (P0):

- [ ] T044 [vmop-NNN] Implement handler dispatch table and all per-op handlers: power state,
      placement (vMotion, folder, RP, storage vMotion), hardware, snapshots, lifecycle (LOST,
      ManagedBy, template), NIC/disk, FT, DRS rules, scheduled tasks, disable methods —
      `controllers/virtualmachine/reverseReconcile/decisions.go`
- [ ] T045 [vmop-NNN] Table-driven unit tests for every §6.4 catalog row —
      `controllers/virtualmachine/reverseReconcile/decisions_test.go`

Story E-04 — Invariant guards (P0):

- [ ] T046 [vmop-NNN] Implement 8 invariant guards: class invariant, encryption class,
      StorageClass immutability, `minHardwareVersion` monotonicity, webhook approval,
      cardinality safety, ExtraConfig reserved prefix, import-window pass-through —
      `controllers/virtualmachine/reverseReconcile/guards.go`
- [ ] T047 [vmop-NNN] Unit tests: each guard triggers `InvariantViolationDowngrade`; REVERT
      when `ReconfigVM` disabled; webhook rejection → OBSERVE —
      `controllers/virtualmachine/reverseReconcile/guards_test.go`

Story E-05 — Conflict resolver (P0):

- [ ] T048 [vmop-NNN] Implement timestamp-based last-writer-wins conflict resolver:
      `tVC` from property collector / EventSub; `tK8s` from `*Synced` condition
      `LastTransitionTime` → `managedFields` → `creationTimestamp` fallback —
      `controllers/virtualmachine/reverseReconcile/conflict.go`
- [ ] T049 [vmop-NNN] Table-driven unit tests: `tVC >> tK8s` → ADOPT; `tK8s >> tVC` →
      OBSERVE; within skew → ADOPT + `ConcurrentAdminConsumerChange` event; undefined `tK8s`
      → fallback → ADOPT — `controllers/virtualmachine/reverseReconcile/conflict_test.go`

Story E-06 — Decision executor (P0):

- [ ] T050 [vmop-NNN] Implement OBSERVE executor: `status.VSphereObserved` update;
      `status.adminActivity[]` ring buffer (FIFO, bounded by `auditRingSize`); K8s event
      emit; vendor-event coalescing; status echo cache —
      `controllers/virtualmachine/reverseReconcile/executor.go`
- [ ] T051 [vmop-NNN] Implement ADOPT executor: idempotency token (`sha256` adoptKey);
      `MergeFromWithOptimisticLock`; split retry by error class (Conflict vs Forbidden);
      annotation TTL scheduling; do NOT write `status.observedGeneration` —
      `controllers/virtualmachine/reverseReconcile/executor.go`
- [ ] T052 [vmop-NNN] Implement REVERT executor: `GenericEvent` injection into standard
      reconcile queue; ping-pong detector (escalate to LOST after N REVERTs in window) —
      `controllers/virtualmachine/reverseReconcile/executor.go`
- [ ] T053 [vmop-NNN] Implement LOST executor: set `VirtualMachineLost` condition; emit
      event; preserve CR and PVCs — `controllers/virtualmachine/reverseReconcile/executor.go`
- [ ] T054 [vmop-NNN] Unit tests: idempotency prevents duplicate ADOPT; Conflict retry ≤
      `adoptionRetryMax`; Forbidden → OBSERVE; vendor coalescing; echo cache; ping-pong
      escalation — `controllers/virtualmachine/reverseReconcile/executor_test.go`

---

## Phase 5 — Controller wiring and RBAC (C-01, C-02)

Story C-01 — Controller registration (P0):

- [ ] T055 [vmop-NNN] Implement `ReverseReconcileController` struct; register with Manager;
      wire aggregator channel; implement `Reconcile()` calling the full pipeline (classify
      → pause → decide → guard → conflict → execute) —
      `controllers/virtualmachine/reverseReconcile/controller.go`,
      `controllers/controllers.go`
- [ ] T056 [vmop-NNN] vcsim integration test: admin power-off → spec adopts `PoweredOff`;
      two events for same VM processed sequentially; channel overflow → drop + metric —
      `controllers/virtualmachine/reverseReconcile/controller_test.go`

Story C-02 — RBAC and vCenter permissions (P0):

- [ ] T057 [vmop-NNN] Add `+kubebuilder:rbac` markers to controller; run `make manifests`;
      implement `CheckVCenterPrivileges()` startup self-check →
      `VirtualMachineReverseReconcileDegraded{Reason=InsufficientVCenterPrivileges}` if
      privileges missing — `controllers/virtualmachine/reverseReconcile/controller.go`,
      `config/rbac/role.yaml`
- [ ] T058 [vmop-NNN] Unit test: mock `AuthorizationManager` returns missing privilege →
      `Degraded` condition set; all privileges present → no condition —
      `controllers/virtualmachine/reverseReconcile/controller_test.go`

---

## Phase 6 — Event Manager subscriber (D-02) [P1]

Story D-02 — Event Manager subscriber (P1 — required for power-state ADOPT in production):

- [ ] T060 [P1] [vmop-NNN] Implement `EventSubscriber` goroutine: poll
      `EventManager.ReadNextEvents`; whitelist ~30 event types; populate `pairedEventCache`
      (TTL=60s); startup replay within `eventReplayWindow` —
      `pkg/util/vsphere/eventsub/eventsub.go`
- [ ] T061 [P1] [vmop-NNN] Wire EventSubscriber start/stop in
      `services/vm-watcher/vm_watcher_service.go`; guard by `subscribeEvents` ConfigMap key
- [ ] T062 [P1] [vmop-NNN] Unit tests: mock Event Manager; whitelist filter; `PairWindow`
      expiry; event replay; reconnect on VC connectivity loss —
      `pkg/util/vsphere/eventsub/eventsub_test.go`

---

## Phase 7 — Webhook changes (W-01, W-02, W-04)

Story W-01 — Validation webhook bypass (P0):

- [ ] T063 [vmop-NNN] Implement `IsReverseReconcileWrite()` helper; add bypass branches on
      `spec.storageClass`, network MTU/MAC immutability checks; negative tests for
      `system:masters` bypass attempt —
      `webhooks/virtualmachine/validation/virtualmachine_validator.go`
- [ ] T064 [vmop-NNN] Unit tests: operator SA + annotation + whitelisted path → accepted;
      `system:masters` + annotation → rejected; operator SA + non-whitelisted path →
      rejected — `webhooks/virtualmachine/validation/virtualmachine_validator_test.go`

Story W-02 — Mutation webhook annotation lifecycle (P0):

- [ ] T065 [vmop-NNN] Conditional annotation-strip: skip stripping `last-adopt-*` for
      operator SA requests; strip for non-operator user requests —
      `webhooks/virtualmachine/mutation/virtualmachine_mutator.go`
- [ ] T066 [vmop-NNN] Unit tests: operator SA introduces annotation → preserved; user
      passes through annotation → stripped —
      `webhooks/virtualmachine/mutation/virtualmachine_mutator_test.go`

Story W-03 — Annotation grammar validation [P1]:

- [ ] T067 [P1] [vmop-NNN] Add validating-webhook entry for `admin-managed-devices`,
      `admin-managed-nics`, `backup-proxy` with regex grammar validation —
      `webhooks/virtualmachine/validation/virtualmachine_validator.go`
- [ ] T068 [P1] [vmop-NNN] Unit tests: valid value accepted; wildcard rejected; wrong
      `backup-proxy` value rejected —
      `webhooks/virtualmachine/validation/virtualmachine_validator_test.go`

Story W-04 — `paused` label value extension (P0):

- [ ] T069 [vmop-NNN] Extend `PausedVMLabelKey` value space with `vendor` and `infra`;
      gate from operator SA only; standard reconciler behaviour unchanged —
      `webhooks/virtualmachine/validation/virtualmachine_validator.go`,
      `api/v1alpha6/virtualmachine_types.go`
- [ ] T070 [vmop-NNN] Unit tests: operator SA sets `vendor` → accepted; regular user sets
      `vendor` → rejected; standard reconciler ignores `vendor`/`infra` —
      `webhooks/virtualmachine/validation/virtualmachine_validator_test.go`

---

## Phase 8 — Observability and rollout (O-01, O-02, M-01, M-02)

Story O-01 — Prometheus metrics (P0):

- [ ] T071 [vmop-NNN] Register all `vmoperator_reverse_reconcile_*` metrics: `events_total`,
      `adoption_conflicts_total`, `invariant_violations_total`, `lost_vms_total`,
      `resync_duration_seconds`, `pending_events_queue_depth`, `aggregator_drops_total`,
      `echo_cache_hits_total` — `controllers/virtualmachine/reverseReconcile/metrics.go`
- [ ] T072 [vmop-NNN] Unit test: metric incremented after mocked operations —
      `controllers/virtualmachine/reverseReconcile/metrics_test.go`

Story O-02 — Audit ring buffer (P0):

- [ ] T073 [vmop-NNN] Unit tests for ring-buffer management: bounded to `auditRingSize`;
      vendor coalescing; crash recovery (no duplicates via idempotency token) —
      `controllers/virtualmachine/reverseReconcile/executor_test.go`

Story M-01 — First-enablement shadow window (P0):

- [ ] T074 [vmop-NNN] Implement shadow-window: record `firstEnableTime` in
      `vmoperator-reverse-reconcile-state` ConfigMap on first `false→true` flag transition;
      force OBSERVE-only for `firstEnableShadowDuration`; set `Degraded{Reason=ShadowWindow}`
      condition during window — `controllers/virtualmachine/reverseReconcile/shadowwindow.go`
- [ ] T075 [vmop-NNN] Unit + vcsim integration tests: first-enable → all OBSERVE for
      window; after expiry → normal decisions; re-enable → no re-apply —
      `controllers/virtualmachine/reverseReconcile/shadowwindow_test.go`

Story M-02 — Upgrade compatibility (P0):

- [ ] T076 [vmop-NNN] JSON round-trip test confirming no existing fields dropped; confirm
      no `+kubebuilder:storageversion` bump required —
      `api/v1alpha6/virtualmachine_types_test.go`

---

## Phase 9 — Tests (T-01, T-02)

Story T-01 — Unit test foundation (P0):

- [ ] T080 [vmop-NNN] Achieve ≥ 85% branch coverage on `classifier.go`, `decisions.go`,
      `conflict.go`, `executor.go`, `IsReverseReconcileWrite`; verify all 8 classifier
      heuristics, all §6.4 decision rows, all conflict branches tested —
      `controllers/virtualmachine/reverseReconcile/...`

Story T-02 — Integration test suite (P0):

- [ ] T081 [vmop-NNN] `AdminEventBatch` with pause + power-off in same set-version →
      OBSERVE (CC1-01) — `controllers/virtualmachine/reverseReconcile/controller_test.go`
- [ ] T082 [vmop-NNN] Leave-event disambiguation: unregister, destroy, ambiguous —
      `pkg/util/vsphere/watcher/watcher_test.go`
- [ ] T083 [vmop-NNN] ManagedBy cleared → REVERT → Critical event —
      `controllers/virtualmachine/reverseReconcile/controller_test.go`
- [ ] T084 [vmop-NNN] `admin-managed-devices` annotation with valid value → OBSERVE for
      admin-attached disk; with wildcard value → treated as absent —
      `controllers/virtualmachine/reverseReconcile/decisions_test.go`
- [ ] T085 [vmop-NNN] `backup-proxy=true` annotation → VADP hot-add disk → OBSERVE,
      no status mutation — `controllers/virtualmachine/reverseReconcile/decisions_test.go`

Story T-03 — E2E tests, P0 minimum [P0]:

- [ ] T090 [vmop-NNN] `power_state.go`: admin powers off via VC; assert spec adopts
      `PoweredOff` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/power_state.go`
- [ ] T091 [vmop-NNN] `pause_batch.go`: pause + power-off in same set.Version → OBSERVE —
      `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/pause_batch.go`
- [ ] T092 [vmop-NNN] `managedby_change.go`: admin clears `config.managedBy` → REVERT +
      Critical event — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/managedby_change.go`
- [ ] T093 [vmop-NNN] `lost_destroy.go`: admin `Destroy_Task` → `VirtualMachineLost`; CR
      preserved — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/lost_destroy.go`
- [ ] T094 [vmop-NNN] `webhook_bypass.go`: operator SA ADOPT → accepted; `system:masters`
      same → rejected — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/webhook_bypass.go`
- [ ] T095 [vmop-NNN] `shadow_window.go`: enable flag → all ops OBSERVE for window;
      normal after — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/shadow_window.go`

Story T-03 — E2E tests, P1 follow-up [P1]:

- [ ] T096 [P1] [vmop-NNN] `ha_failover.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T097 [P1] [vmop-NNN] `storage_drift.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T098 [P1] [vmop-NNN] `backup_proxy.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T099 [P1] [vmop-NNN] `import_window.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T100 [P1] [vmop-NNN] `concurrent_change.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T101 [P1] [vmop-NNN] `drs_host_change.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`
- [ ] T102 [P1] [vmop-NNN] `vadp_snapshot.go` — `test/e2e/vmservice/virtualmachine/admin_reverse_reconcile/`

Story T-04 — Soak/chaos [P1]:

- [ ] T110 [P1] [vmop-NNN] 10k events/min soak via vcsim; kill pod mid-ADOPT; 1k VMs +
      hourly DRS; echo cache churn check —
      `controllers/virtualmachine/reverseReconcile/soak_test.go`

---

## Phase Final — Polish

Story SST-01 — Snapshot status model extension [P1]:

- [ ] T120 [P1] [vmop-NNN] Extend `VirtualMachineSnapshotReference` with `MoRef`,
      `CreateTime`, `Description`; update status-population in standard reconciler —
      `api/v1alpha6/virtualmachine_types.go`,
      `controllers/virtualmachine/virtualmachine_controller.go`

Story SST-02 — Vendor allow-list ConfigMap bootstrap [P1]:

- [ ] T121 [P1] [vmop-NNN] Author default `vmoperator-reverse-reconcile-vendors` ConfigMap
      Helm template with example vendor principal-name patterns (Veeam, Commvault, Veritas,
      Cohesity, Zerto) —
      `config/manager/vmoperator-reverse-reconcile-vendors.yaml`
- [ ] T122 [P1] [vmop-NNN] Wire live-reload of vendor ConfigMap into source classifier;
      absent ConfigMap → Warning event; VENDOR → OBSERVE —
      `controllers/virtualmachine/reverseReconcile/classifier.go`

Final polish (P0):

- [ ] T123 [vmop-NNN] Update `docs/` release notes for `AdminReverseReconcile`; confirm
      all tasks checked; resolve any remaining `[NEEDS CLARIFICATION]`
- [ ] T124 [vmop-NNN] Flip `spec.md` status to `Implemented`; mark GA removal criteria
      for temporary flag tracked in follow-up spec
