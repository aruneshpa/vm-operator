# KubeVM autoscaling research and decision record

**Date**: 2026-10-22 · **Epic**: TBD

## Evidence inspected

- Public [KubeVM proposal PR #1893](https://github.com/vmware-tanzu/vm-operator/pull/1893): generic provider-agnostic VM API and higher-level orchestration direction. The PR overview was accessible; the full files/raw endpoint was not retrievable in this session. Do not treat the historical local copy below as an exact reproduction of the current PR revision.
- Author-specified sibling checkout `vm-operator_kubevm-bootstrap`, HEAD `0d586540f`: inspected `external/kubevm/README.md`, `ROADMAP.md`, API `virtualmachine_types.go`/`common_types.go`, generic VM controller, `internal/contract/contract.go`, `docs/implementing-a-provider.md`, and VM Operator `pkg/kubevm/link.go` and linkage controller.
- Historical one-pager recovered using `git show 71be22369^:external/kubevm/docs/one-pager-kubevm-generic-api.md`. It proposes generic templates/sets and later Deployment, and recognizes matching provider templates. The current file redirects readers to PR #1893. The implementation remains stronger evidence of present behavior than the historical design prose.
- Existing [VM Service design](../../../docs/design/vm-service-horizontal-autoscaling-design.md) and [spec 012](../012-vm-horizontal-autoscaling/spec.md). Field sources remain WIKI page 2590012453 and WIKI page 2733417273; earlier context WIKI page 1007649487 and WIKI page 885824695. These were previously reviewed for spec 012; this session did not refetch them.

## Findings versus proposals

| Inspected finding | Consequence / proposed extension |
|---|---|
| InfrastructureRef contains group/kind/name and is immutable; core resolves preferred served version. | Generate a distinct reference per replica; add UID validation and explicit capability/version negotiation. |
| Core waits for a preexisting provider object; it does not create provider objects. | Generic set must instantiate pairs through a new provider template contract. |
| Mutual link currently uses names; core adds owner metadata and coordinates deletion. | Preserve lifecycle ownership, strengthen UID/owner collision handling and prove deletion races. |
| Status contract has no utilization fields. | Optional versioned provider observation extension is new work. |
| Core Ready mirrors InfrastructureReady. | Application readiness needs independent evidence; reusing Ready would miscount service capacity. |
| Generic templates/sets/deployments are roadmap entries. | Generic set is a prerequisite for the proposed autoscaler; Deployment remains optional. |
| Container provider guide embeds core with provider in one manager. | Embedded generic controllers are viable; singleton registration across provider managers must be explicitly enforced. |
| Bootstrap README calls this first-party temporary source and anticipates a module move. | Resolve owning repository before coding; do not treat a path under external/ as permission to change vendored code in this checkout. |

## Open gates

Q1: API name/version, source ownership, template and UID contract. Q2: comparable memory semantics, timestamp rules and transport scale. Q3: application readiness and retirement. Q4: Supervisor packaging owner/version matrix, single generic host and measured resource envelope. Proposed choices are in design.md; no external approval or qualification is claimed.

## Validation

This change contains design documents only. Local relative links, fence balance, acceptance/task identifiers and whitespace are checked during drafting. No code was changed in the sibling checkout, no runtime tests or Supervisor experiments were run, and no capacity/resource numbers are certified. Implementation tasks stay unchecked.
