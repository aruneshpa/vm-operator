// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

// ConditionRule classifies a VM condition type that is not True.
type ConditionRule struct {
	Severity   string
	Suggestion string
}

// ReasonRule overrides the classification of a condition when it carries a
// specific reason. An empty Severity means the reason is informational
// context for its condition and does not change the classification.
type ReasonRule struct {
	Severity   string
	Suggestion string
}

const (
	suggestClass   = "List classes with list_virtual_machine_classes; the class must exist in the VM's namespace."
	suggestImage   = "List images with list_virtual_machine_images. spec.image and spec.imageName cannot be changed after creation, so the VM must be recreated with a valid image."
	suggestStorage = "Check that the storage class is assigned to the namespace with list_storage_classes, and check the VM's events."
	suggestBoot    = "Check that each bootstrap Secret reference names an existing Secret and key in the namespace. The server cannot read Secrets; verify with kubectl."
	suggestNetwork = "Check the network names in spec.network.interfaces and the VM's events."
	suggestEvents  = "Check the VM's events with get_events for the underlying error."
	suggestPower   = "The change takes effect after the VM is powered off and on again."
	suggestPaused  = "Reconciliation of this VM is paused by an administrator or by the pause extraConfig key; nothing will change until it is resumed."
	suggestGuest   = "Guest customization failed; check the bootstrap configuration and the guest OS logs."
	suggestTools   = "VMware Tools is not running in the guest; guest-reported IPs and soft power operations may be unavailable."
)

// ConditionRules classifies every VirtualMachine condition type. A unit test
// fails if the API defines a condition constant that is missing here or from
// ReasonRules.
var ConditionRules = map[string]ConditionRule{
	vmopv1.ReadyConditionType:                              {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineConditionClassReady:               {contract.SeverityBlocking, suggestClass},
	vmopv1.VirtualMachineConditionImageReady:               {contract.SeverityBlocking, suggestImage},
	vmopv1.VirtualMachineConditionVMSetResourcePolicyReady: {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineConditionStorageReady:             {contract.SeverityBlocking, suggestStorage},
	vmopv1.VirtualMachineConditionBootstrapReady:           {contract.SeverityBlocking, suggestBoot},
	vmopv1.VirtualMachineConditionNetworkReady:             {contract.SeverityBlocking, suggestNetwork},
	vmopv1.VirtualMachineConditionImageCacheReady:          {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineConditionPlacementReady:           {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineEncryptionSynced:                  {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineDiskPromotionStarted:              {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineDiskPromotionSynced:               {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineConditionCreated:                  {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineClassConfigurationSynced:          {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineHardwareDeviceConfigVerified:      {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineHardwareControllersVerified:       {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineExtraConfigSynced:                 {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineNetworkConfigSynced:               {contract.SeverityWarning, suggestNetwork},
	vmopv1.VirtualMachineHardwareVolumesVerified:           {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineHardwareCDROMVerified:             {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachinePowerStateSynced:                  {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineGuestNetworkConfigSynced:          {contract.SeverityWarning, suggestNetwork},
	vmopv1.VirtualMachineLocationValid:                     {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineConditionComputeConfigSynced:      {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineSnapshotRevertSucceeded:           {contract.SeverityWarning, suggestEvents},
	vmopv1.GuestBootstrapCondition:                         {contract.SeverityWarning, suggestGuest},
	vmopv1.GuestIDReconfiguredCondition:                    {contract.SeverityInfo, ""},
	vmopv1.GuestCustomizationCondition:                     {contract.SeverityWarning, suggestGuest},
	vmopv1.VirtualMachineToolsCondition:                    {contract.SeverityWarning, suggestTools},
	vmopv1.VirtualMachineReconcileReady:                    {contract.SeverityWarning, suggestEvents},
	vmopv1.VirtualMachineBackupUpToDateCondition:           {contract.SeverityInfo, ""},
}

// ReasonRules classifies every VirtualMachine condition reason constant.
var ReasonRules = map[string]ReasonRule{
	vmopv1.VirtualMachineSnapshotRevertInProgressReason:              {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineSnapshotRevertTaskFailedReason:              {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineSnapshotRevertFailedInvalidVMManifestReason: {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineSnapshotRevertSkippedReason:                 {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineSnapshotRevertFailedReason:                  {contract.SeverityBlocking, suggestEvents},
	vmopv1.VirtualMachineHardwareControllersMismatchReason:           {},
	vmopv1.VirtualMachineHardwareVolumesMismatchReason:               {},
	vmopv1.VirtualMachineHardwareCDROMMismatchReason:                 {},
	vmopv1.VirtualMachineHardwareDeviceConfigMismatchReason:          {},
	vmopv1.VirtualMachinePrerequisiteNotMetReason:                    {},
	vmopv1.VirtualMachinePowerOffRequiredReason:                      {contract.SeverityWarning, suggestPower},
	vmopv1.VirtualMachinePowerCyclePendingReason:                     {contract.SeverityWarning, suggestPower},
	vmopv1.VirtualMachineInfraInMaintenanceReason:                    {contract.SeverityInfo, "The underlying infrastructure is in maintenance; the change is retried afterwards."},
	vmopv1.VirtualMachineExtraConfigErrorReason:                      {},
	vmopv1.VirtualMachineExtraConfigMismatchReason:                   {},
	vmopv1.VirtualMachineComputeConfigMismatchReason:                 {},
	vmopv1.VirtualMachineNetworkConfigErrorReason:                    {},
	vmopv1.VirtualMachineNetworkConfigMismatchReason:                 {},
	vmopv1.GuestCustomizationIdleReason:                              {contract.SeverityInfo, ""},
	vmopv1.GuestCustomizationPendingReason:                           {contract.SeverityInfo, "Guest customization has not started yet."},
	vmopv1.GuestCustomizationRunningReason:                           {contract.SeverityInfo, "Guest customization is in progress."},
	vmopv1.GuestCustomizationSucceededReason:                         {contract.SeverityInfo, ""},
	vmopv1.GuestCustomizationFailedReason:                            {contract.SeverityBlocking, suggestGuest},
	vmopv1.VirtualMachineToolsNotRunningReason:                       {},
	vmopv1.VirtualMachineToolsRunningReason:                          {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineReconcileRunningReason:                      {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineReconcilePausedReason:                       {contract.SeverityWarning, suggestPaused},
	vmopv1.VirtualMachineBackupPausedReason:                          {contract.SeverityInfo, ""},
	vmopv1.VirtualMachineBackupFailedReason:                          {contract.SeverityWarning, suggestEvents},
}
