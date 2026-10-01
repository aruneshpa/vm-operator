// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package diagnose implements the diagnose_virtual_machine tool. It gathers
// the VM and the objects it references, then delegates to the pure rule
// engine in mcp/pkg/diagnose.
package diagnose

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	engine "github.com/vmware-tanzu/vm-operator/mcp/pkg/diagnose"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/events"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vm"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmimage"
)

// Register registers diagnose_virtual_machine.
func Register(r *toolkit.Registrar) {
	env := r.Env
	toolkit.AddRead(r, toolkit.Spec[contract.Diagnosis]{
		Name:  "diagnose_virtual_machine",
		Title: "Diagnose VM",
		Description: "Explains why a VirtualMachine is not ready or not running. Checks the VM's conditions, its class, " +
			"image, volume claims, bootstrap readiness as reported by VM Operator (Secrets are never read), network " +
			"readiness, and recent warning events, and returns ranked findings with suggested next steps.",
		Summary: func(d contract.Diagnosis) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%s healthy=%t findings=%d", vm.SummaryLine(d.VM), d.Healthy, len(d.Findings))
			for _, f := range d.Findings {
				fmt.Fprintf(&b, "\n[%s] %s %s/%s: %s", f.Severity, f.Check, f.Object.Kind, f.Object.Name, f.Message)
				if f.Suggestion != "" {
					b.WriteString(" Next: " + f.Suggestion)
				}
			}
			return b.String()
		},
	}, func(ctx context.Context, in contract.DiagnoseInput) (contract.Diagnosis, error) {
		obj, err := vm.Get(ctx, env, in.Namespace, in.Name)
		if err != nil {
			return contract.Diagnosis{}, err
		}
		s := Gather(ctx, env, obj, !in.NoEvents)
		findings := engine.Diagnose(s)
		return contract.Diagnosis{
			VM:       projection.VirtualMachineSummary(obj),
			Healthy:  engine.Healthy(findings),
			Findings: findings,
		}, nil
	})
}

// Gather looks up the objects a VM references. Lookup failures are recorded
// in the snapshot rather than returned, so a partial diagnosis is always
// possible.
func Gather(ctx context.Context, env *toolkit.Env, obj *vmopv1.VirtualMachine, withEvents bool) engine.Snapshot {
	ns := obj.Namespace
	s := engine.Snapshot{VM: obj, PVCs: map[string]engine.PVC{}}

	if obj.Spec.ClassName != "" {
		_, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (struct{}, error) {
			return struct{}{}, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: obj.Spec.ClassName}, &vmopv1.VirtualMachineClass{})
		})
		s.Class = lookupOf(err)
	}

	s.Image = ResolveImage(ctx, env, obj)

	for _, v := range obj.Spec.Volumes {
		pvc := v.PersistentVolumeClaim
		if pvc == nil || pvc.InstanceVolumeClaim != nil || pvc.ClaimName == "" {
			continue
		}
		claim, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*corev1.PersistentVolumeClaim, error) {
			claim := &corev1.PersistentVolumeClaim{}
			return claim, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: pvc.ClaimName}, claim)
		})
		p := engine.PVC{Lookup: lookupOf(err)}
		if err == nil {
			p.Phase = claim.Status.Phase
		}
		s.PVCs[pvc.ClaimName] = p
	}

	if withEvents {
		el, err := listEvents(ctx, env, ns, obj.Name)
		if err != nil {
			s.EventsUnavailable = true
		} else {
			s.Events = el
		}
	}
	return s
}

// ResolveImage resolves a VM's image reference. spec.image, when set, names
// the kind exactly; otherwise spec.imageName is tried as a namespaced image
// and then as a cluster image.
func ResolveImage(ctx context.Context, env *toolkit.Env, obj *vmopv1.VirtualMachine) engine.Image {
	try := func(kind, name string) engine.Image {
		st, err := vmimage.Lookup(ctx, env, kind, obj.Namespace, name)
		img := engine.Image{Lookup: lookupOf(err), Kind: kind, Name: name}
		if err == nil {
			img.Ready = projection.ConditionStatus(st.Conditions, vmopv1.ReadyConditionType)
		}
		return img
	}
	if ref := obj.Spec.Image; ref != nil && ref.Name != "" {
		kind := ref.Kind
		if kind != projection.KindClusterVirtualMachineImage {
			kind = projection.KindVirtualMachineImage
		}
		return try(kind, ref.Name)
	}
	if obj.Spec.ImageName == "" {
		return engine.Image{}
	}
	img := try(projection.KindVirtualMachineImage, obj.Spec.ImageName)
	if img.Lookup == engine.Found {
		return img
	}
	if cimg := try(projection.KindClusterVirtualMachineImage, obj.Spec.ImageName); cimg.Lookup == engine.Found {
		return cimg
	}
	return img
}

// listEvents returns the raw events for the VM, newest first, from the same
// lookup get_events uses.
func listEvents(ctx context.Context, env *toolkit.Env, ns, name string) ([]corev1.Event, error) {
	return kube.Do(ctx, env.Provider, func(c *kube.Clients) ([]corev1.Event, error) {
		l := &corev1.EventList{}
		err := c.Client.List(ctx, l,
			ctrlclient.InNamespace(ns),
			ctrlclient.MatchingFields{events.FieldInvolvedObjectKind: "VirtualMachine", events.FieldInvolvedObjectName: name})
		if err != nil {
			return nil, err
		}
		items := l.Items
		sortNewestFirst(items)
		return items, nil
	})
}

func sortNewestFirst(items []corev1.Event) {
	t := func(e *corev1.Event) int64 {
		switch {
		case !e.LastTimestamp.IsZero():
			return e.LastTimestamp.UnixNano()
		case !e.EventTime.IsZero():
			return e.EventTime.UnixNano()
		default:
			return e.CreationTimestamp.UnixNano()
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return t(&items[i]) > t(&items[j]) })
}

func lookupOf(err error) engine.Lookup {
	switch {
	case err == nil:
		return engine.Found
	case apierrors.IsNotFound(err):
		return engine.Missing
	default:
		return engine.Unavailable
	}
}
