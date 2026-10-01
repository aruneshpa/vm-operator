// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package identity implements the whoami and check_access tools.
package identity

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// Identity sources.
const (
	SourceSelfSubjectReview = "SelfSubjectReview"
	SourceKubeconfig        = "kubeconfig"
)

// Register registers the identity tools.
func Register(r *toolkit.Registrar) {
	env := r.Env

	toolkit.AddRead(r, toolkit.Spec[contract.WhoAmI]{
		Name:  "whoami",
		Title: "Who am I",
		Description: "Shows the Supervisor identity the server acts as, the kubeconfig context and default namespace, " +
			"the served VM Service API versions, the contract version, the enabled tiers, and detected capabilities.",
		Summary: func(w contract.WhoAmI) string {
			return fmt.Sprintf("user=%s groups=%s namespace=%s server=%s tiers=%s contract=%s servedVersions=%s",
				w.User, strings.Join(w.Groups, ","), w.Namespace, w.Server,
				strings.Join(w.Tiers, ","), w.ContractVersion, strings.Join(w.ServedVMServiceVersions, ","))
		},
	}, func(ctx context.Context, _ struct{}) (contract.WhoAmI, error) {
		return whoami(ctx, env)
	})

	toolkit.AddRead(r, toolkit.Spec[contract.CheckAccess]{
		Name:  "check_access",
		Title: "Check access",
		Description: "Asks the Supervisor whether the current user may perform a verb on a resource, " +
			"using a SelfSubjectAccessReview. Resources default to the vmoperator.vmware.com group.",
		Summary: func(a contract.CheckAccess) string {
			return fmt.Sprintf("allowed=%t %s", a.Allowed, a.Reason)
		},
	}, func(ctx context.Context, in contract.CheckAccessInput) (contract.CheckAccess, error) {
		return checkAccess(ctx, env, in)
	})
}

func whoami(ctx context.Context, env *toolkit.Env) (contract.WhoAmI, error) {
	info := env.Provider.Info()
	w := contract.WhoAmI{
		Context:                 info.Context,
		Namespace:               env.Namespaces.Default,
		Server:                  info.Server,
		ServedVMServiceVersions: env.ServedVersions,
		BuiltForVersion:         contract.BuiltForAPIVersion,
		ContractVersion:         contract.Version,
		ServerVersion:           buildinfo.Version,
		Tiers:                   env.Tiers(),
		Capabilities:            env.Capabilities,
		NamespaceAllowList:      env.Namespaces.Allowed(),
	}
	if w.Capabilities == nil {
		w.Capabilities = map[string]string{}
	}

	ssr, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*authenticationv1.SelfSubjectReview, error) {
		return c.Kube.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	})
	if err != nil || ssr.Status.UserInfo.Username == "" {
		// Older API servers may not serve SelfSubjectReview. Fall back to the
		// kubeconfig user name, which is a label rather than a verified
		// identity.
		w.User = info.KubeconfigUser
		w.IdentitySource = SourceKubeconfig
		return w, nil //nolint:nilerr // The fallback is the intended result of a failed review.
	}
	ui := ssr.Status.UserInfo
	w.User = ui.Username
	w.Groups = ui.Groups
	w.IdentitySource = SourceSelfSubjectReview
	for k := range ui.Extra {
		w.ExtraKeys = append(w.ExtraKeys, k)
	}
	sort.Strings(w.ExtraKeys)
	return w, nil
}

func checkAccess(ctx context.Context, env *toolkit.Env, in contract.CheckAccessInput) (contract.CheckAccess, error) {
	group := in.Group
	if group == "" && !in.CoreGroup {
		group = kube.VMOperatorGroup
	}
	if in.CoreGroup {
		group = ""
	}
	attrs := &authorizationv1.ResourceAttributes{
		Verb:        in.Verb,
		Group:       group,
		Resource:    in.Resource,
		Subresource: in.Subresource,
		Name:        in.Name,
	}
	// VM Service resources are namespaced, so the review always carries the
	// resolved namespace, which also enforces the allow-list.
	ns, err := env.Namespaces.Resolve(in.Namespace)
	if err != nil {
		return contract.CheckAccess{}, err
	}
	attrs.Namespace = ns

	review, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*authorizationv1.SelfSubjectAccessReview, error) {
		return c.Kube.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attrs},
		}, metav1.CreateOptions{})
	})
	if err != nil {
		return contract.CheckAccess{}, err
	}
	return contract.CheckAccess{
		Allowed: review.Status.Allowed,
		Denied:  review.Status.Denied,
		Reason:  review.Status.Reason,
	}, nil
}
