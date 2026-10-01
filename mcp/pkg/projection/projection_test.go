// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package projection_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1cloudinit "github.com/vmware-tanzu/vm-operator/api/v1alpha6/cloudinit"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	vmopv1sysprep "github.com/vmware-tanzu/vm-operator/api/v1alpha6/sysprep"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
)

const sentinel = "SENTINEL-VALUE-MUST-NOT-LEAK"

// apiStructFields parses the API package and returns the JSON field names of
// each named struct type.
func apiStructFields(names []string) map[string][]string {
	dir := filepath.Join("..", "..", "..", "api", "v1alpha6")
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		files = append(files, f)
	}

	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string][]string{}
	for _, f := range files {
		{
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !want[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				fields := []string{}
				for _, fld := range st.Fields.List {
					if fld.Tag == nil {
						continue
					}
					tag, err := strconv.Unquote(fld.Tag.Value)
					ExpectWithOffset(1, err).ToNot(HaveOccurred())
					name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
					if name == "" || name == "-" {
						// Embedded inline fields carry no name of their own.
						continue
					}
					fields = append(fields, name)
				}
				out[ts.Name.Name] = fields
				return false
			})
		}
	}
	return out
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return string(b)
}

var _ = Describe("Projection", Label(testlabels.MCP), func() {
	Context("field inventory", func() {
		It("classifies every top-level field of every projected API type", func() {
			var names []string
			for n := range projection.Inventory {
				names = append(names, n)
			}
			found := apiStructFields(names)
			for _, n := range names {
				fields, ok := found[n]
				Expect(ok).To(BeTrue(), "type %s not found in api/v1alpha6", n)
				inv := projection.Inventory[n]
				for _, f := range fields {
					Expect(inv).To(HaveKey(f),
						"%s.%s is not classified in projection.Inventory; decide whether it is projected, summarized, or excluded", n, f)
				}
				for f, class := range inv {
					Expect(fields).To(ContainElement(f), "projection.Inventory lists %s.%s, which no longer exists", n, f)
					Expect(class).To(BeElementOf(projection.Projected, projection.Summarized, projection.Excluded))
				}
			}
		})
	})

	Context("Bootstrap", func() {
		It("returns nil for no bootstrap", func() {
			Expect(projection.Bootstrap(nil)).To(BeNil())
		})

		It("never copies values and reports paths and Secret references", func() {
			b := &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
					CloudConfig: &vmopv1cloudinit.CloudConfig{
						Users: []vmopv1cloudinit.User{{
							Name:              "bob",
							HashedPasswd:      &vmopv1common.SecretKeySelector{Name: "user-secret", Key: "hash"},
							SSHAuthorizedKeys: []string{sentinel},
						}},
						RunCmd: json.RawMessage(`["` + sentinel + `", ["nested", "` + sentinel + `"]]`),
						WriteFiles: []vmopv1cloudinit.WriteFile{{
							Path:    "/etc/" + sentinel,
							Content: json.RawMessage(`"` + sentinel + `"`),
						}},
					},
					RawCloudConfig:    &vmopv1common.SecretKeySelector{Name: "raw-ci", Key: "user-data"},
					SSHAuthorizedKeys: []string{sentinel},
				},
				LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{
					Password:   &vmopv1common.PasswordSecretKeySelector{Name: "lp-pass"},
					ScriptText: &vmopv1common.ValueOrSecretKeySelector{Value: new(sentinel)},
				},
				Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
					Sysprep: &vmopv1sysprep.Sysprep{
						GUIRunOnce: &vmopv1sysprep.GUIRunOnce{Commands: []string{sentinel}},
						ScriptText: &vmopv1common.ValueOrSecretKeySelector{
							From: &vmopv1common.SecretKeySelector{Name: "sp-script", Key: "script"},
						},
						UserData: vmopv1sysprep.UserData{FullName: sentinel, OrgName: sentinel},
					},
					RawSysprep: &vmopv1common.SecretKeySelector{Name: "raw-sp", Key: "unattend"},
				},
				VAppConfig: &vmopv1.VirtualMachineBootstrapVAppConfigSpec{
					Properties: []vmopv1common.KeyValueOrSecretKeySelectorPair{
						{Key: "password", Value: vmopv1common.ValueOrSecretKeySelector{Value: new(sentinel)}},
						{Key: "token", Value: vmopv1common.ValueOrSecretKeySelector{
							From: &vmopv1common.SecretKeySelector{Name: "vapp", Key: "token"},
						}},
					},
					RawProperties: sentinel,
				},
			}
			s := projection.Bootstrap(b)
			Expect(toJSON(s)).ToNot(ContainSubstring(sentinel))
			Expect(s.Providers).To(Equal([]string{"cloudInit", "linuxPrep", "sysprep", "vAppConfig"}))
			Expect(s.InlineFieldsPresent).To(ContainElements(
				"spec.bootstrap.cloudInit.cloudConfig.runcmd",
				"spec.bootstrap.cloudInit.cloudConfig.write_files[].content",
				"spec.bootstrap.cloudInit.cloudConfig.users[].name",
				"spec.bootstrap.cloudInit.sshAuthorizedKeys[]",
				"spec.bootstrap.linuxPrep.scriptText.value",
				"spec.bootstrap.sysprep.sysprep.guiRunOnce.commands[]",
				"spec.bootstrap.vAppConfig.properties[].value.value",
				"spec.bootstrap.vAppConfig.rawProperties",
			))
			Expect(s.SecretRefs).To(ContainElements(
				contract.SecretRef{Name: "raw-ci", Key: "user-data", FieldPath: "spec.bootstrap.cloudInit.rawCloudConfig"},
				contract.SecretRef{Name: "user-secret", Key: "hash", FieldPath: "spec.bootstrap.cloudInit.cloudConfig.users[].hashed_passwd"},
				contract.SecretRef{Name: "lp-pass", FieldPath: "spec.bootstrap.linuxPrep.password"},
				contract.SecretRef{Name: "sp-script", Key: "script", FieldPath: "spec.bootstrap.sysprep.sysprep.scriptText.from"},
				contract.SecretRef{Name: "raw-sp", Key: "unattend", FieldPath: "spec.bootstrap.sysprep.rawSysprep"},
				contract.SecretRef{Name: "vapp", Key: "token", FieldPath: "spec.bootstrap.vAppConfig.properties[].value.from"},
			))
			for _, r := range s.SecretRefs {
				Expect(r.Name).ToNot(Equal("bob"), "a cloud-init user must not be reported as a Secret reference")
			}
		})

		It("reports disabled bootstrap", func() {
			s := projection.Bootstrap(&vmopv1.VirtualMachineBootstrapSpec{Disabled: true})
			Expect(s.Disabled).To(BeTrue())
			Expect(s.Providers).To(BeEmpty())
		})
	})

	Context("Bootstrap raw JSON and selector classification", func() {
		const (
			sentinelName = "SENTINEL-RAW-NAME"
			sentinelKey  = "SENTINEL-RAW-KEY"
			sentinelMap  = "SENTINEL-RAW-MAPKEY"
		)

		It("never reports selector-shaped or keyed user data inside raw JSON", func() {
			b := &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
					CloudConfig: &vmopv1cloudinit.CloudConfig{
						RunCmd: []byte(`[{"name":"` + sentinelName + `","key":"` + sentinelKey + `"},` +
							`{"` + sentinelMap + `":"x"}]`),
						WriteFiles: []vmopv1cloudinit.WriteFile{
							{Path: "/etc/a", Content: []byte(`{"` + sentinelMap + `":{"name":"` + sentinelName + `"}}`)},
							{Path: "/etc/b", Content: []byte(`{"name":"bootstrap-secret","key":"file"}`)},
						},
					},
				},
			}
			sum := projection.Bootstrap(b)
			raw, err := json.Marshal(sum)
			Expect(err).ToNot(HaveOccurred())
			for _, sentinel := range []string{sentinelName, sentinelKey, sentinelMap} {
				Expect(string(raw)).ToNot(ContainSubstring(sentinel))
			}
			Expect(sum.InlineFieldsPresent).To(ContainElements(
				"spec.bootstrap.cloudInit.cloudConfig.runcmd",
				"spec.bootstrap.cloudInit.cloudConfig.write_files[].content",
			))
			Expect(sum.SecretRefs).To(ContainElement(contract.SecretRef{
				Name: "bootstrap-secret", Key: "file",
				FieldPath: "spec.bootstrap.cloudInit.cloudConfig.write_files[].content",
			}))
		})

		It("does not treat a {name, key} object at an ordinary field as a Secret reference", func() {
			b := &vmopv1.VirtualMachineBootstrapSpec{
				VAppConfig: &vmopv1.VirtualMachineBootstrapVAppConfigSpec{
					Properties: []vmopv1common.KeyValueOrSecretKeySelectorPair{
						{Key: sentinelKey, Value: vmopv1common.ValueOrSecretKeySelector{Value: new("v")}},
					},
				},
			}
			sum := projection.Bootstrap(b)
			Expect(sum.SecretRefs).To(BeEmpty())
			raw, err := json.Marshal(sum)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(raw)).ToNot(ContainSubstring(sentinelKey))
		})

		It("classifies every raw-JSON and selector field in the bootstrap types", func() {
			rawType := reflect.TypeFor[json.RawMessage]()
			var raws, selectors []string
			seen := map[reflect.Type]bool{}
			var visit func(t reflect.Type)
			visit = func(t reflect.Type) {
				for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
					if t == rawType {
						return
					}
					t = t.Elem()
				}
				if t.Kind() != reflect.Struct || seen[t] {
					return
				}
				seen[t] = true
				for i := range t.NumField() {
					f := t.Field(i)
					name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
					ft := f.Type
					for ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					switch {
					case ft == rawType:
						raws = append(raws, name)
					case strings.HasSuffix(ft.Name(), "SecretKeySelector") && ft.Name() != "ValueOrSecretKeySelector":
						selectors = append(selectors, name)
					default:
						visit(f.Type)
					}
				}
			}
			visit(reflect.TypeFor[vmopv1.VirtualMachineBootstrapSpec]())

			Expect(raws).To(ContainElements("runcmd", "content"))
			for _, n := range raws {
				Expect(projection.OpaqueFields).To(HaveKey(n), "raw JSON field %q must be opaque", n)
			}
			Expect(selectors).ToNot(BeEmpty())
			for _, n := range selectors {
				Expect(projection.SelectorFields).To(HaveKey(n), "selector field %q must be classified", n)
			}
		})
	})

	Context("VirtualMachine", func() {
		var vm *vmopv1.VirtualMachine

		BeforeEach(func() {
			projection.Now = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }
			vm = &vmopv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "dev",
					Name:              "web",
					CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
					Labels:            map[string]string{"app": "web"},
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "Other", Name: "x"},
						{Kind: "VirtualMachineReplicaSet", Name: "rs", Controller: new(true)},
					},
				},
				Spec: vmopv1.VirtualMachineSpec{
					Image:        &vmopv1.VirtualMachineImageRef{Kind: "ClusterVirtualMachineImage", Name: "vmi-1"},
					ImageName:    "ubuntu",
					ClassName:    "small",
					StorageClass: "fast",
					PowerState:   vmopv1.VirtualMachinePowerStateOn,
					GroupName:    "grp",
					Network: &vmopv1.VirtualMachineNetworkSpec{
						Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
							{Name: "eth0", Network: &vmopv1common.PartialObjectRef{Name: "net-a"}},
							{Name: "eth1"},
						},
					},
					Volumes: []vmopv1.VirtualMachineVolume{
						{Name: "data", VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{}}},
						{Name: "inst", VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
							PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
								InstanceVolumeClaim: &vmopv1.InstanceVolumeClaimVolumeSource{}}}},
						{Name: "none"},
					},
					Advanced: &vmopv1.VirtualMachineAdvancedSpec{
						ExtraConfig: []vmopv1common.KeyValuePair{{Key: "b", Value: sentinel}, {Key: "a", Value: sentinel}},
					},
				},
				Status: vmopv1.VirtualMachineStatus{
					PowerState: vmopv1.VirtualMachinePowerStateOff,
					Zone:       "zone-1",
					Conditions: []metav1.Condition{
						{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionFalse},
						{Type: vmopv1.VirtualMachineConditionCreated, Status: metav1.ConditionTrue},
					},
					Network: &vmopv1.VirtualMachineNetworkStatus{
						PrimaryIP4: "10.0.0.5",
						HostName:   "web-host",
						Interfaces: []vmopv1.VirtualMachineNetworkInterfaceStatus{
							{Name: "eth0", IP: &vmopv1.VirtualMachineNetworkInterfaceIPStatus{
								Addresses: []vmopv1.VirtualMachineNetworkInterfaceIPAddrStatus{{Address: "10.0.0.5/24"}}}},
						},
					},
					ExtraConfig:     []vmopv1common.KeyValuePair{{Key: "a", Value: sentinel}, {Key: "c", Value: sentinel}},
					HardwareVersion: 21,
					CurrentSnapshot: &vmopv1.VirtualMachineSnapshotReference{Name: "snap-1"},
				},
			}
			vm.Spec.Volumes[0].PersistentVolumeClaim.ClaimName = "data-pvc"
		})

		AfterEach(func() {
			projection.Now = time.Now
		})

		It("projects the summary", func() {
			s := projection.VirtualMachineSummary(vm)
			Expect(s.Image).To(Equal(contract.ImageRef{Kind: "ClusterVirtualMachineImage", Name: "vmi-1"}))
			Expect(s.PowerState).To(Equal(contract.PowerState{Desired: "PoweredOn", Observed: "PoweredOff"}))
			Expect(s.Ready).To(Equal("False"))
			Expect(s.Created).To(BeTrue())
			Expect(s.OwnedBy).To(Equal(&contract.ObjectRef{Kind: "VirtualMachineReplicaSet", Namespace: "dev", Name: "rs"}))
			Expect(s.PrimaryIP4.Value).To(Equal("10.0.0.5"))
			Expect(s.PrimaryIP6).To(BeNil())
			Expect(s.Zone).To(Equal("zone-1"))
			Expect(s.GroupName).To(Equal("grp"))
			Expect(s.Age).To(Equal("60m"))
		})

		It("falls back to imageName and Unknown readiness", func() {
			vm.Spec.Image = nil
			vm.Status.Conditions = nil
			s := projection.VirtualMachineSummary(vm)
			Expect(s.Image).To(Equal(contract.ImageRef{Name: "ubuntu"}))
			Expect(s.Ready).To(Equal("Unknown"))
			Expect(s.Created).To(BeFalse())
		})

		It("projects the detail without values", func() {
			d := projection.VirtualMachineDetail(vm)
			Expect(toJSON(d)).ToNot(ContainSubstring(sentinel))
			Expect(d.ExtraConfigKeys).To(Equal([]string{"a", "b", "c"}))
			Expect(d.Volumes).To(Equal([]contract.VolumeSummary{
				{Name: "data", ClaimName: "data-pvc", Type: "persistentVolumeClaim"},
				{Name: "inst", Type: "instanceStorage"},
				{Name: "none", Type: "unknown"},
			}))
			Expect(d.Interfaces).To(Equal([]contract.NetworkInterfaceSummary{
				{Name: "eth0", NetworkName: "net-a", Addresses: []contract.Untrusted{{Value: "10.0.0.5/24"}}},
				{Name: "eth1"},
			}))
			Expect(d.HostName.Value).To(Equal("web-host"))
			Expect(d.CurrentSnapshot).To(Equal("snap-1"))
			Expect(d.HardwareVersion).To(BeEquivalentTo(21))
			Expect(d.StorageClass).To(Equal("fast"))
			Expect(d.Conditions).To(HaveLen(2))
			Expect(d.Labels).To(HaveKeyWithValue("app", "web"))
		})
	})

	Context("VirtualMachineClass", func() {
		It("reports configSpec presence only", func() {
			c := &vmopv1.VirtualMachineClass{
				ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "small"},
				Spec: vmopv1.VirtualMachineClassSpec{
					Description: "small class",
					Hardware:    vmopv1.VirtualMachineClassHardware{Cpus: 2, Memory: resource.MustParse("4Gi")},
					ConfigSpec:  json.RawMessage(`{"extraConfig":[{"key":"guestinfo.x","value":"` + sentinel + `"}]}`),
				},
			}
			s := projection.VirtualMachineClass(c)
			Expect(toJSON(s)).ToNot(ContainSubstring(sentinel))
			Expect(s.HasConfigSpec).To(BeTrue())
			Expect(s.CPUs).To(BeEquivalentTo(2))
			Expect(s.Memory).To(Equal("4Gi"))
		})
	})

	Context("images", func() {
		It("reports OVF property keys only", func() {
			st := &vmopv1.VirtualMachineImageStatus{
				Name:            "Ubuntu 24.04",
				HardwareVersion: new(int32(21)),
				OSInfo:          vmopv1.VirtualMachineImageOSInfo{Type: "ubuntu64Guest", Version: "24.04"},
				OVFProperties: []vmopv1.OVFProperty{
					{Key: "user-data", Default: new(sentinel)},
				},
				ProductInfo: vmopv1.VirtualMachineImageProductInfo{Vendor: "Canonical", Product: "Ubuntu"},
				Disks:       []vmopv1.VirtualMachineImageDiskInfo{{Name: "disk-0", Limit: new(resource.MustParse("10Gi"))}},
				Conditions:  []metav1.Condition{{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionTrue}},
			}
			meta := metav1.ObjectMeta{Name: "vmi-1"}
			d := projection.ImageDetail(projection.KindClusterVirtualMachineImage, meta, st)
			Expect(toJSON(d)).ToNot(ContainSubstring(sentinel))
			Expect(d.OVFPropertyKeys).To(Equal([]string{"user-data"}))
			Expect(d.Ready).To(Equal("True"))
			Expect(d.HardwareVersion).To(BeEquivalentTo(21))
			Expect(d.DisplayName.Value).To(Equal("Ubuntu 24.04"))
			Expect(d.ProductInfo.Value).To(Equal("Canonical Ubuntu"))
			Expect(d.Disks).To(Equal([]contract.ImageDisk{{Name: "disk-0", Limit: "10Gi"}}))
			Expect(d.Namespace).To(BeEmpty())
		})
	})

	Context("snapshots", func() {
		It("projects the summary", func() {
			s := &vmopv1.VirtualMachineSnapshot{
				ObjectMeta: metav1.ObjectMeta{Namespace: "dev", Name: "snap"},
				Spec: vmopv1.VirtualMachineSnapshotSpec{
					VMName: "web", Memory: true, Quiesce: &vmopv1.QuiesceSpec{}, Description: "before upgrade",
				},
				Status: vmopv1.VirtualMachineSnapshotStatus{Conditions: []metav1.Condition{
					{Type: vmopv1.VirtualMachineSnapshotReadyCondition, Status: metav1.ConditionTrue},
				}},
			}
			d := projection.SnapshotDetail(s)
			Expect(d.VMName).To(Equal("web"))
			Expect(d.Memory).To(BeTrue())
			Expect(d.Quiesce).To(BeTrue())
			Expect(d.Ready).To(Equal("True"))
			Expect(d.Description.Value).To(Equal("before upgrade"))
			Expect(d.Conditions).To(HaveLen(1))
		})
	})

	Context("conditions", func() {
		It("formats transition times as RFC 3339", func() {
			t := metav1.NewTime(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC))
			out := projection.Conditions([]metav1.Condition{{Type: "X", Status: "True", LastTransitionTime: t}})
			Expect(out[0].LastTransitionTime).To(Equal("2026-02-03T04:05:06Z"))
			Expect(projection.Conditions(nil)).To(BeNil())
		})
	})
})
