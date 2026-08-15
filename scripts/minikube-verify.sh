#!/usr/bin/env bash
set -euo pipefail

PROFILE=${MINIKUBE_PROFILE:-go-metal3}
NAMESPACE=baremetal-operator-system
project_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
credential_dir=${project_dir}/.artifacts/minikube/credentials
PUBLIC_HOST=${PUBLIC_HOST:?set PUBLIC_HOST to the deployed DNS name}
PUBLIC_BIND_IP=${PUBLIC_BIND_IP:-127.0.0.1}
IRONIC_IMAGE_BIND_IP=${IRONIC_IMAGE_BIND_IP:-127.0.0.1}
IRONIC_CALLBACK_BIND_IP=${IRONIC_CALLBACK_BIND_IP:-${IRONIC_IMAGE_BIND_IP}}
TLS_CERT_FILE=${TLS_CERT_FILE:?set TLS_CERT_FILE to the deployed PEM certificate}

[[ -s ${credential_dir}/api-key ]] || { echo "API key file is missing" >&2; exit 1; }
[[ -s ${credential_dir}/ironic-username && -s ${credential_dir}/ironic-password ]] || { echo "Ironic credential files are missing" >&2; exit 1; }
header_file=$(mktemp)
ironic_curl_config=$(mktemp)
trap 'rm -f "${header_file}" "${ironic_curl_config}"' EXIT
chmod 0600 "${header_file}" "${ironic_curl_config}"
printf 'Authorization: Bearer %s\n' "$(<"${credential_dir}/api-key")" >"${header_file}"
printf 'user = "%s:%s"\n' "$(<"${credential_dir}/ironic-username")" "$(<"${credential_dir}/ironic-password")" >"${ironic_curl_config}"

kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pods
[[ $(kubectl --context "${PROFILE}" -n "${NAMESPACE}" get pvc ironic-data -o jsonpath='{.status.phase}') == Bound ]] || {
  echo "Ironic SQLite PVC is not Bound" >&2
  exit 1
}
kubectl --context "${PROFILE}" -n "${NAMESPACE}" wait --for=condition=available \
	deployment/ironic deployment/go-metal3-api --timeout=5m
curl --fail --silent --show-error --cacert "${TLS_CERT_FILE}" \
  --resolve "${PUBLIC_HOST}:443:${PUBLIC_BIND_IP}" "https://${PUBLIC_HOST}/healthz"
printf '\n'
curl --fail --silent --show-error --cacert "${TLS_CERT_FILE}" \
  --resolve "${PUBLIC_HOST}:443:${PUBLIC_BIND_IP}" --header @"${header_file}" \
  "https://${PUBLIC_HOST}/api/v1/cluster"
printf '\n'

unauthenticated_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
  "http://${IRONIC_CALLBACK_BIND_IP}:6385/v1/nodes?limit=1")
[[ ${unauthenticated_status} == 401 ]] || { echo "Ironic API did not reject an unauthenticated node-list request (HTTP ${unauthenticated_status})" >&2; exit 1; }
curl --fail --silent --show-error --config "${ironic_curl_config}" \
  "http://${IRONIC_CALLBACK_BIND_IP}:6385/v1/nodes?limit=1" >/dev/null
printf 'Ironic callback endpoint reachable and Basic Auth enforced.\n'
