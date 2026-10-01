// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/storage"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

func quota(name string, hard, used corev1.ResourceList) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: name},
		Spec:       corev1.ResourceQuotaSpec{Hard: hard},
		Status:     corev1.ResourceQuotaStatus{Hard: hard, Used: used},
	}
}

var _ = Describe("list_storage_classes", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		opts = fakeenv.Options{}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, storage.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	When("quotas assign storage classes", func() {
		BeforeEach(func() {
			opts.Objects = []ctrlclient.Object{
				quota("q1",
					corev1.ResourceList{
						"zeta.storageclass.storage.k8s.io/requests.storage": resource.MustParse("100Gi"),
						corev1.ResourceRequestsCPU:                          resource.MustParse("4"),
					},
					corev1.ResourceList{
						"zeta.storageclass.storage.k8s.io/requests.storage": resource.MustParse("10Gi"),
					}),
				quota("q2",
					corev1.ResourceList{
						"alpha.storageclass.storage.k8s.io/requests.storage":       resource.MustParse("1Ti"),
						"alpha.storageclass.storage.k8s.io/persistentvolumeclaims": resource.MustParse("10"),
						"something-else.example.com/requests.storage":              resource.MustParse("1Gi"),
					},
					nil),
			}
		})

		It("lists them sorted with limits and usage, ignoring other keys", func() {
			res, err := fakeenv.Call[contract.StorageClasses](ctx, cs, "list_storage_classes", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).To(BeNil())
			Expect(res.Out.StorageClasses).To(Equal([]contract.StorageClassQuota{
				{Name: "alpha", QuotaLimit: "1Ti"},
				{Name: "zeta", QuotaLimit: "100Gi", QuotaUsed: "10Gi"},
			}))
			Expect(res.Text).To(ContainSubstring("2 storage classes"))
		})
	})

	It("returns an empty list when there are no quotas", func() {
		res, err := fakeenv.Call[contract.StorageClasses](ctx, cs, "list_storage_classes", map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.StorageClasses).To(BeEmpty())
	})

	It("is a read-tier tool", func() {
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Tools).To(HaveLen(1))
		Expect(res.Tools[0].Annotations.ReadOnlyHint).To(BeTrue())
	})
})
