// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package openapi_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/spec3"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/openapi"
)

const doc = `{
  "openapi": "3.0.0",
  "info": {"title": "test", "version": "v1alpha6"},
  "components": {"schemas": {
    "com.vmware.vmoperator.v1alpha6.VirtualMachine": {
      "type": "object",
      "description": "VirtualMachine is the schema for virtual machines.",
      "x-kubernetes-group-version-kind": [{"group": "vmoperator.vmware.com", "version": "v1alpha6", "kind": "VirtualMachine"}],
      "properties": {
        "metadata": {"allOf": [{"$ref": "#/components/schemas/io.k8s.ObjectMeta"}], "description": "Standard object metadata."},
        "spec": {"$ref": "#/components/schemas/Spec"},
        "status": {"type": "object", "description": "Observed state."}
      }
    },
    "com.vmware.vmoperator.v1alpha5.VirtualMachineClass": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group": "vmoperator.vmware.com", "version": "v1alpha5", "kind": "VirtualMachineClass"}]
    },
    "Spec": {
      "type": "object",
      "description": "Desired state.",
      "required": ["powerState"],
      "properties": {
        "powerState": {"type": "string", "description": "PowerState is the desired power state. It has more detail.", "enum": ["PoweredOn", "PoweredOff", "Suspended"]},
        "powerOffMode": {"type": "string", "enum": ["Hard", "Soft", "TrySoft"], "default": "TrySoft"},
        "volumes": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string", "description": "Volume name."}}}}
      }
    },
    "io.k8s.ObjectMeta": {"type": "object", "properties": {"name": {"type": "string"}}}
  }}
}`

func parse() *spec3.OpenAPI {
	d := &spec3.OpenAPI{}
	ExpectWithOffset(1, json.Unmarshal([]byte(doc), d)).To(Succeed())
	return d
}

func childNames(e contract.Explanation) []string {
	var out []string
	for _, c := range e.Children {
		out = append(out, c.Name)
	}
	return out
}

var _ = Describe("Explainer", Label(testlabels.MCP), func() {
	var (
		fetches int
		fetch   openapi.Fetcher
		ex      *openapi.Explainer
	)

	BeforeEach(func() {
		fetches = 0
		fetch = func() (*spec3.OpenAPI, error) {
			fetches++
			return parse(), nil
		}
	})

	JustBeforeEach(func() {
		ex = openapi.NewExplainer(fetch)
	})

	It("explains the kind root and lists its children", func() {
		e, err := ex.Explain("VirtualMachine", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.APIVersion).To(Equal("vmoperator.vmware.com/v1alpha6"))
		Expect(e.Type).To(Equal("object"))
		Expect(childNames(e)).To(Equal([]string{"metadata", "spec", "status"}))
	})

	It("follows $ref and reports enum, default, and required", func() {
		e, err := ex.Explain("VirtualMachine", "spec.powerOffMode")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Type).To(Equal("string"))
		Expect(e.Enum).To(Equal([]string{"Hard", "Soft", "TrySoft"}))
		Expect(e.Default).To(Equal("TrySoft"))
		Expect(e.Required).To(BeFalse())

		e, err = ex.Explain("VirtualMachine", "spec.powerState")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Required).To(BeTrue())
		Expect(e.Description).To(ContainSubstring("It has more detail."))
	})

	It("summarizes children with their first sentence", func() {
		e, err := ex.Explain("VirtualMachine", "spec")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Description).To(Equal("Desired state."))
		Expect(e.Children).To(ContainElement(contract.FieldChild{
			Name: "powerState", Type: "string", Description: "PowerState is the desired power state.",
		}))
		Expect(e.Children).To(ContainElement(contract.FieldChild{Name: "volumes", Type: "[]object"}))
	})

	It("descends into arrays of objects", func() {
		e, err := ex.Explain("VirtualMachine", "spec.volumes")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Type).To(Equal("[]object"))
		Expect(childNames(e)).To(Equal([]string{"name"}))

		e, err = ex.Explain("VirtualMachine", "spec.volumes.name")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Description).To(Equal("Volume name."))
	})

	It("unwraps allOf and keeps the wrapper's description", func() {
		e, err := ex.Explain("VirtualMachine", "metadata")
		Expect(err).ToNot(HaveOccurred())
		Expect(e.Description).To(Equal("Standard object metadata."))
		Expect(childNames(e)).To(Equal([]string{"name"}))
	})

	It("reports an unknown field as not_found", func() {
		_, err := ex.Explain("VirtualMachine", "spec.bogus")
		Expect(contract.CodeOf(err)).To(Equal(contract.CodeNotFound))
	})

	It("reports an unsupported kind as invalid without fetching", func() {
		_, err := ex.Explain("Pod", "")
		Expect(contract.CodeOf(err)).To(Equal(contract.CodeInvalid))
		Expect(fetches).To(BeZero())
	})

	It("reports a kind missing from this version as not_found", func() {
		_, err := ex.Explain("VirtualMachineClass", "")
		Expect(contract.CodeOf(err)).To(Equal(contract.CodeNotFound))
	})

	It("fetches the document once", func() {
		for range 3 {
			_, err := ex.Explain("VirtualMachine", "spec")
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(fetches).To(Equal(1))
	})

	When("the schema is forbidden", func() {
		BeforeEach(func() {
			fetch = func() (*spec3.OpenAPI, error) {
				fetches++
				return nil, apierrors.NewForbidden(schema.GroupResource{}, "", fmt.Errorf("no openapi for you"))
			}
		})

		It("reports schema_unavailable and retries on the next call", func() {
			_, err := ex.Explain("VirtualMachine", "")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeSchemaUnavailable))
			Expect(err.Error()).To(ContainSubstring("no openapi for you"))
			_, err = ex.Explain("VirtualMachine", "")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeSchemaUnavailable))
			Expect(fetches).To(Equal(2))
		})
	})

	When("the document has no components", func() {
		BeforeEach(func() {
			fetch = func() (*spec3.OpenAPI, error) { return &spec3.OpenAPI{}, nil }
		})

		It("reports schema_unavailable", func() {
			_, err := ex.Explain("VirtualMachine", "")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeSchemaUnavailable))
		})
	})
})
