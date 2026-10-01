// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	capv1 "github.com/vmware-tanzu/vm-operator/external/capabilities/api/v1alpha1"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
)

type fakeProvider struct {
	gets        int
	invalidates int
}

func (f *fakeProvider) Get(context.Context) (*kube.Clients, error) {
	f.gets++
	return &kube.Clients{}, nil
}
func (f *fakeProvider) Invalidate()     { f.invalidates++ }
func (f *fakeProvider) Info() kube.Info { return kube.Info{} }

const kubeconfigTemplate = `apiVersion: v1
kind: Config
current-context: ctx-a
clusters:
- name: c
  cluster:
    server: https://supervisor.example.com:6443
contexts:
- name: ctx-a
  context:
    cluster: c
    user: u
    namespace: NAMESPACE
- name: ctx-b
  context:
    cluster: c
    user: u
    namespace: other
users:
- name: u
  user:
    token: abc
`

const interactiveKubeconfig = `apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: c
  cluster:
    server: https://supervisor.example.com:6443
contexts:
- name: ctx
  context:
    cluster: c
    user: u
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /bin/true
      interactiveMode: Always
`

var _ = Describe("Kube", Label(testlabels.MCP), func() {
	ctx := context.Background()

	Context("IsForbiddenGVK", func() {
		DescribeTable("classifies kinds",
			func(gvk schema.GroupVersionKind, forbidden bool) {
				Expect(kube.IsForbiddenGVK(gvk)).To(Equal(forbidden))
			},
			Entry("Secret", corev1.SchemeGroupVersion.WithKind("Secret"), true),
			Entry("SecretList", corev1.SchemeGroupVersion.WithKind("SecretList"), true),
			Entry("ConfigMap", corev1.SchemeGroupVersion.WithKind("ConfigMap"), true),
			Entry("ConfigMapList", corev1.SchemeGroupVersion.WithKind("ConfigMapList"), true),
			Entry("Event", corev1.SchemeGroupVersion.WithKind("Event"), false),
			Entry("Secret in another group", schema.GroupVersionKind{Group: "x.example.com", Version: "v1", Kind: "Secret"}, false),
			Entry("VirtualMachine", vmopv1.GroupVersion.WithKind("VirtualMachine"), false),
		)
	})

	Context("guarded client", func() {
		var (
			trap  *kube.SecretTrap
			inner ctrlclient.WithWatch
			c     ctrlclient.Client
		)

		BeforeEach(func() {
			trap = kube.NewSecretTrap()
			inner = fake.NewClientBuilder().
				WithScheme(kube.NewScheme()).
				WithObjects(
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s"}},
					&vmopv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "vm"}},
				).
				WithStatusSubresource(&vmopv1.VirtualMachine{}).
				WithInterceptorFuncs(trap.Funcs()).
				Build()
			c = kube.NewGuardedClient(inner)
		})

		AfterEach(func() {
			Expect(trap.Violations()).To(BeEmpty())
		})

		key := ctrlclient.ObjectKey{Namespace: "ns", Name: "s"}
		secret := func() *corev1.Secret {
			return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "s"}}
		}
		cm := func() *corev1.ConfigMap {
			return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"}}
		}

		DescribeTable("refuses Secrets and ConfigMaps",
			func(call func() error) {
				Expect(call()).To(MatchError(kube.ErrForbiddenKind))
			},
			Entry("get secret", func() error { return c.Get(ctx, key, secret()) }),
			Entry("get configmap", func() error { return c.Get(ctx, key, cm()) }),
			Entry("list secrets", func() error { return c.List(ctx, &corev1.SecretList{}) }),
			Entry("list configmaps", func() error { return c.List(ctx, &corev1.ConfigMapList{}) }),
			Entry("create", func() error { return c.Create(ctx, secret()) }),
			Entry("update", func() error { return c.Update(ctx, secret()) }),
			Entry("patch", func() error { return c.Patch(ctx, secret(), ctrlclient.MergeFrom(secret())) }),
			Entry("delete", func() error { return c.Delete(ctx, secret()) }),
			Entry("delete all of", func() error { return c.DeleteAllOf(ctx, cm(), ctrlclient.InNamespace("ns")) }),
			Entry("status update", func() error { return c.Status().Update(ctx, secret()) }),
			Entry("status patch", func() error { return c.Status().Patch(ctx, secret(), ctrlclient.MergeFrom(secret())) }),
			Entry("subresource get", func() error { return c.SubResource("x").Get(ctx, secret(), &corev1.Secret{}) }),
			Entry("subresource create", func() error { return c.SubResource("x").Create(ctx, secret(), &corev1.Secret{}) }),
			Entry("subresource update", func() error { return c.SubResource("x").Update(ctx, secret()) }),
		)

		It("refuses unstructured Secrets by GVK", func() {
			u := &metav1.PartialObjectMetadata{}
			u.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
			Expect(c.Get(ctx, key, u)).To(MatchError(kube.ErrForbiddenKind))
		})

		It("always refuses server-side apply", func() {
			Expect(c.Apply(ctx, nil)).To(HaveOccurred())
			Expect(c.Status().Apply(ctx, nil)).To(HaveOccurred())
		})

		It("passes VirtualMachine requests through", func() {
			vm := &vmopv1.VirtualMachine{}
			Expect(c.Get(ctx, ctrlclient.ObjectKey{Namespace: "ns", Name: "vm"}, vm)).To(Succeed())
			Expect(c.List(ctx, &vmopv1.VirtualMachineList{})).To(Succeed())
			base := vm.DeepCopy()
			vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
			Expect(c.Patch(ctx, vm, ctrlclient.MergeFrom(base))).To(Succeed())
			Expect(c.Status().Update(ctx, vm)).To(Succeed())
		})
	})

	Context("SecretTrap", func() {
		It("records requests that bypass the guard", func() {
			trap := kube.NewSecretTrap()
			raw := fake.NewClientBuilder().WithScheme(kube.NewScheme()).WithInterceptorFuncs(trap.Funcs()).Build()
			_ = raw.Get(ctx, ctrlclient.ObjectKey{Namespace: "ns", Name: "s"}, &corev1.Secret{})
			_ = raw.List(ctx, &corev1.ConfigMapList{})
			Expect(raw.List(ctx, &vmopv1.VirtualMachineList{})).To(Succeed())
			Expect(trap.Violations()).To(ConsistOf("get Secret", "list ConfigMapList"))
		})
	})

	Context("served versions", func() {
		discovery := func(groups ...metav1.APIGroup) *fakediscovery.FakeDiscovery {
			fd := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
			var resources []*metav1.APIResourceList
			for _, g := range groups {
				for _, v := range g.Versions {
					resources = append(resources, &metav1.APIResourceList{GroupVersion: v.GroupVersion})
				}
			}
			fd.Resources = resources
			return fd
		}

		It("lists VM Operator versions sorted", func() {
			fd := discovery(
				metav1.APIGroup{Name: kube.VMOperatorGroup, Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: kube.VMOperatorGroup + "/v1alpha6", Version: "v1alpha6"},
					{GroupVersion: kube.VMOperatorGroup + "/v1alpha5", Version: "v1alpha5"},
				}},
				metav1.APIGroup{Name: "other.example.com", Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "other.example.com/v1", Version: "v1"},
				}},
			)
			versions, err := kube.ServedVMOperatorVersions(fd)
			Expect(err).ToNot(HaveOccurred())
			Expect(versions).To(Equal([]string{"v1alpha5", "v1alpha6"}))
			Expect(kube.CheckServed(versions)).To(Succeed())
		})

		It("fails when the built-for version is not served", func() {
			err := kube.CheckServed([]string{"v1alpha4", "v1alpha5"})
			Expect(err).To(MatchError(ContainSubstring("v1alpha4, v1alpha5")))
			Expect(kube.CheckServed(nil)).To(MatchError(ContainSubstring("none")))
		})
	})

	Context("Do", func() {
		It("retries exactly once after a 401", func() {
			p := &fakeProvider{}
			calls := 0
			_, err := kube.Do(ctx, p, func(*kube.Clients) (int, error) {
				calls++
				return 0, apierrors.NewUnauthorized("expired")
			})
			Expect(apierrors.IsUnauthorized(err)).To(BeTrue())
			Expect(calls).To(Equal(2))
			Expect(p.invalidates).To(Equal(1))
			Expect(p.gets).To(Equal(2))
		})

		It("succeeds on retry", func() {
			p := &fakeProvider{}
			calls := 0
			v, err := kube.Do(ctx, p, func(*kube.Clients) (int, error) {
				calls++
				if calls == 1 {
					return 0, apierrors.NewUnauthorized("expired")
				}
				return 42, nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(v).To(Equal(42))
		})

		It("does not retry other errors", func() {
			p := &fakeProvider{}
			calls := 0
			_, err := kube.Do(ctx, p, func(*kube.Clients) (int, error) {
				calls++
				return 0, errors.New("boom")
			})
			Expect(err).To(HaveOccurred())
			Expect(calls).To(Equal(1))
			Expect(p.invalidates).To(BeZero())
		})
	})

	Context("kubeconfig provider", func() {
		var path string

		write := func(ns string, mtime time.Time) {
			content := []byte(strings.ReplaceAll(kubeconfigTemplate, "NAMESPACE", ns))
			Expect(os.WriteFile(path, content, 0o600)).To(Succeed())
			Expect(os.Chtimes(path, mtime, mtime)).To(Succeed())
		}

		BeforeEach(func() {
			path = filepath.Join(GinkgoT().TempDir(), "kubeconfig")
			write("dev", time.Now().Add(-time.Hour))
		})

		It("reflects the context", func() {
			p, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path})
			Expect(err).ToNot(HaveOccurred())
			Expect(p.Info()).To(Equal(kube.Info{
				Context:        "ctx-a",
				Namespace:      "dev",
				Server:         "https://supervisor.example.com:6443",
				KubeconfigUser: "u",
			}))
		})

		It("honors a context override", func() {
			p, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path, Context: "ctx-b"})
			Expect(err).ToNot(HaveOccurred())
			Expect(p.Info().Namespace).To(Equal("other"))
		})

		It("fails for an unknown context", func() {
			_, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path, Context: "nope"})
			Expect(err).To(HaveOccurred())
		})

		It("reuses clients while the file is unchanged and rebuilds when it changes", func() {
			p, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path})
			Expect(err).ToNot(HaveOccurred())
			c1, err := p.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			c2, err := p.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(c2).To(BeIdenticalTo(c1))

			write("staging", time.Now())
			c3, err := p.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(c3).ToNot(BeIdenticalTo(c1))
			Expect(p.Info().Namespace).To(Equal("staging"))
		})

		It("rebuilds after Invalidate", func() {
			p, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path})
			Expect(err).ToNot(HaveOccurred())
			c1, _ := p.Get(ctx)
			p.Invalidate()
			c2, err := p.Get(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(c2).ToNot(BeIdenticalTo(c1))
		})

		It("rejects interactive exec plugins", func() {
			Expect(os.WriteFile(path, []byte(interactiveKubeconfig), 0o600)).To(Succeed())
			_, err := kube.NewKubeconfigProvider(kube.KubeconfigOptions{Path: path})
			Expect(err).To(MatchError(ContainSubstring("stdin")))
		})
	})

	Context("REST mapper", func() {
		m := kube.NewRESTMapper()

		DescribeTable("resolves known kinds",
			func(gvk schema.GroupVersionKind, resource string, scope meta.RESTScopeName) {
				mapping, err := m.RESTMapping(gvk.GroupKind(), gvk.Version)
				Expect(err).ToNot(HaveOccurred())
				Expect(mapping.Resource.Resource).To(Equal(resource))
				Expect(mapping.Scope.Name()).To(Equal(scope))
			},
			Entry("VM", vmopv1.GroupVersion.WithKind("VirtualMachine"), "virtualmachines", meta.RESTScopeNameNamespace),
			Entry("class", vmopv1.GroupVersion.WithKind("VirtualMachineClass"), "virtualmachineclasses", meta.RESTScopeNameNamespace),
			Entry("image", vmopv1.GroupVersion.WithKind("VirtualMachineImage"), "virtualmachineimages", meta.RESTScopeNameNamespace),
			Entry("cluster image", vmopv1.GroupVersion.WithKind("ClusterVirtualMachineImage"), "clustervirtualmachineimages", meta.RESTScopeNameRoot),
			Entry("snapshot", vmopv1.GroupVersion.WithKind("VirtualMachineSnapshot"), "virtualmachinesnapshots", meta.RESTScopeNameNamespace),
			Entry("event", corev1.SchemeGroupVersion.WithKind("Event"), "events", meta.RESTScopeNameNamespace),
			Entry("pvc", corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"), "persistentvolumeclaims", meta.RESTScopeNameNamespace),
			Entry("quota", corev1.SchemeGroupVersion.WithKind("ResourceQuota"), "resourcequotas", meta.RESTScopeNameNamespace),
			Entry("capabilities", capv1.GroupVersion.WithKind("Capabilities"), "capabilities", meta.RESTScopeNameRoot),
		)

		It("does not resolve Secrets or ConfigMaps", func() {
			_, err := m.RESTMapping(schema.GroupKind{Kind: "Secret"}, "v1")
			Expect(err).To(HaveOccurred())
			_, err = m.RESTMapping(schema.GroupKind{Kind: "ConfigMap"}, "v1")
			Expect(err).To(HaveOccurred())
		})
	})
})
