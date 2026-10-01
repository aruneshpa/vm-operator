// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package projection converts VM Operator API objects into contract DTOs.
//
// Every projection copies fields explicitly. Nothing is copied by reflection
// or by default, so a field added to the API never leaves the server until it
// is deliberately projected here. The field inventory in inventory.go records
// the decision for every top-level field.
package projection

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// Now returns the current time. Tests may replace it.
var Now = time.Now

// Age returns the human-readable age of an object.
func Age(created metav1.Time) string {
	if created.IsZero() {
		return ""
	}
	return duration.HumanDuration(Now().Sub(created.Time))
}

// Conditions projects Kubernetes conditions.
func Conditions(in []metav1.Condition) []contract.Condition {
	if len(in) == 0 {
		return nil
	}
	out := make([]contract.Condition, 0, len(in))
	for _, c := range in {
		pc := contract.Condition{
			Type:    c.Type,
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		}
		if !c.LastTransitionTime.IsZero() {
			pc.LastTransitionTime = c.LastTransitionTime.UTC().Format(contract.RFC3339)
		}
		out = append(out, pc)
	}
	return out
}

// ConditionStatus returns the status of the condition with the given type, or
// "Unknown" if the condition is absent.
func ConditionStatus(conds []metav1.Condition, condType string) string {
	for _, c := range conds {
		if c.Type == condType {
			return string(c.Status)
		}
	}
	return string(metav1.ConditionUnknown)
}

// ControllerOwner returns the controller owner reference of obj, if any.
func ControllerOwner(obj metav1.Object) *contract.ObjectRef {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller {
			return &contract.ObjectRef{
				Kind:      ref.Kind,
				Namespace: obj.GetNamespace(),
				Name:      ref.Name,
			}
		}
	}
	return nil
}
