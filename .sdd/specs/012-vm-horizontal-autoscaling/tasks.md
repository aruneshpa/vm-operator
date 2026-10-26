# Tasks for VM horizontal autoscaling

**Status**: Draft implementation handoff. **Epic**: TBD (author explicitly requested no epic creation yet).

Read [spec.md](spec.md), [plan.md](plan.md), [model.md](model.md), and [research.md](research.md) before implementing. Repository rules are indexed by [../../INDEX.md](../../INDEX.md).

## Instructions for the implementation agent

- Implement one VirtualMachineAutoscaler resource and one autoscaling controller. Measurements are inputs. Do not add control modes, an external replica-recommendation API, or a second writer.
- This task file authorizes no work by itself; begin implementation only when the user asks. No implementation was performed while drafting it.
- Work in dependency order. Each task is unchecked until its acceptance criteria and verification pass. A blocked or skipped test is not completion.
- Preserve existing uncommitted work. Recheck paths and the active API version before editing; update this plan if the repository changed.
- Tasks marked **Q1/Q2/Q3/Q4 blocked** cannot complete their dependent behavior until that decision is recorded. Continue independent tasks. Do not silently invent memory semantics, platform identifiers, or a drain adapter.
- All shipping-code tasks below have **Ticket: TBD**. This is a draft placeholder authorized by the author's Epic TBD request, not a real issue tag. Replace it with an actual `[vmop-NNN]` story/sub-task linked to the epic before shipping/merge. T001 tracks this; do not fabricate IDs or create issues without the user's direction.
- A task can be developed locally as part of a slice; it is not automatically an independent merge boundary. Any PR introducing cluster-observable behavior MUST include the corresponding E2E portion of T020. Keep incomplete behavior gated off.
- Use the required copyright, godoc, import aliases, external test packages, labels, and one test file/suite per new package. Use an injected clock, not sleeps.
- Record test commands and outcomes in research.md. If real hardware/credentials are unavailable, leave affected E2E/qualification tasks open and report the limitation.
- Do not commit, push, create a PR, or enable a Supervisor capability as an implicit consequence of checking tasks.

## Dependency map

```text
T001                         ticket and merge readiness
T002(Q1) ------------------> T014 (memory) ----+
T003(Q2) ------------------> T017/T018/T022    |
T004(Q3) ------------------> T023 (integration scope)
T005(Q4) ------------------> T021 (scale qualification)
T006 -> T007 -> T008 -> T009 -> T010 -> T011
                  T008 -> T012 -> T013
                  T007 -> T015
T011 + T013 + T015 -> T016 -> T017 -> T018 -> T019
T020 accompanies every behavior slice; T014 adds memory cases
T019 + T020 -> T021/T022 -> T024
```

T006–T013 and T015 can establish CPU foundations without Q1. Q2 permits local packaging preparation but blocks production installation/qualification. Q3 affects customer integrations, not eligible stateless core workloads. Q4 blocks production enablement, not functional unit testing.

## Phase 1 — Resolve gates and establish the contract

- [ ] **[T001]** Record tracking and release target when supplied. **Files**: `spec.md`, `tasks.md`, `research.md`, `../../INDEX.md`. **Depends on**: none. **Done when**: actual epic and shipping-task stories are recorded, story epic links verified, and PR target confirmed. **Current state**: intentionally pending; author requested Epic TBD. No JIRA creation in this draft.

- [ ] **[T002] [US1]** Resolve Q1 memory semantics with product/field owners. **Files**: `spec.md`, `model.md`, `plan.md`, `research.md`. **Depends on**: none. **Done when**: numerator, denominator, metric source/counter, aggregation, guest prerequisites, and supported versions are recorded. **Gate**: blocks memory acceptance and T014; CPU work remains available.

- [ ] **[T003] [US5]** Resolve Q2 platform integration. **Files**: `plan.md`, `research.md`, `tasks.md`. **Depends on**: none. **Done when**: supported Supervisor/vCenter versions, capability key, packaging owner/repository, credential/privilege requirements, image publication path, and upgrade/rollback matrix are identified. Record external work items rather than inventing a repository path. **Gate**: production portions of T017/T018/T022.

- [ ] **[T004] [US1]** Resolve Q3 supported workloads and integration scope. **Files**: `spec.md`, `model.md`, `research.md`. **Depends on**: none. **Done when**: core stateless workload readiness/deletion contract is documented and each IPAM/F5 workload is supported by an existing contract or explicitly deferred. No new lifecycle API is implied. **Gate**: integration-specific T023 only.

- [ ] **[T005] [US5]** Define Q4 qualification targets. **Files**: `spec.md`, `plan.md`, `research.md`. **Depends on**: none. **Done when**: VM/policy/namespace counts, metric/provider budgets, acceptable decision latency, restart delay, and CPU/RSS budgets have numeric test targets and owner signoff. **Gate**: T021 qualification and production enablement.

## Phase 2 — API and foundations

- [ ] **[T006] [US1] [Ticket: TBD]** Add the new API type, registration, and generated schema. **Files**: `api/v1alpha6/virtualmachineautoscaler_types.go`, `api/v1alpha6/zz_generated.deepcopy.go`, `config/crd/bases/vmoperator.vmware.com_virtualmachineautoscalers.yaml`, `config/crd/kustomization.yaml`, `test/builder/dummies.go`, `test/builder/fake.go`. **Depends on**: reviewed model baseline. **Coverage**: AC01–05. **Done when**: namespace scope, immutable UID reference, behavior defaults, list-map metric keys, status, conditions, print columns, and markers are generated. Do not change existing API dependency versions or add old-version conversions. Until Q1, reject memory policies explicitly rather than assigning guessed semantics. **Verify**: generation diff, API module build, and admission/schema cases added with T009/T020.

- [ ] **[T007] [US5] [Ticket: TBD]** Add default-off feature/configuration and safe CRD lifecycle. **Files**: `pkg/config/config.go`, `pkg/config/default.go`, `pkg/config/config_test.go`, `pkg/config/capabilities/capabilities.go`, `pkg/config/capabilities/capabilities_test.go`, `pkg/crd/crd.go`, `pkg/crd/crd_test.go`. **Depends on**: T006; Q2 for final capability identifier. **Coverage**: AC24/28. **Done when**: feature depends on K8sWorkloadMgmtAPI, config budgets have bounded defaults, and disabling after enablement preserves CRD/data and cleanup access. **Verify**: feature-off fresh install, enabled install, disable-after-existing-objects, and no unsupported schema deletion.

- [ ] **[T008] [US2] [Ticket: TBD]** Define measurement/decision contracts and deterministic test fixtures. **Files**: `pkg/providers/metrics_provider_interface.go`, `pkg/autoscaling/types.go`, `pkg/autoscaling/autoscaling_test.go`, `pkg/autoscaling/autoscaling_suite_test.go`. **Depends on**: T006. **Coverage**: AC06/13/15. **Done when**: sample identity, interval, capacity epoch, freshness/coverage, decision inputs/results, and injected clock are typed; provider interface has no lifecycle side effects. **Verify**: fake samples distinguish unknown/zero, duplicate intervals, and reused names.

- [ ] **[T009] [US1] [Ticket: TBD]** Implement autoscaler admission and authorization. **Files**: `webhooks/virtualmachineautoscaler/webhooks.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator_test.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator_suite_test.go`, `webhooks/webhooks.go`, `config/webhook/manifests.yaml`, `config/rbac/role.yaml`. **Depends on**: T006–T008. **Coverage**: AC01/02/04/18/20/24. **Done when**: CEL/local rules and live UID, version, name, readiness, positive-count, feature, and SubjectAccessReview checks match model.md; suspended staging is supported. **Verify**: real envtest rejection/defaulting and caller authorization cases, including status-only updates and disabled cleanup access. Include T020 API E2E cases in the same behavior PR.

## Phase 3 — Ownership and metric pipeline

- [ ] **[T010] [US4] [Ticket: TBD]** Protect active target replica changes on main and scale endpoints. **Files**: `webhooks/virtualmachinereplicaset/scale/scale_validator.go`, `webhooks/virtualmachinereplicaset/scale/scale_validator_test.go`, `webhooks/virtualmachinereplicaset/scale/scale_validator_suite_test.go`, `webhooks/virtualmachinereplicaset/validation/virtualmachinereplicaset_validator.go`, `webhooks/virtualmachinereplicaset/validation/virtualmachinereplicaset_validator_test.go`, `webhooks/virtualmachinereplicaset/webhooks.go`, `config/webhook/manifests.yaml`. **Depends on**: T009. **Coverage**: AC19–23. **Done when**: request-resource identity resolves Scale objects correctly; all served target versions enforce active ownership, permit no-op/unrelated edits and suspended manual scale, and reject stale target UID/unauthorized writers. **Verify**: envtest requests to both endpoints, older target versions, lookup failures, and a real autoscaler service account.

- [ ] **[T011] [US4] [Ticket: TBD]** Prove concurrent activation/suspend/delete ownership semantics. **Files**: `webhooks/virtualmachinereplicaset/scale/scale_validator_test.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator_test.go`, `pkg/autoscaling/scale.go`, `pkg/autoscaling/autoscaling_test.go`, `research.md`. **Depends on**: T010. **Coverage**: AC19–23. **Done when**: duplicate creation, stale writes, deleted/recreated targets, manual writes racing activation, and suspension acknowledgment behave as specified. **Verify**: concurrent envtest schedules with deterministic barriers and resourceVersion conflicts. If direct checks cannot satisfy the contract, revise plan/model before implementing a different locking protocol; do not treat the test as optional.

- [ ] **[T012] [US2] [Ticket: TBD]** Implement batched vSphere CPU metrics provider. **Files**: `pkg/providers/vsphere/autoscaling/provider.go`, `pkg/providers/vsphere/autoscaling/counters.go`, `pkg/providers/vsphere/autoscaling/autoscaling_test.go`, `pkg/providers/vsphere/autoscaling/autoscaling_suite_test.go`. **Depends on**: T008. **Coverage**: AC13/15/27. **Done when**: authenticated client injection, counter discovery, interval selection, unit normalization, VM identity/capacity checks, cancellation, deadlines, operation IDs, and partial-result handling work. **Verify**: provider tests plus supported-platform counter/privilege evidence; VCSim is not accepted as evidence of real counter accuracy.

- [ ] **[T013] [US3] [Ticket: TBD]** Add bounded collection runnable and sample cache. **Files**: `services/vm-autoscaler-metrics/collector.go`, `services/vm-autoscaler-metrics/collector_test.go`, `services/vm-autoscaler-metrics/collector_suite_test.go`. **Depends on**: T007/T008/T012. **Coverage**: AC13–15/27. **Done when**: only targeted VMs are polled, batches/cache/queues/concurrency are bounded, namespaces receive fair service, feature/leadership loss cancels work, and complete common windows exclude stale or reused identities. **Verify**: injected-clock outage, churn, partial samples, overlapping intervals, backoff, and cancellation tests.

- [ ] **[T014] [US2] [Ticket: TBD]** Add approved memory metric and combined-metric behavior. **Files**: `pkg/providers/vsphere/autoscaling/counters.go`, `pkg/providers/vsphere/autoscaling/autoscaling_test.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator.go`, `webhooks/virtualmachineautoscaler/validation/virtualmachineautoscaler_validator_test.go`, `test/e2e/vmservice/vmservice/virtualmachineautoscaler/autoscaler.go`, `model.md`. **Depends on**: T002/T012/T015; **Q1 blocked**. **Coverage**: AC01/06/07/14. **Done when**: approved memory samples are accepted, accurate units/denominator are verified on supported hosts, and CPU/memory conflicts plus one-metric failure follow maximum-recommendation semantics. **Verify**: memory-only/combined E2E alongside behavior. If Q1 chooses guest telemetry, revise paths/provider work before proceeding.

## Phase 4 — Reconciliation and runtime

- [ ] **[T015] [US2] [US3] [Ticket: TBD]** Implement pure eligibility, calculation, history, and limits. **Files**: `pkg/autoscaling/eligibility.go`, `pkg/autoscaling/calculation.go`, `pkg/autoscaling/history.go`, `pkg/autoscaling/autoscaling_test.go`. **Depends on**: T007/T008. **Coverage**: AC06–17. **Done when**: converged-set eligibility, tolerance, ceiling/max selection, incomplete-metric rules, min/max precedence, scale-in disable, per-period limits, outage/history invalidation, policy edits, and conservative cold-start recovery match plan.md. **Verify**: table-driven/property-style cases including exact tolerance boundaries, integer overflow, unchanged source windows, 4→5 capped example, direction reversal, history gaps, and manual count changes after suspension.

- [ ] **[T016] [US2] [US4] [Ticket: TBD]** Add thin controller and target scale writes. **Files**: `controllers/virtualmachineautoscaler/virtualmachineautoscaler_controller.go`, `controllers/virtualmachineautoscaler/virtualmachineautoscaler_controller_test.go`, `controllers/virtualmachineautoscaler/virtualmachineautoscaler_controller_suite_test.go`, `pkg/context/virtualmachineautoscaler_context.go`, `pkg/autoscaling/scale.go`. **Depends on**: T011/T013/T015. **Coverage**: AC03/09/16–23/26. **Done when**: indexed watches and periodic evaluation call the policy engine; reconcile joins config, defers status patch with reterr, checks feature/leader/policy before Scale update, and recomputes conflicts. No VM mutations or lifecycle logic in controller. **Verify**: no-op write suppression, stale status, crash after scale-before-status, suspension draining, UID reuse, delete cleanup, and T020 CPU scenarios.

- [ ] **[T017] [US5] [Ticket: TBD]** Wire dedicated autoscaler process. **Files**: `cmd/vm-autoscaler/main.go`, `pkg/manager/autoscaler/setup.go`, `pkg/manager/autoscaler/setup_test.go`, `pkg/manager/autoscaler/setup_suite_test.go`, `Makefile`, `Dockerfile`. **Depends on**: T016 and Q2 for final platform wiring. **Coverage**: AC17/24/25/27. **Done when**: only autoscaler/collector run, unique Lease is used, provider credentials rotate, capability changes stop work/restart coherently, health probes do not restart on provider outages, and core manager remains sole admission/CRD owner. **Verify**: startup component inventory, feature-off test, SIGTERM/leader-loss cancellation, and successful image/binary build. Do not duplicate the full controller manager's broad privileges.

- [ ] **[T018] [US5] [Ticket: TBD]** Add Supervisor deployment and minimum RBAC. **Files**: `config/autoscaler/kustomization.yaml`, `config/autoscaler/manager.yaml`, `config/autoscaler/rbac.yaml`, `config/autoscaler/pdb.yaml`, `config/wcp/vmoperator/kustomization.yaml`, `research.md`. **Depends on**: T003/T017; **Q2 blocked for production integration**. **Coverage**: AC24/25/28. **Done when**: two replicas, own service account/Lease, requests/limits, PDB, anti-affinity/scheduling, probes, image, credential references, and rolling strategy render correctly. External installer delivery identified in T003 must also be completed before claiming Supervisor support. **Verify**: Kustomize render and least-privilege authorization; no VM create/delete grant, no unwanted webhook port/service, no impact to existing manager command.

- [ ] **[T019] [US5] [Ticket: TBD]** Add bounded observability and support output. **Files**: `pkg/autoscaling/observability.go`, `pkg/autoscaling/autoscaling_test.go`, `controllers/virtualmachineautoscaler/virtualmachineautoscaler_controller.go`, `services/vm-autoscaler-metrics/collector.go`, `docs/concepts/workloads/autoscaling.md`. **Depends on**: T016–T018. **Coverage**: AC26/27. **Done when**: stable condition reasons and timestamped diagnostics distinguish applied counts from ready capacity; events/status heartbeats are throttled; metrics have bounded labels; provider outage remains diagnosable. **Verify**: repeated identical failures/no-op reconciles do not flood API writes or events.

## Phase 5 — Behavioral and operational qualification

- [ ] **[T020] [US1] [US2] [US3] [US4] [US5] [Ticket: TBD]** Deliver real Supervisor E2E coverage with each behavior slice. **Files**: `test/e2e/vmservice/vmservice/virtualmachineautoscaler/autoscaler.go`, `test/e2e/vmservice/lib/vmoperator/virtualmachineautoscaler.go`, `test/e2e/vmservice/vmservice_test.go`, `test/e2e/vmservice/consts/consts.go`, `test/e2e/README.md`, `research.md`. **Depends on**: relevant implementation slice; full completion after T014/T019. **Coverage**: AC01–28. **Done when**: CPU, memory, both metrics, boot/readiness, quota, telemetry failure, authorization, suspend/manual/resume, target/policy deletion, capability disable, and restart scenarios pass on supported hardware. Use typed manifests and existing load/readiness helpers where available; do not add an unauthenticated production metric-injection endpoint for testing. Keep experimental labels until real validation, and preserve per-case evidence.

- [ ] **[T021] [US5] [Ticket: TBD]** Measure scale and finalize resource profile. **Files**: `test/e2e/vmservice/vmservice/virtualmachineautoscaler/scale.go`, `config/autoscaler/manager.yaml`, `docs/concepts/workloads/autoscaling.md`, `research.md`. **Depends on**: T005/T019/T020; **Q4 blocked** until numeric targets exist. **Coverage**: AC27/29. **Done when**: stable and burst/churn workloads meet agreed latency, vCenter/API budgets, queue bounds, and RSS/CPU limits; standby and rolling-surge cost are included. **Verify**: actual measurements, not the draft 10,000-VM arithmetic example. Keep configuration changes and qualification evidence together.

- [ ] **[T022] [US5] [Ticket: TBD]** Qualify upgrade, disablement, and rollback. **Files**: `test/e2e/vmservice/vmservice/virtualmachineautoscaler/lifecycle.go`, `pkg/crd/crd_test.go`, `docs/concepts/workloads/autoscaling.md`, `research.md`. **Depends on**: T003/T018/T020; **Q2 blocked** for platform matrix. **Coverage**: AC17/24/28. **Done when**: old/new manager+autoscaler combinations preserve schema/data, leadership transfer respects cooldown/history, credentials rotate, and disabling/unsupported rollback hands control back without deleting VMs or policies. **Verify**: real supported upgrade matrix; if external installer changes are unavailable, leave qualification open.

- [ ] **[T023] [US1] [US3] [Ticket: TBD]** Validate field application lifecycle integrations or record explicit deferral. **Files**: `test/e2e/vmservice/vmservice/virtualmachineautoscaler/integration.go`, `docs/concepts/workloads/autoscaling.md`, `spec.md`, `research.md`. **Depends on**: T004/T020; **Q3 blocked**. **Coverage**: AC03/05/09/16. **Done when**: agreed first-release integration has proven allocation/bootstrap/in-service and cleanup ordering, including partial failure and bounded replacement; or product explicitly defers that integration and documentation excludes it. Do not implement an unspecified F5/IPAM adapter or mark core readiness probes as evidence of traffic draining.

## Phase Final — Handoff and release readiness

- [ ] **[T024]** Complete API examples, migration/runbook, generated checks, and SDD reconciliation. **Files**: `docs/concepts/workloads/autoscaling.md`, `docs/concepts/workloads/README.md`, `docs/design/vm-service-horizontal-autoscaling-design.md`, `spec.md`, `plan.md`, `tasks.md`, `model.md`, `research.md`, `../../INDEX.md`. **Depends on**: T001 and all required release tasks, including resolved/deferred gates. **Done when**: examples cover CPU/memory/both, UID lookup, scale-in opt-in, suspend/resume/delete, failure diagnosis, and migration off old workflow writers; generation/lint/focused tests/rendering pass; E2E/qualification evidence is recorded; no unresolved release gate is presented as complete. Change SDD status only when its observable acceptance criteria actually pass.

## Validation commands and evidence

Use repository-documented commands and prerequisites, not a new ad hoc test workflow:

- `make generate-go`, `make generate-manifests`, and `make lint-go`; inspect generated API/CRD/webhook/RBAC changes.
- Focused `go test` for new root packages; run API-module validation from its own module without upgrading its Go/dependency floor.
- Envtest with configured assets for actual admission/scale paths; fake clients alone cannot prove them.
- Render the WCP Kustomize overlay with the repository's pinned tooling; verify independent manager/autoscaler commands and RBAC.
- Real E2E per `test/e2e/README.md`, including `make test-e2e` with the autoscaling focus/capability. New cases stay experimental until validated.
- Final checks include documentation links, internal-link masking, `git diff --check`, and a clear distinction between tests passed, skipped, unavailable, and still failing.

No production test was run during SDD authoring. All implementation tasks remain unchecked.

