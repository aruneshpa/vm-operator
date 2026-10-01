// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package envtest starts a real kube-apiserver with the VM Operator CRDs for
// vmop-mcp integration tests, and creates authenticated users with
// namespaced RBAC. VM Operator's admission webhooks are not installed, so
// tests exercise CRD schema validation only.
package envtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
)

// RepoRoot returns the vm-operator repository root, derived from this file's
// location so that it works from any test package's working directory.
func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot determine the envtest helper's source location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// Env is a running test API server.
type Env struct {
	env *ctrlenvtest.Environment

	// Config is an administrator config.
	Config *rest.Config

	// Admin is an administrator client. It is not guarded.
	Admin ctrlclient.Client
}

// Start starts the API server with the VM Operator and Capabilities CRDs.
func Start() (*Env, error) {
	root := RepoRoot()
	e := &ctrlenvtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases"),
			filepath.Join(root, "config", "crd", "external-crds", "iaas.vmware.com_capabilities.yaml"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := e.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start envtest: %w", err)
	}
	s := kube.NewScheme()
	if err := rbacv1.AddToScheme(s); err != nil {
		return nil, err
	}
	admin, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: s})
	if err != nil {
		_ = e.Stop()
		return nil, err
	}
	return &Env{env: e, Config: cfg, Admin: admin}, nil
}

// Stop stops the API server.
func (e *Env) Stop() error {
	return e.env.Stop()
}

// CreateNamespace creates a namespace.
func (e *Env) CreateNamespace(ctx context.Context, name string) error {
	return e.Admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
}

// User is an authenticated test user.
type User struct {
	Name string

	// Config authenticates as the user.
	Config *rest.Config

	// KubeconfigPath is a kubeconfig file for the user whose context
	// namespace is the namespace the user was created for.
	KubeconfigPath string
}

// AddUser creates an authenticated user, binds it in namespace to a Role with
// rules, and writes a kubeconfig for it into dir.
func (e *Env) AddUser(ctx context.Context, name, namespace, dir string, rules []rbacv1.PolicyRule) (*User, error) {
	au, err := e.env.AddUser(ctrlenvtest.User{Name: name}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to add user %s: %w", name, err)
	}
	if len(rules) > 0 {
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Rules:      rules,
		}
		if err := e.Admin.Create(ctx, role); err != nil {
			return nil, err
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: name}},
		}
		if err := e.Admin.Create(ctx, rb); err != nil {
			return nil, err
		}
	}
	raw, err := au.KubeConfig()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".kubeconfig")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, err
	}
	if err := SetNamespace(path, namespace); err != nil {
		return nil, err
	}
	return &User{Name: name, Config: au.Config(), KubeconfigPath: path}, nil
}

// VMServiceReadRules grant read access to the VM Service kinds and events.
var VMServiceReadRules = []rbacv1.PolicyRule{
	{
		APIGroups: []string{kube.VMOperatorGroup},
		Resources: []string{"*"},
		Verbs:     []string{"get", "list", "watch"},
	},
	{
		APIGroups: []string{""},
		Resources: []string{"events", "persistentvolumeclaims", "resourcequotas"},
		Verbs:     []string{"get", "list", "watch"},
	},
}

// VMServiceEditRules grant read and write access to the VM Service kinds.
var VMServiceEditRules = append([]rbacv1.PolicyRule{
	{
		APIGroups: []string{kube.VMOperatorGroup},
		Resources: []string{"virtualmachines"},
		Verbs:     []string{"create", "update", "patch", "delete"},
	},
}, VMServiceReadRules...)
