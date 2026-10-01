// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/identity"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

const extraSentinel = "SENTINEL-EXTRA-VALUE"

var _ = Describe("Identity tools", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession

		ssrErr  error
		ssarReq *authorizationv1.SelfSubjectAccessReview
	)

	BeforeEach(func() {
		ctx = context.Background()
		opts = fakeenv.Options{}
		ssrErr = nil
		ssarReq = nil
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		env.Kube.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			if ssrErr != nil {
				return true, nil, ssrErr
			}
			return true, &authenticationv1.SelfSubjectReview{
				Status: authenticationv1.SelfSubjectReviewStatus{
					UserInfo: authenticationv1.UserInfo{
						Username: "sso:devops@vsphere.local",
						Groups:   []string{"system:authenticated", "devops"},
						Extra: map[string]authenticationv1.ExtraValue{
							"session": {extraSentinel},
							"aud":     {extraSentinel},
						},
					},
				},
			}, nil
		})
		env.Kube.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			req := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview).DeepCopy()
			ssarReq = req
			req.Status = authorizationv1.SubjectAccessReviewStatus{
				Allowed: req.Spec.ResourceAttributes.Verb == "get",
				Reason:  "fake policy",
			}
			return true, req, nil
		})
		var err error
		cs, _, err = env.ConnectTools(ctx, identity.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	whoami := func() fakeenv.Result[contract.WhoAmI] {
		res, err := fakeenv.Call[contract.WhoAmI](ctx, cs, "whoami", map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		return res
	}

	Context("whoami", func() {
		It("reports the identity from SelfSubjectReview with extra keys only", func() {
			res := whoami()
			w := res.Out
			Expect(w.User).To(Equal("sso:devops@vsphere.local"))
			Expect(w.Groups).To(Equal([]string{"system:authenticated", "devops"}))
			Expect(w.ExtraKeys).To(Equal([]string{"aud", "session"}))
			Expect(w.IdentitySource).To(Equal(identity.SourceSelfSubjectReview))
			Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(extraSentinel))
			Expect(res.Text).ToNot(ContainSubstring(extraSentinel))
			Expect(res.Text).To(ContainSubstring("user=sso:devops@vsphere.local"))
		})

		It("reports the context, versions, tiers, and capabilities", func() {
			w := whoami().Out
			Expect(w.Context).To(Equal("fake-context"))
			Expect(w.Namespace).To(Equal(fakeenv.DefaultNamespace))
			Expect(w.Server).To(Equal("https://fake.example.com"))
			Expect(w.ServedVMServiceVersions).To(Equal([]string{contract.BuiltForAPIVersion}))
			Expect(w.BuiltForVersion).To(Equal(contract.BuiltForAPIVersion))
			Expect(w.ContractVersion).To(Equal(contract.Version))
			Expect(w.ServerVersion).To(Equal(buildinfo.Version))
			Expect(w.Tiers).To(Equal([]string{toolkit.TierRead}))
			Expect(w.Capabilities).To(HaveKeyWithValue(toolkit.CapabilityVMSnapshots, toolkit.CapabilityActive))
			Expect(w.NamespaceAllowList).To(BeEmpty())
		})

		When("the write tier and an allow-list are enabled", func() {
			BeforeEach(func() {
				opts.EnableWrite = true
				opts.Namespaces = []string{"prod", fakeenv.DefaultNamespace}
			})

			It("reports them", func() {
				w := whoami().Out
				Expect(w.Tiers).To(Equal([]string{toolkit.TierRead, toolkit.TierWrite}))
				Expect(w.NamespaceAllowList).To(Equal([]string{fakeenv.DefaultNamespace, "prod"}))
			})
		})

		When("SelfSubjectReview is not available", func() {
			BeforeEach(func() { ssrErr = errors.New("the server could not find the requested resource") })

			It("falls back to the kubeconfig user", func() {
				w := whoami().Out
				Expect(w.User).To(Equal("fake-user"))
				Expect(w.IdentitySource).To(Equal(identity.SourceKubeconfig))
				Expect(w.Groups).To(BeEmpty())
			})
		})
	})

	Context("check_access", func() {
		check := func(args map[string]any) fakeenv.Result[contract.CheckAccess] {
			res, err := fakeenv.Call[contract.CheckAccess](ctx, cs, "check_access", args)
			Expect(err).ToNot(HaveOccurred())
			return res
		}

		It("defaults to the VM Operator group and the default namespace", func() {
			res := check(map[string]any{"verb": "get", "resource": "virtualmachines", "name": "web-01"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Allowed).To(BeTrue())
			Expect(res.Out.Reason).To(Equal("fake policy"))
			attrs := ssarReq.Spec.ResourceAttributes
			Expect(attrs.Group).To(Equal("vmoperator.vmware.com"))
			Expect(attrs.Namespace).To(Equal(fakeenv.DefaultNamespace))
			Expect(attrs.Resource).To(Equal("virtualmachines"))
			Expect(attrs.Name).To(Equal("web-01"))
		})

		It("uses the core group when asked", func() {
			res := check(map[string]any{"verb": "delete", "resource": "events", "coreGroup": true, "namespace": "prod"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Allowed).To(BeFalse())
			attrs := ssarReq.Spec.ResourceAttributes
			Expect(attrs.Group).To(BeEmpty())
			Expect(attrs.Namespace).To(Equal("prod"))
		})

		It("passes an explicit group and subresource", func() {
			check(map[string]any{"verb": "get", "resource": "virtualmachinereplicasets", "subresource": "scale", "group": "example.com"})
			Expect(ssarReq.Spec.ResourceAttributes.Group).To(Equal("example.com"))
			Expect(ssarReq.Spec.ResourceAttributes.Subresource).To(Equal("scale"))
		})

		When("the namespace is not allowed", func() {
			BeforeEach(func() { opts.Namespaces = []string{fakeenv.DefaultNamespace} })

			It("refuses without asking the Supervisor", func() {
				res := check(map[string]any{"verb": "get", "resource": "virtualmachines", "namespace": "prod"})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))
				Expect(ssarReq).To(BeNil())
			})
		})
	})
})
