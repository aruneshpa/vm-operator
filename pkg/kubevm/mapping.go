// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kubevm

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kubevmv1a1 "github.com/vm-api/vm-api/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
)

// ApplyDelegatedFields copies the generic object's four delegated spec
// values onto the provider object, filling only fields that are currently
// empty so a value the user set directly on the provider object is never
// overwritten.
func ApplyDelegatedFields(vm *vmopv1.VirtualMachine, generic *kubevmv1a1.VirtualMachine) {
	spec := generic.Spec

	if vm.Spec.ClassName == "" {
		if it := spec.InstanceType; it != nil {
			vm.Spec.ClassName = it.Name
		}
	}

	if vm.Spec.Image == nil {
		if bd := spec.BootDisk; bd != nil && bd.Source.Image != nil {
			vm.Spec.Image = &vmopv1.VirtualMachineImageRef{
				Kind: bd.Source.Image.Kind,
				Name: bd.Source.Image.Name,
			}
		}
	}

	if vm.Spec.StorageClass == "" {
		if bd := spec.BootDisk; bd != nil {
			vm.Spec.StorageClass = bd.StorageClassName
		}
	}

	if vm.Spec.PowerState == "" {
		vm.Spec.PowerState = PowerStateFor(spec.PowerState)
	}
}

// ApplyDelegatedNetwork copies the generic object's network configuration
// onto the provider object at create time: spec.network.interfaces (name,
// network reference, dhcp4, dhcp6; see SyncInterfaces), spec.network.nameservers and
// spec.network.searchDomains. Each is filled only when currently empty, so a
// value the user set directly on the provider object is never overwritten.
//
// A generic network reference names its group and kind but no version, so the
// version is resolved with the given RESTMapper. Interface addresses and
// publicIP are not mapped: VM Operator addresses require a prefix length, which
// the generic API deliberately omits, and publicIP has no vSphere equivalent.
func ApplyDelegatedNetwork(
	vm *vmopv1.VirtualMachine,
	generic *kubevmv1a1.VirtualMachine,
	mapper meta.RESTMapper) error {

	n := generic.Spec.Network
	if n == nil {
		return nil
	}

	if vm.Spec.Network == nil {
		vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}
	}

	if len(vm.Spec.Network.Interfaces) == 0 {
		if err := SyncInterfaces(vm, generic, mapper); err != nil {
			return err
		}
	}

	if len(vm.Spec.Network.Nameservers) == 0 {
		vm.Spec.Network.Nameservers = append([]string(nil), n.Nameservers...)
	}
	if len(vm.Spec.Network.SearchDomains) == 0 {
		vm.Spec.Network.SearchDomains = append([]string(nil), n.SearchDomains...)
	}

	return nil
}

// SyncInterfaces makes the provider object's spec.network.interfaces match the
// generic object's, keyed on the interface name: an interface that exists on
// both has its network reference, dhcp4 and dhcp6 set from the generic object
// (leaving the provider-only fields such as macAddr and guestDeviceName alone),
// an interface only on the generic object is added, and an interface only on
// the provider object is removed. Providers keep their existing order; added
// interfaces are appended in the generic object's order.
//
// It does nothing when the generic object lists no interfaces, since then it
// has no opinion and the provider's default interface is left alone.
func SyncInterfaces(
	vm *vmopv1.VirtualMachine,
	generic *kubevmv1a1.VirtualMachine,
	mapper meta.RESTMapper) error {

	n := generic.Spec.Network
	if n == nil || len(n.Interfaces) == 0 {
		return nil
	}

	desired := make(map[string]vmopv1.VirtualMachineNetworkInterfaceSpec, len(n.Interfaces))
	for _, gi := range n.Interfaces {
		i := vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:  gi.Name,
			DHCP4: gi.DHCP4,
			DHCP6: gi.DHCP6,
		}
		if ref := gi.Network; ref != nil {
			mapping, err := mapper.RESTMapping(
				schema.GroupKind{Group: ref.APIGroup, Kind: ref.Kind})
			if err != nil {
				return fmt.Errorf(
					"failed to resolve network %s/%s %q of interface %q: %w",
					ref.APIGroup, ref.Kind, ref.Name, gi.Name, err)
			}
			i.Network = &vmopv1common.PartialObjectRef{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mapping.GroupVersionKind.GroupVersion().String(),
					Kind:       ref.Kind,
				},
				Name: ref.Name,
			}
		}
		desired[gi.Name] = i
	}

	if vm.Spec.Network == nil {
		vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}
	}

	var out []vmopv1.VirtualMachineNetworkInterfaceSpec
	seen := make(map[string]struct{}, len(desired))
	for _, cur := range vm.Spec.Network.Interfaces {
		want, ok := desired[cur.Name]
		if !ok {
			continue // Only on the provider object: removed.
		}
		seen[cur.Name] = struct{}{}
		if want.Network != nil {
			cur.Network = want.Network
		}
		cur.DHCP4 = want.DHCP4
		cur.DHCP6 = want.DHCP6
		out = append(out, cur)
	}
	for _, gi := range n.Interfaces {
		if _, ok := seen[gi.Name]; !ok {
			out = append(out, desired[gi.Name])
		}
	}
	vm.Spec.Network.Interfaces = out

	return nil
}

// PowerStateFor down-maps the generic object's desired power state onto
// VM Operator's own power state type. The enum values are spelled
// identically in both APIs, so this is a type conversion, not a translation.
func PowerStateFor(p kubevmv1a1.PowerState) vmopv1.VirtualMachinePowerState {
	return vmopv1.VirtualMachinePowerState(p)
}

// DelegatedFieldsUpToDate reports whether the generic object's instance
// type, boot disk image reference and storage class still match the values
// already persisted on the provider object. Once they diverge, the change
// cannot be applied — those provider fields are immutable after create —
// and the caller should report UpToDate=False with reason
// UnsupportedByProvider rather than treat it as an error to retry.
func DelegatedFieldsUpToDate(vm *vmopv1.VirtualMachine, generic *kubevmv1a1.VirtualMachine) bool {
	spec := generic.Spec

	if it := spec.InstanceType; it != nil && it.Name != vm.Spec.ClassName {
		return false
	}

	if bd := spec.BootDisk; bd != nil {
		if img := bd.Source.Image; img != nil {
			if vm.Spec.Image == nil ||
				vm.Spec.Image.Kind != img.Kind ||
				vm.Spec.Image.Name != img.Name {
				return false
			}
		}
		if bd.StorageClassName != "" && bd.StorageClassName != vm.Spec.StorageClass {
			return false
		}
	}

	return true
}
