#!/usr/bin/env bash

# Orchestrates the VM API + VM Operator demo build:
#
#   1. Generates the VM API (vm-api.io) VirtualMachine CRD from the vm-api Go
#      module pinned in go.mod, plus VM Operator's CRDs and RBAC.
#   2. Renders artifacts/local-deployment.yaml and checks it carries the VM API
#      CRD and the manager-role rules for vm-api.io.
#   3. Optionally builds the VM Operator image and/or deploys CRDs and the
#      image to a Supervisor with hack/deploy-wcp.sh.
#
# The VM API core controller (github.com/vm-api/vm-api/controller) is linked
# into the VM Operator manager and started in-process when the
# FSS_WCP_VMSERVICE_KUBEVM_PROVIDER feature is enabled, so there is no separate
# controller Pod to deploy.
#
# Usage: run.sh [-b] [-d "<deploy-wcp.sh target args>"]
#   -b  Build the VM Operator image (make image-build-amd64) into ${IMAGE_TAR}
#   -d  Deploy CRDs (and the image, if -b) using hack/deploy-wcp.sh, passing
#       the given target args, e.g. -d "-v 10.0.0.2" or -d "-T testbedInfo.json"

set -o errexit
set -o nounset
set -o pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

BUILD_IMAGE=false
DEPLOY_ARGS=
while getopts "bd:" opt; do
    case $opt in
        b) BUILD_IMAGE=true ;;
        d) DEPLOY_ARGS=$OPTARG ;;
        *) sed -n '3,25p' "${BASH_SOURCE[0]}"; exit 1 ;;
    esac
done

LOCAL_YAML=artifacts/local-deployment.yaml
VM_API_CRD='virtualmachines.vm-api.io'
CRD_PATTERNS="${VM_API_CRD},*.vmoperator.vmware.com"
IMAGE_TAR=artifacts/vmoperator-controller-linux_amd64.tar

echo ">> Generating manifests and rendering ${LOCAL_YAML}"
make kustomize-wcp

echo ">> Verifying ${LOCAL_YAML} contains the VM API CRD and RBAC"
grep -q "name: ${VM_API_CRD}\$" "${LOCAL_YAML}" || {
    echo "ERROR: ${VM_API_CRD} CRD missing from ${LOCAL_YAML}" >&2; exit 1; }
grep -q -- '- vm-api.io$' "${LOCAL_YAML}" || {
    echo "ERROR: vm-api.io RBAC rules missing from ${LOCAL_YAML}" >&2; exit 1; }

if [[ $BUILD_IMAGE == true ]]; then
    echo ">> Building the VM Operator image"
    make image-build-amd64
fi

if [[ -n $DEPLOY_ARGS ]]; then
    args=()
    # shellcheck disable=SC2206
    args+=($DEPLOY_ARGS)
    args+=(-C "${CRD_PATTERNS}")
    if [[ $BUILD_IMAGE == true ]]; then args+=("${IMAGE_TAR}"); fi
    echo ">> hack/deploy-wcp.sh ${args[*]}"
    ./hack/deploy-wcp.sh "${args[@]}"
else
    cat <<MSG

Done. To install the CRDs/RBAC on a Supervisor:
  hack/deploy-wcp.sh <-v VC_IP | -s SV_IPS | -T testbedInfo.json> -C '${CRD_PATTERNS}' [image.tar]
Remember to enable FSS_WCP_VMSERVICE_KUBEVM_PROVIDER on the vmop deployment.
MSG
fi
