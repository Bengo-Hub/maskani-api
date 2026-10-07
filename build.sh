#!/usr/bin/env bash
# CI build and deploy for maskani-api: scan, build, push, provision database and secrets through
# devops-k8s scripts, then bump the image tag in devops-k8s for ArgoCD. Runs in CI only; never run
# it by hand against production.
set -euo pipefail
set +H

info() { echo "[INFO] $1"; }
success() { echo "[OK] $1"; }
warn() { echo "[WARN] $1"; }
error() { echo "[ERROR] $1"; }

APP_NAME=${APP_NAME:-"maskani-api"}
NAMESPACE=${NAMESPACE:-"maskani"}
ENV_SECRET_NAME=${ENV_SECRET_NAME:-"maskani-api-secrets"}
DEPLOY=${DEPLOY:-true}
SETUP_DATABASES=${SETUP_DATABASES:-true}
DB_TYPES=${DB_TYPES:-postgres,redis}
SERVICE_DB_NAME=${SERVICE_DB_NAME:-maskani}
SERVICE_DB_USER=${SERVICE_DB_USER:-maskani_user}

REGISTRY_SERVER=${REGISTRY_SERVER:-docker.io}
REGISTRY_NAMESPACE=${REGISTRY_NAMESPACE:-codevertex}
IMAGE_REPO="${REGISTRY_SERVER}/${REGISTRY_NAMESPACE}/${APP_NAME}"

DEVOPS_REPO=${DEVOPS_REPO:-"Bengo-Hub/devops-k8s"}
DEVOPS_DIR=${DEVOPS_DIR:-"$HOME/devops-k8s"}
VALUES_FILE_PATH=${VALUES_FILE_PATH:-"apps/${APP_NAME}/values.yaml"}
GIT_EMAIL=${GIT_EMAIL:-"dev@bengobox.com"}
GIT_USER=${GIT_USER:-"Maskani Bot"}
TRIVY_ECODE=${TRIVY_ECODE:-0}

if [[ -z ${GITHUB_SHA:-} ]]; then
  GIT_COMMIT_ID=$(git rev-parse --short=8 HEAD || echo "localbuild")
else
  GIT_COMMIT_ID=${GITHUB_SHA::8}
fi

info "Service  : ${APP_NAME}"
info "Namespace: ${NAMESPACE}"
info "Image    : ${IMAGE_REPO}:${GIT_COMMIT_ID}"

for tool in git docker trivy; do
  command -v "$tool" >/dev/null || { error "$tool is required"; exit 1; }
done
if [[ ${DEPLOY} == "true" ]]; then
  for tool in kubectl helm yq jq; do
    command -v "$tool" >/dev/null || { error "$tool is required"; exit 1; }
  done
fi

if [[ ${DEPLOY} == "true" ]]; then
  SYNC_SCRIPT=$(mktemp)
  if curl -fsSL https://raw.githubusercontent.com/Bengo-Hub/devops-k8s/main/scripts/tools/check-and-sync-secrets.sh -o "$SYNC_SCRIPT" 2>/dev/null; then
    source "$SYNC_SCRIPT"
    check_and_sync_secrets "REGISTRY_USERNAME" "REGISTRY_PASSWORD" "GIT_TOKEN" "POSTGRES_PASSWORD" "REDIS_PASSWORD" "KUBE_CONFIG" || warn "Secret sync failed, continuing with existing secrets"
    rm -f "$SYNC_SCRIPT"
  else
    warn "Unable to download the secret sync script, continuing with existing secrets"
  fi
fi

info "Running Trivy filesystem scan"
trivy fs . --exit-code "$TRIVY_ECODE" --format table || true

info "Building Docker image"
DOCKER_BUILDKIT=1 docker build -t "${IMAGE_REPO}:${GIT_COMMIT_ID}" .
success "Docker build complete"

if [[ ${DEPLOY} != "true" ]]; then
  warn "DEPLOY=false, skipping push and deploy"
  exit 0
fi

if [[ -n ${REGISTRY_USERNAME:-} && -n ${REGISTRY_PASSWORD:-} ]]; then
  echo "$REGISTRY_PASSWORD" | docker login "$REGISTRY_SERVER" -u "$REGISTRY_USERNAME" --password-stdin
fi
docker push "${IMAGE_REPO}:${GIT_COMMIT_ID}"
success "Image pushed"

if [[ -n ${KUBE_CONFIG:-} ]]; then
  mkdir -p ~/.kube
  if echo "$KUBE_CONFIG" | base64 -d > ~/.kube/config 2>/dev/null; then
    info "KUBE_CONFIG decoded from base64"
  else
    echo "$KUBE_CONFIG" > ~/.kube/config
  fi
  chmod 600 ~/.kube/config
  export KUBECONFIG=~/.kube/config
fi

kubectl get ns "$NAMESPACE" >/dev/null 2>&1 || kubectl create ns "$NAMESPACE"

if [[ -n ${REGISTRY_USERNAME:-} && -n ${REGISTRY_PASSWORD:-} ]]; then
  kubectl -n "$NAMESPACE" create secret docker-registry registry-credentials \
    --docker-server="$REGISTRY_SERVER" --docker-username="$REGISTRY_USERNAME" --docker-password="$REGISTRY_PASSWORD" \
    --dry-run=client -o yaml | kubectl apply -f - || warn "registry secret creation failed"
fi

if [[ ! -d "$DEVOPS_DIR" ]]; then
  TOKEN="${GH_PAT:-${GIT_SECRET:-${GIT_TOKEN:-}}}"
  CLONE_URL="https://github.com/${DEVOPS_REPO}.git"
  [[ -n $TOKEN ]] && CLONE_URL="https://x-access-token:${TOKEN}@github.com/${DEVOPS_REPO}.git"
  git clone "$CLONE_URL" "$DEVOPS_DIR" || warn "Unable to clone the devops repo"
fi

if [[ "$SETUP_DATABASES" == "true" && -n "${KUBE_CONFIG:-}" ]]; then
  if kubectl -n infra get statefulset postgresql >/dev/null 2>&1; then
    kubectl -n infra rollout status statefulset/postgresql --timeout=180s || warn "PostgreSQL not fully ready"
    if [[ -f "$DEVOPS_DIR/scripts/infrastructure/create-service-database.sh" ]]; then
      info "Ensuring database '${SERVICE_DB_NAME}'"
      SERVICE_DB_NAME="$SERVICE_DB_NAME" APP_NAME="$APP_NAME" NAMESPACE="$NAMESPACE" \
        bash "$DEVOPS_DIR/scripts/infrastructure/create-service-database.sh" || warn "Database creation failed or already exists"
    fi
  fi
fi

if [[ -f "$DEVOPS_DIR/scripts/infrastructure/create-service-secrets.sh" ]]; then
  info "Updating ${ENV_SECRET_NAME}"
  SERVICE_NAME="$APP_NAME" NAMESPACE="$NAMESPACE" DB_NAME="$SERVICE_DB_NAME" DB_USER="$SERVICE_DB_USER" \
  SECRET_NAME="$ENV_SECRET_NAME" POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-}" REDIS_PASSWORD="${REDIS_PASSWORD:-}" \
    bash "$DEVOPS_DIR/scripts/infrastructure/create-service-secrets.sh" || warn "Secret sync failed"
fi

if [[ -f "${DEVOPS_DIR}/scripts/helm/update-values.sh" ]]; then
  source "${DEVOPS_DIR}/scripts/helm/update-values.sh"
fi
if declare -f update_helm_values >/dev/null 2>&1; then
  update_helm_values "$APP_NAME" "$GIT_COMMIT_ID" "$IMAGE_REPO"
else
  warn "update_helm_values not available, helm values not updated"
fi

info "Deployed ${IMAGE_REPO}:${GIT_COMMIT_ID} to ${NAMESPACE}"
