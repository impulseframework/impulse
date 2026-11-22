package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
)

// The waits of a development run: for the server to listen, for the processes to end once
// asked, and for the auth's mail to reach the server's output.
const (
	devListenWait = 5 * time.Minute
	devEndWait    = 10 * time.Second
	devMailWait   = 30 * time.Second
)

var (
	// devCodeLine finds the sign-in code in the line the development mail driver writes for
	// a code message: mail (not sent) to the address, subject "... sign-in code is 483 920".
	devCodeLine = regexp.MustCompile(`mail \(not sent\) to \S+, subject .*?sign-in code is (\d{3}) (\d{3})`)
	// devCodeQuery finds the value of a code in a query, which a request's log line may carry.
	devCodeQuery = regexp.MustCompile(`code=[^&\s"\\]+`)
	// devXSRFCookie reads the cookie the console echoes its XSRF token from, as its app.config.ts names it.
	devXSRFCookie = regexp.MustCompile(`withXsrfConfiguration\(\{\s*cookieName:\s*'([^']+)'`)
)

// signsInAsDeveloped starts the application the way its next steps tell its author to,
// without direnv and without overmind, and signs a person in through the registration the
// way the login page does: the development environment is the template with free ports;
// the processes are the Procfile's that run no package manager (the emulators and the
// server, which bootstraps and then serves); the browser goes to the registration's login
// route, follows the simulated directory to the development callback, which the browser
// project's proxy forwards to the server, and completes the code step where the sign-in
// waits for one, with the code the development mail driver wrote to the server's output.
// The session is then the username's, and the person's permission digest answers. Every
// process ends with the test, and nothing the auth mailed reaches the test's output.
func signsInAsDeveloped(t *testing.T, made *authoredApplication, registration, username string) {
	t.Helper()

	env, ports := developmentEnv(t, made)
	run := startDevelopment(t, made, env, ports)
	run.awaitListening(t, ports.server)
	b := newDevBrowser(t, made, run, ports.server)
	b.signIn(registration, username)
	if sess := b.session(); !sess.Authenticated || sess.Username != username {
		t.Fatalf("a development run signed in through the %s registration: the session is %+v, want authenticated as %s", registration, sess, username)
	}
	if status, body, _ := b.do(http.MethodGet, "/api/permission-digest", nil); status != http.StatusOK {
		t.Errorf("a development run's GET /api/permission-digest for %s: status %d: %s", username, status, body)
	}
}

// devPorts are the ports a development run listens on, each free when the test took it:
// the server's, and each emulator's the template names (empty where it names none).
type devPorts struct {
	server, spanner, firestore string
}

// emulators lists the emulators' ports the template names.
func (p *devPorts) emulators() []string {
	var ports []string
	for _, port := range []string{p.spanner, p.firestore} {
		if port != "" {
			ports = append(ports, port)
		}
	}

	return ports
}

// developmentEnv writes .envrc from the application's environment template, with the
// server's port and each emulator's set to a free port, as the author copies it, and
// answers the environment bash has once it has sourced that file in the application's
// root, with the ports it set.
func developmentEnv(t *testing.T, made *authoredApplication) ([]string, *devPorts) {
	t.Helper()

	template := made.read(t, ".envrc.template")
	ports := &devPorts{}
	for _, line := range []struct {
		re       *regexp.Regexp
		variable string
		port     *string
	}{
		{re: portLine, variable: "PORT", port: &ports.server},
		{re: emulatorLine, variable: "SPANNER_EMULATOR_PORT", port: &ports.spanner},
		{re: firestoreLine, variable: "FIRESTORE_EMULATOR_PORT", port: &ports.firestore},
	} {
		if !line.re.MatchString(template) {
			continue
		}
		*line.port = freePort(t)
		template = line.re.ReplaceAllString(template, "export "+line.variable+"="+*line.port)
	}
	if ports.server == "" {
		t.Fatal(".envrc.template names no server port (export PORT=)")
	}
	if err := os.WriteFile(filepath.Join(made.dir, ".envrc"), []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	// The environment holds the application secret the template makes, so the output is
	// read and never printed.
	cmd := osexec.CommandContext(t.Context(), "bash", "-c", "source ./.envrc >/dev/null && env -0") //nolint:gosec // a fixed command: bash sources the .envrc the test wrote from the made application's template
	cmd.Dir = made.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash could not source the .envrc written from .envrc.template: %v\n%s", err, stderr.String())
	}
	var env []string
	for entry := range strings.SplitSeq(string(out), "\x00") {
		if entry != "" {
			env = append(env, entry)
		}
	}

	return env, ports
}

// freePort answers a port on 127.0.0.1 that nothing listens on as the test takes it.
func freePort(t *testing.T) string {
	t.Helper()

	var config net.ListenConfig
	l, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listening on 127.0.0.1:0 gave the address %v", l.Addr())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	return strconv.Itoa(addr.Port)
}

// devOutput is a process's output, kept in memory and read while the process writes it.
type devOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write keeps what the process wrote.
func (o *devOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	n, err := o.buf.Write(p)
	if err != nil {
		return n, errors.Wrap(err, "bytes.Buffer.Write()")
	}

	return n, nil
}

// String answers what the process wrote so far.
func (o *devOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.buf.String()
}

// withheld answers what the process wrote up to the first line that holds the mail the
// auth wrote, where sign-in codes and links start, with the value of every code in a query
// blanked: what the test may print.
func (o *devOutput) withheld() string {
	out := o.String()
	if i := strings.Index(out, "mail (not sent)"); i >= 0 {
		out = out[:strings.LastIndex(out[:i], "\n")+1] + "[the rest is withheld: from here the output holds the mail the auth wrote, sign-in codes among it]\n"
	}

	return devCodeQuery.ReplaceAllString(out, "code=[withheld]")
}

// devProcess is one process of the Procfile, started in a process group of its own.
type devProcess struct {
	name, command string
	cmd           *osexec.Cmd
	out           *devOutput
	done          chan struct{}
}

// devRun is the development run's processes.
type devRun struct {
	procs []*devProcess
}

// startDevelopment starts each process of the Procfile that runs no package manager, by
// the command on its line, with bash in the application's root under the environment, and
// ends them all when the test ends: each process group is asked to end, then killed when
// it has not, and what a container tool left on the run's emulator ports is removed. When
// the test failed, each process's output is printed as far as it may be (withheld).
func startDevelopment(t *testing.T, made *authoredApplication, env []string, ports *devPorts) *devRun {
	t.Helper()

	names, err := goProcesses(made.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("the Procfile names no process that runs no package manager: nothing starts the server")
	}
	run := &devRun{}
	t.Cleanup(func() {
		run.end(t)
		run.removeContainers(t, ports)
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, p := range run.procs {
			t.Logf("the %s process wrote:\n%s", p.name, p.out.withheld())
		}
	})
	for line := range strings.Lines(made.read(t, "Procfile")) {
		name, command, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.HasPrefix(name, "#") || !slices.Contains(names, strings.TrimSpace(name)) {
			continue
		}
		p := &devProcess{name: strings.TrimSpace(name), command: strings.TrimSpace(command), out: &devOutput{}, done: make(chan struct{})}
		// The command is the made application's Procfile line, which the commands the test
		// runs wrote: the development processes the application's author starts. The test's
		// context ends before its cleanups run, and would kill bash alone, so the run ends
		// the process group itself.
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		p.cmd = osexec.CommandContext(context.WithoutCancel(t.Context()), "bash", "-c", p.command) //nolint:gosec // the made application's own Procfile line
		p.cmd.Dir = made.dir
		p.cmd.Env = env
		p.cmd.Stdout = p.out
		p.cmd.Stderr = p.out
		p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		p.cmd.WaitDelay = devEndWait
		if err := p.cmd.Start(); err != nil {
			t.Fatalf("starting the %s process: %v", p.name, err)
		}
		run.procs = append(run.procs, p)
		go func() {
			_ = p.cmd.Wait() // the process ends when the test ends it; how it ended is not held
			close(p.done)
		}()
	}

	return run
}

// awaitListening waits for the server to accept a connection on its port, failing the test
// when a process ends first or the server does not listen within devListenWait.
func (r *devRun) awaitListening(t *testing.T, port string) {
	t.Helper()

	deadline := time.Now().Add(devListenWait)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", "127.0.0.1:"+port)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}

			return
		}
		for _, p := range r.procs {
			select {
			case <-p.done:
				t.Fatalf("the %s process ended before the server listened on port %s", p.name, port)
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server did not listen on port %s within %s", port, devListenWait)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-tick.C:
		}
	}
}

// code waits for the sign-in code the auth mailed after the outputs were as long as
// marks says, and answers it.
func (r *devRun) code(t *testing.T, marks []int) string {
	t.Helper()

	deadline := time.Now().Add(devMailWait)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		for i, p := range r.procs {
			if m := devCodeLine.FindStringSubmatch(p.out.String()[marks[i]:]); m != nil {
				return m[1] + m[2]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no sign-in code reached the server's output within %s of the request", devMailWait)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-tick.C:
		}
	}
}

// marks answers how long each process's output is now.
func (r *devRun) marks() []int {
	marks := make([]int, 0, len(r.procs))
	for _, p := range r.procs {
		marks = append(marks, len(p.out.String()))
	}

	return marks
}

// end asks every process group to end, and kills each that has not after devEndWait.
func (r *devRun) end(t *testing.T) {
	t.Helper()

	for _, p := range r.procs {
		signalGroup(t, p, syscall.SIGTERM)
	}
	deadline := time.After(devEndWait)
	for _, p := range r.procs {
		select {
		case <-p.done:
		case <-deadline:
			for _, q := range r.procs {
				signalGroup(t, q, syscall.SIGKILL)
			}
			<-p.done
		}
	}
}

// signalGroup sends the signal to the process's group, which is gone once it has ended.
func signalGroup(t *testing.T, p *devProcess, sig syscall.Signal) {
	t.Helper()

	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("signaling the %s process group: %v", p.name, err)
	}
}

// removeContainers removes the containers a process's container tool still runs on the
// run's emulator ports. An emulator in a container can ignore the signal to end, as the
// container's first process, and killing the tool's client leaves the container running.
func (r *devRun) removeContainers(t *testing.T, ports *devPorts) {
	t.Helper()

	var tools []string
	for _, p := range r.procs {
		if tool, _, _ := strings.Cut(p.command, " "); (tool == "podman" || tool == "docker") && !slices.Contains(tools, tool) {
			tools = append(tools, tool)
		}
	}
	for _, tool := range tools {
		// The tool is podman or docker, the Procfile's own; the test's context has ended.
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		out, err := osexec.CommandContext(context.WithoutCancel(t.Context()), tool, "ps", "--format", "{{.ID}} {{.Ports}}").Output() //nolint:gosec // podman or docker, as the Procfile names it
		if err != nil {
			t.Errorf("%s ps: %v", tool, err)

			continue
		}
		for line := range strings.Lines(string(out)) {
			id, published, _ := strings.Cut(strings.TrimSpace(line), " ")
			for _, port := range ports.emulators() {
				if !strings.Contains(published, "127.0.0.1:"+port+"->") {
					continue
				}
				// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
				if out, err := osexec.CommandContext(context.WithoutCancel(t.Context()), tool, "rm", "-f", id).CombinedOutput(); err != nil { //nolint:gosec // podman or docker, as the Procfile names it, on a container of the run's
					t.Errorf("%s rm -f %s: %v\n%s", tool, id, err, out)
				}
				t.Logf("removed the container %s the run left on port %s", id, port)
			}
		}
	}
}

// devBrowser is a browser on a development run: a cookie jar that follows no redirect, and
// the cookie the console echoes its XSRF token from.
type devBrowser struct {
	t      *testing.T
	base   string
	xsrf   string
	client *http.Client
	run    *devRun
}

// newDevBrowser answers a browser on the server's port, with the XSRF cookie read from the
// made application's console.
func newDevBrowser(t *testing.T, made *authoredApplication, run *devRun, port string) *devBrowser {
	t.Helper()

	config := "web/console/src/app/app.config.ts"
	m := devXSRFCookie.FindStringSubmatch(made.read(t, config))
	if m == nil {
		t.Fatalf("%s names no XSRF cookie (withXsrfConfiguration)", config)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	return &devBrowser{
		t:    t,
		base: "http://127.0.0.1:" + port,
		xsrf: m[1],
		client: &http.Client{Jar: jar, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		run: run,
	}
}

// do issues one request as this browser, with the XSRF token, and answers the status, the
// body and where a redirect points.
func (b *devBrowser) do(method, target string, body []byte) (status int, respBody []byte, location string) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(b.t.Context(), method, b.base+target, bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range b.client.Jar.Cookies(req.URL) {
		if cookie.Name == b.xsrf {
			req.Header.Set("X-XSRF-TOKEN", cookie.Value)
		}
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, respBody, resp.Header.Get("Location")
}

// signIn signs the username in through the registration as the login page does: the
// login route, the development callback the simulated directory sends the browser to, at
// the server's port as the browser project's proxy forwards it, and the code step where
// the sign-in waits for one.
func (b *devBrowser) signIn(registration, username string) {
	b.t.Helper()

	if status, body, _ := b.do(http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		b.t.Fatalf("GET /api/user/session: status %d: %s", status, body)
	}
	login := "/api/user/" + registration + "/login?returnUrl=%2F"
	status, _, location := b.do(http.MethodGet, login, nil)
	if status != http.StatusFound {
		b.t.Fatalf("GET %s: status %d, want %d to the development callback", login, status, http.StatusFound)
	}
	// The callback carries the simulated directory's code: its query is never printed.
	callback, err := url.Parse(location)
	if err != nil {
		b.t.Fatalf("the %s login route sent the browser to an address that does not parse", registration)
	}
	status, _, landing := b.do(http.MethodGet, callback.RequestURI(), nil)
	if status != http.StatusFound {
		b.t.Fatalf("GET %s: status %d, want %d", callback.Path, status, http.StatusFound)
	}
	if strings.Contains(landing, "pending=") {
		b.secondFactor(username)
	}
	if sess := b.session(); !sess.Authenticated {
		b.t.Fatalf("the %s callback sent the browser to %s, and the session is not signed in", registration, landing)
	}
}

// secondFactor completes the code step as the login page does: the address given first
// where the sign-in asks for one, the code asked for, read from the server's output, and
// verified.
func (b *devBrowser) secondFactor(username string) {
	b.t.Helper()

	status, body, _ := b.do(http.MethodGet, "/api/user/pending", nil)
	if status != http.StatusOK {
		b.t.Fatalf("GET /api/user/pending: status %d: %s", status, body)
	}
	var pending struct {
		Reason    string `json:"reason"`
		EmailCode *struct {
			EnrollEmail bool `json:"enrollEmail"`
		} `json:"emailCode"`
	}
	if err := json.Unmarshal(body, &pending); err != nil {
		b.t.Fatalf("decoding the pending sign-in: %v: %s", err, body)
	}
	if pending.EmailCode == nil {
		b.t.Fatalf("the sign-in of %s waits for %s, with no sign-in code offered", username, pending.Reason)
	}
	marks := b.run.marks()
	if pending.EmailCode.EnrollEmail {
		address := username
		if !strings.Contains(address, "@") {
			address += "@example.com"
		}
		give, err := json.Marshal(map[string]string{"email": address})
		if err != nil {
			b.t.Fatal(err)
		}
		if status, body, _ := b.do(http.MethodPost, "/api/user/pending/email", give); status != http.StatusOK {
			b.t.Fatalf("giving %s for the sign-in code: status %d: %s", address, status, body)
		}
	} else if status, body, _ := b.do(http.MethodPost, "/api/user/pending/code", nil); status != http.StatusOK {
		b.t.Fatalf("asking for the sign-in code: status %d: %s", status, body)
	}
	verify, err := json.Marshal(map[string]any{"code": b.run.code(b.t, marks), "rememberDevice": false})
	if err != nil {
		b.t.Fatal(err)
	}
	if status, body, _ := b.do(http.MethodPost, "/api/user/pending/code/verify", verify); status != http.StatusOK {
		b.t.Fatalf("verifying the sign-in code of %s: status %d: %s", username, status, body)
	}
}

// devSession is the session route's answer: whether the browser is signed in, and as whom.
type devSession struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username"`
}

// session reads the session route as this browser.
func (b *devBrowser) session() devSession {
	b.t.Helper()

	status, body, _ := b.do(http.MethodGet, "/api/user/session", nil)
	if status != http.StatusOK {
		b.t.Fatalf("GET /api/user/session: status %d: %s", status, body)
	}
	var sess devSession
	if err := json.Unmarshal(body, &sess); err != nil {
		b.t.Fatalf("decoding the session: %v: %s", err, body)
	}

	return sess
}
