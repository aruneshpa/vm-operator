// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contract_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
)

func schemaFor[T any]() error {
	_, err := jsonschema.For[T](nil)
	return err
}

var _ = Describe("Contract", Label(testlabels.MCP), func() {
	Context("NewUntrusted", func() {
		It("keeps short values intact", func() {
			u := contract.NewUntrusted("hello")
			Expect(u.Value).To(Equal("hello"))
			Expect(u.Truncated).To(BeFalse())
		})

		It("truncates long values on a rune boundary", func() {
			s := strings.Repeat("é", contract.UntrustedMaxBytes) // 2 bytes each
			u := contract.NewUntrusted(s)
			Expect(u.Truncated).To(BeTrue())
			Expect(len(u.Value)).To(BeNumerically("<=", contract.UntrustedMaxBytes))
			Expect(utf8.ValidString(u.Value)).To(BeTrue())
		})

		It("truncates multi-byte runes that straddle the limit", func() {
			s := "a" + strings.Repeat("€", contract.UntrustedMaxBytes) // 3 bytes each
			u := contract.NewUntrusted(s)
			Expect(u.Truncated).To(BeTrue())
			Expect(utf8.ValidString(u.Value)).To(BeTrue())
		})

		It("returns nil pointers for empty strings", func() {
			Expect(contract.NewUntrustedPtr("")).To(BeNil())
			Expect(contract.NewUntrustedPtr("x").Value).To(Equal("x"))
		})
	})

	Context("cursor", func() {
		It("round-trips", func() {
			s := contract.EncodeCursor("ns", "tok")
			c, err := contract.DecodeCursor(s)
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Phase).To(Equal("ns"))
			Expect(c.Token).To(Equal("tok"))
			Expect(s).ToNot(ContainSubstring("tok"))
		})

		It("decodes empty to zero", func() {
			c, err := contract.DecodeCursor("")
			Expect(err).ToNot(HaveOccurred())
			Expect(c).To(Equal(contract.Cursor{}))
		})

		DescribeTable("rejects malformed cursors",
			func(s string) {
				_, err := contract.DecodeCursor(s)
				Expect(err).To(HaveOccurred())
				Expect(contract.CodeOf(err)).To(Equal(contract.CodeInvalid))
			},
			Entry("not base64", "!!!"),
			Entry("not json", "bm90LWpzb24"),
			Entry("wrong version", "eyJ2IjoyfQ"),
		)
	})

	Context("ToolError", func() {
		It("renders as JSON", func() {
			err := contract.NewError(contract.CodeForbidden, "no", "hint")
			var te contract.ToolError
			Expect(json.Unmarshal([]byte(err.Error()), &te)).To(Succeed())
			Expect(te).To(Equal(contract.ToolError{Code: "forbidden", Message: "no", Hint: "hint"}))
		})

		It("reports the code of wrapped errors", func() {
			err := fmt.Errorf("wrapped: %w", contract.NewError(contract.CodeTimeout, "x", ""))
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeTimeout))
			Expect(contract.CodeOf(errors.New("plain"))).To(BeEmpty())
		})
	})

	It("ranks severities", func() {
		Expect(contract.SeverityRank(contract.SeverityBlocking)).To(BeNumerically("<", contract.SeverityRank(contract.SeverityWarning)))
		Expect(contract.SeverityRank(contract.SeverityWarning)).To(BeNumerically("<", contract.SeverityRank(contract.SeverityInfo)))
	})

	DescribeTable("infers a JSON schema for every DTO",
		func(f func() error) {
			Expect(f()).To(Succeed())
		},
		Entry("ObjectRef", schemaFor[contract.ObjectRef]),
		Entry("Condition", schemaFor[contract.Condition]),
		Entry("Untrusted", schemaFor[contract.Untrusted]),
		Entry("ListInput", schemaFor[contract.ListInput]),
		Entry("GetInput", schemaFor[contract.GetInput]),
		Entry("Page", schemaFor[contract.Page]),
		Entry("Finding", schemaFor[contract.Finding]),
		Entry("ToolError", schemaFor[contract.ToolError]),
		Entry("Cursor", schemaFor[contract.Cursor]),
		Entry("PowerState", schemaFor[contract.PowerState]),
		Entry("ImageRef", schemaFor[contract.ImageRef]),
		Entry("VirtualMachineSummary", schemaFor[contract.VirtualMachineSummary]),
		Entry("SecretRef", schemaFor[contract.SecretRef]),
		Entry("BootstrapSummary", schemaFor[contract.BootstrapSummary]),
		Entry("VolumeSummary", schemaFor[contract.VolumeSummary]),
		Entry("NetworkInterfaceSummary", schemaFor[contract.NetworkInterfaceSummary]),
		Entry("VirtualMachineDetail", schemaFor[contract.VirtualMachineDetail]),
		Entry("VirtualMachineList", schemaFor[contract.VirtualMachineList]),
		Entry("VirtualMachineClassSummary", schemaFor[contract.VirtualMachineClassSummary]),
		Entry("VirtualMachineClassList", schemaFor[contract.VirtualMachineClassList]),
		Entry("OSInfo", schemaFor[contract.OSInfo]),
		Entry("VirtualMachineImageSummary", schemaFor[contract.VirtualMachineImageSummary]),
		Entry("ImageDisk", schemaFor[contract.ImageDisk]),
		Entry("VirtualMachineImageDetail", schemaFor[contract.VirtualMachineImageDetail]),
		Entry("VirtualMachineImageList", schemaFor[contract.VirtualMachineImageList]),
		Entry("VirtualMachineSnapshotSummary", schemaFor[contract.VirtualMachineSnapshotSummary]),
		Entry("VirtualMachineSnapshotDetail", schemaFor[contract.VirtualMachineSnapshotDetail]),
		Entry("VirtualMachineSnapshotList", schemaFor[contract.VirtualMachineSnapshotList]),
		Entry("Event", schemaFor[contract.Event]),
		Entry("EventsInput", schemaFor[contract.EventsInput]),
		Entry("EventList", schemaFor[contract.EventList]),
		Entry("WhoAmI", schemaFor[contract.WhoAmI]),
		Entry("CheckAccessInput", schemaFor[contract.CheckAccessInput]),
		Entry("CheckAccess", schemaFor[contract.CheckAccess]),
		Entry("DiagnoseInput", schemaFor[contract.DiagnoseInput]),
		Entry("Diagnosis", schemaFor[contract.Diagnosis]),
		Entry("ExplainInput", schemaFor[contract.ExplainInput]),
		Entry("FieldChild", schemaFor[contract.FieldChild]),
		Entry("Explanation", schemaFor[contract.Explanation]),
		Entry("SetPowerStateInput", schemaFor[contract.SetPowerStateInput]),
		Entry("SetPowerStateOutput", schemaFor[contract.SetPowerStateOutput]),
		Entry("RestartInput", schemaFor[contract.RestartInput]),
		Entry("RestartOutput", schemaFor[contract.RestartOutput]),
		Entry("ConditionMatch", schemaFor[contract.ConditionMatch]),
		Entry("WaitInput", schemaFor[contract.WaitInput]),
		Entry("WaitOutput", schemaFor[contract.WaitOutput]),
		Entry("StorageClassesInput", schemaFor[contract.StorageClassesInput]),
		Entry("StorageClassQuota", schemaFor[contract.StorageClassQuota]),
		Entry("StorageClasses", schemaFor[contract.StorageClasses]),
		Entry("BootstrapRefInput", schemaFor[contract.BootstrapRefInput]),
		Entry("CreateVirtualMachineInput", schemaFor[contract.CreateVirtualMachineInput]),
		Entry("CreateVirtualMachineOutput", schemaFor[contract.CreateVirtualMachineOutput]),
	)
})
