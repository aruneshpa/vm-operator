// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package projection

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// Image kinds.
const (
	KindVirtualMachineImage        = "VirtualMachineImage"
	KindClusterVirtualMachineImage = "ClusterVirtualMachineImage"
)

// VirtualMachineClass projects a VirtualMachineClass. The class configSpec
// is never returned; only its presence is reported.
func VirtualMachineClass(c *vmopv1.VirtualMachineClass) contract.VirtualMachineClassSummary {
	s := contract.VirtualMachineClassSummary{
		Name:              c.Name,
		Namespace:         c.Namespace,
		Description:       c.Spec.Description,
		CPUs:              c.Spec.Hardware.Cpus,
		ReservedProfileID: c.Spec.ReservedProfileID,
		HasConfigSpec:     len(c.Spec.ConfigSpec) > 0,
		Age:               Age(c.CreationTimestamp),
	}
	if !c.Spec.Hardware.Memory.IsZero() {
		s.Memory = c.Spec.Hardware.Memory.String()
	}
	return s
}

// ImageSummary projects a VirtualMachineImage or ClusterVirtualMachineImage.
func ImageSummary(kind string, meta metav1.ObjectMeta, st *vmopv1.VirtualMachineImageStatus) contract.VirtualMachineImageSummary {
	s := contract.VirtualMachineImageSummary{
		Kind:        kind,
		Name:        meta.Name,
		Namespace:   meta.Namespace,
		DisplayName: contract.NewUntrustedPtr(st.Name),
		OSInfo: contract.OSInfo{
			ID:      st.OSInfo.ID,
			Type:    st.OSInfo.Type,
			Version: st.OSInfo.Version,
		},
		Firmware: st.Firmware,
		Type:     st.Type,
		Ready:    ConditionStatus(st.Conditions, vmopv1.ReadyConditionType),
		Age:      Age(meta.CreationTimestamp),
	}
	if st.HardwareVersion != nil {
		s.HardwareVersion = *st.HardwareVersion
	}
	return s
}

// ImageDetail projects an image into its full detail. OVF property default
// values are never returned; only their keys are reported.
func ImageDetail(kind string, meta metav1.ObjectMeta, st *vmopv1.VirtualMachineImageStatus) contract.VirtualMachineImageDetail {
	d := contract.VirtualMachineImageDetail{
		VirtualMachineImageSummary: ImageSummary(kind, meta, st),
		Conditions:                 Conditions(st.Conditions),
		ProviderItemID:             st.ProviderItemID,
		ProviderVersion:            st.ProviderContentVersion,
		ImageCapabilites:           st.Capabilities,
	}
	pi := st.ProductInfo
	product := strings.TrimSpace(strings.Join([]string{pi.Vendor, pi.Product, pi.FullVersion}, " "))
	if product == "" {
		product = strings.TrimSpace(pi.Version)
	}
	d.ProductInfo = contract.NewUntrustedPtr(product)
	for _, disk := range st.Disks {
		id := contract.ImageDisk{Name: disk.Name}
		if disk.Limit != nil {
			id.Limit = disk.Limit.String()
		}
		if disk.Requested != nil {
			id.Requested = disk.Requested.String()
		}
		d.Disks = append(d.Disks, id)
	}
	for _, p := range st.OVFProperties {
		d.OVFPropertyKeys = append(d.OVFPropertyKeys, p.Key)
	}
	return d
}

// SnapshotSummary projects a VirtualMachineSnapshot.
func SnapshotSummary(s *vmopv1.VirtualMachineSnapshot) contract.VirtualMachineSnapshotSummary {
	return contract.VirtualMachineSnapshotSummary{
		Name:        s.Name,
		Namespace:   s.Namespace,
		VMName:      s.Spec.VMName,
		Memory:      s.Spec.Memory,
		Quiesce:     s.Spec.Quiesce != nil,
		Description: contract.NewUntrustedPtr(s.Spec.Description),
		Ready:       ConditionStatus(s.Status.Conditions, vmopv1.VirtualMachineSnapshotReadyCondition),
		Age:         Age(s.CreationTimestamp),
	}
}

// SnapshotDetail projects a VirtualMachineSnapshot into its full detail.
func SnapshotDetail(s *vmopv1.VirtualMachineSnapshot) contract.VirtualMachineSnapshotDetail {
	return contract.VirtualMachineSnapshotDetail{
		VirtualMachineSnapshotSummary: SnapshotSummary(s),
		Conditions:                    Conditions(s.Status.Conditions),
	}
}
