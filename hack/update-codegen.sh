#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname ${BASH_SOURCE})/..
CODEGEN_PKG=${CODEGEN_PKG:-$(cd ${SCRIPT_ROOT}; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ${GOPATH}/src/k8s.io/code-generator)}

${SCRIPT_ROOT}/hack/generate-groups.sh all \
  github.com/linki/cloudformation-operator/pkg/client github.com/linki/cloudformation-operator/pkg/apis \
  cloudformation:v1alpha1 \
  --go-header-file ${SCRIPT_ROOT}/hack/no-boilerplate.go.txt
