// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package projection

import (
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// VirtualMachineSummary projects a VirtualMachine into its compact summary.
func VirtualMachineSummary(vm *vmopv1.VirtualMachine) contract.VirtualMachineSummary {
	s := contract.VirtualMachineSummary{
		Name:      vm.Name,
		Namespace: vm.Namespace,
		PowerState: contract.PowerState{
			Desired:  string(vm.Spec.PowerState),
			Observed: string(vm.Status.PowerState),
		},
		ClassName: vm.Spec.ClassName,
		Zone:      vm.Status.Zone,
		Ready:     ConditionStatus(vm.Status.Conditions, vmopv1.ReadyConditionType),
		Created: ConditionStatus(vm.Status.Conditions, vmopv1.VirtualMachineConditionCreated) ==
			string(metav1.ConditionTrue),
		GroupName: vm.Spec.GroupName,
		OwnedBy:   ControllerOwner(vm),
		Age:       Age(vm.CreationTimestamp),
	}
	if vm.Spec.Image != nil {
		s.Image = contract.ImageRef{Kind: vm.Spec.Image.Kind, Name: vm.Spec.Image.Name}
	} else if vm.Spec.ImageName != "" {
		s.Image = contract.ImageRef{Name: vm.Spec.ImageName}
	}
	if n := vm.Status.Network; n != nil {
		s.PrimaryIP4 = contract.NewUntrustedPtr(n.PrimaryIP4)
		s.PrimaryIP6 = contract.NewUntrustedPtr(n.PrimaryIP6)
	}
	return s
}

// VirtualMachineDetail projects a VirtualMachine into its full detail.
func VirtualMachineDetail(vm *vmopv1.VirtualMachine) contract.VirtualMachineDetail {
	d := contract.VirtualMachineDetail{
		VirtualMachineSummary: VirtualMachineSummary(vm),
		Conditions:            Conditions(vm.Status.Conditions),
		StorageClass:          vm.Spec.StorageClass,
		Bootstrap:             Bootstrap(vm.Spec.Bootstrap),
		HardwareVersion:       vm.Status.HardwareVersion,
		BiosUUID:              vm.Status.BiosUUID,
		InstanceUUID:          vm.Status.InstanceUUID,
		UniqueID:              vm.Status.UniqueID,
		Labels:                vm.Labels,
	}
	if vm.Status.CurrentSnapshot != nil {
		d.CurrentSnapshot = vm.Status.CurrentSnapshot.Name
	}

	for _, v := range vm.Spec.Volumes {
		vs := contract.VolumeSummary{Name: v.Name, Type: "unknown"}
		if pvc := v.PersistentVolumeClaim; pvc != nil {
			vs.ClaimName = pvc.ClaimName
			vs.Type = "persistentVolumeClaim"
			if pvc.InstanceVolumeClaim != nil {
				vs.Type = "instanceStorage"
			}
		}
		d.Volumes = append(d.Volumes, vs)
	}

	addrs := map[string][]contract.Untrusted{}
	if n := vm.Status.Network; n != nil {
		d.HostName = contract.NewUntrustedPtr(n.HostName)
		for _, ifc := range n.Interfaces {
			if ifc.IP == nil {
				continue
			}
			for _, a := range ifc.IP.Addresses {
				addrs[ifc.Name] = append(addrs[ifc.Name], contract.NewUntrusted(a.Address))
			}
		}
	}
	if n := vm.Spec.Network; n != nil {
		for _, ifc := range n.Interfaces {
			is := contract.NetworkInterfaceSummary{Name: ifc.Name, Addresses: addrs[ifc.Name]}
			if ifc.Network != nil {
				is.NetworkName = ifc.Network.Name
			}
			d.Interfaces = append(d.Interfaces, is)
		}
	}

	var extra []vmopv1common.KeyValuePair
	if vm.Spec.Advanced != nil {
		extra = append(extra, vm.Spec.Advanced.ExtraConfig...)
	}
	extra = append(extra, vm.Status.ExtraConfig...)
	d.ExtraConfigKeys = keys(extra)
	return d
}

func keys(pairs []vmopv1common.KeyValuePair) []string {
	if len(pairs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if _, ok := seen[p.Key]; ok {
			continue
		}
		seen[p.Key] = struct{}{}
		out = append(out, p.Key)
	}
	sort.Strings(out)
	return out
}
