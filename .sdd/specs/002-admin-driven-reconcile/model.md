# Model: Admin-Driven Reverse Reconcile

This document defines the complete API surface: new types, new conditions, new
annotations, new status fields, and webhook contract changes.

---

## 1. New status fields on `VirtualMachineStatus`

Two new optional fields added to `VirtualMachineStatus` in `api/v1alpha6/virtualmachine_types.go`:

```go
// AdminActivity is a ring buffer of the last AuditRingSize admin operations
// detected on this VM through reverse-reconcile.
// +optional
// +listType=atomic
// +kubebuilder:validation:MaxItems=10
AdminActivity []AdminActivityEntry `json:"adminActivity,omitempty"`

// VSphereObserved exposes vSphere-side metadata observed by the
// reverse-reconcile framework. Read-only.
// +optional
VSphereObserved *VirtualMachineVSphereObservedStatus `json:"vsphereObserved,omitempty"`
```

### 1.1 `AdminActivityEntry`

```go
// +kubebuilder:validation:Enum=INFRA;VENDOR;ADMIN;UNKNOWN
type AdminActivitySource string

// +kubebuilder:validation:Enum=OBSERVE;ADOPT;REVERT;LOST
type AdminActivityDecision string

type AdminActivityEntry struct {
    // Operation is the catalog ID (e.g. "ADM-01") or coalesced code
    // (e.g. "VENDOR_BACKUP_WINDOW").
    // +kubebuilder:validation:Required
    Operation     string               `json:"operation"`
    // Source classification.
    // +kubebuilder:validation:Required
    Source        AdminActivitySource  `json:"source"`
    // Decision taken by the framework.
    // +kubebuilder:validation:Required
    Decision      AdminActivityDecision `json:"decision"`
    // PrincipalName is the vCenter user/service account, when known.
    // +optional
    PrincipalName string               `json:"principalName,omitempty"`
    // DetectedAt is when the operator detected the operation.
    // +kubebuilder:validation:Required
    DetectedAt    metav1.Time          `json:"detectedAt"`
    // VCChangeTime is the timestamp from the property collector / Event Manager.
    // +kubebuilder:validation:Required
    VCChangeTime  metav1.Time          `json:"vcChangeTime"`
    // Path of the change (property path, event type ID, or symbolic name).
    // +kubebuilder:validation:Required
    Path          string               `json:"path"`
    // Message is a short human-readable summary.
    // +optional
    Message       string               `json:"message,omitempty"`
}
```

### 1.2 `VirtualMachineVSphereObservedStatus`

```go
type VirtualMachineVSphereObservedStatus struct {
    // Notes echoes config.annotation if set by an admin.
    // +optional
    Notes             string          `json:"notes,omitempty"`

    // CustomAttributes echoes vSphere CustomFields/customValue entries.
    // +optional
    // +listType=map
    // +listMapKey=key
    // +kubebuilder:validation:MaxItems=50
    CustomAttributes  []KeyValuePair  `json:"customAttributes,omitempty"`

    // ResourcePoolPath is the vSphere resource pool the VM is currently in.
    // +optional
    ResourcePoolPath  string          `json:"resourcePoolPath,omitempty"`

    // FolderPath is the vSphere folder the VM is currently in.
    // +optional
    FolderPath        string          `json:"folderPath,omitempty"`

    // DatastoreURL is the home datastore URL.
    // +optional
    DatastoreURL      string          `json:"datastoreUrl,omitempty"`

    // DisabledMethods is the set of method names disabled via
    // AuthorizationManager.DisableMethods on this VM.
    // +optional
    // +listType=set
    // +kubebuilder:validation:MaxItems=50
    DisabledMethods   []string        `json:"disabledMethods,omitempty"`
}
```

> **Note:** vSphere CIS tags are intentionally excluded. VM Operator maintains the
> invariant that vSphere tags are admin-only and are not surfaced to the DevOps/consumer
> persona.

### 1.3 `VirtualMachineFTStatus` and `VirtualMachineHAStatus`

These are additional optional fields (on `VirtualMachineStatus`, not inside
`VSphereObserved`) for fault-tolerance and HA tracking:

```go
// FaultTolerance reflects vSphere Fault Tolerance configuration.
// +optional
FaultTolerance *VirtualMachineFTStatus `json:"faultTolerance,omitempty"`

// HAProtection reflects vSphere HA protection state.
// +optional
HAProtection *VirtualMachineHAStatus `json:"haProtection,omitempty"`

type VirtualMachineFTStatus struct {
    // State: "notConfigured" | "starting" | "running" | "needSecondary".
    // +optional
    State         string `json:"state,omitempty"`
    // SecondaryHost is the host of the FT secondary, when configured.
    // +optional
    SecondaryHost string `json:"secondaryHost,omitempty"`
}

type VirtualMachineHAStatus struct {
    // ProtectionState: "protected" | "unprotected" | "disabled".
    // +optional
    ProtectionState  string       `json:"protectionState,omitempty"`
    // LastFailoverTime records the most recent HA-driven VM restart.
    // +optional
    LastFailoverTime *metav1.Time `json:"lastFailoverTime,omitempty"`
    // LastFailoverHost is the host the VM was restarted on by HA.
    // +optional
    LastFailoverHost string       `json:"lastFailoverHost,omitempty"`
}
```

---

## 2. New conditions

Add the following constants to the `VirtualMachineConditionType` block in
`api/v1alpha6/virtualmachine_types.go`:

| Constant | Reason examples | Semantics |
|---|---|---|
| `VirtualMachineReverseReconcileActive` | `Active` / `Disabled` | Framework is running and processing events |
| `VirtualMachineAdminPaused` | `PauseKeySet` / `LabelSet` | Reverse-reconcile is suppressed |
| `VirtualMachineAdminManagedDevicesInvalid` | `MalformedAnnotation` | `admin-managed-devices` or `admin-managed-nics` annotation grammar invalid |
| `VirtualMachineLost` | `DestroyedOutOfBand` / `UnregisteredOutOfBand` / `MigratedToOtherVC` / `FolderRevertPingPong` / `ManagedByLost` | VM is no longer reachable in vSphere |
| `VirtualMachineHAProtection` | `Protected` / `Unprotected` / `MaintenanceDrain` | HA protection state |
| `AffinityDrift` | `ForeignDrsRule` | DRS rule not authored by vm-operator conflicts with `spec.affinity` |
| `StorageClassDrift` | `DatastoreNotCompliant` | VM datastore does not match SPBM policy of `spec.storageClass` |
| `ReverseReconcileQuarantined` | `PingPongDetected` | Framework has stopped reverting for this VM due to repeated conflicts |
| `VirtualMachineReverseReconcileDegraded` | `EventSubRequired` / `StaleCache` / `VendorAllowListMissing` / `InsufficientVCenterPrivileges` / `ShadowWindow` | Framework is running but in degraded / reduced mode |

---

## 3. New annotations

All annotations are on `VirtualMachine` objects in `vmoperator.vmware.com/`.

| Annotation key (suffix) | Set by | Value grammar | Purpose |
|---|---|---|---|
| `last-adopt-source` | reverse-reconcile controller | literal `vcenter` | Marks spec patch as adoption-driven; consumed by validation webhook bypass |
| `last-adopt-event-uid` | reverse-reconcile controller | 64-char lowercase hex (sha256) | Idempotency token; prevents duplicate ADOPT on controller restart |
| `last-adopt-time` | reverse-reconcile controller | RFC3339 | Audit timestamp; TTL 5 min |
| `paused-by-source` | reverse-reconcile controller | `vendor` \| `admin` \| `infra` | Attributes source of automatic pause flip |
| `paused-by-principal` | reverse-reconcile controller | RFC-3986 unreserved chars, max 256 chars | vCenter principal that triggered the pause |
| `admin-managed-devices` | privileged user | `^[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*$`, max 1024 chars | Comma-separated `VirtualDevice.key` values excluded from reconcile; wildcards NOT supported |
| `admin-managed-nics` | privileged user | same regex as above | Same, by interface name |
| `backup-proxy` | privileged user | literal `"true"` | VM is a VADP hot-add transport proxy; disk attach/detach is OBSERVE-only with no status mutation |

Grammar validation for `admin-managed-devices`, `admin-managed-nics`, and `backup-proxy`
is enforced by the validation webhook (P1 story W-03). Invalid values fail closed:
annotation treated as absent, `VirtualMachineAdminManagedDevicesInvalid=True` condition
set.

---

## 4. Tuning ConfigMap (`vmoperator-reverse-reconcile-config`)

Namespace-scoped ConfigMap, optional (all keys have built-in defaults). Operator
reads on startup and live-reloads on change.

| Key | Type | Default | Effect |
|---|---|---|---|
| `subscribeEvents` | bool | `false` | Enables Event Manager subscriber. Required for power-state ADOPT. |
| `periodicResync` | bool | `true` | Enables periodic full-property resync backstop. |
| `resyncIntervalSeconds` | int | `1800` | Periodic resync interval (30 min). |
| `conflictSkewSeconds` | int | `30` | Last-writer-wins skew tolerance. |
| `adoptionRetryMax` | int | `3` | ADOPT retry count on optimistic-lock conflict. |
| `auditRingSize` | int | `10` | `status.adminActivity[]` max entries (FIFO). |
| `adminEventQueueSize` | int | `4096` | Aggregator channel buffer depth. |
| `infraPrincipalPatterns` | []string (JSON) | `["^vpxd-extension(-.*)?$","^wcp-.*$","^wcpsvc@.*$","^system@vsphere\\\\.local$"]` | Principal-name regexes classified as INFRA. |
| `adoptablePathWhitelist` | []string (JSON) | `["spec.powerState","spec.minHardwareVersion"]` | Spec paths ADOPT may modify; enforced by webhook bypass. |
| `eventReplayWindow` | duration string | `"30m"` | Event Manager lookback on startup / leader handoff. |
| `vendorCoalesceWindow` | duration string | `"30m"` | Coalescing window for VENDOR backup-window events. |
| `importWindowExpiry` | duration string | `"30m"` | OBSERVE-only window after import/restore/failover annotation. |
| `revertPingPongWindow` | duration string | `"5m"` | Window for ping-pong detection. |
| `revertPingPongMax` | int | `3` | Reverts within window before LOST escalation. |
| `revertPingPongCooldown` | duration string | `"1h"` | Cooldown before ping-pong counter resets. |
| `firstEnableShadowDuration` | duration string | `"1h"` | OBSERVE-only shadow window after first flag enablement. |

---

## 5. Vendor allow-list ConfigMap (`vmoperator-reverse-reconcile-vendors`)

Separate ConfigMap shipped as a Helm chart default. Optional; absent = safe OBSERVE-only
degraded mode.

| Key | Type | Default | Effect |
|---|---|---|---|
| `vendorPrincipals` | []string (JSON) | `[]` | Principal-name regexes classified as VENDOR. Empty = all VENDOR decisions degrade to OBSERVE + Warning. |

---

## 6. `PausedVMLabelKey` value space extension

The existing `PausedVMLabelKey` (`vmoperator.vmware.com/paused`) value space is extended:

| Value | Meaning | Who may set |
|---|---|---|
| `admin` | Human admin via vCenter | Privileged user |
| `devops` | DevOps consumer | Privileged user |
| `both` | Both | Privileged user |
| `vendor` | VADP / backup vendor (new) | Operator SA only |
| `infra` | DRS/HA/infra system (new) | Operator SA only |

The standard `VirtualMachineReconciler` checks only `admin`, `devops`, and `both` for
its pause logic. The new values `vendor` and `infra` are written by the reverse-reconcile
controller to attribute automatic pauses; they are ignored by the standard reconciler
(no behavioural change).

---

## 7. New property paths watched

When `AdminReverseReconcile=true`, the following paths are added to
`DefaultWatchedPropertyPaths()` in `pkg/util/vsphere/watcher/watcher.go`:

| Path | Why |
|---|---|
| `config.managedBy` | Detect ManagedBy theft; also used for §5.1.0 WCP filter |
| `config.template` | Detect Mark-as-Template |
| `config.flags.changeTrackingEnabled` | Detect CBT toggle (VADP context) |
| `runtime.dasVmProtection` | Per-VM HA protection state (replaces cluster-level watch) |
| `datastore` | Detect storage vMotion / SDRS |
| `config.version` | Detect VM hardware upgrade |
| `config.bootOptions` | Detect boot-order change |
| `config.flags.vbsEnabled` | Detect VBS toggle |
| `config.latencySensitivity.level` | Detect latency-sensitivity change |
| `disabledMethod` | Detect method disable (VADP / admin lock-out) |
| `customValue` | Detect custom attribute write |
| `config.changeVersion` | Dirty-check sentinel (fires on every ReconfigVM) |

Already watched today (no change): `summary.runtime.powerState`, `summary.runtime.host`,
`resourcePool`, `parent`, `config.extraConfig`, `config.hardware.device`,
`summary.config.name`, `rootSnapshot`, `snapshot`, `config.keyId`.

---

## 8. Example canonical YAML

### 8.1 VM after an admin power-off (ADOPT decision)

```yaml
apiVersion: vmoperator.vmware.com/v1alpha6
kind: VirtualMachine
metadata:
  name: my-vm
  namespace: my-ns
  annotations:
    vmoperator.vmware.com/last-adopt-source: vcenter
    vmoperator.vmware.com/last-adopt-event-uid: a3f1b2c4...  # 64-char sha256
    vmoperator.vmware.com/last-adopt-time: "2026-06-08T22:05:00Z"
spec:
  powerState: PoweredOff    # was PoweredOn; adopted from vCenter
status:
  adminActivity:
    - operation: ADM-01
      source: ADMIN
      decision: ADOPT
      principalName: administrator@vsphere.local
      detectedAt: "2026-06-08T22:05:01Z"
      vcChangeTime: "2026-06-08T22:05:00Z"
      path: summary.runtime.powerState
      message: "Admin powered off VM; adopted into spec.powerState"
```

### 8.2 VM after a DRS vMotion (OBSERVE decision)

```yaml
status:
  vsphereObserved:
    resourcePoolPath: /datacenter/host/cluster/Resources/ns-rp
  adminActivity:
    - operation: ADM-08
      source: INFRA
      decision: OBSERVE
      principalName: vpxd-extension-wcp
      detectedAt: "2026-06-08T22:10:00Z"
      vcChangeTime: "2026-06-08T22:09:58Z"
      path: summary.runtime.host
      message: "DRS vMotion detected; host updated in status; spec unchanged"
```

### 8.3 VM destroyed out-of-band (LOST decision)

```yaml
status:
  conditions:
    - type: VirtualMachineLost
      status: "True"
      reason: DestroyedOutOfBand
      message: "VM was destroyed in vSphere by administrator@vsphere.local at 2026-06-08T23:00:00Z"
      lastTransitionTime: "2026-06-08T23:00:01Z"
  adminActivity:
    - operation: ADM-27
      source: ADMIN
      decision: LOST
      principalName: administrator@vsphere.local
      detectedAt: "2026-06-08T23:00:01Z"
      vcChangeTime: "2026-06-08T23:00:00Z"
      path: LeaveEvent
      message: "VM destroyed out-of-band; CR preserved"
```
