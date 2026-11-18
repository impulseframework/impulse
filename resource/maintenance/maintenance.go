// Package maintenance is what an application serves while it is down for maintenance: a
// revision the deploy pipeline starts with the maintenance variable set, before a release
// that replaces or interrupts the database, so that the people using the application see
// a maintenance page instead of errors while the database is away. The framework owns it;
// no application writes it. The skeletons' mains, and every application's, call Requested
// before the site configuration is built: when the variable is set, the process serves the
// maintenance handler on PORT and returns, so no database client, no session store and no
// secret is opened. The handler answers every request 503 Service Unavailable with a
// Retry-After header and the marker header, X-Maintenance: 1, by which the browser
// library tells maintenance from an ordinary 503: a navigation gets the embedded page,
// which reloads on its own when the application becomes available again; anything
// else gets no body. The pipeline trusts none of this at deploy time: it probes the
// revision for the 503 and the marker before any traffic moves.
//
// The page says whose it is: the main hands Serve the brands of the browser applications
// the process serves, by mount path, and a navigation gets the page under the name and
// the logo of the application its path is under. The brands are the ones the browser
// applications declare (brand.json), which impulse brand writes into the application as
// pkg/brand, so the page reads no file and opens nothing to show them.
package maintenance

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"html/template"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
)

// Variable is the environment variable whose presence (any value but the empty string)
// puts the process in maintenance mode: the deploy pipeline sets it on a maintenance
// revision, and the application's stack declares it empty on the service.
const Variable = "APP_MAINTENANCE"

// Header is the marker every maintenance answer carries, with HeaderValue, by which a
// 503 from maintenance is told from an ordinary 503 (an overloaded or failing revision).
const (
	Header      = "X-Maintenance"
	HeaderValue = "1"
)

// The browser's fetch metadata that names a navigation: a request for a document.
const (
	fetchDest     = "Sec-Fetch-Dest"
	fetchMode     = "Sec-Fetch-Mode"
	destDocument  = "document"
	modeNavigate  = "navigate"
	acceptHeader  = "Accept"
	acceptingHTML = "text/html"
)

// retryAfter is how long an answer tells its caller to wait before trying again; checkBack
// is how often the page itself checks, in seconds, which the page reads from its markup.
const (
	retryAfter   = 30 * time.Second
	portVariable = "PORT"
	defaultPort  = "8080"
	// shutdownGrace is how long Serve lets the requests in flight finish once it is told
	// to stop, the way the application's own server does.
	shutdownGrace = 10 * time.Second
	// readHeaderTimeout bounds a slow client, as any server of ours does.
	readHeaderTimeout = 10 * time.Second
)

// page is the maintenance page: the name and the logo of a brand where it is given one,
// and the page as it reads without where it is not.
//
//go:embed page.html
var page string

// rootMount is the mount path of a browser application at the root, under which every
// path is.
const rootMount = "/"

// Brand is a browser application's brand as the maintenance page shows it: the name it
// declares (brand.json) and its logo. impulse brand writes both into the application
// (pkg/brand), which is where a main takes them from.
type Brand struct {
	// Name is the application's name as people read it ("Harbor"); the page names no
	// application where it is empty.
	Name string
	// Logo is the logo, a PNG; the page shows none where it is empty.
	Logo []byte
}

// Brands are the brands of the browser applications a process serves, by the mount path
// of each as the generator program's WebApp option spells it: "/" for an application at
// the root, "/console" and "/portal" for two on one host. A navigation gets the page
// under the brand whose mount path its path is under, the longest where two match, so "/"
// is every path's brand unless another names it; a path under none gets the page with no
// name and no logo, as an application with no brands (nil) serves it everywhere.
type Brands map[string]Brand

// Requested reports whether the process was started in maintenance mode: the variable
// is set to anything but the empty string.
func Requested() bool {
	return os.Getenv(Variable) != ""
}

// Serve serves the maintenance handler on PORT (8080 when unset) until the context ends,
// then lets the requests in flight finish and returns nil. It is what a main calls in place
// of building the site configuration when Requested reports maintenance, with the brands
// of the browser applications the process serves (nil for a process serving none).
func Serve(ctx context.Context, brands Brands) error {
	handler, err := Handler(brands)
	if err != nil {
		return err
	}
	port := os.Getenv(portVariable)
	if port == "" {
		port = defaultPort
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", ":"+port)
	if err != nil {
		return errors.Wrapf(err, "net.Listen(): the maintenance server on port %s", port)
	}

	return serve(ctx, listener, handler)
}

// serve serves the handler on the listener until the context ends. It speaks HTTP/1.1 and
// unencrypted HTTP/2 (h2c) on the one listener, as resource/server does, because the
// maintenance revision runs the same image on the same service as the application: a
// service whose port is named h2c (the application's main imports resource/server) is
// spoken to in HTTP/2 by Cloud Run, and a maintenance server that answered HTTP/1.1 alone
// would be a 502 there, which the pipeline's probe refuses before any traffic moves.
func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout, Protocols: protocols}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(listener)
	}()
	select {
	case err := <-errs:
		return errors.Wrap(err, "http.Server.Serve()")
	case <-ctx.Done():
	}
	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(grace); err != nil {
		return errors.Wrap(err, "http.Server.Shutdown()")
	}

	return nil
}

// Handler answers every request 503 Service Unavailable with Retry-After and the marker: a
// navigation (a request for a document, as the browser's Sec-Fetch headers or its Accept
// header say) gets the maintenance page, under the brand whose mount path the request's
// path is under; anything else gets no body. Nothing is cached. A mount path that is not
// one (it does not start with "/", or ends with one and is not the root) is refused.
func Handler(brands Brands) (http.Handler, error) {
	pages, err := newPages(brands)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set(Header, HeaderValue)
		h.Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		h.Set("Cache-Control", "no-store")
		if !navigation(r) {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}
		body := pages.under(r.URL.Path)
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}), nil
}

// pages are the maintenance page as each brand's mount path gets it, rendered once: the
// mount paths longest first, so the first one a path is under is its own, and the page
// with no brand for a path under none.
type pages struct {
	mounts  []string
	byMount map[string][]byte
	plain   []byte
}

// newPages renders the page for every brand and for no brand.
func newPages(brands Brands) (*pages, error) {
	tmpl, err := template.New("page").Parse(page)
	if err != nil {
		return nil, errors.Wrap(err, "template.Parse(): the maintenance page")
	}
	plain, err := render(tmpl, Brand{})
	if err != nil {
		return nil, err
	}
	p := &pages{byMount: make(map[string][]byte, len(brands)), plain: plain}
	for mount, brand := range brands {
		if !strings.HasPrefix(mount, rootMount) || (mount != rootMount && strings.HasSuffix(mount, "/")) {
			return nil, errors.Newf("maintenance: the brand %q is given under %q, which is not a mount path: a mount path starts with \"/\" and ends without one, such as \"/\" or \"/console\"", brand.Name, mount)
		}
		body, err := render(tmpl, brand)
		if err != nil {
			return nil, err
		}
		p.mounts = append(p.mounts, mount)
		p.byMount[mount] = body
	}
	sort.Slice(p.mounts, func(i, j int) bool {
		if len(p.mounts[i]) != len(p.mounts[j]) {
			return len(p.mounts[i]) > len(p.mounts[j])
		}

		return p.mounts[i] < p.mounts[j]
	})

	return p, nil
}

// under returns the page a request for the path gets: the one of the longest mount path
// the path is under, or the page with no brand.
func (p *pages) under(requestPath string) []byte {
	for _, mount := range p.mounts {
		if mount == rootMount || requestPath == mount || strings.HasPrefix(requestPath, mount+"/") {
			return p.byMount[mount]
		}
	}

	return p.plain
}

// render writes the page for one brand. The logo rides in the page itself, as a data
// URL, since every request to a maintenance revision is answered 503 and none could
// fetch it.
func render(tmpl *template.Template, brand Brand) ([]byte, error) {
	data := struct {
		Name string
		Logo template.URL
	}{Name: strings.TrimSpace(brand.Name)}
	if len(brand.Logo) > 0 {
		// The URL is built here from the logo's bytes, so it is safe to mark as one.
		data.Logo = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(brand.Logo)) //nolint:gosec // G203: built from bytes, never from a request
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return nil, errors.Wrapf(err, "template.Execute(): the maintenance page of %q", brand.Name)
	}

	return b.Bytes(), nil
}

// navigation reports whether the request asks for a document: a GET or HEAD whose
// Sec-Fetch-Dest is document, whose Sec-Fetch-Mode is navigate, or, from a client without
// those headers, whose Accept names text/html.
func navigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch {
	case r.Header.Get(fetchDest) == destDocument, r.Header.Get(fetchMode) == modeNavigate:
		return true
	case r.Header.Get(fetchDest) != "" || r.Header.Get(fetchMode) != "":
		return false
	}

	return strings.Contains(r.Header.Get(acceptHeader), acceptingHTML)
}
