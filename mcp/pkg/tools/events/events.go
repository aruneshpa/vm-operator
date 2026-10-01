// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package events implements the get_events tool.
package events

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
)

// Limits.
const (
	DefaultLimit        = 20
	MaxLimit            = 100
	DefaultSinceMinutes = 60
)

// Field selector keys for events. The fake client in tests must index these.
const (
	FieldInvolvedObjectKind = "involvedObject.kind"
	FieldInvolvedObjectName = "involvedObject.name"
)

// Register registers get_events.
func Register(r *toolkit.Registrar) {
	env := r.Env
	toolkit.AddRead(r, toolkit.Spec[contract.EventList]{
		Name:        "get_events",
		Title:       "Get events",
		Description: "Gets recent Kubernetes events for a named object in a namespace, newest first.",
		Summary: func(l contract.EventList) string {
			var b strings.Builder
			fmt.Fprintf(&b, "%d events", len(l.Events))
			for _, e := range l.Events {
				fmt.Fprintf(&b, "\n%s %s %s x%d: %q", e.LastTimestamp, e.Type, e.Reason, e.Count, e.Message.Value)
			}
			return b.String()
		},
	}, func(ctx context.Context, in contract.EventsInput) (contract.EventList, error) {
		ns, err := env.Namespaces.Resolve(in.Namespace)
		if err != nil {
			return contract.EventList{}, err
		}
		return Get(ctx, env, ns, in.Kind, in.Name, in.Limit, in.SinceMinutes)
	})
}

// Get returns events for an object in an already-resolved namespace.
func Get(ctx context.Context, env *toolkit.Env, ns, kind, name string, limit, sinceMinutes int) (contract.EventList, error) {
	out := contract.EventList{Events: []contract.Event{}}
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	if sinceMinutes <= 0 {
		sinceMinutes = DefaultSinceMinutes
	}
	cutoff := projection.Now().Add(-time.Duration(sinceMinutes) * time.Minute)

	l, err := kube.Do(ctx, env.Provider, func(c *kube.Clients) (*corev1.EventList, error) {
		l := &corev1.EventList{}
		return l, c.Client.List(ctx, l,
			ctrlclient.InNamespace(ns),
			ctrlclient.MatchingFields{FieldInvolvedObjectKind: kind, FieldInvolvedObjectName: name})
	})
	if err != nil {
		return out, err
	}

	type timed struct {
		t time.Time
		e contract.Event
	}
	var items []timed
	for _, e := range l.Items {
		t := eventTime(&e)
		if t.Before(cutoff) {
			continue
		}
		pe := contract.Event{
			Type:    e.Type,
			Reason:  e.Reason,
			Message: contract.NewUntrusted(e.Message),
			Count:   e.Count,
			Source:  e.Source.Component,
		}
		if !t.IsZero() {
			pe.LastTimestamp = t.UTC().Format(contract.RFC3339)
		}
		if pe.Source == "" {
			pe.Source = e.ReportingController
		}
		items = append(items, timed{t, pe})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].t.After(items[j].t) })
	for i := range items {
		if i >= limit {
			break
		}
		out.Events = append(out.Events, items[i].e)
	}
	return out, nil
}

func eventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case e.Series != nil && !e.Series.LastObservedTime.IsZero():
		return e.Series.LastObservedTime.Time
	default:
		return e.CreationTimestamp.Time
	}
}
