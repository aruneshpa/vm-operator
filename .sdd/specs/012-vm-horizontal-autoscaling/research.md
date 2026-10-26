# Autoscaling research and decisions

**Date**: 2026-10-20. **Baseline**: d2e068ec0. This file records evidence and unresolved validation, not a test execution report.

## Agreed product direction

The user explicitly confirmed one HPA resource declaring metrics/policy and one controller computing and applying replica counts. Native/external control modes and a separate recommendation/request API were rejected. The metric source is an input to the controller; collection/cache are internal implementation details. VCF Automation creates/updates the same resource. Epic remains TBD by explicit instruction.

Architecture discussion: [VM Service Horizontal Autoscaling Design](../../../docs/design/vm-service-horizontal-autoscaling-design.md). This SDD refines that discussion into a reviewable implementation contract; unresolved Q1–Q4 are preserved in spec.md and task gates.

## Source precedence and findings

| Source | Finding | Design use |
| --- | --- | --- |
| WIKI page 2590012453 — Horizontal Autoscaling SDD | VCF Operations demand detection, Day-2 desired-count changes, cooldown/max/pending checks, IPAM/bootstrap/F5 lifecycle | Customer outcomes take precedence; move metric decisions into the agreed HPA controller |
| WIKI page 2733417273 — All Apps VM Autoscaler PoC | Custom ReplicaSet API, Windows/Linux blueprints, secret references, Orchestrator +1 example | Reuse existing VM Service ReplicaSet; preserve workload requirements, not PoC control API or installation workarounds |
| WIKI page 1007649487 — Workload Management One Pager | ReplicaSet/template management, autoscaling excluded from original scope | Separate capacity policy from existing lifecycle controllers |
| WIKI page 885824695 — Prometheus autoscaling PoC | CPU metric collection and proportional scaling | Historical context only; no inherited Prometheus dependency |

All four pages were retrieved through Confluence MCP during the design discussion. Their storage text and embedded diagram source were reviewed; rendered attachments/videos and linked source repositories were not evaluated. None of these documents settles production scale, supported memory semantics, or a complete safe scale-in lifecycle for every customer integration.

## Repository evidence

- api/v1alpha6/virtualmachinereplicaset_types.go and config/crd/bases/vmoperator.vmware.com_virtualmachinereplicasets.yaml expose /scale with desired/observed counts but no scale selector mapping.
- controllers/virtualmachinereplicaset/virtualmachinereplicaset_controller.go stamps templates, adopts matching orphans without capacity conformance, and treats missing readiness probes as implicitly ready. The new autoscaler must establish its own stronger eligibility.
- controllers/virtualmachinereplicaset/virtualmachinereplicaset_delete_policy.go does not provide least-utilized deletion or an application drain handshake.
- controllers/controllers.go gates ReplicaSet registration on K8sWorkloadMgmtAPI.
- webhooks/virtualmachinereplicaset/validation/virtualmachinereplicaset_validator.go currently registers main-resource validation, not a complete autoscaling ownership guard for Scale objects.
- pkg/crd/crd.go and its tests contain capability-driven CRD removal paths. Retain existing autoscaler objects/schema on disablement; this needs deliberate lifecycle work.
- pkg/manager/manager.go creates a webhook server and configurable manager callbacks. A separate autoscaler binary must register only its own components and use its own Lease.
- pkg/config/capabilities/capabilities.go handles capability changes; verify restart behavior for both deployments.
- config/wcp/vmoperator/kustomization.yaml is a manifest integration point, but the external Supervisor release packaging owner is not identified in this checkout.
- Root go.mod uses Go 1.26.9; api/go.mod deliberately stays at Go 1.23.0 with older dependencies. No dependency upgrade is implied.
- .sdd/specs/009-virtualmachinereplicaset/tds.md and test-plan.md include historical gaps that have since changed. Verify code and test intent instead of copying stale gap descriptions.

## Public technical references

Previously consulted primary references:

- [Kubernetes HPA](https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/) — ratio-based recommendations and multi-metric policy precedent.
- [CRD scale interface](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#scale-subresource) — replica paths and selector mapping.
- [Upstream HPA controller](https://github.com/kubernetes/kubernetes/blob/master/pkg/controller/podautoscaler/horizontal.go) — selector validation; qualify behavior against the Supervisor's pinned version, not master.
- [vSphere CPU counters](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/cpu_counters.html) — aggregate VM CPU usage and percent normalization.
- [vSphere memory counters](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/memory_counters.html) — active memory is an estimate, distinct from guest OS used memory.

These references explain the draft choices; the implementation must verify available counters, rollups, intervals, and privileges on the supported platform versions.

## Investigation records to complete

| Gate/task | Required evidence | Current state |
| --- | --- | --- |
| T001 | Real epic/stories and confirmed PR target | Epic TBD requested; no issues created |
| T002 / Q1 | Approved memory definition and counter/unit mapping | Unresolved |
| T003 / Q2 | Packaging owner, capability key, metric privilege set, supported skew | Unresolved |
| T004 / Q3 | First-release supported workload/deletion contract | Eligible core workloads proposed; integration contract unresolved |
| T005 / Q4 | Workload scale, latency budget, measured CPU/RSS and vCenter cost | No benchmark run |
| T010–T011 | Admission ownership and scale-route concurrency evidence | Not implemented/tested |
| T020–T022 | E2E, scale, restart and upgrade results | Not implemented/tested |

Record source versions, exact commands, measured values, failures, and skipped tests here as work proceeds. Never mark implementation complete based only on docs, fake clients, or a successful manifest render.

