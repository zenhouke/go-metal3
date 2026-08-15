#!/usr/bin/env bash
set -euo pipefail

PROFILE=${MINIKUBE_PROFILE:-go-metal3}
NAMESPACE=baremetal-operator-system
KUBERNETES_VERSION=v1.35.0
BMO_VERSION=v0.13.2
IRONIC_BASE_IMAGE=quay.io/metal3-io/ironic@sha256:364352ae334d362f8c0ada9618cbb7dd03d333786c5f821b08ef6b4a2d7569f8
case $(uname -m) in
  x86_64) ;;
  *) echo "this pinned Metal3/Ironic Minikube profile currently supports x86_64 only" >&2; exit 1 ;;
esac

project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
artifact_dir=${project_dir}/.artifacts/minikube
credential_dir=${artifact_dir}/credentials

PUBLIC_HOST=${PUBLIC_HOST:?set PUBLIC_HOST to the DNS name users will call}
TLS_CERT_FILE=${TLS_CERT_FILE:?set TLS_CERT_FILE to a PEM certificate for PUBLIC_HOST}
TLS_KEY_FILE=${TLS_KEY_FILE:?set TLS_KEY_FILE to its PEM private key}
IRONIC_IMAGE_BASE_URL=${IRONIC_IMAGE_BASE_URL:?set IRONIC_IMAGE_BASE_URL to an HTTP(S) URL reachable by BMCs, ending at the exposed port 6180}
IRONIC_CALLBACK_BASE_URL=${IRONIC_CALLBACK_BASE_URL:?set IRONIC_CALLBACK_BASE_URL to the HTTP Ironic API URL reachable by IPA, ending at the exposed port 6385}
PUBLIC_BIND_IP=${PUBLIC_BIND_IP:-127.0.0.1}
IRONIC_IMAGE_BIND_IP=${IRONIC_IMAGE_BIND_IP:-127.0.0.1}
IRONIC_CALLBACK_BIND_IP=${IRONIC_CALLBACK_BIND_IP:-${IRONIC_IMAGE_BIND_IP}}

case ${PUBLIC_HOST} in
  *[!A-Za-z0-9.-]*|.*|*.) echo "PUBLIC_HOST is not a valid DNS host" >&2; exit 1 ;;
esac
case ${PUBLIC_BIND_IP}:${IRONIC_IMAGE_BIND_IP}:${IRONIC_CALLBACK_BIND_IP} in
  *[!0-9.:]*) echo "bind addresses must be numeric IPv4 addresses" >&2; exit 1 ;;
esac
for bind_ip in "${PUBLIC_BIND_IP}" "${IRONIC_IMAGE_BIND_IP}" "${IRONIC_CALLBACK_BIND_IP}"; do
  [[ ${bind_ip} != *:* ]] || { echo "this Minikube port-mapping profile currently supports IPv4 bind addresses only" >&2; exit 1; }
done
case ${IRONIC_IMAGE_BASE_URL} in
  http://*|https://*) ;;
  *) echo "IRONIC_IMAGE_BASE_URL must be an absolute HTTP(S) URL" >&2; exit 1 ;;
esac
case ${IRONIC_CALLBACK_BASE_URL} in
  http://*) ;;
  *) echo "IRONIC_CALLBACK_BASE_URL must use HTTP for the direct Minikube 6385 mapping" >&2; exit 1 ;;
esac
[[ -r ${TLS_CERT_FILE} && -r ${TLS_KEY_FILE} ]] || { echo "TLS certificate/key are not readable" >&2; exit 1; }

for tool in minikube kubectl docker openssl; do
  command -v "${tool}" >/dev/null || { echo "missing required command: ${tool}" >&2; exit 1; }
done
docker info >/dev/null

umask 077
mkdir -p "${credential_dir}"

generate_credentials() {
  if [[ ! -s ${credential_dir}/api-key ]]; then
    openssl rand -hex 32 | tr -d '\n' >"${credential_dir}/api-key"
  fi
  if [[ ! -s ${credential_dir}/ironic-username ]]; then
    printf 'ironic-%s' "$(openssl rand -hex 8)" >"${credential_dir}/ironic-username"
  fi
  if [[ ! -s ${credential_dir}/ironic-password ]]; then
    openssl rand -base64 36 | tr -d '\n' >"${credential_dir}/ironic-password"
  fi
  docker run --rm -i --entrypoint /usr/bin/htpasswd "${IRONIC_BASE_IMAGE}" \
    -niB "$(<"${credential_dir}/ironic-username")" \
    <"${credential_dir}/ironic-password" >"${credential_dir}/htpasswd"
}

build_images() {
	minikube -p "${PROFILE}" image build -t go-metal3-api:dev "${project_dir}"
}

create_runtime_resources() {
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic ironic-api-auth \
    --from-file=htpasswd="${credential_dir}/htpasswd" --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic ironic-credentials \
    --from-file=username="${credential_dir}/ironic-username" \
    --from-file=password="${credential_dir}/ironic-password" --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret generic go-metal3-api-secrets \
    --from-file=api-key="${credential_dir}/api-key" --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create secret tls go-metal3-public-tls \
    --cert="${TLS_CERT_FILE}" --key="${TLS_KEY_FILE}" --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -

  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create configmap ironic-runtime \
    --from-literal=HTTP_PORT=6180 \
    --from-literal=PROVISIONING_INTERFACE=eth0 \
    --from-literal=IRONIC_ENDPOINT=http://ironic.${NAMESPACE}.svc.cluster.local:6385/v1/ \
    --from-literal=IRONIC_HTTP_URL="${IRONIC_IMAGE_BASE_URL%/}" \
    --from-literal=IRONIC_EXTERNAL_CALLBACK_URL="${IRONIC_CALLBACK_BASE_URL%/}" \
    --from-literal=IRONIC_KERNEL_PARAMS=console=ttyS0 \
    --from-literal=IRONIC_INSPECTOR_VLAN_INTERFACES=all \
    --from-literal=USE_IRONIC_INSPECTOR=false --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -

	kubectl --context "${PROFILE}" -n "${NAMESPACE}" create configmap go-metal3-runtime \
		--from-literal=MANAGED_NAMESPACES="${NAMESPACE}" --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -

  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create configmap ironic \
    --from-literal=IRONIC_ENDPOINT=http://ironic.${NAMESPACE}.svc.cluster.local:6385/v1/ \
    --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -
}

create_ingress() {
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" create ingress go-metal3-api \
    --class=nginx --rule="${PUBLIC_HOST}/=go-metal3-api:8080,tls=go-metal3-public-tls" \
    --dry-run=client -o yaml | kubectl --context "${PROFILE}" apply -f -
  kubectl --context "${PROFILE}" -n "${NAMESPACE}" annotate ingress go-metal3-api --overwrite \
    nginx.ingress.kubernetes.io/ssl-redirect=true \
    nginx.ingress.kubernetes.io/proxy-read-timeout=3600 \
    nginx.ingress.kubernetes.io/proxy-send-timeout=3600
}

generate_credentials
minikube start -p "${PROFILE}" --driver=docker --kubernetes-version="${KUBERNETES_VERSION}" \
  --cpus=4 --memory=8192 --disk-size=40g \
  --ports="${PUBLIC_BIND_IP}:80:80,${PUBLIC_BIND_IP}:443:443,${IRONIC_IMAGE_BIND_IP}:6180:6180,${IRONIC_CALLBACK_BIND_IP}:6385:6385"
minikube -p "${PROFILE}" addons enable ingress
kubectl --context "${PROFILE}" wait --for=condition=available deployment/ingress-nginx-controller -n ingress-nginx --timeout=5m

build_images
kubectl --context "${PROFILE}" apply -f "https://github.com/metal3-io/baremetal-operator/releases/download/${BMO_VERSION}/baremetal-operator.yaml"
kubectl --context "${PROFILE}" wait --for=condition=Established crd/baremetalhosts.metal3.io --timeout=3m
create_runtime_resources
kubectl --context "${PROFILE}" apply -k "${project_dir}/deploy/minikube"
kubectl --context "${PROFILE}" -n "${NAMESPACE}" patch deployment baremetal-operator-controller-manager \
  --type=strategic --patch-file "${project_dir}/deploy/minikube/bmo-auth-patch.yaml"
create_ingress

kubectl --context "${PROFILE}" -n "${NAMESPACE}" rollout status deployment/baremetal-operator-controller-manager --timeout=5m
kubectl --context "${PROFILE}" -n "${NAMESPACE}" rollout status deployment/ironic --timeout=15m
kubectl --context "${PROFILE}" -n "${NAMESPACE}" rollout status deployment/go-metal3-api --timeout=5m

printf 'Deployment ready. Public base URL: https://%s\n' "${PUBLIC_HOST}"
printf 'API key is stored locally at %s and in Kubernetes Secret go-metal3-api-secrets.\n' "${credential_dir}/api-key"
printf 'Run scripts/minikube-verify.sh for an authenticated API check.\n'
