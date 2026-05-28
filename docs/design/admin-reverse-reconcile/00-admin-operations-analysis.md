# Admin Operations on VM Service VMs — Analysis

Status: DRAFT
Owner: vm-operator core
Companion docs: [`01-design.md`](01-design.md), [`02-story-breakdown.md`](02-story-breakdown.md)

---

## 1. Purpose

Identify the set of operations that admins legitimately perform on VMs through the traditional **VMODL1 / SOAP / VAPI / vCenter UI / ESXi** surfaces in a post-migration VCFA environment, and decide for each one how vm-operator should react. The output of this analysis feeds the design of the **reverse-reconcile framework** (see `01-design.md`).

**Invariant.** A consumer-centric operation must be exposed only through the consumer surface (Kubernetes API / VCFA UI plugin). An admin-centric operation may remain on the traditional vCenter surfaces. The traditional surfaces should be kept narrow on purpose.

---

## 2. Personas (recap)

| Persona | Surface | Examples |
|---|---|---|
| **DevOps / Consumer** | Kubernetes (`VirtualMachine` CR), VCFA UI plugin | Power on/off, attach PVC, snapshot via CR, change class, restart |
| **Infra Admin** | vCenter UI, VMODL1, SOAP, VAPI, ESXi shell | Host maintenance, quarantine, forensic capture, datastore decom, RBAC, fleet upgrade |

The admin can act **with or without the consumer's awareness**, including during incidents when the consumer control plane is degraded.

---

## 3. Methodology

1. **Phase 1a — Discovery.** A single read-only sub-agent enumerated candidate admin operations against the vSphere API surface and the existing vm-operator primitives. Output: 30 candidates (`ADM-01..ADM-30`).
2. **Phase 1b — Per-op validation.** 10 fresh-context sub-agents validated the candidates in thematic clusters. Each agent confirmed (a) whether the operation is genuinely performed in production, (b) whether it should remain on traditional surfaces, and (c) the degree of overlap with the consumer surface.
3. **Phase 1b — Gap discovery.** A second fresh-context sub-agent looked for operations the original 30 missed. Output: 32 additional candidates (`ADM-31..ADM-62`), with overlaps deduped in the consolidated table below.
4. **Phase 1c — Synthesis.** This document.

### 3.1 Verdict scale

Each operation receives one of four verdicts:

| Verdict | Meaning | Reverse-reconcile policy |
|---|---|---|
| `KEEP_ADMIN_ONLY` | Legitimate admin action with no equivalent on the consumer surface | **Observe**: surface in status / conditions / events; do NOT fight |
| `DUAL_SURFACE` | Legitimate on both surfaces; admin path is break-glass / vendor / forensic | **Observe + tolerate**: respect admin intent for a bounded window (pause / annotation), then resume reconciliation |
| `CONSUMER_ONLY` | Consumer surface fully covers the intent; admin out-of-band action is drift | **Revert**: adopt the new observed state if it aligns with the existing model; otherwise reconcile spec→observed (i.e. revert the admin change) |
| `OUT_OF_SCOPE` | Admin should not perform this on a VM Service VM; structurally unsafe | **Hard revert** + alert; consider vCenter-side RBAC / `DisableMethods` enforcement |

> Per the parent decision (see `01-design.md §3`), reverse-reconcile for the `CONSUMER_ONLY` and admin-tolerated `DUAL_SURFACE` cases works by **adopting the observed state into `vm.Spec`** when (and only when) it does not violate a stronger invariant (class, encryption class, storage class). See `01-design.md §5` for the algorithm.

---

## 4. Consolidated catalog

Operations from `ADM-01..62` deduplicated and grouped by domain. The "Watcher gap" column states what would be needed beyond today's `DefaultWatchedPropertyPaths()` in `pkg/util/vsphere/watcher/watcher.go`.

### 4.1 Power lifecycle

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-01 | Power Off (out-of-band, hard) | `DUAL_SURFACE` (break-glass) | HIGH | None — `summary.runtime.powerState` covered |
| ADM-02 | Reset (hard reset) | `DUAL_SURFACE` (break-glass) | MEDIUM | Optional `runtime.bootTime`, `summary.runtime.cleanPowerOff` to distinguish from HA/guest reboot |
| ADM-03 | Suspend (forensic / pre-evac) | `DUAL_SURFACE` (rare, forensic) | LOW | None — covered |
| ADM-53 | Power management policy (`config.powerOpInfo`) | `DUAL_SURFACE` | LOW | Add `config.powerOpInfo`, `config.flags.runFromBackingPower*` |
| ADM-33 | HA failover (FDM-driven restart) | `KEEP_ADMIN_ONLY` (info) | LOW–HIGH (situational) | Add `summary.runtime.bootTime`, `runtime.cleanPowerOff`; Event Manager for `VmDasResetEvent` chain |

### 4.2 Snapshot

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-04 | Snapshot create (incl. VADP backup vendors) | `DUAL_SURFACE` (vendors must keep VMODL) | HIGH | None — `snapshot`, `rootSnapshot` covered |
| ADM-05 | Snapshot remove (incl. VADP cleanup) | `DUAL_SURFACE` | HIGH | None — covered |
| ADM-06 | Revert to snapshot | `DUAL_SURFACE` (break-glass) | LOW | None — covered |
| ADM-07 | Consolidate disks | `KEEP_ADMIN_ONLY` | MEDIUM | Add `runtime.consolidationNeeded` |
| ADM-51 | Consolidation-needed signal | `KEEP_ADMIN_ONLY` (info condition) | MEDIUM | Add `summary.runtime.consolidationNeeded` |

### 4.3 Placement (compute, storage, inventory)

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-08 | vMotion / compute migrate (incl. DRS) | `KEEP_ADMIN_ONLY` | HIGH (DRS) | None — `summary.runtime.host` covered |
| ADM-09 / ADM-50 | Storage vMotion / Relocate (incl. SDRS) | `KEEP_ADMIN_ONLY` | HIGH | Add `config.files.vmPathName`, `config.vmStorageObjectId`, `config.vmProfile` |
| ADM-10 / ADM-44 | Folder reparent | `OUT_OF_SCOPE` for VM Service VMs (deterministic from namespace) | n/a (must block) | Add `parent`; **must fix Leave-event handling in `watcher.go`** |
| ADM-11 / ADM-45 | Resource Pool reassignment | `KEEP_ADMIN_ONLY` (with drift) | LOW | Add `resourcePool` |
| ADM-59 | Cross-vCenter migration (xVC) | `KEEP_ADMIN_ONLY` | LOW | Source-VC Leave + destination-VC Enter; `ManagerID` reconcile needed |
| ADM-61 | Host enters maintenance mode | `KEEP_ADMIN_ONLY` (info) | MEDIUM | Watch `HostSystem.runtime.inMaintenanceMode`; Event Manager for `EnteredMaintenanceModeEvent` |
| ADM-62 | Datastore unmount / decommission | `KEEP_ADMIN_ONLY` (info) | LOW | Add `config.files.vmPathName`; covered via `connectionState` |

### 4.4 Network (NIC)

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-12 | NIC disconnect (quarantine) | `KEEP_ADMIN_ONLY` | MEDIUM (incident) | None — `config.hardware.device` covered |
| ADM-13 | NIC backing change (quarantine VLAN/segment) | `DUAL_SURFACE` (quarantine = admin) | LOW | None — covered |
| ADM-14 | NIC add / remove (debug NIC vs tenant) | `DUAL_SURFACE` | LOW | None — covered |
| ADM-37 | vCenter tag attach/detach (NSX security tag, SRM, backup) | `KEEP_ADMIN_ONLY` | HIGH | **UNDETECTABLE via property collector**; needs VAPI tagging poller |

### 4.5 Storage / volumes / CD-ROM

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-15 | Disk add / remove (forensic VMDK) | `DUAL_SURFACE` (rare; classic-disk model exists) | LOW | None — covered |
| ADM-16 | CD-ROM attach (rescue ISO) | `DUAL_SURFACE` (rescue) | LOW | None — covered |
| ADM-46 | SPBM profile / VM home policy change | `KEEP_ADMIN_ONLY` (with `StorageClass` drift) | LOW | Add `config.vmProfile`, per-disk profile already in device hash |

### 4.6 Identity / metadata

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-17 | Rename (`config.name`) | `CONSUMER_ONLY` (operator already forces back) | n/a | None — `summary.config.name` covered |
| ADM-18 | vSphere notes / annotation (`config.annotation`) | `KEEP_ADMIN_ONLY` (already preserved by operator) | LOW | None — covered |
| ADM-19 / ADM-38 | Custom Fields / Custom Attributes (`customValue`) | `KEEP_ADMIN_ONLY` (rare; legacy) | LOW | Add `customValue` (low priority) |
| ADM-20 / ADM-37 | vSphere tags (CIS tagging via VAPI) | `DUAL_SURFACE` (admin: SPBM/backup/NSX; consumer: policy) | HIGH | **UNDETECTABLE via property collector** |

### 4.7 Hardware / class-governed reconfigure

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-21 | Direct CPU / Memory reconfigure | `CONSUMER_ONLY` (class system) | n/a (must revert) | Add `config.hardware.numCPU`, `config.hardware.memoryMB`, `config.cpuAllocation`, `config.memoryAllocation` |
| ADM-22 | vTPM add / remove | `OUT_OF_SCOPE` (class system) | n/a (must revert) | None — `config.hardware.device`, `config.keyId` covered |
| ADM-23 | Encryption rekey / decrypt | `CONSUMER_ONLY` (EncryptionClass) | n/a (must revert) | None — covered |
| ADM-29 | HW version upgrade (`UpgradeVM_Task`) | `DUAL_SURFACE` (fleet upgrade) | LOW | Add `config.version`, `summary.config.hwVersion` |
| ADM-30 | Boot options (firmware, secureBoot, order) | `CONSUMER_ONLY` (`spec.bootOptions`) | n/a (must revert) | Add `config.bootOptions` |
| ADM-47 | Latency sensitivity (`config.latencySensitivity`) | `DUAL_SURFACE` (lift to first-class consumer knob) | LOW | Add `config.latencySensitivity` |
| ADM-48 | NUMA / `cpuAffinity` / `cpuFeatureMask` | `DUAL_SURFACE` (partial via `Advanced.PNUMANodeAffinity`) | LOW | Add `config.cpuAffinity`, `config.numaInfo`, `config.cpuFeatureMask` |
| ADM-49 | vApp / OVF environment (`config.vAppConfig`) | `KEEP_ADMIN_ONLY` (with drift on bootstrap) | LOW | Add `config.vAppConfig` |
| ADM-52 | VMware Tools settings / auto-upgrade (`config.tools`) | `KEEP_ADMIN_ONLY` | MEDIUM | Add `config.tools` |
| ADM-54 | VMX flags edit (`config.flags`: vbsEnabled, vvtdEnabled, monitorType) | `KEEP_ADMIN_ONLY` | LOW | Add `config.flags` |

### 4.8 Security / authorization / console

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-24 | Disable VM Console / MKS (`RemoteDisplay.*`) | `KEEP_ADMIN_ONLY` (do not stomp) | LOW | None — `config.extraConfig` covered (currently stripped by ignored-EC list; must be unignored) |
| ADM-25 / ADM-39 | Disable Methods (`AuthorizationManager.DisableMethods`) | `KEEP_ADMIN_ONLY` (heavy use by backup vendors) | MEDIUM | Add `disabledMethod`; **must surface `MethodDisabled` faults clearly on CR status** |
| ADM-40 | RBAC change on the VM (`Permission*`, `Role*`) | `KEEP_ADMIN_ONLY` | LOW | **Event-only**: Event Manager subscriptions |
| ADM-55 | Acquire MKS / WebMKS ticket | `KEEP_ADMIN_ONLY` (audit only) | MEDIUM | **Event-only**: `VmAcquiredMksTicketEvent` |
| ADM-56 | Guest Operations Manager (in-guest exec via hypervisor channel) | `KEEP_ADMIN_ONLY` (audit only) | LOW | **Event-only** (partial: only guest power transitions are events) |
| ADM-58 | `ManagedBy` / extension claim change | `KEEP_ADMIN_ONLY` (**HIGH BLAST RADIUS**) | LOW | Add `config.managedBy` |

### 4.9 Availability / replication / scheduling

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-31 | Fault Tolerance (SMP-FT) | `KEEP_ADMIN_ONLY` | LOW | Add `summary.runtime.faultToleranceState`, `config.ftInfo`; event-only for failover |
| ADM-32 | Per-VM HA override (DAS protection / restart priority) | `KEEP_ADMIN_ONLY` | LOW | Add `summary.runtime.dasVmProtection`; cluster-side is event-only |
| ADM-34 | vSphere Replication (HBR) configure / failover | `KEEP_ADMIN_ONLY` | LOW | Add `config.repConfig`; HBR event subscription |
| ADM-35 | DRS rules (VM-VM / VM-Host affinity / anti-affinity) | `KEEP_ADMIN_ONLY` (drift vs `spec.affinity`) | MEDIUM | Cluster-side property + Event Manager (`DrsRuleViolationEvent`) |
| ADM-36 | Cluster VM Overrides (DRS automation, vCLS exemption) | `KEEP_ADMIN_ONLY` | LOW | Cluster-side property + Event Manager |
| ADM-41 | Scheduled tasks targeting the VM | `KEEP_ADMIN_ONLY` | LOW | **Event-only**: `ScheduledTask*Event` |
| ADM-57 | Triggered alarm with VM-side action | `KEEP_ADMIN_ONLY` | LOW | Add `triggeredAlarmState`; event-only for action |

### 4.10 Lifecycle removal / template

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-26 / ADM-43 | Unregister VM (keep VMX) | `KEEP_ADMIN_ONLY` (DR / xVC prep) | LOW | **Fix `watcher.go` `ObjectUpdateKindLeave` handling**; Event Manager for `VmRemovedEvent` disambiguation |
| ADM-27 | Destroy_Task (delete from disk) | `DUAL_SURFACE` (break-glass; consumer canonical) | LOW | Same Leave-event fix |
| ADM-28 / ADM-42 | Mark as Template / Mark as VM | `KEEP_ADMIN_ONLY` (informational; consider vCenter-side guard) | LOW | Add `config.template`, `summary.config.template` |

### 4.11 Below-the-floor

| ID | Name | Verdict | Freq | Watcher gap |
|---|---|---|---|---|
| ADM-60 | Direct ESXi (`vim-cmd`, `esxcli`, `vmkfstools`) | `KEEP_ADMIN_ONLY` (last-resort) | LOW | **UNDETECTABLE in real time**: periodic full-property reconcile backstop |

---

## 5. Cross-cutting findings

1. **The Watcher framework already covers the majority of high-frequency VM-MO property changes** (`summary.runtime.*`, `config.hardware.device`, `config.extraConfig`, `snapshot`, `rootSnapshot`, `config.annotation`, `config.keyId`). The bulk of the design work is in the **controller** that consumes the signal, not in the Watcher itself.

2. **Three structural detection gaps must be closed** for any meaningful coverage of admin operations:
   - **Property paths**: ~25 additional VM-MO paths (table in `§6`).
   - **`ObjectUpdateKindLeave` handling**: `watcher.go` filters out Leave updates today, which makes Unregister, folder reparent out of the Supervisor folder, cross-VC migration, and Destroy *all* indistinguishable and silent. This is the single highest-impact bug for the framework.
   - **Event Manager subscription**: ~6 classes of operations are event-only (RBAC, scheduled tasks, MKS ticket acquisition, guest operations, alarm action triggers, HA failover semantics).
   - **VAPI tagging poller**: vCenter tag associations live in the CIS tagging service and are invisible to the property collector. NSX security-tag and SRM-tag flows depend on this.

3. **Backup vendors (VADP) drive most of the `DUAL_SURFACE` verdicts.** Veeam, Rubrik, Cohesity, NetBackup all use VMODL/SOAP `CreateSnapshot_Task` / `RemoveSnapshot_Task` directly, on every backup. The framework MUST coexist with this; it cannot fight VADP snapshots. The existing `VirtualMachineSnapshotReferenceTypeUnmanaged` model is the right template.

4. **DRS, SDRS, HA, FDM, and vCLS are non-human admin actors.** They mutate `summary.runtime.host`, `config.files.vmPathName`, `summary.runtime.powerState` etc. continuously and on their own schedule. The framework must classify their changes as **infrastructure-driven** and never treat them as user intent — but should record them on status for observability.

5. **The Managed/Unmanaged duality from snapshots applies broadly.** It should be the consistent model for admin-introduced NICs (`ADM-14`), classic disks (`ADM-15`), rescue CD-ROMs (`ADM-16`), and vSphere tags (`ADM-20/37`). The operator observes-but-does-not-stomp.

6. **`PauseVMExtraConfigKey` (`vmservice.virtualmachine.pause`) is the existing emergency brake.** Many admin-only operations that overlap the consumer surface (power, suspend, CD-ROM, NICs, encryption, boot options) should be guarded by the pause check before reverse-reconcile fires; otherwise the admin's emergency action is undone on the next reconcile. The design must **formalize the pause check inside the reverse-reconcile loop** rather than relying on the existing pre-reconcile gate alone.

7. **Several "admin operations" turn out to be `CONSUMER_ONLY`.** Direct CPU/memory (`ADM-21`), vTPM add/remove (`ADM-22`), encryption rekey (`ADM-23`), boot options (`ADM-30`), rename (`ADM-17`) all have full coverage on the v1alpha6 consumer surface. The framework should revert admin out-of-band changes in these cases (or at minimum surface them as `*Synced=False` conditions and let the next reconcile drive correction).

8. **`config.managedBy` is the single highest-blast-radius property.** Clearing or re-assigning the WCP extension claim makes the VM either ignored by vm-operator or claimable by another extension. It must be watched and any unauthorized change must immediately page (Critical condition + Event + reverse-reconcile attempt to restore the claim).

---

## 6. Required detection-surface additions

### 6.1 New property paths for `DefaultWatchedPropertyPaths()`

Grouped by signal class. All paths listed below are estimated to be **low-noise** (admin actions are infrequent compared to VKS pod churn that motivated the existing `guest.ipStack` exclusion).

| Group | Paths |
|---|---|
| Hardware identity | `config.hardware.numCPU`, `config.hardware.memoryMB`, `config.cpuAllocation`, `config.memoryAllocation`, `config.version` |
| Lifecycle / management | `config.managedBy`, `config.template`, `summary.config.template`, `config.files.vmPathName` |
| Boot & firmware | `config.bootOptions`, `config.firmware` |
| Tools / flags | `config.tools`, `config.flags`, `config.powerOpInfo` |
| Performance / placement | `config.latencySensitivity`, `config.cpuAffinity`, `config.numaInfo`, `config.cpuFeatureMask` |
| Inventory location | `parent`, `resourcePool` |
| Availability / replication | `summary.runtime.faultToleranceState`, `summary.runtime.dasVmProtection`, `config.ftInfo`, `config.repConfig` |
| Maintenance signals | `summary.runtime.consolidationNeeded`, `summary.runtime.bootTime`, `runtime.cleanPowerOff` |
| Storage policy | `config.vmProfile`, `config.vmStorageObjectId` |
| OVF / vApp | `config.vAppConfig` |
| Authorization | `disabledMethod` |
| Alarms | `triggeredAlarmState`, `alarmActionsEnabled` |
| Metadata (low priority) | `customValue` |

### 6.2 Watcher structural fixes

| Item | Change |
|---|---|
| **F1** | Stop filtering `ObjectUpdateKindLeave` in `watcher.go` `onUpdate`. Emit a `Result{LeaveKind: <Unregister|Reparent|XvcMove|Destroy>}` that the consumer can disambiguate. |
| **F2** | Add Event Manager subscription (`HistoryCollector` or `CreateCollectorForEvents`) for the event-only operations enumerated in `§4`. |
| **F3** | Add per-cluster and per-host property filters for HA / DRS / maintenance-mode signals (the existing `view.Manager` infrastructure can be reused with a `Cluster`/`HostSystem` container view). |
| **F4** | Add a VAPI tagging poller (using `vapi/rest` client) keyed by VM MoRef → tag set, with diff-based change detection. |
| **F5** | Add a **periodic full-property reconcile backstop** (e.g., once every 30 minutes) per VM to defend against direct-ESXi changes that bypass vCenter (`ADM-60`). |

---

## 7. Operations explicitly out of scope (v1)

The following items are recognized but explicitly out of scope for the v1 framework. They are documented here to avoid scope creep:

- **In-guest changes via Guest Operations** (`ADM-56`). Event-only audit signal is sufficient; no reverse-reconcile.
- **MKS ticket acquisition** (`ADM-55`). Audit-only.
- **Permission / role edits** (`ADM-40`). Surfaced as a "VC-level access changed" event; vm-operator does not try to restore.
- **Direct ESXi shell ops** (`ADM-60`). Backstopped by periodic full-property reconcile; no real-time detection.
- **Cross-vCenter migration** (`ADM-59`). v1 surfaces this as `VMLost` with an actionable condition; **automatic re-discovery on the destination VC is not in v1**.

---

## 8. Inputs to the design document

Distilled from this analysis, the design must answer at least these questions (each addressed in `01-design.md`):

1. How does vm-operator distinguish an *infrastructure-driven* change (DRS, SDRS, HA, vCLS) from a *human-admin* change?
2. What is the exact "adopt into spec" algorithm — when does the controller mutate `spec.X` vs. condition `*Synced=False` vs. emit only an event?
3. How is the existing `PauseVMExtraConfigKey` / `PausedVMLabelKey` integrated as the admin's break-glass signal?
4. What is the conflict-resolution policy when admin and consumer race?
5. How is the v1 framework gated by feature flag and rolled out incrementally?
6. How is the `ObjectUpdateKindLeave` ambiguity resolved (Unregister vs Reparent vs xVC vs Destroy)?
7. How does the framework avoid pathological reconcile loops when DRS/SDRS continuously mutates watched fields?

---

## 9. Open questions for the team

1. **Folder reparent policy.** For VM Service VMs, `ADM-10/44` is `OUT_OF_SCOPE` — the namespace folder is deterministic. Do we auto-revert the move (call `MoveIntoFolder_Task` back) or surface a hard error and require admin intervention? Auto-revert risks an admin↔operator ping-pong; hard error risks the VM disappearing from the watcher's container view.
2. **`ManagedBy` change policy** (`ADM-58`). If an admin clears the WCP extension claim, do we restore it (and risk fighting a deliberate hand-off), or stop reconciling and require admin to re-claim explicitly?
3. **VADP filter precision.** How does vm-operator distinguish a VADP-driven snapshot from a human admin's pre-maintenance snapshot? Both arrive via VMODL `CreateSnapshot_Task`. Tag / creator field on the snapshot? ExtraConfig hint? Or treat all out-of-band snapshots identically as `Unmanaged`?
4. **Tags poller cost.** Polling CIS tagging is O(#VMs × poll-interval); at 25,000 VMs (the `CacheMaxKeys` ceiling) this is non-trivial. Acceptable, or do we require a vendor-supplied event source?
5. **Periodic full-property reconcile interval** (`ADM-60` backstop). 30 minutes? 1 hour? Trade-off between detection latency for ESXi-direct ops and load on the property collector.

These are picked up explicitly as design decisions in `01-design.md §§7–9`.
