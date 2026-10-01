// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contract

// SetPowerStateInput is the input of set_virtual_machine_power_state.
type SetPowerStateInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name      string `json:"name" jsonschema:"VirtualMachine name."`
	State     string `json:"state" jsonschema:"Desired power state."`
	Mode      string `json:"mode,omitempty" jsonschema:"Power-off or suspend mode. Not allowed with PoweredOn."`
	Force     bool   `json:"force,omitempty" jsonschema:"Act even though the VM's power state is managed by a VM group."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"Run admission without persisting the change."`
}

// SetPowerStateOutput is the output of set_virtual_machine_power_state.
type SetPowerStateOutput struct {
	Changed bool                  `json:"changed"`
	DryRun  bool                  `json:"dryRun"`
	VM      VirtualMachineSummary `json:"vm"`
}

// RestartInput is the input of restart_virtual_machine.
type RestartInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name      string `json:"name" jsonschema:"VirtualMachine name."`
	Mode      string `json:"mode,omitempty" jsonschema:"Restart mode."`
	Force     bool   `json:"force,omitempty" jsonschema:"Act even though the VM's power state is managed by a VM group."`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"Run admission without persisting the change."`
}

// RestartOutput is the output of restart_virtual_machine.
type RestartOutput struct {
	Requested       bool   `json:"requested"`
	DryRun          bool   `json:"dryRun"`
	NextRestartTime string `json:"nextRestartTime,omitempty"`
}

// ConditionMatch selects a condition by type and status.
type ConditionMatch struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// WaitInput is the input of wait_for_virtual_machine.
type WaitInput struct {
	Namespace      string          `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name           string          `json:"name" jsonschema:"VirtualMachine name."`
	PowerState     string          `json:"powerState,omitempty" jsonschema:"Observed power state to wait for. Exactly one of powerState or condition is required."`
	Condition      *ConditionMatch `json:"condition,omitempty" jsonschema:"Condition to wait for. Exactly one of powerState or condition is required."`
	TimeoutSeconds int             `json:"timeoutSeconds,omitempty" jsonschema:"Default 300, maximum 600."`
}

// WaitOutput is the output of wait_for_virtual_machine.
type WaitOutput struct {
	Satisfied      bool                  `json:"satisfied"`
	ElapsedSeconds int                   `json:"elapsedSeconds"`
	VM             VirtualMachineSummary `json:"vm"`
}

// StorageClassesInput is the input of list_storage_classes.
type StorageClassesInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
}

// StorageClassQuota describes a storage class usable by a namespace.
type StorageClassQuota struct {
	Name       string `json:"name"`
	QuotaLimit string `json:"quotaLimit,omitempty"`
	QuotaUsed  string `json:"quotaUsed,omitempty"`
}

// StorageClasses is the output of list_storage_classes.
type StorageClasses struct {
	StorageClasses []StorageClassQuota `json:"storageClasses"`
}

// BootstrapRefInput references an existing bootstrap Secret.
type BootstrapRefInput struct {
	Provider   string `json:"provider" jsonschema:"Bootstrap provider."`
	SecretName string `json:"secretName" jsonschema:"Name of an existing Secret in the namespace. The server never reads it."`
	Key        string `json:"key,omitempty" jsonschema:"Key in the Secret. Defaults to user-data for cloudInit and unattend for sysprep."`
}

// CreateVirtualMachineInput is the input of create_virtual_machine.
type CreateVirtualMachineInput struct {
	Namespace         string             `json:"namespace,omitempty" jsonschema:"Namespace. Defaults to the kubeconfig context namespace."`
	Name              string             `json:"name" jsonschema:"VirtualMachine name (DNS-1123 subdomain)."`
	ClassName         string             `json:"className" jsonschema:"VirtualMachineClass name."`
	ImageName         string             `json:"imageName" jsonschema:"VirtualMachineImage or ClusterVirtualMachineImage name, or image display name."`
	StorageClass      string             `json:"storageClass" jsonschema:"Storage class name."`
	NetworkInterfaces []string           `json:"networkInterfaces,omitempty" jsonschema:"Network names, one interface per entry. Omit to use the namespace default network."`
	Bootstrap         *BootstrapRefInput `json:"bootstrap,omitempty"`
	PowerState        string             `json:"powerState,omitempty" jsonschema:"Initial power state. Default PoweredOn."`
	Labels            map[string]string  `json:"labels,omitempty"`
	DryRun            bool               `json:"dryRun" jsonschema:"Required. true runs preflight and Supervisor admission without persisting."`
}

// CreateVirtualMachineOutput is the output of create_virtual_machine.
type CreateVirtualMachineOutput struct {
	Preflight []Finding             `json:"preflight"`
	Admitted  bool                  `json:"admitted"`
	DryRun    bool                  `json:"dryRun"`
	VM        *VirtualMachineDetail `json:"vm,omitempty"`
}
