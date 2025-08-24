// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package virtualmachineclassinstance

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha5"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/patch"
	"github.com/vmware-tanzu/vm-operator/pkg/record"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AddToManager adds this package's controller to the provided manager.
func AddToManager(ctx *pkgctx.ControllerManagerContext, mgr manager.Manager) error {
	var (
		controlledType     = &vmopv1.VirtualMachineClassInstance{}
		controlledTypeName = reflect.TypeOf(controlledType).Elem().Name()

		controllerNameShort = fmt.Sprintf("%s-controller", strings.ToLower(controlledTypeName))
		controllerNameLong  = fmt.Sprintf("%s/%s/%s", ctx.Namespace, ctx.Name, controllerNameShort)
	)

	r := NewReconciler(
		ctx,
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName(controlledTypeName),
		record.New(mgr.GetEventRecorderFor(controllerNameLong)),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(controlledType).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Watches(&vmopv1.VirtualMachine{},
			handler.EnqueueRequestsFromMapFunc(r.vmToVMClassInstanceRequests)).
		Watches(&vmopv1.VirtualMachineClass{},
			handler.EnqueueRequestsFromMapFunc(r.vmClassToVMClassInstanceRequests)).
		Complete(r)
}

func NewReconciler(
	ctx context.Context,
	client client.Client,
	logger logr.Logger,
	recorder record.Recorder) *Reconciler {
	return &Reconciler{
		Context:  ctx,
		Client:   client,
		Logger:   logger,
		Recorder: recorder,
	}
}

// Reconciler reconciles a VirtualMachineClassInstance object.
type Reconciler struct {
	client.Client
	Context  context.Context
	Logger   logr.Logger
	Recorder record.Recorder
}

// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachineclassinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachineclassinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachineclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=vmoperator.vmware.com,resources=virtualmachines,verbs=get;list;watch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	ctx = pkgcfg.JoinContext(ctx, r.Context)

	vmClassInstance := &vmopv1.VirtualMachineClassInstance{}
	if err := r.Get(ctx, req.NamespacedName, vmClassInstance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	vmClassInstanceCtx := &pkgctx.VirtualMachineClassInstanceContext{
		Context:         ctx,
		Logger:          ctrl.Log.WithName("VirtualMachineClassInstance").WithValues("name", req.Name, "namespace", req.Namespace),
		VMClassInstance: vmClassInstance,
	}

	patchHelper, err := patch.NewHelper(vmClassInstance, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to init patch helper for %s: %w", vmClassInstanceCtx, err)
	}
	defer func() {
		if err := patchHelper.Patch(ctx, vmClassInstance); err != nil {
			if reterr == nil {
				reterr = err
			}
			vmClassInstanceCtx.Logger.Error(err, "patch failed")
		}
	}()

	if !vmClassInstance.DeletionTimestamp.IsZero() {
		return r.ReconcileDelete(vmClassInstanceCtx)
	}

	return r.ReconcileNormal(vmClassInstanceCtx)
}

// ReconcileNormal handles the normal reconciliation of a VirtualMachineClassInstance.
func (r *Reconciler) ReconcileNormal(vmClassInstanceCtx *pkgctx.VirtualMachineClassInstanceContext) (ctrl.Result, error) {
	// Check if the owning class still exists and handle orphaned instances
	if err := r.reconcileClassReference(vmClassInstanceCtx); err != nil {
		vmClassInstanceCtx.Logger.Error(err, "Failed to reconcile class reference")
		return ctrl.Result{}, err
	}

	// Schedule periodic cleanup check
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// ReconcileDelete handles the deletion of a VirtualMachineClassInstance.
func (r *Reconciler) ReconcileDelete(vmClassInstanceCtx *pkgctx.VirtualMachineClassInstanceContext) (ctrl.Result, error) {
	// The VM controller is responsible for managing owner references to VMClassInstances
	// This controller just needs to handle its own cleanup
	return ctrl.Result{}, nil
}

// reconcileClassReference handles the class reference via annotation instead of owner reference.
func (r *Reconciler) reconcileClassReference(vmClassInstanceCtx *pkgctx.VirtualMachineClassInstanceContext) error {
	vmClassInstance := vmClassInstanceCtx.VMClassInstance

	// Get the class name from the instance name (format: className-hash)
	className := r.extractClassNameFromInstance(vmClassInstance.Name)
	if className == "" {
		vmClassInstanceCtx.Logger.Error(nil, "Unable to extract class name from instance", "instanceName", vmClassInstance.Name)
		return nil
	}

	// Check if the class still exists
	vmClass := &vmopv1.VirtualMachineClass{}
	classKey := client.ObjectKey{
		Namespace: vmClassInstance.Namespace,
		Name:      className,
	}

	err := r.Get(vmClassInstanceCtx, classKey, vmClass)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Class doesn't exist, check if we should clean up this instance
			return r.handleOrphanedInstance(vmClassInstanceCtx, className)
		}
		return fmt.Errorf("failed to get VM class %s: %w", className, err)
	}

	// Class exists, update the class reference annotation
	if vmClassInstance.Annotations == nil {
		vmClassInstance.Annotations = make(map[string]string)
	}
	vmClassInstance.Annotations["vmoperator.vmware.com/vm-class-name"] = className

	return nil
}

// handleOrphanedInstance handles instances whose owning class no longer exists.
func (r *Reconciler) handleOrphanedInstance(vmClassInstanceCtx *pkgctx.VirtualMachineClassInstanceContext, className string) error {
	vmClassInstance := vmClassInstanceCtx.VMClassInstance

	// Check if any VMs are still using this instance
	vmList := &vmopv1.VirtualMachineList{}
	if err := r.List(vmClassInstanceCtx, vmList, client.InNamespace(vmClassInstance.Namespace)); err != nil {
		return fmt.Errorf("failed to list VMs: %w", err)
	}

	hasReferencingVMs := false
	for _, vm := range vmList.Items {
		if r.vmReferencesClassInstance(&vm, vmClassInstance) {
			hasReferencingVMs = true
			break
		}
	}

	if !hasReferencingVMs {
		// No VMs are using this instance and the class doesn't exist, mark for deletion
		vmClassInstanceCtx.Logger.Info("Marking orphaned instance for deletion", "className", className)
		return r.Delete(vmClassInstanceCtx, vmClassInstance)
	}

	// VMs are still using this instance, keep it around
	vmClassInstanceCtx.Logger.Info("Keeping orphaned instance as VMs are still referencing it", "className", className)
	return nil
}

// vmReferencesClassInstance checks if a VM references the given class instance.
func (r *Reconciler) vmReferencesClassInstance(vm *vmopv1.VirtualMachine, vmClassInstance *vmopv1.VirtualMachineClassInstance) bool {
	// Check spec.class field
	if vm.Spec.Class != nil && vm.Spec.Class.Name == vmClassInstance.Name {
		return true
	}

	// Check if VM references the class by name and the instance is active
	if vm.Spec.ClassName != "" {
		className := r.extractClassNameFromInstance(vmClassInstance.Name)
		if vm.Spec.ClassName == className {
			// Check if this instance is active
			if _, isActive := vmClassInstance.Labels[vmopv1.VMClassInstanceActiveLabelKey]; isActive {
				return true
			}
		}
	}

	return false
}

// extractClassNameFromInstance extracts the class name from the instance name (format: className-hash).
func (r *Reconciler) extractClassNameFromInstance(instanceName string) string {
	// Instance names are in format: className-hash
	// Find the last dash and extract everything before it
	lastDash := strings.LastIndex(instanceName, "-")
	if lastDash == -1 {
		return ""
	}
	return instanceName[:lastDash]
}

// vmToVMClassInstanceRequests maps VM events to VMClassInstance reconcile requests.
func (r *Reconciler) vmToVMClassInstanceRequests(_ context.Context, obj client.Object) []reconcile.Request {
	vm, ok := obj.(*vmopv1.VirtualMachine)
	if !ok {
		return nil
	}

	var requests []reconcile.Request

	// If VM has spec.class set, reconcile that instance
	if vm.Spec.Class != nil && vm.Spec.Class.Name != "" {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{
				Namespace: vm.Namespace,
				Name:      vm.Spec.Class.Name,
			},
		})
	}

	// If VM has spec.className set, find the active instance for that class
	if vm.Spec.ClassName != "" {
		// List all instances in the namespace to find the active one for this class
		vmClassInstanceList := &vmopv1.VirtualMachineClassInstanceList{}
		if err := r.List(context.Background(), vmClassInstanceList, client.InNamespace(vm.Namespace)); err != nil {
			r.Logger.Error(err, "Failed to list VMClassInstances for VM watch", "vm", vm.Name)
			return requests
		}

		for _, instance := range vmClassInstanceList.Items {
			className := r.extractClassNameFromInstance(instance.Name)
			if className == vm.Spec.ClassName {
				if _, isActive := instance.Labels[vmopv1.VMClassInstanceActiveLabelKey]; isActive {
					requests = append(requests, reconcile.Request{
						NamespacedName: client.ObjectKey{
							Namespace: instance.Namespace,
							Name:      instance.Name,
						},
					})
				}
			}
		}
	}

	return requests
}

// vmClassToVMClassInstanceRequests maps VMClass events to VMClassInstance reconcile requests.
func (r *Reconciler) vmClassToVMClassInstanceRequests(_ context.Context, obj client.Object) []reconcile.Request {
	vmClass, ok := obj.(*vmopv1.VirtualMachineClass)
	if !ok {
		return nil
	}

	// List all instances in the namespace that belong to this class
	vmClassInstanceList := &vmopv1.VirtualMachineClassInstanceList{}
	if err := r.List(context.Background(), vmClassInstanceList, client.InNamespace(vmClass.Namespace)); err != nil {
		r.Logger.Error(err, "Failed to list VMClassInstances for VMClass watch", "vmClass", vmClass.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, instance := range vmClassInstanceList.Items {
		className := r.extractClassNameFromInstance(instance.Name)
		if className == vmClass.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: instance.Namespace,
					Name:      instance.Name,
				},
			})
		}
	}

	return requests
}
