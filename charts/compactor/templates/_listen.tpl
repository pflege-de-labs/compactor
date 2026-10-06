{{/*
Pod template shared by the listener's Deployment and StatefulSet.
*/}}
{{- define "compactor.listenPodTemplate" -}}
metadata:
  annotations:
    {{- /* Roll pods when the rendered config or credentials change. */}}
    checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
    checksum/credentials: {{ include (print $.Template.BasePath "/secret-credentials.yaml") . | sha256sum }}
    {{- with .Values.podAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  labels:
    {{- include "compactor.labels" . | nindent 4 }}
    app.kubernetes.io/component: listen
    {{- with .Values.podLabels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  {{- include "compactor.podSpecShared" . | nindent 2 }}
  terminationGracePeriodSeconds: {{ .Values.listen.terminationGracePeriodSeconds }}
  containers:
    - name: compactor
      {{- with .Values.securityContext }}
      securityContext:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      image: {{ include "compactor.image" . | quote }}
      imagePullPolicy: {{ .Values.image.pullPolicy }}
      args:
        - --config=/etc/compactor/config.yaml
        - listen
        {{- with .Values.listen.args }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      ports:
        - name: http
          containerPort: {{ .Values.listen.port }}
          protocol: TCP
      env:
        {{- include "compactor.env" . | nindent 8 }}
      {{- with (include "compactor.envFrom" .) }}
      envFrom:
        {{- . | nindent 8 }}
      {{- end }}
      {{- with .Values.listen.livenessProbe }}
      livenessProbe:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.listen.readinessProbe }}
      readinessProbe:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.resources }}
      resources:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      volumeMounts:
        {{- include "compactor.volumeMounts" . | nindent 8 }}
  volumes:
    {{- include "compactor.volumes" . | nindent 4 }}
{{- end }}
