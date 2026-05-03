{{- with listSecrets "__INFISICAL_PROJECT_ID__" "__INFISICAL_ENV__" "/" }}
{{- range . }}
{{ .Key }}={{ .Value }}
{{- end }}
{{- end }}
