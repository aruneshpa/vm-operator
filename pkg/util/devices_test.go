// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	"github.com/vmware-tanzu/vm-operator/pkg/util"
)

func newPCIPassthroughDevice(profile string) *vimtypes.VirtualPCIPassthrough {
	var dev vimtypes.VirtualPCIPassthrough
	if profile != "" {
		dev.Backing = &vimtypes.VirtualPCIPassthroughVmiopBackingInfo{
			Vgpu: profile,
		}
	} else {
		dev.Backing = &vimtypes.VirtualPCIPassthroughDynamicBackingInfo{}
	}
	return &dev
}

var _ = Describe("SelectDevices", func() {

	var (
		devIn       []vimtypes.BaseVirtualDevice
		devOut      []vimtypes.BaseVirtualDevice
		selectorFns []util.SelectDeviceFn[vimtypes.BaseVirtualDevice]
	)

	JustBeforeEach(func() {
		// Select the devices.
		devOut = util.SelectDevices(devIn, selectorFns...)
	})

	When("selecting Vmxnet3 NICs with UPTv2 enabled", func() {
		newUptv2EnabledNIC := func() *vimtypes.VirtualVmxnet3 {
			uptv2Enabled := true
			return &vimtypes.VirtualVmxnet3{Uptv2Enabled: &uptv2Enabled}
		}

		BeforeEach(func() {
			devIn = []vimtypes.BaseVirtualDevice{
				&vimtypes.VirtualPCIPassthrough{},
				newUptv2EnabledNIC(),
				&vimtypes.VirtualSriovEthernetCard{},
				&vimtypes.VirtualVmxnet3{},
				newUptv2EnabledNIC(),
			}
			selectorFns = []util.SelectDeviceFn[vimtypes.BaseVirtualDevice]{
				func(dev vimtypes.BaseVirtualDevice) bool {
					nic, ok := dev.(*vimtypes.VirtualVmxnet3)
					return ok && nic.Uptv2Enabled != nil && *nic.Uptv2Enabled
				},
			}
		})
		It("selects only the expected device(s)", func() {
			Expect(devOut).To(HaveLen(2))
			Expect(devOut[0]).To(BeEquivalentTo(newUptv2EnabledNIC()))
			Expect(devOut[1]).To(BeEquivalentTo(newUptv2EnabledNIC()))
		})
	})
})

var _ = Describe("SelectDevicesByType", func() {
	Context("selecting a VirtualPCIPassthrough", func() {
		It("will return only the selected device type", func() {
			devOut := util.SelectDevicesByType[*vimtypes.VirtualPCIPassthrough](
				[]vimtypes.BaseVirtualDevice{
					&vimtypes.VirtualVmxnet3{},
					&vimtypes.VirtualPCIPassthrough{},
					&vimtypes.VirtualSriovEthernetCard{},
				},
			)
			Expect(devOut).To(BeAssignableToTypeOf([]*vimtypes.VirtualPCIPassthrough{}))
			Expect(devOut).To(HaveLen(1))
			Expect(devOut[0]).To(BeEquivalentTo(&vimtypes.VirtualPCIPassthrough{}))
		})
	})
})

var _ = Describe("IsDeviceNvidiaVgpu", func() {
	Context("a VGPU", func() {
		It("will return true", func() {
			Expect(util.IsDeviceNvidiaVgpu(newPCIPassthroughDevice("profile1"))).To(BeTrue())
		})
	})
	Context("a dynamic direct path I/O device", func() {
		It("will return false", func() {
			Expect(util.IsDeviceNvidiaVgpu(newPCIPassthroughDevice(""))).To(BeFalse())
		})
	})
	Context("a virtual CD-ROM", func() {
		It("will return false", func() {
			Expect(util.IsDeviceNvidiaVgpu(&vimtypes.VirtualCdrom{})).To(BeFalse())
		})
	})
})

var _ = Describe("IsDeviceDynamicDirectPathIO", func() {
	Context("a VGPU", func() {
		It("will return false", func() {
			Expect(util.IsDeviceDynamicDirectPathIO(newPCIPassthroughDevice("profile1"))).To(BeFalse())
		})
	})
	Context("a dynamic direct path I/O device", func() {
		It("will return true", func() {
			Expect(util.IsDeviceDynamicDirectPathIO(newPCIPassthroughDevice(""))).To(BeTrue())
		})
	})
	Context("a virtual CD-ROM", func() {
		It("will return false", func() {
			Expect(util.IsDeviceDynamicDirectPathIO(&vimtypes.VirtualCdrom{})).To(BeFalse())
		})
	})
})

var _ = Describe("SelectDynamicDirectPathIO", func() {
	Context("selecting a dynamic direct path I/O device", func() {
		It("will return only the selected device type", func() {
			devOut := util.SelectDynamicDirectPathIO(
				[]vimtypes.BaseVirtualDevice{
					newPCIPassthroughDevice(""),
					&vimtypes.VirtualVmxnet3{},
					newPCIPassthroughDevice("profile1"),
					&vimtypes.VirtualSriovEthernetCard{},
					newPCIPassthroughDevice(""),
					newPCIPassthroughDevice("profile2"),
				},
			)
			Expect(devOut).To(BeAssignableToTypeOf([]*vimtypes.VirtualPCIPassthrough{}))
			Expect(devOut).To(HaveLen(2))
			Expect(devOut[0].Backing).To(BeAssignableToTypeOf(&vimtypes.VirtualPCIPassthroughDynamicBackingInfo{}))
			Expect(devOut[0]).To(BeEquivalentTo(newPCIPassthroughDevice("")))
			Expect(devOut[1].Backing).To(BeAssignableToTypeOf(&vimtypes.VirtualPCIPassthroughDynamicBackingInfo{}))
			Expect(devOut[1]).To(BeEquivalentTo(newPCIPassthroughDevice("")))
		})
	})
})

var _ = Describe("HasVirtualPCIPassthroughDeviceChange", func() {

	var (
		devices []vimtypes.BaseVirtualDeviceConfigSpec
		has     bool
	)

	JustBeforeEach(func() {
		has = util.HasVirtualPCIPassthroughDeviceChange(devices)
	})

	AfterEach(func() {
		devices = nil
	})

	Context("empty list", func() {
		It("return false", func() {
			Expect(has).To(BeFalse())
		})
	})

	Context("non passthrough device", func() {
		BeforeEach(func() {
			devices = append(devices, &vimtypes.VirtualDeviceConfigSpec{
				Device: &vimtypes.VirtualVmxnet3{},
			})
		})

		It("returns false", func() {
			Expect(has).To(BeFalse())
		})
	})

	Context("vGPU device", func() {
		BeforeEach(func() {
			devices = append(devices,
				&vimtypes.VirtualDeviceConfigSpec{
					Device: &vimtypes.VirtualVmxnet3{},
				},
				&vimtypes.VirtualDeviceConfigSpec{
					Device: newPCIPassthroughDevice(""),
				},
			)
		})

		It("returns true", func() {
			Expect(has).To(BeTrue())
		})
	})

	Context("DDPIO device", func() {
		BeforeEach(func() {
			devices = append(devices,
				&vimtypes.VirtualDeviceConfigSpec{
					Device: &vimtypes.VirtualVmxnet3{},
				},
				&vimtypes.VirtualDeviceConfigSpec{
					Device: newPCIPassthroughDevice("profile1"),
				},
			)
		})

		It("returns true", func() {
			Expect(has).To(BeTrue())
		})
	})

})

var _ = Describe("SelectNvidiaVgpu", func() {
	Context("selecting Nvidia vGPU devices", func() {
		It("will return only the selected device type", func() {
			devOut := util.SelectNvidiaVgpu(
				[]vimtypes.BaseVirtualDevice{
					newPCIPassthroughDevice(""),
					&vimtypes.VirtualVmxnet3{},
					newPCIPassthroughDevice("profile1"),
					&vimtypes.VirtualSriovEthernetCard{},
					newPCIPassthroughDevice(""),
					newPCIPassthroughDevice("profile2"),
				},
			)
			Expect(devOut).To(BeAssignableToTypeOf([]*vimtypes.VirtualPCIPassthrough{}))
			Expect(devOut).To(HaveLen(2))
			Expect(devOut[0].Backing).To(BeAssignableToTypeOf(&vimtypes.VirtualPCIPassthroughVmiopBackingInfo{}))
			Expect(devOut[0]).To(BeEquivalentTo(newPCIPassthroughDevice("profile1")))
			Expect(devOut[1].Backing).To(BeAssignableToTypeOf(&vimtypes.VirtualPCIPassthroughVmiopBackingInfo{}))
			Expect(devOut[1]).To(BeEquivalentTo(newPCIPassthroughDevice("profile2")))
		})
	})
})

var _ = Describe("SelectDevicesByTypes", func() {

	var (
		devIn  []vimtypes.BaseVirtualDevice
		devOut []vimtypes.BaseVirtualDevice
		devT2S []vimtypes.BaseVirtualDevice
	)

	BeforeEach(func() {
		devIn = []vimtypes.BaseVirtualDevice{
			&vimtypes.VirtualPCIPassthrough{},
			&vimtypes.VirtualSriovEthernetCard{},
			&vimtypes.VirtualVmxnet3{},
		}
	})

	JustBeforeEach(func() {
		devOut = util.SelectDevicesByTypes(devIn, devT2S...)
	})

	Context("selecting a VirtualPCIPassthrough", func() {
		BeforeEach(func() {
			devT2S = []vimtypes.BaseVirtualDevice{
				&vimtypes.VirtualPCIPassthrough{},
			}
		})
		It("will return only the selected device type(s)", func() {
			Expect(devOut).To(HaveLen(1))
			Expect(devOut[0]).To(BeEquivalentTo(&vimtypes.VirtualPCIPassthrough{}))
		})
	})

	Context("selecting a VirtualSriovEthernetCard and VirtualVmxnet3", func() {
		BeforeEach(func() {
			devT2S = []vimtypes.BaseVirtualDevice{
				&vimtypes.VirtualSriovEthernetCard{},
				&vimtypes.VirtualVmxnet3{},
			}
		})
		It("will return only the selected device type(s)", func() {
			Expect(devOut).To(HaveLen(2))
			Expect(devOut[0]).To(BeEquivalentTo(&vimtypes.VirtualSriovEthernetCard{}))
			Expect(devOut[1]).To(BeEquivalentTo(&vimtypes.VirtualVmxnet3{}))
		})
	})

	Context("selecting a type of device not in the ConfigSpec", func() {
		BeforeEach(func() {
			devT2S = []vimtypes.BaseVirtualDevice{
				&vimtypes.VirtualDisk{},
			}
		})
		It("will not return any devices", func() {
			Expect(devOut).To(HaveLen(0))
		})
	})

	Context("selecting no device types", func() {
		It("will not return any devices", func() {
			Expect(devOut).To(HaveLen(0))
		})
	})
})

var _ = Describe("GetPreferredDiskFormat", func() {

	DescribeTable("[]string",
		func(in []string, exp vimtypes.DatastoreSectorFormat) {
			Expect(util.GetPreferredDiskFormat(in...)).To(Equal(exp))
		},
		Entry(
			"no available formats",
			[]string{},
			vimtypes.DatastoreSectorFormat(""),
		),
		Entry(
			"4kn is available",
			[]string{
				string(vimtypes.DatastoreSectorFormatEmulated_512),
				string(vimtypes.DatastoreSectorFormatNative_512),
				string(vimtypes.DatastoreSectorFormatNative_4k),
			},
			vimtypes.DatastoreSectorFormatNative_4k,
		),
		Entry(
			"native 512 is available",
			[]string{
				string(vimtypes.DatastoreSectorFormatEmulated_512),
				string(vimtypes.DatastoreSectorFormatNative_512),
			},
			vimtypes.DatastoreSectorFormatNative_512,
		),
		Entry(
			"neither 4kn nor 512 are available",
			[]string{
				string(vimtypes.DatastoreSectorFormatEmulated_512),
			},
			vimtypes.DatastoreSectorFormatEmulated_512,
		),
	)

	DescribeTable("[]vimtypes.DatastoreSectorFormat",
		func(in []vimtypes.DatastoreSectorFormat, exp vimtypes.DatastoreSectorFormat) {
			Expect(util.GetPreferredDiskFormat(in...)).To(Equal(exp))
		},
		Entry(
			"no available formats",
			[]vimtypes.DatastoreSectorFormat{},
			vimtypes.DatastoreSectorFormat(""),
		),
		Entry(
			"4kn is available",
			[]vimtypes.DatastoreSectorFormat{
				vimtypes.DatastoreSectorFormatEmulated_512,
				vimtypes.DatastoreSectorFormatNative_512,
				vimtypes.DatastoreSectorFormatNative_4k,
			},
			vimtypes.DatastoreSectorFormatNative_4k,
		),
		Entry(
			"native 512 is available",
			[]vimtypes.DatastoreSectorFormat{
				vimtypes.DatastoreSectorFormatEmulated_512,
				vimtypes.DatastoreSectorFormatNative_512,
			},
			vimtypes.DatastoreSectorFormatNative_512,
		),
		Entry(
			"neither 4kn nor 512 are available",
			[]vimtypes.DatastoreSectorFormat{
				vimtypes.DatastoreSectorFormatEmulated_512,
			},
			vimtypes.DatastoreSectorFormatEmulated_512,
		),
	)
})

var _ = Describe("ExtractDeviceNameAndUUID", func() {
	var (
		disk          *vimtypes.VirtualDisk
		diskCount     uint
		existingNames map[string]struct{}
	)

	BeforeEach(func() {
		existingNames = map[string]struct{}{}
		diskCount = 0
	})

	When("disk is nil", func() {
		It("should generate fallback name when diskCount is provided", func() {
			name, uuid := util.ExtractDeviceNameAndUUID(nil, diskCount, existingNames)
			Expect(name).To(Equal("disk-0"))
			Expect(uuid).To(BeEmpty())
		})
	})

	When("disk backing is nil", func() {
		It("should generate fallback name when diskCount is provided", func() {
			disk = &vimtypes.VirtualDisk{}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk-0"))
			Expect(uuid).To(BeEmpty())
		})
	})

	When("disk has SeSparse backing", func() {
		It("should extract name and UUID", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskSeSparseBackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/disk1.vmdk",
						},
						Uuid: "test-uuid-123",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk1"))
			Expect(uuid).To(Equal("test-uuid-123"))
		})
	})

	When("disk has SparseVer2 backing", func() {
		It("should extract name and UUID", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskSparseVer2BackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/disk2.vmdk",
						},
						Uuid: "test-uuid-456",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk2"))
			Expect(uuid).To(Equal("test-uuid-456"))
		})
	})

	When("disk has FlatVer2 backing", func() {
		It("should extract name and UUID", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskFlatVer2BackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/disk3.vmdk",
						},
						Uuid: "test-uuid-789",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk3"))
			Expect(uuid).To(Equal("test-uuid-789"))
		})
	})

	When("disk has RawDiskVer2 backing", func() {
		It("should extract name from DescriptorFileName and UUID", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskRawDiskVer2BackingInfo{
						DescriptorFileName: "/vmfs/volumes/datastore1/vm1/disk4.vmdk",
						Uuid:               "test-uuid-raw",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk4"))
			Expect(uuid).To(Equal("test-uuid-raw"))
		})
	})

	When("disk has SparseVer1 backing (no UUID)", func() {
		It("should extract name only", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskSparseVer1BackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/disk5.vmdk",
						},
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk5"))
			Expect(uuid).To(BeEmpty())
		})
	})

	When("disk has unknown backing type", func() {
		It("should fallback to DeviceInfo label", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDeviceFileBackingInfo{
						FileName: "/vmfs/volumes/datastore1/vm1/disk6.vmdk",
					},
					DeviceInfo: &vimtypes.Description{
						Label: "Custom Disk Label",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("Custom Disk Label"))
			Expect(uuid).To(BeEmpty())
		})
	})

	When("disk has unknown backing type and no DeviceInfo", func() {
		It("should generate fallback name when diskCount is provided", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDeviceFileBackingInfo{
						FileName: "/vmfs/volumes/datastore1/vm1/disk7.vmdk",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk-0"))
			Expect(uuid).To(BeEmpty())
		})
	})

	When("filename has no extension", func() {
		It("should return the full filename", func() {
			disk = &vimtypes.VirtualDisk{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDiskFlatVer2BackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/disk-without-extension",
						},
						Uuid: "test-uuid-no-ext",
					},
				},
			}
			name, uuid := util.ExtractDeviceNameAndUUID(disk, diskCount, existingNames)
			Expect(name).To(Equal("disk-without-extension"))
			Expect(uuid).To(Equal("test-uuid-no-ext"))
		})
	})
})

var _ = Describe("ExtractCdromName", func() {
	var (
		cdrom         *vimtypes.VirtualCdrom
		cdromCount    uint
		existingNames map[string]struct{}
	)

	BeforeEach(func() {
		existingNames = map[string]struct{}{}
		cdromCount = 0
	})

	When("cdrom is nil", func() {
		It("should return fallback name", func() {
			name := util.ExtractCdromName(nil, cdromCount, existingNames)
			Expect(name).To(Equal("cdrom-0"))
		})
	})

	When("cdrom backing is nil", func() {
		It("should return fallback name", func() {
			cdrom = &vimtypes.VirtualCdrom{}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("cdrom-0"))
		})
	})

	When("cdrom has ISO backing", func() {
		It("should extract name from filename", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromIsoBackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/ubuntu.iso",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("ubuntu"))
		})
	})

	When("cdrom has remote passthrough backing", func() {
		It("should extract name from device name", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromRemotePassthroughBackingInfo{
						VirtualDeviceRemoteDeviceBackingInfo: vimtypes.VirtualDeviceRemoteDeviceBackingInfo{
							DeviceName: "Remote CD-ROM Device",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("Remote CD-ROM Device"))
		})
	})

	When("cdrom has ATAPI backing", func() {
		It("should extract name from device name", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromAtapiBackingInfo{
						VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
							DeviceName: "ATAPI CD-ROM",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("ATAPI CD-ROM"))
		})
	})

	When("cdrom has remote ATAPI backing", func() {
		It("should extract name from device name", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromRemoteAtapiBackingInfo{
						VirtualDeviceRemoteDeviceBackingInfo: vimtypes.VirtualDeviceRemoteDeviceBackingInfo{
							DeviceName: "Remote ATAPI CD-ROM",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("Remote ATAPI CD-ROM"))
		})
	})

	When("cdrom has passthrough backing", func() {
		It("should extract name from device name", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromPassthroughBackingInfo{
						VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
							DeviceName: "Passthrough CD-ROM",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("Passthrough CD-ROM"))
		})
	})

	When("cdrom has unknown backing with DeviceInfo", func() {
		It("should extract name from DeviceInfo label", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDeviceFileBackingInfo{
						FileName: "/vmfs/volumes/datastore1/vm1/cdrom.vmdk",
					},
					DeviceInfo: &vimtypes.Description{
						Label: "Custom CD-ROM Label",
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("Custom CD-ROM Label"))
		})
	})

	When("cdrom has unknown backing without DeviceInfo", func() {
		It("should return fallback name", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualDeviceFileBackingInfo{
						FileName: "/vmfs/volumes/datastore1/vm1/cdrom.vmdk",
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("cdrom-0"))
		})
	})

	When("fallback name conflicts with existing names", func() {
		It("should generate unique name by appending count", func() {
			existingNames["cdrom-0"] = struct{}{}
			existingNames["cdrom-0-0"] = struct{}{}
			existingNames["cdrom-0-0-0"] = struct{}{}

			cdrom = &vimtypes.VirtualCdrom{}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("cdrom-0-0-0-0"))
		})
	})

	When("ISO filename has no extension", func() {
		It("should return the full filename", func() {
			cdrom = &vimtypes.VirtualCdrom{
				VirtualDevice: vimtypes.VirtualDevice{
					Backing: &vimtypes.VirtualCdromIsoBackingInfo{
						VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
							FileName: "/vmfs/volumes/datastore1/vm1/ubuntu",
						},
					},
				},
			}
			name := util.ExtractCdromName(cdrom, cdromCount, existingNames)
			Expect(name).To(Equal("ubuntu"))
		})
	})
})
