{{/*
Expand the name of the chart.
*/}}
{{- define "compactor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name, truncated to the 63 chars Kubernetes name fields allow.
If the release name already contains the chart name it is used as is.
*/}}
{{- define "compactor.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "compactor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "compactor.labels" -}}
helm.sh/chart: {{ include "compactor.chart" . }}
{{ include "compactor.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels shared by every workload of the release.
*/}}
{{- define "compactor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "compactor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Selector labels for the listener alone, so its Service and PDB do not select CronJob pods.
*/}}
{{- define "compactor.listenSelectorLabels" -}}
{{ include "compactor.selectorLabels" . }}
app.kubernetes.io/component: listen
{{- end }}

{{- define "compactor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "compactor.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "compactor.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
Name of the ConfigMap holding config.yaml: the user's, or the chart's own.
*/}}
{{- define "compactor.configMapName" -}}
{{- .Values.existingConfig | default (printf "%s-config" (include "compactor.fullname" .)) }}
{{- end }}

{{/*
True when the chart renders its own S3 credentials Secret (inline values, no existing secret).
*/}}
{{- define "compactor.createCredentialsSecret" -}}
{{- if and (not .Values.credentials.existingSecret) (or .Values.credentials.accessKeyId .Values.credentials.secretAccessKey) -}}true{{- end -}}
{{- end }}

{{/*
Name of the Secret carrying AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, or "" when credentials come from the environment (IRSA, workload identity, instance profile).
*/}}
{{- define "compactor.credentialsSecretName" -}}
{{- if .Values.credentials.existingSecret -}}
{{- .Values.credentials.existingSecret -}}
{{- else if include "compactor.createCredentialsSecret" . -}}
{{- printf "%s-credentials" (include "compactor.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Environment shared by every compactor container.
*/}}
{{- define "compactor.env" -}}
# Writable HOME so XDG path resolution works with a read-only root filesystem.
- name: HOME
  value: /tmp
- name: OTEL_SERVICE_NAME
  value: {{ .Values.otel.serviceName | quote }}
- name: OTEL_METRICS_EXPORTER
  value: {{ .Values.otel.exporter | quote }}
{{- with .Values.otel.endpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
{{- end }}
{{- with .Values.otel.protocol }}
- name: OTEL_EXPORTER_OTLP_PROTOCOL
  value: {{ . | quote }}
{{- end }}
{{- if .Values.sourceAesCtrGzipKey.existingSecret }}
- name: COMPACTOR_SOURCE_AES_CTR_GZIP_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.sourceAesCtrGzipKey.existingSecret }}
      key: {{ .Values.sourceAesCtrGzipKey.key }}
{{- end }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "compactor.envFrom" -}}
{{- with (include "compactor.credentialsSecretName" .) }}
- secretRef:
    name: {{ . }}
{{- end }}
{{- with .Values.extraEnvFrom }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "compactor.volumeMounts" -}}
- name: config
  mountPath: /etc/compactor
  readOnly: true
{{- if .Values.ageIdentity.existingSecret }}
- name: age-identity
  mountPath: /etc/compactor/age
  readOnly: true
{{- end }}
# Scratch space for `compactor materialize` and the XDG directories; the root filesystem is read-only.
- name: tmp
  mountPath: /tmp
{{- with .Values.volumeMounts }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "compactor.volumes" -}}
- name: config
  configMap:
    name: {{ include "compactor.configMapName" . }}
{{- if .Values.ageIdentity.existingSecret }}
- name: age-identity
  secret:
    secretName: {{ .Values.ageIdentity.existingSecret }}
    defaultMode: 0400
    items:
      - key: {{ .Values.ageIdentity.key }}
        path: {{ .Values.ageIdentity.key }}
{{- end }}
- name: tmp
  emptyDir: {}
{{- with .Values.volumes }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Pod-level settings shared by the listener and the CronJobs.
*/}}
{{- define "compactor.podSpecShared" -}}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
serviceAccountName: {{ include "compactor.serviceAccountName" . }}
automountServiceAccountToken: {{ .Values.serviceAccount.automount }}
{{- with .Values.podSecurityContext }}
securityContext:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
