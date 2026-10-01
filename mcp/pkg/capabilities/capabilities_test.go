// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package capabilities_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	capv1 "github.com/vmware-tanzu/vm-operator/external/capabilities/api/v1alpha1"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/capabilities"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

func caps(status map[capv1.CapabilityName]capv1.CapabilityStatus) *capv1.Capabilities {
	return &capv1.Capabilities{
		ObjectMeta: metav1.ObjectMeta{Name: capabilities.SupervisorCapabilitiesName},
		Status:     capv1.CapabilitiesStatus{Supervisor: status},
	}
}

var _ = Describe("Capabilities", Label(testlabels.MCP), func() {
	ctx := context.Background()

	detect := func(funcs interceptor.Funcs, objs ...ctrlclient.Object) string {
		c := fake.NewClientBuilder().WithScheme(kube.NewScheme()).WithObjects(objs...).WithInterceptorFuncs(funcs).Build()
		return capabilities.Detect(ctx, c)[toolkit.CapabilityVMSnapshots]
	}

	It("is unknown when the object does not exist", func() {
		Expect(detect(interceptor.Funcs{})).To(Equal(toolkit.CapabilityUnknown))
	})

	It("is active when activated", func() {
		Expect(detect(interceptor.Funcs{}, caps(map[capv1.CapabilityName]capv1.CapabilityStatus{
			capabilities.KeyVMSnapshots: {Activated: true},
		}))).To(Equal(toolkit.CapabilityActive))
	})

	It("is inactive when not activated", func() {
		Expect(detect(interceptor.Funcs{}, caps(map[capv1.CapabilityName]capv1.CapabilityStatus{
			capabilities.KeyVMSnapshots: {Activated: false},
		}))).To(Equal(toolkit.CapabilityInactive))
	})

	It("is inactive when the key is missing", func() {
		Expect(detect(interceptor.Funcs{}, caps(map[capv1.CapabilityName]capv1.CapabilityStatus{
			"other": {Activated: true},
		}))).To(Equal(toolkit.CapabilityInactive))
	})

	It("is unknown when the object cannot be read", func() {
		funcs := interceptor.Funcs{
			Get: func(context.Context, ctrlclient.WithWatch, ctrlclient.ObjectKey, ctrlclient.Object, ...ctrlclient.GetOption) error {
				return errors.New("forbidden")
			},
		}
		Expect(detect(funcs, caps(map[capv1.CapabilityName]capv1.CapabilityStatus{
			capabilities.KeyVMSnapshots: {Activated: true},
		}))).To(Equal(toolkit.CapabilityUnknown))
	})
})
