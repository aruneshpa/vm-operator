// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contract

// PowerState is the desired and observed power state of a VM.
type PowerState struct {
	Desired  string `json:"desired,omitempty"`
	Observed string `json:"observed,omitempty"`
}

// ImageRef identifies the image a VM was deployed from.
type ImageRef struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

// VirtualMachineSummary is the compact projection of a VirtualMachine.
type VirtualMachineSummary struct {
	Name       string     `json:"name"`
	Namespace  string     `json:"namespace"`
	PowerState PowerState `json:"powerState"`
	ClassName  string     `json:"className,omitempty"`
	Image      ImageRef   `json:"image"`
	PrimaryIP4 *Untrusted `json:"primaryIP4,omitempty"`
	PrimaryIP6 *Untrusted `json:"primaryIP6,omitempty"`
	Zone       string     `json:"zone,omitempty"`
	Ready      string     `json:"ready" jsonschema:"Status of the Ready condition: True, False, or Unknown."`
	Created    bool       `json:"created"`
	GroupName  string     `json:"groupName,omitempty"`
	OwnedBy    *ObjectRef `json:"ownedBy,omitempty"`
	Age        string     `json:"age,omitempty"`
}

// SecretRef is a reference to a key in a Secret. The server never reads the
// referenced Secret.
type SecretRef struct {
	Name      string `json:"name"`
	Key       string `json:"key,omitempty"`
	FieldPath string `json:"fieldPath"`
}

// BootstrapSummary describes a VM's bootstrap configuration without
// revealing any inline value.
type BootstrapSummary struct {
	Providers           []string    `json:"providers"`
	Disabled            bool        `json:"disabled,omitempty"`
	SecretRefs          []SecretRef `json:"secretRefs,omitempty"`
	InlineFieldsPresent []string    `json:"inlineFieldsPresent,omitempty" jsonschema:"Field paths that hold inline values. The values are never returned."`
}

// VolumeSummary describes a VM volume.
type VolumeSummary struct {
	Name      string `json:"name"`
	ClaimName string `json:"claimName,omitempty"`
	Type      string `json:"type"`
}

// NetworkInterfaceSummary describes a VM network interface.
type NetworkInterfaceSummary struct {
	Name        string      `json:"name"`
	NetworkName string      `json:"networkName,omitempty"`
	Addresses   []Untrusted `json:"addresses,omitempty"`
}

// VirtualMachineDetail is the full projection of a VirtualMachine.
type VirtualMachineDetail struct {
	VirtualMachineSummary
	Conditions      []Condition               `json:"conditions,omitempty"`
	StorageClass    string                    `json:"storageClass,omitempty"`
	Volumes         []VolumeSummary           `json:"volumes,omitempty"`
	Interfaces      []NetworkInterfaceSummary `json:"interfaces,omitempty"`
	HostName        *Untrusted                `json:"hostName,omitempty"`
	Bootstrap       *BootstrapSummary         `json:"bootstrap,omitempty"`
	ExtraConfigKeys []string                  `json:"extraConfigKeys,omitempty" jsonschema:"Keys of spec and status extraConfig. Values are never returned."`
	HardwareVersion int32                     `json:"hardwareVersion,omitempty"`
	BiosUUID        string                    `json:"biosUUID,omitempty"`
	InstanceUUID    string                    `json:"instanceUUID,omitempty"`
	UniqueID        string                    `json:"uniqueID,omitempty"`
	CurrentSnapshot string                    `json:"currentSnapshot,omitempty"`
	Labels          map[string]string         `json:"labels,omitempty"`
}

// VirtualMachineList is the output of list_virtual_machines.
type VirtualMachineList struct {
	Items []VirtualMachineSummary `json:"items"`
	Page
}

// VirtualMachineClassSummary is the projection of a VirtualMachineClass.
type VirtualMachineClassSummary struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	Description       string `json:"description,omitempty"`
	CPUs              int64  `json:"cpus,omitempty"`
	Memory            string `json:"memory,omitempty"`
	ReservedProfileID string `json:"reservedProfileID,omitempty"`
	HasConfigSpec     bool   `json:"hasConfigSpec"`
	Age               string `json:"age,omitempty"`
}

// VirtualMachineClassList is the output of list_virtual_machine_classes.
type VirtualMachineClassList struct {
	Items []VirtualMachineClassSummary `json:"items"`
	Page
}

// OSInfo describes an image's guest OS.
type OSInfo struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type,omitempty"`
	Version string `json:"version,omitempty"`
}

// VirtualMachineImageSummary is the projection of a VirtualMachineImage or
// ClusterVirtualMachineImage.
type VirtualMachineImageSummary struct {
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Namespace       string     `json:"namespace,omitempty"`
	DisplayName     *Untrusted `json:"displayName,omitempty"`
	OSInfo          OSInfo     `json:"osInfo"`
	Firmware        string     `json:"firmware,omitempty"`
	HardwareVersion int32      `json:"hardwareVersion,omitempty"`
	Type            string     `json:"type,omitempty"`
	Ready           string     `json:"ready"`
	Age             string     `json:"age,omitempty"`
}

// ImageDisk describes a disk of an image.
type ImageDisk struct {
	Name      string `json:"name"`
	Limit     string `json:"limit,omitempty"`
	Requested string `json:"requested,omitempty"`
}

// VirtualMachineImageDetail is the full projection of an image.
type VirtualMachineImageDetail struct {
	VirtualMachineImageSummary
	ProductInfo      *Untrusted  `json:"productInfo,omitempty"`
	Disks            []ImageDisk `json:"disks,omitempty"`
	OVFPropertyKeys  []string    `json:"ovfPropertyKeys,omitempty" jsonschema:"OVF property keys. Default values are never returned."`
	Conditions       []Condition `json:"conditions,omitempty"`
	ProviderItemID   string      `json:"providerItemID,omitempty"`
	ProviderVersion  string      `json:"providerContentVersion,omitempty"`
	ImageCapabilites []string    `json:"capabilities,omitempty"`
}

// VirtualMachineImageList is the output of list_virtual_machine_images.
type VirtualMachineImageList struct {
	Items []VirtualMachineImageSummary `json:"items"`
	Page
}

// VirtualMachineSnapshotSummary is the projection of a VirtualMachineSnapshot.
type VirtualMachineSnapshotSummary struct {
	Name        string     `json:"name"`
	Namespace   string     `json:"namespace"`
	VMName      string     `json:"vmName,omitempty"`
	Memory      bool       `json:"memory"`
	Quiesce     bool       `json:"quiesce"`
	Description *Untrusted `json:"description,omitempty"`
	Ready       string     `json:"ready"`
	Age         string     `json:"age,omitempty"`
}

// VirtualMachineSnapshotDetail is the full projection of a snapshot.
type VirtualMachineSnapshotDetail struct {
	VirtualMachineSnapshotSummary
	Conditions []Condition `json:"conditions,omitempty"`
}

// VirtualMachineSnapshotList is the output of list_virtual_machine_snapshots.
type VirtualMachineSnapshotList struct {
	Items []VirtualMachineSnapshotSummary `json:"items"`
	Page
}

// Event is a projected Kubernetes event.
type Event struct {
	Type          string    `json:"type"`
	Reason        string    `json:"reason,omitempty"`
	Message       Untrusted `json:"message"`
	Count         int32     `json:"count,omitempty"`
	LastTimestamp string    `json:"lastTimestamp,omitempty"`
	Source        string    `json:"source,omitempty"`
}

// EventsInput is the input of get_events.
type EventsInput struct {
	Kind         string `json:"kind" jsonschema:"Kind of the involved object, e.g. VirtualMachine."`
	Namespace    string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name         string `json:"name" jsonschema:"Name of the involved object."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum events. Default 20, maximum 100."`
	SinceMinutes int    `json:"sinceMinutes,omitempty" jsonschema:"Only events newer than this many minutes. Default 60."`
}

// EventList is the output of get_events.
type EventList struct {
	Events []Event `json:"events"`
}

// WhoAmI is the output of whoami.
type WhoAmI struct {
	User                    string            `json:"user"`
	Groups                  []string          `json:"groups,omitempty"`
	ExtraKeys               []string          `json:"extraKeys,omitempty"`
	IdentitySource          string            `json:"identitySource"`
	Context                 string            `json:"context,omitempty"`
	Namespace               string            `json:"namespace,omitempty"`
	Server                  string            `json:"server"`
	ServedVMServiceVersions []string          `json:"servedVMServiceVersions"`
	BuiltForVersion         string            `json:"builtForVersion"`
	ContractVersion         string            `json:"contractVersion"`
	ServerVersion           string            `json:"serverVersion"`
	Tiers                   []string          `json:"tiers"`
	Capabilities            map[string]string `json:"capabilities"`
	NamespaceAllowList      []string          `json:"namespaceAllowList,omitempty"`
}

// CheckAccessInput is the input of check_access.
type CheckAccessInput struct {
	Verb        string `json:"verb" jsonschema:"Kubernetes verb, e.g. get, list, create, patch, delete."`
	Resource    string `json:"resource" jsonschema:"Plural resource name, e.g. virtualmachines."`
	Group       string `json:"group,omitempty" jsonschema:"API group. Defaults to vmoperator.vmware.com. Use an empty string with resource events for core resources."`
	Namespace   string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name        string `json:"name,omitempty"`
	Subresource string `json:"subresource,omitempty"`
	CoreGroup   bool   `json:"coreGroup,omitempty" jsonschema:"Set to true to check a resource in the core API group."`
}

// CheckAccess is the output of check_access.
type CheckAccess struct {
	Allowed bool   `json:"allowed"`
	Denied  bool   `json:"denied,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// DiagnoseInput is the input of diagnose_virtual_machine.
type DiagnoseInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name      string `json:"name" jsonschema:"VirtualMachine name."`
	NoEvents  bool   `json:"noEvents,omitempty" jsonschema:"Skip the event lookup."`
}

// Diagnosis is the output of diagnose_virtual_machine.
type Diagnosis struct {
	VM       VirtualMachineSummary `json:"vm"`
	Healthy  bool                  `json:"healthy" jsonschema:"True if and only if there are no blocking findings."`
	Findings []Finding             `json:"findings"`
}

// ExplainInput is the input of explain_field.
type ExplainInput struct {
	Kind      string `json:"kind" jsonschema:"VM Service kind, e.g. VirtualMachine."`
	FieldPath string `json:"fieldPath,omitempty" jsonschema:"Dotted field path, e.g. spec.powerOffMode. Empty explains the kind itself."`
}

// FieldChild describes a direct child field in an explanation.
type FieldChild struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Explanation is the output of explain_field.
type Explanation struct {
	Kind        string       `json:"kind"`
	APIVersion  string       `json:"apiVersion"`
	FieldPath   string       `json:"fieldPath"`
	Type        string       `json:"type,omitempty"`
	Description string       `json:"description,omitempty"`
	Enum        []string     `json:"enum,omitempty"`
	Default     string       `json:"default,omitempty"`
	Required    bool         `json:"required"`
	Children    []FieldChild `json:"children,omitempty"`
}
