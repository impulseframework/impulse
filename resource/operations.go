package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// OperationType defines the type of a patch operation (add, patch, remove, and the two
// that decide from whether the row is there: patch-or-add and add-or-skip).
type OperationType string

const (
	// OperationCreate corresponds to an "add" operation.
	OperationCreate OperationType = "add"
	// OperationUpdate corresponds to a "patch" operation.
	OperationUpdate OperationType = "patch"
	// OperationDelete corresponds to a "remove" operation.
	OperationDelete OperationType = "remove"
	// OperationUpdateOrCreate corresponds to a "patch-or-add" operation: an update of
	// the row its path names, and a create of that row when it is not there. A handler
	// asks the decoder which of the two it is (Decoder.DecideOperation: the caller's
	// Update grant on the resource, then a look for the row inside the transaction) and
	// runs the operation as the patch or the add it turned out to be, so everything a
	// patch or an add is held to holds for it.
	OperationUpdateOrCreate OperationType = "patch-or-add"
	// OperationCreateOrSkip corresponds to an "add-or-skip" operation: a create of the
	// row its path names that leaves the row exactly as it is when it is there. A
	// handler runs it as the add it is (AsCreate) with the create patch marked OrSkip.
	OperationCreateOrSkip OperationType = "add-or-skip"
)

// Operation represents a single operation within a batch request, containing its type and a corresponding http.Request.
type Operation struct {
	Type       OperationType
	Req        *http.Request
	pathPrefix string
}

// ReqWithPattern creates a new http.Request from the operation, applying a new URL pattern to its context.
// This is useful when a batch operation's path contains more segments than the initial prefix pattern.
func (o *Operation) ReqWithPattern(pattern string, opts ...Option) (*http.Request, error) {
	var os options
	for _, opt := range opts {
		os = opt(os)
	}

	method, err := httpMethod(string(o.Type))
	if err != nil {
		return nil, err
	}

	ctx, _, err := withParams(o.Req.Context(), method, pattern, o.Req.URL.Path, o.pathPrefix, os)
	if err != nil {
		return nil, err
	}

	return o.Req.WithContext(ctx), nil
}

// AsCreate returns a copy of the operation that runs as an add: what a patch-or-add
// whose row is not there turns into, and what an add-or-skip is decoded as. The request,
// with the key its path carries, is the operation's own.
func (o *Operation) AsCreate() *Operation {
	return &Operation{Type: OperationCreate, Req: o.Req, pathPrefix: o.pathPrefix}
}

// AsUpdate returns a copy of the operation that runs as a patch: what a patch-or-add
// whose row is there turns into.
func (o *Operation) AsUpdate() *Operation {
	return &Operation{Type: OperationUpdate, Req: o.Req, pathPrefix: o.pathPrefix}
}

// PathDepth returns the number of segments in the operation's path ("/organizations"
// → 1, "/organizations/abc" → 2). The consolidated handler uses it to distinguish
// operations on the resource named like the domain route segment (the tenant record,
// depth ≤ 2) from domain descents (depth ≥ 3) — disjoint by construction because the
// segment-named resource is required to have a single primary key.
func (o *Operation) PathDepth() int {
	trimmed := strings.Trim(o.Req.URL.Path, "/")
	if trimmed == "" {
		return 0
	}

	return strings.Count(trimmed, "/") + 1
}

// WithPrefixPattern returns a copy of the operation re-anchored to a longer path
// prefix: the pattern is prefix-matched against the operation's path, binding its
// params into the request context, and the copy's create-path semantics use the new,
// longer prefix. The consolidated handler uses it to descend into the domain route
// segment (binding the domain param) before dispatching on the resource segment.
func (o *Operation) WithPrefixPattern(pattern string) (*Operation, error) {
	method, err := httpMethod(string(o.Type))
	if err != nil {
		return nil, err
	}

	ctx, pathPrefix, err := withParams(o.Req.Context(), method, pattern, o.Req.URL.Path, o.pathPrefix, options{matchPrefix: true})
	if err != nil {
		return nil, err
	}

	return &Operation{Type: o.Type, Req: o.Req.WithContext(ctx), pathPrefix: pathPrefix}, nil
}

type patchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type options struct {
	requireCreatePath bool
	matchPrefix       bool
}

// Option is a function that configures the behavior of the Operations parser.
type Option func(opt options) options

// RequireCreatePath is an option that mandates a path for "add" operations.
func RequireCreatePath() Option {
	return func(o options) options {
		o.requireCreatePath = true

		return o
	}
}

// MatchPrefix is an option that allows matching only the prefix of an operation's path against the provided pattern.
func MatchPrefix() Option {
	return func(o options) options {
		o.matchPrefix = true

		return o
	}
}

// Operations parses a batch JSON patch request and yields an iterator of individual Operation objects.
func Operations(r *http.Request, pattern string, opts ...Option) iter.Seq2[*Operation, error] {
	var o options
	for _, opt := range opts {
		o = opt(o)
	}

	return func(yield func(r *Operation, err error) bool) {
		if !strings.HasPrefix(pattern, "/") {
			yield(nil, errors.New("pattern must start with /"))

			return
		}

		dec := json.NewDecoder(r.Body)

		for {
			t, err := dec.Token()
			if err != nil {
				yield(nil, bodyError(err))

				return
			}
			token := fmt.Sprintf("%s", t)
			if token == "[" {
				break
			}
			if strings.TrimSpace(token) != "" {
				yield(nil, httpio.NewBadRequestMessagef("expected start of array, got %q", t))

				return
			}
		}

		for dec.More() {
			var op patchOperation
			if err := dec.Decode(&op); err != nil {
				yield(nil, bodyError(err))

				return
			}

			if op.Value == nil {
				op.Value = []byte("{}")
			}

			method, err := httpMethod(op.Op)
			if err != nil {
				yield(nil, err)

				return
			}

			ctx, pathPrefix, err := withParams(r.Context(), method, pattern, op.Path, "", o)
			if err != nil {
				yield(nil, err)

				return
			}

			r2, err := http.NewRequestWithContext(ctx, method, op.Path, bytes.NewReader([]byte(op.Value)))
			if err != nil {
				yield(nil, err)

				return
			}

			// The name is read without regard to case, here as in httpMethod, so the
			// handlers and the decoder meet one spelling of each operation.
			if !yield(&Operation{Type: OperationType(strings.ToLower(op.Op)), Req: r2, pathPrefix: pathPrefix}, nil) {
				return
			}
		}

		t, err := dec.Token()
		if err != nil {
			yield(nil, httpio.NewBadRequestMessageWithErrorf(err, "failed find end of array"))

			return
		}

		token := fmt.Sprintf("%s", t)
		if token == "]" {
			return
		}
	}
}

func httpMethod(op string) (string, error) {
	switch OperationType(strings.ToLower(op)) {
	case OperationCreate:
		return http.MethodPost, nil
	case OperationUpdate, OperationUpdateOrCreate, OperationCreateOrSkip:
		// The two that decide from the row name it in their path, as a patch does.
		return http.MethodPatch, nil
	case OperationDelete:
		return http.MethodDelete, nil
	default:
		return "", httpio.NewBadRequestMessagef("unsupported operation %q", op)
	}
}

func withParams(ctx context.Context, method, pattern, path, pathPrefix string, o options) (context.Context, string, error) {
	servePath := path
	if o.matchPrefix {
		patternParts := strings.Split(pattern, "/")
		pathParts := strings.Split(path, "/")
		if len(pathParts) > len(patternParts) {
			servePath = strings.Join(pathParts[:len(patternParts)], "/")
		}
		pathPrefix = servePath
	}

	switch method {
	case http.MethodPost:
		if !o.matchPrefix {
			p := strings.TrimSuffix(path, "/")
			if o.requireCreatePath && p == pathPrefix {
				return ctx, pathPrefix, httpio.NewBadRequestMessage("path is required for create operation")
			}

			if !o.requireCreatePath && p != pathPrefix {
				return ctx, pathPrefix, httpio.NewBadRequestMessage("path is not allowed for create operation")
			}

			if p == pathPrefix {
				return ctx, pathPrefix, nil
			}
		}

		fallthrough
	case http.MethodPatch, http.MethodDelete:
		if path == "" {
			return ctx, pathPrefix, httpio.NewBadRequestMessage("path is required for patch, patch-or-add, add-or-skip and delete operations")
		}

		var chiContext *chi.Context
		r := chi.NewRouter()

		r.Handle(pattern, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			chiContext = chi.RouteContext(r.Context())
		}))
		r.ServeHTTP(httptest.NewRecorder(), &http.Request{Method: method, Header: make(map[string][]string), URL: &url.URL{Path: servePath}})

		if chiContext == nil {
			return ctx, pathPrefix, httpio.NewBadRequestMessagef("path %q does not match pattern %q", path, pattern)
		}

		ctx = context.WithValue(ctx, chi.RouteCtxKey, chiContext)
	}

	return ctx, pathPrefix, nil
}

func permissionFromType(typ OperationType) accesstypes.Permission {
	switch typ {
	case OperationCreate:
		return accesstypes.Create
	case OperationUpdate:
		return accesstypes.Update
	case OperationDelete:
		return accesstypes.Delete
	case OperationUpdateOrCreate, OperationCreateOrSkip:
		// Neither asks for a permission under its own name: a handler runs each as the
		// patch or the add it turns out to be, and a decode refuses one that reaches it
		// as it came (refuseUndecided).
	}

	panic("implementation error")
}

// bodyError answers a failed read of the operations body: 413 naming the limit when the
// body ran past the route's (limitError), the error itself otherwise.
func bodyError(err error) error {
	if tooLarge := limitError(err); tooLarge != nil {
		return tooLarge
	}

	return err
}
