package generation

// changeHistoryTemplate emits the handler of a record's change history,
// GET <the read route>/changes, for a table-backed resource with a read route: the read
// handler's request type and decoder, so the grants, the masks and the flags are the
// read's; the record named by the read route's key, in the request's tenant where the
// resource belongs to one; and the fields the read leaves out of its answer that are not
// secrets (an input_only field, a stored file's key) named as never returned. A history
// read registers no live read.
const changeHistoryTemplate = `// {{ .Resource.Name }}ChangeHistory answers a page of one {{ .Resource.SubjectRecordWords }}'s change records, newest first, to whoever may read the record, with the fields they may see on it. It answers 404 while the resource does not track its changes.
func ({{ .ReceiverName }} *{{ .ApplicationName }}) {{ .Resource.Name }}ChangeHistory() http.HandlerFunc {
	type response struct {
		{{- range $field := .Resource.Fields }}
		{{ $field.Name }} {{ $field.Type}} ` + "`{{ $field.JSONTag }} {{ $field.UniqueIndexTag }} {{ $field.PermTag }} {{ $field.PIITag }} {{ $field.MaskingTag }} {{ $field.FeatureTag }} {{ $field.FormerlyTag }} {{ $field.SearchTag }}`" + `
		{{- end }}
		{{- range $name := .Resource.NameFields }}
		{{ $name.Name }} *string ` + "`{{ $name.StructTag }}`" + `
		{{- end }}
	}

	decoder := NewQueryDecoder[{{ .ResourcePackage }}.{{ .Resource.Name }}, response]({{ .ReceiverName }}, accesstypes.Read)

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

	{{ if .Resource.HasCompoundPrimaryKey }}
	{{- range $_, $field := .Resource.PrimaryKeys }}
		{{ GoCamel $field.Name }} := httpio.Param[{{ $field.Type }}](r, router.{{ $.Resource.Name }}{{ $field.Name }})
	{{- end }}
	{{ else }}
		id := httpio.Param[{{ .Resource.PrimaryKeyType }}](r, router.{{ .Resource.Name }}{{ .Resource.PrimaryKey.Name }})
	{{ end }}
		{{ if .Resource.IsDomainScoped -}}
		` + domainParamLine + `
		{{ end -}}
		history, err := decoder.DecodeChangeHistory(r, {{ .ReceiverName }}.UserPermissions(r), {{ if .Resource.IsDomainScoped }}accesstypes.DomainScope(domain){{ else }}accesstypes.GlobalScope(){{ end }}{{ range .Resource.Fields }}{{ if and .IsInputOnly (not .IsSecret) }}, "{{ .Name }}"{{ end }}{{ end }})
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
	{{ if .Resource.HasCompoundPrimaryKey }}
		{{ .ResourcePackage }}.New{{ .Resource.Name }}QueryFromQuerySet(history.QuerySet()){{ range $_, $field := .Resource.PrimaryKeys }}.Set{{ $field.Name }}({{ GoCamel $field.Name }}){{ end }}
	{{- else }}
		{{ .ResourcePackage }}.New{{ .Resource.Name }}QueryFromQuerySet(history.QuerySet()).Set{{ .Resource.PrimaryKey.Name }}(id)
	{{- end }}

		page, err := history.Read(ctx, {{ .ReceiverName }}.ResourceClient())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(page)
	})
}`
