package generation

import (
	"cmp"
	"fmt"
	"go/types"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/resource"
)

// A stored secret is a field declared conditions:"secret" and typed resource.Secret, on
// a BYTES column that holds the value sealed (resource/secret.go). The field's own type
// holds only what the column stores, so everything that carries the value as it is
// given, the patch request structs, the typed patches' setters and the TypeScript
// shapes, carries it as text.

const (
	// secretTypeName is the resource package's type of a stored secret.
	secretTypeName = "Secret"
	// resourceSecretType is the type's qualified name, as the value kinds spell one.
	resourceSecretType = resourcePackagePath + "." + secretTypeName
	// secretGoType is the type as a source file of another package writes it.
	secretGoType = "resource." + secretTypeName
)

// isSecretType reports whether a field's Go type is resource.Secret or a pointer to one.
func isSecretType(goType types.Type) bool {
	t := types.Unalias(goType)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}

	return isNamedType(t, resourcePackagePath, secretTypeName)
}

// HoldsSecret reports whether the field is a stored secret: declared conditions:"secret"
// and typed resource.Secret, so its column holds the value sealed.
func (f *resourceField) HoldsSecret() bool {
	return f.IsSecret() && isSecretType(f.GoType())
}

// PatchValueType is the type a typed patch takes the field's value in, and its write
// struct declares: the field's own, or, for a stored secret, the text it is given as
// (string, or *string where the field is a pointer).
func (f *resourceField) PatchValueType() string {
	if f.HoldsSecret() {
		return f.secretTextType()
	}

	return f.ResolvedType()
}

// RequestType is the type a handler's patch request struct declares the field in: the
// field's own, package-qualified, or, for a stored secret, the text a request carries.
func (f *resourceField) RequestType() string {
	if f.HoldsSecret() {
		return f.secretTextType()
	}

	return f.Type()
}

// RequestUnderlyingType is the underlying type of RequestType: the field's own, or a
// string for a stored secret.
func (f *resourceField) RequestUnderlyingType() string {
	if f.HoldsSecret() {
		return stringGoType
	}

	return f.UnderlyingType()
}

// secretTextType is the text form of a stored secret's value: nullable where the field
// is.
func (f *resourceField) secretTextType() string {
	if f.IsPointer() {
		return "*" + stringGoType
	}

	return stringGoType
}

// SecretFields are the resource's stored secrets, in field order: each gets a generated
// Open<Field> method on the resource struct.
func (r *resourceInfo) SecretFields() []*resourceField {
	var fields []*resourceField
	for _, f := range r.Fields {
		if f.HoldsSecret() {
			fields = append(fields, f)
		}
	}

	return fields
}

// SecretContext are the fields a secret of the resource is bound to: its primary key,
// column by column in key order, whatever order the struct declares them in, so
// reordering the struct's fields never changes what a stored value opens on.
func (r *resourceInfo) SecretContext() []*resourceField {
	var keys []*resourceField
	for _, f := range r.PrimaryKeys() {
		keys = append(keys, f)
	}
	slices.SortStableFunc(keys, func(a, b *resourceField) int {
		return cmp.Compare(a.KeyOrdinalPosition, b.KeyOrdinalPosition)
	})

	return keys
}

// SecretKeyExpr spells the row's primary key in key order as a generated method of the
// resource struct reads it off its receiver, row.
func (r *resourceInfo) SecretKeyExpr() string {
	var expr strings.Builder
	expr.WriteString("resource.KeySet{}")
	for _, f := range r.SecretContext() {
		fmt.Fprintf(&expr, ".Add(%q, row.%s)", f.Name(), f.Name())
	}

	return expr.String()
}

// secretFieldRefusal is the refusal of a stored secret whose type or column would keep
// the value as it was given, and empty for one that is sealed. The field is typed
// resource.Secret, and its column is BYTES with room for the 42 bytes sealing adds.
func secretFieldRefusal(path string, field *resourceField) string {
	wanted := secretGoType
	if field.IsPointer() || field.IsNullable {
		wanted = "*" + secretGoType
	}

	switch length := resource.DeclaredLength(field.SpannerType); {
	case !isSecretType(field.GoType()):
		return fmt.Sprintf("%s: conditions:%q on a field typed %s: a stored secret is kept encrypted, and the field's type is what says so; type the field %s and declare its column BYTES(n), n the longest value it takes plus %d, or BYTES(MAX)", path, secretCondition, field.Type(), wanted, resource.SecretOverhead)
	case !strings.HasPrefix(field.SpannerType, "BYTES("):
		return fmt.Sprintf("%s: conditions:%q on a %s column: a stored secret is encrypted bytes; declare the column BYTES(n), n the longest value it takes plus %d, or BYTES(MAX)", path, secretCondition, field.SpannerType, resource.SecretOverhead)
	case length != 0 && length <= resource.SecretOverhead:
		return fmt.Sprintf("%s: conditions:%q on a %s column: sealing adds %d bytes to a value, so the column has room for none; declare it BYTES(n), n the longest value it takes plus %d, or BYTES(MAX)", path, secretCondition, field.SpannerType, resource.SecretOverhead, resource.SecretOverhead)
	default:
		return ""
	}
}

// undeclaredSecretRefusal is the refusal of a field typed resource.Secret that does not
// declare conditions:"secret": the declaration is what keeps the field off the wire and
// has the write path seal it, and the type alone does neither.
func undeclaredSecretRefusal(path string, field *resourceField) string {
	return fmt.Sprintf("%s: typed %s and not declared conditions:%q: the declaration is what keeps the field from being served and has its value sealed when it is written; add it", path, field.Type(), secretCondition)
}

// generateHolders writes the resources package's two holders files: the file holders
// the orphaned-file cleanup reads, and the secret holders a retirement of a version of
// the application secret reads and rewrites.
func (r *resourceGenerator) generateHolders() error {
	if err := r.generateFileHolders(); err != nil {
		return err
	}

	return r.generateSecretHolders()
}

// generateSecretHolders writes the resources package's zz_gen_secret_holders.go:
// SecretHolders(), every resource that stores a secret. It is written on every
// application, so the migrate command calls it unconditionally.
func (r *resourceGenerator) generateSecretHolders() error {
	begin := time.Now()
	destinationFilePath := filepath.Join(r.resource.Dir(), generatedGoFileName(secretHoldersOutputName))
	if err := r.writeFormattedGoFile(destinationFilePath, "sealedHoldersTemplate", sealedHoldersTemplate, r.secretHoldersData()); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}
	log.Printf("Generated secret holders file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

// secretHoldersData gathers the holders: the resources with a stored secret, in
// resource order. A view takes no writes and declares no secret.
func (r *resourceGenerator) secretHoldersData() *secretHoldersData {
	data := &secretHoldersData{Source: r.resource.Dir(), Package: r.resource.Package(), Holders: nil}
	for _, res := range r.resources {
		if res.IsVirtual || len(res.SecretFields()) == 0 {
			continue
		}
		data.Holders = append(data.Holders, res.Name())
	}

	return data
}

// servedSecretFields names every stored secret of the application's resources, as
// Struct.Field in resource order: what the generated router requires the resource
// client's secret keys for before the server starts.
func (r *resourceGenerator) servedSecretFields() []string {
	var fields []string
	for _, res := range r.resources {
		for _, f := range res.SecretFields() {
			fields = append(fields, res.Name()+"."+f.Name())
		}
	}

	return fields
}
