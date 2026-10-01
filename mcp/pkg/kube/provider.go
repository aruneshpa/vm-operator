// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo"
)

// Clients are the clients available to tools. Client is always guarded.
type Clients struct {
	// Client is a guarded controller-runtime client.
	Client ctrlclient.Client

	// Kube is a typed clientset, used for identity reviews, discovery, and
	// OpenAPI. It is never used for Secrets or ConfigMaps.
	Kube kubernetes.Interface
}

// Info describes the kubeconfig context the server is using.
type Info struct {
	Context        string
	Namespace      string
	Server         string
	KubeconfigUser string
}

// Provider supplies the current clients.
type Provider interface {
	// Get returns the current clients, rebuilding them first if the
	// credentials have changed.
	Get(ctx context.Context) (*Clients, error)

	// Invalidate forces the next Get to rebuild the clients.
	Invalidate()

	// Info returns the kubeconfig context information.
	Info() Info
}

// Do calls fn with the current clients. If fn fails because the credentials
// were rejected, the clients are rebuilt and fn is retried exactly once.
func Do[T any](ctx context.Context, p Provider, fn func(*Clients) (T, error)) (T, error) {
	c, err := p.Get(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	out, err := fn(c)
	if err == nil || !apierrors.IsUnauthorized(err) {
		return out, err
	}
	p.Invalidate()
	if c, err = p.Get(ctx); err != nil {
		var zero T
		return zero, err
	}
	return fn(c)
}

// NewStaticProvider returns a provider that always returns c. The client in c
// is wrapped with the guard. It is used by tests and by callers that already
// hold a rest.Config.
func NewStaticProvider(c *Clients, info Info) Provider {
	return &staticProvider{
		clients: &Clients{Client: NewGuardedClient(c.Client), Kube: c.Kube},
		info:    info,
	}
}

type staticProvider struct {
	clients *Clients
	info    Info
}

func (s *staticProvider) Get(context.Context) (*Clients, error) { return s.clients, nil }
func (s *staticProvider) Invalidate()                           {}
func (s *staticProvider) Info() Info                            { return s.info }

// KubeconfigOptions configures a kubeconfig-backed provider.
type KubeconfigOptions struct {
	// Path is an explicit kubeconfig path. If empty, the standard loading
	// rules ($KUBECONFIG, then ~/.kube/config) apply.
	Path string

	// Context overrides the kubeconfig current-context.
	Context string

	// Warnings receives API server warnings. Defaults to os.Stderr. Stdout is
	// never used because it is the MCP stdio channel.
	Warnings io.Writer
}

// NewKubeconfigProvider returns a provider that builds clients from the
// user's kubeconfig and rebuilds them when the kubeconfig file changes or
// after the API server rejects the credentials.
func NewKubeconfigProvider(opts KubeconfigOptions) (Provider, error) {
	if opts.Warnings == nil {
		opts.Warnings = os.Stderr
	}
	p := &kubeconfigProvider{opts: opts}
	if _, err := p.Get(context.Background()); err != nil {
		return nil, err
	}
	return p, nil
}

type kubeconfigProvider struct {
	opts KubeconfigOptions

	mu        sync.Mutex
	signature string
	clients   *Clients
	info      Info
}

func (p *kubeconfigProvider) rules() *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if p.opts.Path != "" {
		rules.ExplicitPath = p.opts.Path
	}
	return rules
}

// fileSignature returns a string that changes whenever any kubeconfig file in
// the loading precedence changes.
func fileSignature(rules *clientcmd.ClientConfigLoadingRules) string {
	paths := rules.Precedence
	if rules.ExplicitPath != "" {
		paths = []string{rules.ExplicitPath}
	}
	var b strings.Builder
	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&b, "%s:missing;", path)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", path, fi.ModTime().UnixNano(), fi.Size())
	}
	return b.String()
}

func (p *kubeconfigProvider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signature = ""
}

func (p *kubeconfigProvider) Info() Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

func (p *kubeconfigProvider) Get(context.Context) (*Clients, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rules := p.rules()
	sig := fileSignature(rules)
	if p.clients != nil && sig == p.signature {
		return p.clients, nil
	}

	clients, info, err := build(rules, p.opts)
	if err != nil {
		return nil, err
	}
	p.clients, p.info, p.signature = clients, info, sig
	return clients, nil
}

func build(rules *clientcmd.ClientConfigLoadingRules, opts KubeconfigOptions) (*Clients, Info, error) {
	overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.Context}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	raw, err := cc.RawConfig()
	if err != nil {
		return nil, Info{}, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	ctxName := opts.Context
	if ctxName == "" {
		ctxName = raw.CurrentContext
	}
	kctx, ok := raw.Contexts[ctxName]
	if !ok {
		return nil, Info{}, fmt.Errorf("kubeconfig context %q not found", ctxName)
	}
	if ai, ok := raw.AuthInfos[kctx.AuthInfo]; ok && ai.Exec != nil &&
		ai.Exec.InteractiveMode == clientcmdapi.AlwaysExecInteractiveMode {
		return nil, Info{}, fmt.Errorf(
			"kubeconfig user %q uses an exec credential plugin with interactiveMode Always; "+
				"vmop-mcp uses stdin for the MCP protocol and cannot run interactive plugins",
			kctx.AuthInfo)
	}

	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, Info{}, fmt.Errorf("failed to build client config: %w", err)
	}
	ns, _, err := cc.Namespace()
	if err != nil {
		return nil, Info{}, fmt.Errorf("failed to resolve namespace: %w", err)
	}

	clients, err := NewClients(cfg, opts.Warnings)
	if err != nil {
		return nil, Info{}, err
	}
	return clients, Info{
		Context:        ctxName,
		Namespace:      ns,
		Server:         cfg.Host,
		KubeconfigUser: kctx.AuthInfo,
	}, nil
}

// NewClients returns guarded clients for cfg. API server warnings are written
// to warnings.
func NewClients(cfg *rest.Config, warnings io.Writer) (*Clients, error) {
	cfg = rest.CopyConfig(cfg)
	cfg.UserAgent = "vmop-mcp/" + buildinfo.Version
	if warnings == nil {
		warnings = os.Stderr
	}
	cfg.WarningHandler = rest.NewWarningWriter(warnings, rest.WarningWriterOptions{Deduplicate: true})

	c, err := ctrlclient.New(cfg, ctrlclient.Options{
		Scheme: NewScheme(),
		Mapper: NewRESTMapper(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}
	return &Clients{Client: NewGuardedClient(c), Kube: kc}, nil
}
