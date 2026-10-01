// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package diagnose is a pure rule engine that turns a snapshot of a
// VirtualMachine and the objects it references into ranked findings. It
// performs no I/O.
package diagnose

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
)

// Lookup is the result of fetching a referenced object.
type Lookup int

// Lookup results.
const (
	// NotChecked means the reference was absent or not looked up.
	NotChecked Lookup = iota
	// Found means the object exists.
	Found
	// Missing means the object does not exist.
	Missing
	// Unavailable means the lookup failed, e.g. the user may not read it.
	Unavailable
)

// Image is the result of resolving a VM's image.
type Image struct {
	Lookup Lookup
	Kind   string
	Name   string
	Ready  string
}

// PVC is the result of fetching a referenced claim.
type PVC struct {
	Lookup Lookup
	Phase  corev1.PersistentVolumeClaimPhase
}

// Snapshot is everything the engine needs to diagnose a VM.
type Snapshot struct {
	VM *vmopv1.VirtualMachine

	// Class is the result of looking up spec.className.
	Class Lookup

	// Image is the result of resolving spec.image or spec.imageName.
	Image Image

	// PVCs is keyed by claim name.
	PVCs map[string]PVC

	// Events are the VM's recent events. EventsUnavailable is set when
	// they could not be read.
	Events            []corev1.Event
	EventsUnavailable bool
}

// maxEventFindings caps warning-event findings.
const maxEventFindings = 5

// Diagnose returns ranked findings for s. Findings are ordered blocking,
// warning, info, then by the order in which rules are evaluated.
func Diagnose(s Snapshot) []contract.Finding {
	vm := s.VM
	vmRef := contract.ObjectRef{Kind: "VirtualMachine", Namespace: vm.Namespace, Name: vm.Name}
	var out []contract.Finding

	// Referenced class.
	switch s.Class {
	case Missing:
		out = append(out, contract.Finding{
			Severity:   contract.SeverityBlocking,
			Object:     contract.ObjectRef{Kind: "VirtualMachineClass", Namespace: vm.Namespace, Name: vm.Spec.ClassName},
			Check:      "class.not_found",
			Reason:     "NotFound",
			Message:    "The referenced VM class does not exist in the VM's namespace.",
			Suggestion: suggestClass,
		})
	case Unavailable:
		out = append(out, unavailable("VirtualMachineClass", vm.Namespace, vm.Spec.ClassName, "class.unavailable"))
	}

	// Referenced image.
	switch s.Image.Lookup {
	case Missing:
		out = append(out, contract.Finding{
			Severity:   contract.SeverityBlocking,
			Object:     imageRef(vm, s.Image),
			Check:      "image.not_found",
			Reason:     "NotFound",
			Message:    "The referenced VM image does not exist.",
			Suggestion: suggestImage,
		})
	case Unavailable:
		ref := imageRef(vm, s.Image)
		out = append(out, unavailable(ref.Kind, ref.Namespace, ref.Name, "image.unavailable"))
	case Found:
		if s.Image.Ready == string(metav1.ConditionFalse) {
			out = append(out, contract.Finding{
				Severity:   contract.SeverityWarning,
				Object:     imageRef(vm, s.Image),
				Check:      "image.not_ready",
				Message:    "The referenced VM image exists but is not ready.",
				Suggestion: "Wait for the image to become ready, or choose another image.",
			})
		}
	}

	// Volume claims, in spec order.
	for _, v := range vm.Spec.Volumes {
		if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.InstanceVolumeClaim != nil {
			continue
		}
		name := v.PersistentVolumeClaim.ClaimName
		ref := contract.ObjectRef{Kind: "PersistentVolumeClaim", Namespace: vm.Namespace, Name: name}
		p := s.PVCs[name]
		switch p.Lookup {
		case Missing:
			out = append(out, contract.Finding{
				Severity:   contract.SeverityBlocking,
				Object:     ref,
				Check:      "volume.pvc_not_found",
				Reason:     "NotFound",
				Message:    fmt.Sprintf("Volume %q references a PersistentVolumeClaim that does not exist.", v.Name),
				Suggestion: "Create the PersistentVolumeClaim, or remove the volume from the VM.",
			})
		case Unavailable:
			out = append(out, unavailable(ref.Kind, ref.Namespace, ref.Name, "volume.pvc_unavailable"))
		case Found:
			if p.Phase != corev1.ClaimBound {
				out = append(out, contract.Finding{
					Severity:   contract.SeverityWarning,
					Object:     ref,
					Check:      "volume.pvc_not_bound",
					Reason:     string(p.Phase),
					Message:    fmt.Sprintf("Volume %q references a PersistentVolumeClaim that is not bound.", v.Name),
					Suggestion: suggestStorage,
				})
			}
		}
	}

	// Conditions.
	out = append(out, conditionFindings(vmRef, vm)...)

	// Desired versus observed power state.
	if vm.Spec.PowerState != "" && vm.Status.PowerState != "" && vm.Spec.PowerState != vm.Status.PowerState {
		out = append(out, contract.Finding{
			Severity: contract.SeverityInfo,
			Object:   vmRef,
			Check:    "power.transitioning",
			Message: fmt.Sprintf("Desired power state is %s but observed power state is %s.",
				vm.Spec.PowerState, vm.Status.PowerState),
			Suggestion: "If this persists, check the VM's conditions and events.",
		})
	}

	// Events.
	if s.EventsUnavailable {
		out = append(out, unavailable("Event", vm.Namespace, vm.Name, "events.unavailable"))
	} else {
		out = append(out, eventFindings(vmRef, s.Events)...)
	}

	if len(out) == 0 && len(vm.Status.Conditions) == 0 {
		out = append(out, contract.Finding{
			Severity:   contract.SeverityInfo,
			Object:     vmRef,
			Check:      "vm.no_status",
			Message:    "The VM has no status conditions yet; VM Operator may not have reconciled it.",
			Suggestion: "Wait and diagnose again, or check the VM's events.",
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return contract.SeverityRank(out[i].Severity) < contract.SeverityRank(out[j].Severity)
	})
	if out == nil {
		out = []contract.Finding{}
	}
	return out
}

// Healthy reports whether findings contain no blocking finding.
func Healthy(findings []contract.Finding) bool {
	for _, f := range findings {
		if f.Severity == contract.SeverityBlocking {
			return false
		}
	}
	return true
}

func conditionFindings(vmRef contract.ObjectRef, vm *vmopv1.VirtualMachine) []contract.Finding {
	var out []contract.Finding
	for _, c := range vm.Status.Conditions {
		if c.Status == metav1.ConditionTrue {
			continue
		}
		rule, known := ConditionRules[c.Type]
		if !known {
			rule = ConditionRule{Severity: contract.SeverityWarning, Suggestion: suggestEvents}
		}
		if rr, ok := ReasonRules[c.Reason]; ok && rr.Severity != "" {
			rule = ConditionRule(rr)
		}
		// The summary Ready condition restates other conditions; report it
		// only when nothing more specific explains it.
		if c.Type == vmopv1.ReadyConditionType {
			continue
		}
		// VMware Tools not running matters only for a powered-on VM.
		if c.Type == vmopv1.VirtualMachineToolsCondition && vm.Status.PowerState != vmopv1.VirtualMachinePowerStateOn {
			rule.Severity = contract.SeverityInfo
		}
		f := contract.Finding{
			Severity:   rule.Severity,
			Object:     vmRef,
			Check:      "condition." + c.Type,
			Reason:     c.Reason,
			Message:    fmt.Sprintf("Condition %s is %s.", c.Type, c.Status),
			Suggestion: rule.Suggestion,
		}
		if c.Message != "" {
			f.Evidence = []string{contract.NewUntrusted(c.Message).Value}
		}
		out = append(out, f)
	}
	return out
}

func eventFindings(vmRef contract.ObjectRef, events []corev1.Event) []contract.Finding {
	var out []contract.Finding
	seen := map[string]struct{}{}
	// Events arrive newest first.
	for _, e := range events {
		if e.Type != corev1.EventTypeWarning {
			continue
		}
		if _, ok := seen[e.Reason]; ok {
			continue
		}
		seen[e.Reason] = struct{}{}
		out = append(out, contract.Finding{
			Severity:   contract.SeverityWarning,
			Object:     vmRef,
			Check:      "event.warning",
			Reason:     e.Reason,
			Message:    "A recent warning event was recorded for the VM.",
			Evidence:   []string{contract.NewUntrusted(e.Message).Value},
			Suggestion: suggestEvents,
		})
		if len(out) >= maxEventFindings {
			break
		}
	}
	return out
}

func unavailable(kind, ns, name, check string) contract.Finding {
	return contract.Finding{
		Severity:   contract.SeverityInfo,
		Object:     contract.ObjectRef{Kind: kind, Namespace: ns, Name: name},
		Check:      check,
		Message:    "The referenced object could not be read, so this check was skipped.",
		Suggestion: "Use check_access to see whether you may read it.",
	}
}

func imageRef(vm *vmopv1.VirtualMachine, img Image) contract.ObjectRef {
	kind, name := img.Kind, img.Name
	if kind == "" {
		kind = projection.KindVirtualMachineImage
	}
	ref := contract.ObjectRef{Kind: kind, Name: name}
	if kind == projection.KindVirtualMachineImage {
		ref.Namespace = vm.Namespace
	}
	return ref
}
