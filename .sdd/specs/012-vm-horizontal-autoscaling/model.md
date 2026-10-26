# VirtualMachineAutoscaler API model

**Status**: Draft. Implements [spec.md](spec.md); exact names below are the proposed implementation baseline, not an already served API.

## Resource and versioning

Introduce `VirtualMachineAutoscaler` and list in `vmoperator.vmware.com/v1alpha6`, namespaced, short name `vma`, with a status subresource. It is a new resource, not an extra field on an existing shipped VM/ReplicaSet type. Serve only this version initially. Do not generate older-version autoscaler schemas or conversions by mechanically copying other CRDs. If maintainers select a new active API version, update every path and fixture before implementation.

Reuse existing group registration markers, register both types, generate deepcopy, CRD schema and RBAC. The new type has no prior stored version to convert. Any future shipped-schema change follows the constitution's version/conversion rules. Existing ReplicaSet schemas and scale paths stay unchanged.

## Specification fields

All Go fields require explicit required/optional markers; optional fields use pointers where omitted differs from zero and appropriate JSON omission tags.

| Field | Required/default | Validation and semantics |
| --- | --- | --- |
| metadata.name | Required | Equal to target name; DNS-label form of at most 63 characters for this first API. Longer target names are unsupported and rejected explicitly |
| spec.scaleTargetRef.apiVersion | Required | An explicitly supported served VM Operator version; initial supported target is v1alpha6 |
| spec.scaleTargetRef.kind | Required | VirtualMachineReplicaSet only |
| spec.scaleTargetRef.name | Required | Same namespace as autoscaler; immutable |
| spec.scaleTargetRef.uid | Required | Existing target UID, validated at admission; immutable with the entire target reference |
| spec.minReplicas | Required | Integer 1..maxReplicas |
| spec.maxReplicas | Required | Positive int32; supported operational ceiling remains Q4 |
| spec.metrics | Required | List-map keyed by name, length 1..2; no duplicates |
| spec.metrics[].name | Required | cpu or memory; memory acceptance blocked by Q1 until its semantics are settled |
| spec.metrics[].targetPercentage | Required | Integer 1..100; measured values retain fractional precision |
| spec.suspend | false | Stops new scale writes; resume restarts observation safety windows |
| spec.behavior.tolerancePercentage | 10 | Integer 0..50; relative ratio tolerance, not percentage-point difference |
| spec.behavior.startupTimeoutSeconds | 600 | Integer 60..3600; reporting timeout only |
| spec.behavior.scaleUp.maxPercent | 25 | Integer 1..100; allow max(1, ceil(N × percent/100)) additional replicas per period |
| spec.behavior.scaleUp.periodSeconds | 60 | Integer 30..3600 |
| spec.behavior.scaleDown.enabled | false | Explicit opt-in to existing VM deletion semantics |
| spec.behavior.scaleDown.maxReplicas | 1 | Integer 1..maxReplicas, maximum removals per period |
| spec.behavior.scaleDown.periodSeconds | 300 | Integer 30..3600 |
| spec.behavior.scaleDown.stabilizationWindowSeconds | 300 | Integer 0..3600; zero explicitly disables recommendation-history stabilization but not data validity checks |

Default nested behavior objects fully; test omitted objects versus explicitly provided zero/false values. The scale-down default is deliberately conservative until the user opts into deletion of interchangeable VMs. Administrative max changes respect this switch and may remain above max until the user enables scale-in or suspends and scales manually.

The collection cadence (30 seconds), measurement window (60 seconds), maximum age (90 seconds), bounded concurrency, and cache sizes are operator configuration, not per-object arbitrary provider/query settings. They are provisional qualification inputs. Units and semantics must be consistent across every implementation of the internal metric interface.

The first API does not contain control-mode, metric-source selection, callback URL, desired-replica recommendation, or template fields. No additional public scaling-request or lifecycle-report resource is introduced. Workload integrations must use an agreed existing readiness contract; new integration APIs require a separate approved scope.

## Admission and ownership

CEL handles local structural rules, uniqueness, integer ranges, name/target agreement, and immutable target reference. Go admission verifies live target existence/UID, target eligibility, supported version, feature state, and caller authorization using SubjectAccessReview for target scale rights. Do not infer authorization from the controller's own permissions.

For active policies require positive target desired count and a configured readiness probe; allow suspended policy creation for staging, but perform full checks before activation. Reject parent-managed targets that have another replica-count authority.

Name equality plus namespace/type uniqueness prevents duplicate policies without list/check races. The UID binding prevents accidental reuse when the target name is recreated. This design intentionally restricts long target names; do not hash names without revising this contract.

Guard both ReplicaSet main-resource replica edits and `virtualmachinereplicasets/scale` for all served target versions, including requests from a parent/HPA controller. A scale admission request contains a Scale object, not a ReplicaSet: use request resource/name/namespace to retrieve target and policy authoritatively. Only the configured autoscaler service account can change active-target replicas. Permit unchanged replica fields during unrelated updates. Do not affect targets without a policy.

Suspension/deletion races and fail-closed lookups require integration tests. Already accepted writes may finish; acknowledgment of suspension must follow cancellation/draining of controller-owned work. Users must not be locked out of manual control after confirmed suspension or removal. Admission errors must identify the policy and the required suspend action.

## Status contract

Use observedGeneration, immutable bound target UID, currentReplicas (observed non-terminating owned VMs), desiredReplicas (last observed target desired count), eligibleReplicas, pendingReplicas, terminatingReplicas, and recommendations (raw and limited). Record timestamp/window/coverage and validity for each metric, lastEvaluationTime (throttled heartbeat), lastScaleTime, and lastScaleTargetReplicas.

The target remains authoritative if status persistence fails. Do not interpret status.desiredReplicas as another desired-state writer. Optional status fields use absence to mean unknown rather than fabricated zero. Bound diagnostic lists; do not store per-VM time series in status.

| Condition | True means | Example false/limited reasons |
| --- | --- | --- |
| Ready | Valid target/policy, usable configured measurements, and operational reconciliation | InvalidTarget, UnsupportedWorkload, MetricsUnavailable, Suspended, FeatureDisabled |
| ScalingActive | Controller currently allowed to evaluate/apply policy | Suspended, FeatureDisabled, TargetNotFound, TargetUIDMismatch |
| MetricsAvailable | All configured metrics have complete fresh coverage | MissingSamples, StaleSamples, UnsupportedCounter, ProviderUnavailable |
| ScalingLimited | A bound, cooldown, pending operation, or safety restriction limits calculated change | MinReplicas, MaxReplicas, RateLimited, Stabilizing, WaitingForReplicas, ScaleInDisabled |

Ready need not imply every desired VM is already in service; pending counts and reasons communicate convergence. ScalingActive may remain true when one metric permits scale-out and another is unavailable. Conditions are outputs, never stored workflow gates. Define constants and observedGeneration on each condition; update transition time only when condition status changes.

## Examples

These proposed examples are not apply-ready against current code. Resolve the real target UID from the existing ReplicaSet. The policy and target share a namespace and name.

### CPU only

```yaml
apiVersion: vmoperator.vmware.com/v1alpha6
kind: VirtualMachineAutoscaler
metadata:
  name: web-tier
  namespace: payments
spec:
  scaleTargetRef:
    apiVersion: vmoperator.vmware.com/v1alpha6
    kind: VirtualMachineReplicaSet
    name: web-tier
    uid: REPLACE_WITH_EXISTING_REPLICASET_UID
  minReplicas: 2
  maxReplicas: 20
  metrics:
    - name: cpu
      targetPercentage: 60
  behavior:
    scaleDown:
      enabled: true
```

The scale-down opt-in is suitable only when this workload tolerates existing ReplicaSet deletion behavior.

### Memory or combined metrics

After Q1 is resolved, replace the metrics list with memory alone or the following combined list:

```yaml
metrics:
  - name: cpu
    targetPercentage: 60
  - name: memory
    targetPercentage: 70
```

### Suspend and resume

Set `spec.suspend: true`, wait for observedGeneration to acknowledge the change and ScalingActive=False/Suspended, then manually scale the target if needed. Set false to resume after validating target eligibility. Delete only the autoscaler to stop automation permanently while preserving VMs.

### Invalid and unsupported cases

Reject duplicate metrics, a different resource name from its target, target UID mismatch on creation, an active target at zero, cross-namespace/kind targets, caller without scale rights, and target without a readiness probe. Missing metrics at runtime produce status and hold behavior, not an admission failure or zero utilization.

