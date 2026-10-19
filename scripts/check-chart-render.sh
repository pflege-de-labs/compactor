#!/usr/bin/env bash
# Asserts what the chart renders; rendering alone proves only that it is not broken. Run by CI after helm lint.
set -euo pipefail

chart=${1:-charts/compactor}
fail() { echo "::error::$1"; exit 1; }
render() { helm template ci "$chart" --values "$chart/ci/$1-values.yaml" "${@:2}"; }

out=$(render default)
echo "$out" | grep -q 'image: "ghcr.io/pflege-de-labs/compactor:' || fail "image default does not point at ghcr.io/pflege-de-labs/compactor"
echo "$out" | grep -qx 'kind: Deployment' || fail "default values must render the listener Deployment"
echo "$out" | grep -q 'http-addr: :8080' || fail "listen.port is not rendered into config as listen.http-addr"

out=$(render statefulset)
echo "$out" | grep -qx 'kind: StatefulSet' || fail "listen.kind=statefulset must render a StatefulSet"
echo "$out" | grep -qx 'kind: Deployment' && fail "statefulset values still rendered a Deployment"
echo "$out" | grep -q 'clusterIP: None' || fail "the StatefulSet has no headless Service"
echo "$out" | grep -qx 'kind: PodDisruptionBudget' || fail "podDisruptionBudget.enabled did not render a PDB"
echo "$out" | grep -qx 'kind: NetworkPolicy' || fail "networkPolicy.enabled did not render a NetworkPolicy"

out=$(render cronjobs-only)
echo "$out" | grep -qx 'kind: Deployment' && fail "listen.enabled=false still rendered a Deployment"
[ "$(echo "$out" | grep -cx 'kind: CronJob')" = 3 ] || fail "cronjobs-only values must render three CronJobs"
echo "$out" | grep -q -- '--day=2026-10-01' || fail "cronJobs.hourly.args were not appended"
cj() { echo "$out" | awk -v n="name: ci-compactor-$1" '$0 ~ n {f=1} f && /^---/ {exit} f'; }
cj promote-month | grep -q 'memory: 2Gi' || fail "cronJobs.promoteMonth.resources did not override cronJobs.resources"
cj hourly | grep -q 'memory: 256Mi' || fail "cronJobs.resources is not the default for jobs without their own"
echo "$out" | grep -q 'if-match: false' || fail "config.s3.if-match is not rendered into the config"

out=$(render existing-secrets)
echo "$out" | grep -qx 'kind: ConfigMap' && fail "existingConfig must suppress the chart's own ConfigMap"
echo "$out" | grep -q 'name: compactor-managed-config' || fail "existingConfig is not mounted"
echo "$out" | grep -q 'name: compactor-s3' || fail "credentials.existingSecret is not injected"
echo "$out" | grep -q 'secretName: compactor-age' || fail "ageIdentity.existingSecret is not mounted"
echo "$out" | grep -q 'name: compactor-source-key' || fail "sourceAesCtrGzipKey.existingSecret is not injected"

out=$(render extra-objects)
echo "$out" | grep -qE "name: '?ci-extra-object'?$" || fail "extraObjects map entries are not rendered through tpl"
echo "$out" | grep -q 'name: ci-extra-string$' || fail "extraObjects string entries are not rendered through tpl"
out=$(render default --set 'extraObjects[0].kind=ConfigMap' --set 'extraObjects[0].apiVersion=v1' --set 'extraObjects[0].metadata.name=from-list')
echo "$out" | grep -q 'name: from-list$' || fail "extraObjects as a list is not rendered"

# helm exits non-zero on these by design, so capture past pipefail.
out=$(helm template ci "$chart" 2>&1 || true)
echo "$out" | grep -q 'config.s3.bucket is required' || fail "a missing bucket must fail the render"
out=$(helm template ci "$chart" --set config.s3.bucket=b --set listen.kind=daemonset 2>&1 || true)
echo "$out" | grep -q 'listen.kind must be' || fail "an invalid listen.kind must fail the render"

echo "chart render assertions passed"
