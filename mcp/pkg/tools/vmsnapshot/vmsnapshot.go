// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vmsnapshot implements the VirtualMachineSnapshot read tools. They
// are registered only when the VM snapshot capability is not known to be
// inactive.
package vmsnapshot

import (
	"context"
	"fmt"
	"strings"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// ListInput is the input of list_virtual_machine_snapshots.
type ListInput struct {
	contract.ListInput
	VMName string `json:"vmName,omitempty" jsonschema:"Only snapshots of this VirtualMachine."`
}

// Register registers the snapshot read tools.
func Register(r *toolkit.Registrar) {
	env := r.Env
	state := env.Capability(toolkit.CapabilityVMSnapshots)
	if state == toolkit.CapabilityInactive {
		return
	}
	note := ""
	if state == toolkit.CapabilityUnknown {
		note = " The VM snapshot feature could not be confirmed on this Supervisor and may be disabled."
	}

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineSnapshotList]{
		Name:        "list_virtual_machine_snapshots",
		Title:       "List VM snapshots",
		Description: "Lists VirtualMachineSnapshots in a namespace, optionally for one VM." + note,
		Summary: func(l contract.VirtualMachineSnapshotList) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d snapshots", len(l.Items))
			for _, s := range l.Items {
				fmt.Fprintf(&b, "\n%s vm=%s memory=%t ready=%s age=%s", s.Name, s.VMName, s.Memory, s.Ready, s.Age)
			}
			toolkit.WritePage(&b, l.Page)
			return b.String()
		},
	}, func(ctx context.Context, in ListInput) (contract.VirtualMachineSnapshotList, error) {
		out := contract.VirtualMachineSnapshotList{Items: []contract.VirtualMachineSnapshotSummary{}}
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return out, err
		}
		cur, err := contract.DecodeCursor(in.Cursor)
		if err != nil {
			return out, err
		}
		opts, err := toolkit.ListOptions(ns, in.ListInput, cur.Token)
		if err != nil {
			return out, err
		}
		l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineSnapshotList, error) {
			l := &vmopv1.VirtualMachineSnapshotList{}
			return l, c.Client.List(ctx, l, opts...)
		})
		if err != nil {
			return out, err
		}
		for i := range l.Items {
			if in.VMName != "" && l.Items[i].Spec.VMName != in.VMName {
				continue
			}
			out.Items = append(out.Items, projection.SnapshotSummary(&l.Items[i]))
		}
		var truncated bool
		if out.Items, truncated = toolkit.Fit(out.Items); truncated {
			out.Truncated, out.Hint = true, toolkit.TruncationHint(len(out.Items))
		} else if l.Continue != "" {
			out.NextCursor = contract.EncodeCursor("", l.Continue)
		}
		return out, nil
	})

	toolkit.AddRead(r, toolkit.Spec[contract.VirtualMachineSnapshotDetail]{
		Name:        "get_virtual_machine_snapshot",
		Title:       "Get VM snapshot",
		Description: "Gets one VirtualMachineSnapshot with its conditions." + note,
		Summary: func(s contract.VirtualMachineSnapshotDetail) string {
			return fmt.Sprintf("%s vm=%s memory=%t quiesce=%t ready=%s", s.Name, s.VMName, s.Memory, s.Quiesce, s.Ready)
		},
	}, func(ctx context.Context, in contract.GetInput) (contract.VirtualMachineSnapshotDetail, error) {
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return contract.VirtualMachineSnapshotDetail{}, err
		}
		obj, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*vmopv1.VirtualMachineSnapshot, error) {
			obj := &vmopv1.VirtualMachineSnapshot{}
			return obj, c.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: in.Name}, obj)
		})
		if err != nil {
			return contract.VirtualMachineSnapshotDetail{}, err
		}
		return projection.SnapshotDetail(obj), nil
	})
}
