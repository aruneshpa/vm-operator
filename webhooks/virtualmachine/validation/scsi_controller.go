// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha5"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
)

const (
	invalidControllerBusNumberRangeFmt     = "must be between 0 and %d"
	invalidControllerBusNumberDoesNotExist = "SCSI controller with bus number %d does not exist"
	invalidUnitNumberReserved              = "unit number 7 is reserved for the SCSI controller itself"
	invalidUnitNumberRangeFmt              = "unit number must be less than %d for %s controller"
)

// validateControllerSlots validates that all volumes have valid
// controller and unit number assignments.
//
// Since the mutation webhook populates controllerBusNumber and
// unitNumber for all volumes during UPDATE operations, this validation
// primarily checks:
//   - controllerBusNumber is in valid range (0-3)
//   - referenced controller exists in spec.hardware.controllers
//   - unitNumber is not 7 (reserved for controller)
//   - unitNumber is within the controller's capacity
//   - no duplicate unit numbers on the same controller
//
// For CREATE operations or volumes without these fields set, we skip
// validation as the mutation webhook will populate them on UPDATE.
func (v validator) validateControllerSlots(
	_ *pkgctx.WebhookRequestContext,
	vm *vmopv1.VirtualMachine) field.ErrorList {

	var allErrs field.ErrorList

	if len(vm.Spec.Volumes) == 0 {
		return allErrs
	}

	volumesPath := field.NewPath("spec", "volumes")

	// Build controller map from spec.hardware.controllers
	specControllers := make(map[int32]controllerInfo)
	if vm.Spec.Hardware != nil {
		for _, controller := range vm.Spec.Hardware.SCSIControllers {
			specControllers[controller.BusNumber] = controllerInfo{
				busNumber:   controller.BusNumber,
				ctrlType:    controller.Type,
				sharingMode: controller.SharingMode,
				maxSlots:    getMaxSlotsForControllerType(controller.Type),
			}
		}
	}

	// Track used unit numbers per controller to detect duplicates
	usedUnitNumbers := make(map[int32]map[int32]string) // busNumber -> unitNumber -> volumeName

	// Process each volume
	for i, vol := range vm.Spec.Volumes {
		if vol.PersistentVolumeClaim == nil {
			continue
		}

		pvc := vol.PersistentVolumeClaim
		volPath := volumesPath.Index(i)

		// Skip volumes without controllerBusNumber set (mutation webhook will populate)
		if pvc.ControllerBusNumber == nil {
			continue
		}

		busNumber := *pvc.ControllerBusNumber

		// Validate bus number range
		if busNumber < 0 || busNumber >= pkgutil.MaxSCSIControllers {
			allErrs = append(allErrs, field.Invalid(
				volPath.Child("persistentVolumeClaim", "controllerBusNumber"),
				busNumber,
				fmt.Sprintf(invalidControllerBusNumberRangeFmt, pkgutil.MaxSCSIControllers-1)))
			continue
		}

		// Validate controller exists
		controller, exists := specControllers[busNumber]
		if !exists {
			allErrs = append(allErrs, field.Invalid(
				volPath.Child("persistentVolumeClaim", "controllerBusNumber"),
				busNumber,
				fmt.Sprintf(invalidControllerBusNumberDoesNotExist, busNumber)))
			continue
		}

		// Validate unit number if specified
		if pvc.UnitNumber != nil {
			unitNum := *pvc.UnitNumber

			// Unit number 7 is reserved for the controller itself
			if unitNum == pkgutil.SCSIControllerUnitNumber {
				allErrs = append(allErrs, field.Invalid(
					volPath.Child("persistentVolumeClaim", "unitNumber"),
					unitNum,
					invalidUnitNumberReserved))
				continue
			}

			// Validate unit number is within range for controller type
			if unitNum >= controller.maxSlots {
				allErrs = append(allErrs, field.Invalid(
					volPath.Child("persistentVolumeClaim", "unitNumber"),
					unitNum,
					fmt.Sprintf(invalidUnitNumberRangeFmt,
						controller.maxSlots, controller.ctrlType)))
				continue
			}

			// Check for duplicate unit numbers on the same controller
			if usedUnitNumbers[busNumber] == nil {
				usedUnitNumbers[busNumber] = make(map[int32]string)
			}
			if existingVol, exists := usedUnitNumbers[busNumber][unitNum]; exists {
				allErrs = append(allErrs, field.Invalid(
					volPath.Child("persistentVolumeClaim", "unitNumber"),
					unitNum,
					fmt.Sprintf("unit number %d on controller %d is already used by volume %s",
						unitNum, busNumber, existingVol)))
				continue
			}
			usedUnitNumbers[busNumber][unitNum] = vol.Name
		}
	}

	return allErrs
}

// controllerInfo holds information about a controller.
type controllerInfo struct {
	busNumber   int32
	ctrlType    vmopv1.SCSIControllerType
	sharingMode vmopv1.VirtualControllerSharingMode
	maxSlots    int32
}

// getMaxSlotsForControllerType returns the maximum number of slots for a controller type.
func getMaxSlotsForControllerType(ctrlType vmopv1.SCSIControllerType) int32 {
	switch ctrlType {
	case vmopv1.SCSIControllerTypeParaVirtualSCSI:
		return pkgutil.MaxParaVirtualSCSISlots
	case vmopv1.SCSIControllerTypeBusLogic:
		return pkgutil.MaxBusLogicSlots
	case vmopv1.SCSIControllerTypeLsiLogic:
		return pkgutil.MaxLsiLogicSlots
	case vmopv1.SCSIControllerTypeLsiLogicSAS:
		return pkgutil.MaxLsiLogicSASSlots
	default:
		// Default to ParaVirtual if unknown
		return pkgutil.MaxParaVirtualSCSISlots
	}
}
