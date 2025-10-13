// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package validation_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha5"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func scsiControllerTests() {
	Describe(
		"SCSI Controller Validation",
		Label(
			testlabels.Create,
			testlabels.Update,
			testlabels.Validation,
			testlabels.Webhook,
		),
		scsiControllerValidationTests,
	)
}

func scsiControllerValidationTests() {
	var (
		ctx *unitValidatingWebhookContext
	)

	BeforeEach(func() {
		ctx = newUnitTestContextForValidatingWebhook(false)
		ctx.vm.Status.UniqueID = "vm-123" // VM exists on infrastructure

		// Enable VMSharedDisks feature flag for consistency
		pkgcfg.SetContext(&ctx.WebhookRequestContext, func(config *pkgcfg.Config) {
			config.Features.VMSharedDisks = true
		})
	})

	// Update ctx.Obj from ctx.vm before each test
	JustBeforeEach(func() {
		var err error
		ctx.WebhookRequestContext.Obj, err = builder.ToUnstructured(ctx.vm)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Controller capacity validation", func() {
		When("volume fits on existing controller", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				// Controller has 10 devices
				devices := make([]vmopv1.VirtualDeviceStatus, 10)
				for i := range devices {
					devices[i] = vmopv1.VirtualDeviceStatus{
						Type:       vmopv1.VirtualDeviceTypeDisk,
						UnitNumber: int32(i),
					}
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   devices,
						},
					},
				}

				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
							},
						},
					},
				}
			})

			It("should allow the volume", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})

		When("ParaVirtual SCSI controller at capacity", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				// Controller has 62 devices (leaving room for 1 more)
				devices := make([]vmopv1.VirtualDeviceStatus, 62)
				for i := range devices {
					// Skip unit 7 (reserved for controller)
					unitNum := int32(i)
					if unitNum >= 7 {
						unitNum++
					}
					devices[i] = vmopv1.VirtualDeviceStatus{
						Type:       vmopv1.VirtualDeviceTypeDisk,
						UnitNumber: unitNum,
					}
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   devices,
						},
					},
				}

				busNum0 := int32(0)
				unitNum63 := int32(63) // Try to use unit number beyond max (63)
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum0,
								UnitNumber:          &unitNum63,
							},
						},
					},
				}
			})

			It("should reject the volume with invalid unit number", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("unit number must be less than 63"))
			})
		})

		When("BusLogic SCSI controller at capacity", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeBusLogic,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				// Controller has 14 devices (leaving room for 1 more, max is 15)
				devices := make([]vmopv1.VirtualDeviceStatus, 14)
				for i := range devices {
					// Skip unit 7 (reserved for controller)
					unitNum := int32(i)
					if unitNum >= 7 {
						unitNum++
					}
					devices[i] = vmopv1.VirtualDeviceStatus{
						Type:       vmopv1.VirtualDeviceTypeDisk,
						UnitNumber: unitNum,
					}
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   devices,
						},
					},
				}

				busNum0 := int32(0)
				unitNum15 := int32(15) // Try to use unit number beyond max (15)
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum0,
								UnitNumber:          &unitNum15,
							},
						},
					},
				}
			})

			It("should reject the volume with invalid unit number", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("unit number must be less than 15"))
			})
		})

		When("multiple volumes with duplicate unit numbers", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				busNum0 := int32(0)
				unitNum0 := int32(0)
				// Add multiple volumes trying to use the same unit number
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum0,
								UnitNumber:          &unitNum0,
							},
						},
					},
					{
						Name: "vol2",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc2",
								},
								ControllerBusNumber: &busNum0,
								UnitNumber:          &unitNum0, // Duplicate unit number
							},
						},
					},
				}
			})

			It("should reject due to duplicate unit numbers", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("already used by volume"))
			})
		})
	})

	Context("Controller bus number validation", func() {
		When("volume specifies valid controller bus number", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				busNum := int32(0)
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum,
							},
						},
					},
				}
			})

			It("should allow the volume", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})

		When("volume specifies non-existent controller bus number", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				busNum := int32(2) // Controller 2 doesn't exist
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum,
							},
						},
					},
				}
			})

			It("should reject the volume", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("SCSI controller with bus number 2 does not exist"))
			})
		})

		When("volume specifies out-of-range bus number", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				busNum := int32(5) // Out of range (max is 3)
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum,
							},
						},
					},
				}
			})

			It("should reject the volume", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("must be between 0 and 3"))
			})
		})
	})

	Context("Application type validation", func() {
		When("OracleRAC volume without sharingMode=None controller", func() {
			BeforeEach(func() {
				// Only have a Physical sharing mode controller
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModePhysical,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ApplicationType: vmopv1.VolumeApplicationTypeOracleRAC,
							},
						},
					},
				}
			})

			It("should allow (mutation webhook will add controller)", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})

		When("MicrosoftWSFC volume without sharingMode=Physical controller", func() {
			BeforeEach(func() {
				// Only have a None sharing mode controller
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ApplicationType: vmopv1.VolumeApplicationTypeMicrosoftWSFC,
							},
						},
					},
				}
			})

			It("should allow (mutation webhook will add controller)", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})

		When("OracleRAC volume with appropriate controller", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ApplicationType: vmopv1.VolumeApplicationTypeOracleRAC,
							},
						},
					},
				}
			})

			It("should allow the volume", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})
	})

	Context("No controllers specified", func() {
		When("VM has volumes but no controllers", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
							},
						},
					},
				}
			})

			It("should allow (mutation webhook will add controller)", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeTrue())
			})
		})
	})

	Context("Mixed scenarios", func() {
		When("volume with unit number 7 (reserved)", func() {
			BeforeEach(func() {
				ctx.vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
					SCSIControllers: []vmopv1.SCSIControllerSpec{
						{
							BusNumber:   0,
							Type:        vmopv1.SCSIControllerTypeParaVirtualSCSI,
							SharingMode: vmopv1.VirtualControllerSharingModeNone,
						},
					},
				}

				ctx.vm.Status.Hardware = &vmopv1.VirtualMachineHardwareStatus{
					Controllers: []vmopv1.VirtualControllerStatus{
						{
							Type:      vmopv1.VirtualControllerTypeSCSI,
							BusNumber: 0,
							Devices:   []vmopv1.VirtualDeviceStatus{},
						},
					},
				}

				busNum0 := int32(0)
				unitNum7 := int32(7) // Reserved for controller
				ctx.vm.Spec.Volumes = []vmopv1.VirtualMachineVolume{
					{
						Name: "vol1",
						VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc1",
								},
								ControllerBusNumber: &busNum0,
								UnitNumber:          &unitNum7,
							},
						},
					},
				}
			})

			It("should reject unit number 7", func() {
				response := ctx.ValidateCreate(&ctx.WebhookRequestContext)
				Expect(response.Allowed).To(BeFalse())
				Expect(string(response.Result.Reason)).To(ContainSubstring("unit number 7 is reserved"))
			})
		})
	})
}
