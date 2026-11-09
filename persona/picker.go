package persona

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/gofrs/uuid/v5"
)

// pickerPath is the picker's path under the issuer; the sign-in request's ID follows it
// (/pick/{request}), so nothing the picker redirects to is read from the request.
const pickerPath = "/pick"

// refusal is why a sign-in cannot go on, as the picker shows it: the status and the
// sentence the person reads. A server-side failure carries its cause, which is logged,
// and shows a generic sentence.
type refusal struct {
	status  int
	message string
	cause   error
}

// failed is a refusal for a server-side failure.
func failed(err error) *refusal {
	return &refusal{status: http.StatusInternalServerError, cause: err}
}

// pickerPage shows the signed-in person the cast of the application the browser came
// from, or signs them straight in as the persona the application named. A browser with
// no session is sent to the host's login page and comes back here.
func (s *Service) pickerPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := chi.URLParam(r, "request")
	actor, ok := s.cfg.Actor(r)
	if !ok {
		http.Redirect(w, r, s.cfg.LoginURL+"?"+url.Values{"returnUrl": {s.pickerReturn(requestID)}}.Encode(), http.StatusFound)

		return
	}
	req, reg, app, ref := s.pickable(ctx, requestID, actor)
	if ref != nil {
		s.refuse(ctx, w, ref)

		return
	}
	if req.Done {
		http.Redirect(w, r, s.callbackURL(ctx, req.ID), http.StatusFound)

		return
	}
	if req.LoginHint != "" {
		if p := reg.PersonaNamed(req.LoginHint); p != nil {
			s.complete(ctx, w, r, req, reg, app, p, actor)

			return
		}
	}
	s.renderPicker(w, req, reg, app)
}

// pick signs the person in as the persona the form named.
func (s *Service) pick(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor, ok := s.cfg.Actor(r)
	if !ok {
		s.refuse(ctx, w, &refusal{status: http.StatusUnauthorized, message: "Sign in first, then pick a persona."})

		return
	}
	if err := r.ParseForm(); err != nil {
		s.refuse(ctx, w, &refusal{status: http.StatusBadRequest, message: "The form could not be read."})

		return
	}
	req, reg, app, ref := s.pickable(ctx, chi.URLParam(r, "request"), actor)
	if ref != nil {
		s.refuse(ctx, w, ref)

		return
	}
	p := reg.PersonaNamed(r.PostForm.Get("login"))
	if p == nil {
		s.refuse(ctx, w, &refusal{status: http.StatusBadRequest, message: "That persona is not in this application's cast."})

		return
	}
	s.complete(ctx, w, r, req, reg, app, p, actor)
}

// pickerReturn is the picker's path for a request as the host serves it: the issuer's
// path, the picker's and the request's ID. The host's login page returns a browser that
// had no session to it, and the picker's forms post to it.
func (s *Service) pickerReturn(requestID string) string {
	return strings.TrimSuffix(s.issuer.Path, "/") + pickerPath + "/" + url.PathEscape(requestID)
}

// pickable loads what a pick needs and decides whether the person may make it: the live
// request, its deployment's registration, the application, and the host's word on the
// person's access.
func (s *Service) pickable(ctx context.Context, requestID string, actor *Actor) (req *Request, reg *Registration, app *Application, ref *refusal) {
	if requestID == "" {
		return nil, nil, nil, &refusal{status: http.StatusBadRequest, message: "The sign-in request is missing. Start again from the application."}
	}
	req, err := s.liveRequest(ctx, requestID)
	if err != nil {
		if errors.Is(err, ErrUnknownRequest) {
			return nil, nil, nil, &refusal{status: http.StatusNotFound, message: "The sign-in request has expired or was already used. Start again from the application."}
		}

		return nil, nil, nil, failed(err)
	}
	reg, err = s.store.Registration(ctx, req.ClientID)
	if err != nil {
		if errors.Is(err, ErrUnknownRegistration) {
			return nil, nil, nil, &refusal{status: http.StatusNotFound, message: "The application's deployment is no longer registered."}
		}

		return nil, nil, nil, failed(errors.Wrap(err, "persona.Store.Registration()"))
	}
	app, err = s.host.ApplicationByClient(ctx, reg.ApplicationClientID)
	if err != nil {
		if errors.Is(err, ErrUnknownApplication) {
			return nil, nil, nil, &refusal{status: http.StatusNotFound, message: "The application is not known any more."}
		}

		return nil, nil, nil, failed(errors.Wrap(err, "persona.Host.ApplicationByClient()"))
	}
	may, err := s.host.MayEnter(ctx, actor, app, &reg.Deployment)
	if err != nil {
		return nil, nil, nil, failed(errors.Wrap(err, "persona.Host.MayEnter()"))
	}
	if !may {
		return nil, nil, nil, &refusal{status: http.StatusForbidden, message: "You do not have access to the test environments of " + app.Name + "."}
	}

	return req, reg, app, nil
}

// complete marks the request done with the persona, records the sign-in, and sends the
// browser to the provider's callback, which issues the code to the application.
func (s *Service) complete(ctx context.Context, w http.ResponseWriter, r *http.Request, req *Request, reg *Registration, app *Application, p *Persona, actor *Actor) {
	now := s.now()
	if err := s.store.CompleteRequest(ctx, req.ID, p.Login, actor.ID, now); err != nil {
		s.refuse(ctx, w, failed(errors.Wrap(err, "persona.Store.CompleteRequest()")))

		return
	}
	id, err := uuid.NewV4()
	if err != nil {
		s.refuse(ctx, w, failed(errors.Wrap(err, "uuid.NewV4()")))

		return
	}
	entry := &SignIn{ID: id.String(), Deployment: reg.Deployment, Application: *app, Persona: p.Login, Actor: *actor, At: now}
	if err := s.store.RecordSignIn(ctx, entry); err != nil {
		s.refuse(ctx, w, failed(errors.Wrap(err, "persona.Store.RecordSignIn()")))

		return
	}
	http.Redirect(w, r, s.callbackURL(ctx, req.ID), http.StatusFound)
}

// refuse writes a refusal page. A server-side failure is logged and shown as a plain
// sentence.
func (*Service) refuse(ctx context.Context, w http.ResponseWriter, ref *refusal) {
	message := ref.message
	if ref.cause != nil {
		slog.ErrorContext(ctx, "persona: the sign-in could not go on", "error", ref.cause)
		message = "Something went wrong on our side. Try again in a moment."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(ref.status)
	_ = refusalTemplate.Execute(w, map[string]any{"Message": message})
}

// renderPicker writes the picker page.
func (s *Service) renderPicker(w http.ResponseWriter, req *Request, reg *Registration, app *Application) {
	where := reg.Environment
	if reg.PullRequest > 0 {
		where = "pull request " + strconv.Itoa(reg.PullRequest)
	}
	personas := make([]pickerPersona, 0, len(reg.Cast))
	for _, p := range reg.Cast {
		personas = append(personas, pickerPersona{Login: p.Login, Name: p.Name, Roles: strings.Join(p.Roles, ", ")})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = pickerTemplate.Execute(w, map[string]any{"Application": app.Name, "Where": where, "Action": s.pickerReturn(req.ID), "Personas": personas})
}

// pickerPersona is one row of the picker.
type pickerPersona struct {
	Login string
	Name  string
	Roles string
}

var pickerTemplate = template.Must(template.New("picker").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in to {{.Application}}</title>
<style>
body { margin: 0; padding: 2rem 1rem; font: 16px/1.5 system-ui, sans-serif; background: #f6f5f2; color: #1c1b19; }
main { max-width: 36rem; margin: 0 auto; }
h1 { font-size: 1.5rem; margin: 0 0 .25rem; }
p.where { margin: 0 0 1.5rem; color: #5c5955; }
form { margin: 0 0 .75rem; }
button { width: 100%; text-align: left; padding: .9rem 1rem; border: 1px solid #d6d2ca; border-radius: .5rem; background: #fff; font: inherit; cursor: pointer; }
button:hover { border-color: #1c1b19; }
button b { display: block; }
button span { color: #5c5955; font-size: .9rem; }
</style>
</head>
<body>
<main>
<h1>Sign in to {{.Application}}</h1>
<p class="where">The {{.Where}} environment. Pick who to sign in as.</p>
{{range .Personas}}<form method="post" action="{{$.Action}}">
<input type="hidden" name="login" value="{{.Login}}">
<button type="submit"><b>{{.Name}}</b><span>{{.Login}}{{if .Roles}} · {{.Roles}}{{end}}</span></button>
</form>
{{else}}<p>This deployment has no personas to sign in as.</p>{{end}}
</main>
</body>
</html>
`))

var refusalTemplate = template.Must(template.New("refusal").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Persona sign-in</title>
<style>
body { margin: 0; padding: 2rem 1rem; font: 16px/1.5 system-ui, sans-serif; background: #f6f5f2; color: #1c1b19; }
main { max-width: 36rem; margin: 0 auto; }
</style>
</head>
<body>
<main><p>{{.Message}}</p></main>
</body>
</html>
`))
