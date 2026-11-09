package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/ci"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/ledger"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// The commands an upgrade step runs in the application, and the name the pinned impulse
// runs under (go tool impulse).
const (
	goCommand        = "go"
	gitCommand       = "git"
	bunCommand       = "bun"
	impulseTool      = "impulse"
	upgradeCommitTyp = "upgrade"
	// generateCommand is the regeneration as the walk names it.
	generateCommand = "go generate ./..."
)

// subjectLimit is the longest commit subject a step writes, in characters: one that still
// reads in a one-line log. Past it the pins' versions leave the subject for the body.
const subjectLimit = 100

// toolPinNote is the body of the commit of the pin-only move, the walk's last: the
// running impulse is a release that added no step.
const toolPinNote = "the framework pin moves to the running impulse, the web roots' pins with it, and the owned files are rendered again from the code"

// handoffStagedSince is the first impulse release whose handoff takes the walk's staged
// tree (impulse handoff --staged). Every release of the framework as one module does, so
// it is the first; the distinction stays for a pinned release that would not, were one
// ever recorded: at a step checked by such a release, the walk reads its report back and
// writes the brief itself.
const handoffStagedSince = "v0.1.0"

func newUpgrade() *cobra.Command {
	var (
		f      transitionFlags
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Move the application through the ledger's steps after the one its pins stand at, then to the running impulse, one commit each",
		Long: `upgrade moves an application through the steps the ledger records after the one its pin
stands at, then to the running impulse, and commits each. Where the application stands is
read from the framework pin in its go.mod, the version it requires
github.com/impulseframework/impulse at, never from a file of its own: the latest step whose
release the pin reaches is the position, and every step after it is pending. Each pending
step is one commit: its recipes run (each detects the old form in the application and edits
only where it finds it, so running twice is safe), the pin moves to the step's release with
go get -tool on impulse's package (the tool and the libraries are one module, so one
requirement moves both; a pin already at or beyond the step's stays), go mod tidy runs and
go.mod is read back (a pin below the step's version after the move refuses the step, naming
what go.mod holds), the web roots' pins move to the release (the versions of
@impulseframework/resource, @impulseframework/resource-angular and
@impulseframework/resource-firestore are rewritten in place in each package.json that names
them, and bun install runs there so bun.lock records the registry versions; a pin at or
beyond the release stays; the walk refuses to start without bun when it moves them), the
owned files are rendered again, go generate runs, impulse check runs (the pinned impulse
renders and checks, through go tool impulse, when it is not the running one), and
everything is committed under "` + upgradeCommitTyp + `: impulse <version>". Last, when the
running impulse is a release the pin is still behind (one that added no step), the pin
moves to it the same way, web roots included, and the result is committed as
"` + upgradeCommitTyp + `: impulse <version>"; an impulse built from a checkout makes no such
move, and one older than the pin refuses, since the pinned one is the impulse to run. Run
the impulse to upgrade to without pinning it first (go run
github.com/impulseframework/impulse/cmd/impulse@<version> upgrade): go get -tool ahead of
the walk would skip the steps' recipes. A failing check, or a go generate that fails after
the move, stops the walk with the step's changes staged and the handoff brief written
(` + handoff.File + `); for a failed generation the brief quotes the generation's last lines
and says what the step moved, so the author learns what the step needs of the generator
program.
At a step checked by the pinned impulse, that impulse writes the brief from the staged
tree and, with --agent, runs the agent and verifies its work, through go tool impulse
handoff --staged, from impulse ` + handoffStagedSince + ` on; a pinned release before it leaves the
walk to write the brief from that release's report, and the agent to the user. A step the
agent cleared (its work verified clean) is committed under the step's own subject and the
walk goes on to the next step; it stops when the agent's work does not verify, or when no
agent ran. Then fix or hand off, commit, and run upgrade again; it resumes from whatever
go.mod says. No step is skipped, since a recipe is written against the shape the step
before it left behind.

Upgrade impulse first, then bedrock (bedrock upgrade), and land both in one change: bedrock's
checks read what the walk writes (the drivers' settings the configuration embeds, the release
file, the processes), and the walk reads nothing bedrock renders; the code after a step may
need what only the newer stack provides.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := &upgrader{
				exec:    check.OSExec{},
				steps:   ledger.Steps,
				running: check.RunningBuild(),
				verify: func(ctx context.Context, env *check.Env) []check.Result {
					return check.Run(ctx, env, check.All())
				},
				owned: func(a *app.App) ([]string, error) {
					outcome, err := ci.Write(a)

					return outcome.WrittenFiles(), err
				},
				out: cmd.OutOrStdout(),
				err: cmd.ErrOrStderr(),
			}

			return u.run(cmd.Context(), &f, dryRun)
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what the walk would do, and change nothing")

	return cmd
}

// execer runs the walk's commands: held until they end (Run), or streamed as they run
// (Stream), for the pinned impulse's handoff, whose agent talks as it works.
type execer interface {
	check.Execer
	check.Streamer
}

// upgrader walks an application through the ledger. Its parts are injected so the walk is
// tested without go, git or the checks.
type upgrader struct {
	exec    execer
	steps   []ledger.Step
	running check.Build
	// verify runs the checks on the application after a step; owned rewrites the owned
	// files from the code and reports whether anything changed.
	verify func(ctx context.Context, env *check.Env) []check.Result
	owned  func(a *app.App) ([]string, error)
	out    io.Writer
	err    io.Writer
}

// release is the running impulse's version when it was built from a module version, else
// "": a build from a checkout is no release.
func (u *upgrader) release() string {
	if u.running.FromModule && semver.IsValid(u.running.Version) {
		return u.running.Version
	}

	return ""
}

// plan is what a walk does: the pending steps, each moving the pin to the release that
// added it, and last the pin-only move, when the running impulse is a release the steps
// leave the pin behind.
type plan struct {
	// position is the index of the step the application stands at, -1 before the first.
	position int
	pending  []ledger.Step
	// pin is go.mod's framework pin, "" when it has none; release is the running
	// impulse's version when it was built from a module version, else "".
	pin     string
	release string
	// web is where the walk leaves the web pins: the browser packages at the release the
	// walk ends at; nil when it ends nowhere. webRoots are the application's web roots
	// naming the browser packages, where the walk moves them.
	web      map[string]string
	webRoots []check.WebRoot
}

// movesWeb reports whether the walk moves web pins: it ends at a release and the
// application has a web root to move them in.
func (p *plan) movesWeb() bool {
	return len(p.web) > 0 && len(p.webRoots) > 0
}

// afterSteps is the release the application pins once the pending steps are walked: the
// last pending step's, or go.mod's pin when none is pending.
func (p *plan) afterSteps() string {
	pin := p.pin
	for i := range p.pending {
		if !p.pending[i].Reached(pin) {
			pin = p.pending[i].Impulse
		}
	}

	return pin
}

// movesTool reports whether the walk ends with the pin-only move: the running impulse is
// a release and the steps leave the pin behind it.
func (p *plan) movesTool() bool {
	if p.release == "" {
		return false
	}
	pin := p.afterSteps()

	return pin == "" || semver.Compare(pin, p.release) < 0
}

// end is the release the application pins when the walk is done.
func (p *plan) end() string {
	if p.movesTool() {
		return p.release
	}

	return p.afterSteps()
}

// run reads the position and the tool pin, prints the plan, and walks it: the pending
// steps in order, then the tool-only move.
func (u *upgrader) run(ctx context.Context, f *transitionFlags, dryRun bool) error {
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	if err := refuseLegacyAuth(a); err != nil {
		return err
	}
	p, err := u.plan(a)
	if err != nil {
		return err
	}
	at := "before the ledger's first step"
	if p.position >= 0 {
		at = fmt.Sprintf("at step %d of %d", p.position+1, len(u.steps))
	}
	pinned := "no impulse"
	if p.pin != "" {
		pinned = framework.Label(p.pin)
	}
	if len(p.pending) == 0 && !p.movesTool() {
		fmt.Fprintf(u.out, "%s stands %s, the ledger's last, and pins %s, the running impulse: nothing to do.\n", appLabel(a), at, pinned)

		return nil
	}
	fmt.Fprintf(u.out, "%s stands %s and pins %s; the walk:\n", appLabel(a), at, pinned)
	for i := range p.pending {
		fmt.Fprintf(u.out, "  %s\n", planLine(u.steps, p.position+1+i))
	}
	if p.movesTool() {
		fmt.Fprintf(u.out, "  %s: %s\n", framework.Label(p.release), toolPinNote)
	}
	u.printWebRoots(p)
	if p.release == "" {
		fmt.Fprintf(u.out, "This impulse was built from a checkout (%s), so the pin-only move does not apply: the pin ends where the last step leaves it.\n", u.running.Version)
	}
	fmt.Fprintln(u.out)
	if dryRun {
		return nil
	}
	repo := handoff.For(a, u.exec)
	if err := repo.Check(ctx); err != nil {
		return err
	}
	dirty, err := repo.Dirty(ctx)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return errors.Newf("the working tree is not clean (%d path(s)): commit or stash first, so each step is one commit", len(dirty))
	}
	if p.movesWeb() {
		if out, err := u.exec.Run(ctx, a.Root, nil, bunCommand, "--version"); err != nil {
			return errors.Newf("bun could not run (bun --version: %s): the walk moves the browser packages' pins in each web root's package.json and runs bun install there, so bun.lock records them; install bun (https://bun.sh) or put it on PATH, and run impulse upgrade again; nothing moved", lastLine(append(out, []byte(err.Error())...)))
		}
	}
	for i := range p.pending {
		if err := u.step(ctx, f, repo, &p.pending[i], p.position+2+i); err != nil {
			return err
		}
	}
	if p.movesTool() {
		if err := u.moveTool(ctx, f, repo, p.release); err != nil {
			return err
		}
	}
	ends := p.end()
	if after, err := app.Discover(f.appDir); err == nil {
		// go.mod decides: another requirement may have carried the pin beyond the step.
		ends = ledger.Pin(after.GoMod)
	}
	fmt.Fprintf(u.out, "%s stands at step %d of %d and pins %s. Review the commits and open the pull request.\n", appLabel(a), len(u.steps), len(u.steps), framework.Label(ends))

	return nil
}

// refuseLegacyAuth refuses the walk, before anything moves, for an application whose
// generator program declares an auth with a single-method session type's identifier
// (generation.Password and its kin, types of the framework's earlier releases that this
// module does not carry): no recipe moves an application off them, so a step would only
// break its generator program and leave a brief nothing in the rules can satisfy. The
// sentence says what to do instead.
func refuseLegacyAuth(a *app.App) error {
	for _, g := range a.Generators {
		for _, l := range g.LegacyAuths() {
			return errors.Newf("%s declares the auth %s with %s (%s), a single-method session type of the framework's earlier releases, which this module does not carry: no recipe moves an application off it. Re-create the application from the skeleton (impulse new, then impulse add for its options) and bring its resources over; nothing moved", appLabel(a), l.Package, l.Text, l.Pos)
		}
	}

	return nil
}

// plan reads where the application stands and what the walk does; a pin newer than the
// running impulse is refused, since the pinned impulse is the one to run.
func (u *upgrader) plan(a *app.App) (*plan, error) {
	pin := ledger.Pin(a.GoMod)
	p := &plan{position: ledger.Position(u.steps, pin), pending: ledger.Pending(u.steps, pin), pin: pin, release: u.release()}
	if p.release != "" && p.pin != "" && semver.Compare(p.pin, p.release) > 0 {
		return nil, errors.Newf("go.mod pins the framework at %s, newer than the running impulse %s: run the pinned one (go tool impulse upgrade)", p.pin, p.release)
	}
	if end := p.end(); end != "" && (len(p.pending) > 0 || p.movesTool()) {
		p.web = framework.WebPins(end)
		roots, err := check.WebRoots(a, ledger.WebPackages)
		if err != nil {
			return nil, err
		}
		p.webRoots = roots
	}

	return p, nil
}

// printWebRoots says where the web pins move, for the plan: each web root naming the
// browser packages with the moves the walk ends with (from what it holds to the release
// the walk ends at), or that there is none to move them in. Nothing when the walk moves
// no pin.
func (u *upgrader) printWebRoots(p *plan) {
	if len(p.web) == 0 {
		return
	}
	if len(p.webRoots) == 0 {
		fmt.Fprintln(u.out, "No web root names the browser packages, so their pins have nowhere to move.")

		return
	}
	fmt.Fprintf(u.out, "The browser packages' pins move in %d web root(s):\n", len(p.webRoots))
	for i := range p.webRoots {
		fmt.Fprintf(u.out, "  %s: %s\n", p.webRoots[i].File, webRootLine(&p.webRoots[i], p.web))
	}
}

// webRootLine says what the walk does to one web root's pins: each package moved, from
// the spec the file holds to the version; one ahead of the version, which stays; one the
// file does not name, which nothing moves; or that the root is at the pins already.
func webRootLine(r *check.WebRoot, web map[string]string) string {
	var parts []string
	for _, name := range ledger.WebNames(web) {
		have := r.Spec(name)
		switch {
		case have == "":
			parts = append(parts, fmt.Sprintf("%s is not named, so nothing moves it (the step pins %s)", name, web[name]))
		case have == web[name]:
		case check.WebBehind(have, web[name]):
			parts = append(parts, fmt.Sprintf("%s %s to %s", name, have, web[name]))
		default:
			parts = append(parts, fmt.Sprintf("%s %s stays, ahead of %s", name, have, web[name]))
		}
	}
	if len(parts) == 0 {
		return "at the web pins already"
	}

	return strings.Join(parts, ", ")
}

// appLabel names the application after its module path's last segment.
func appLabel(a *app.App) string {
	if a.GoMod == nil || a.GoMod.Module == nil {
		return "the application"
	}

	return path.Base(a.GoMod.Module.Mod.Path)
}

// planLine says what a step does: its number, the pin it moves from the step before
// ("impulse v0.2.0", with the web pins), its recipes, and its note.
func planLine(steps []ledger.Step, i int) string {
	var prevWeb map[string]string
	prev := ""
	if i > 0 {
		prev, prevWeb = steps[i-1].Impulse, steps[i-1].Web()
	}
	s := &steps[i]
	line := fmt.Sprintf("step %d", i+1)
	if moves := s.Moves(prev); len(moves) > 0 {
		line += ", " + strings.Join(moves, ", ")
	}
	if moves := s.WebMoves(prevWeb); len(moves) > 0 {
		line += ", web " + strings.Join(moves, ", ")
	}
	if names := recipeNames(s); names != "" {
		line += " (" + names + ")"
	}

	return line + ": " + s.Note
}

// recipeNames lists a step's recipes as "recipe paging" or "recipes paging, filters", ""
// when it has none.
func recipeNames(s *ledger.Step) string {
	names := make([]string, 0, len(s.Recipes))
	for _, recipe := range s.Recipes {
		names = append(names, recipe.Name())
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "recipe " + names[0]
	default:
		return "recipes " + strings.Join(names, ", ")
	}
}

// moveTool is the pin-only move, after the steps: the framework pin moves to the running
// impulse, a release that added no step, the web roots' pins with it, and the move
// finishes as a step does: the owned files, the regeneration, the check, and the commit.
func (u *upgrader) moveTool(ctx context.Context, f *transitionFlags, repo handoff.Repo, release string) error {
	fmt.Fprintf(u.out, "=== %s: %s ===\n", framework.Label(release), toolPinNote)
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	if err := u.bumpPin(ctx, a, release); err != nil {
		return err
	}
	webMoves, err := u.bumpWebPins(ctx, a, framework.WebPins(release))
	if err != nil {
		return err
	}
	moves := append([]string{framework.Label(release)}, webMoves...)
	commit := &commit{label: "the " + framework.Label(release) + " pin step", subject: upgradeCommitTyp + ": " + framework.Label(release), body: toolPinNote, moved: moves, note: toolPinNote}

	return u.finish(ctx, f, repo, commit)
}

// step walks one step: the recipes, the pin move (the framework pin, then the web pins in
// every web root), and the finish; a failing check stops with the brief written.
func (u *upgrader) step(ctx context.Context, f *transitionFlags, repo handoff.Repo, s *ledger.Step, number int) error {
	fmt.Fprintf(u.out, "=== step %d: %s ===\n", number, s.Note)
	changes, meanings, err := u.recipes(ctx, f.appDir, s, number)
	if err != nil {
		return err
	}
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	moves := s.Moves(ledger.Pin(a.GoMod))
	if len(moves) > 0 {
		if err := u.bumpPin(ctx, a, s.Impulse); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(u.out, "%s is at %s already.\n", ledger.FrameworkModule, ledger.Pin(a.GoMod))
	}
	webMoves, err := u.bumpWebPins(ctx, a, s.Web())
	if err != nil {
		return err
	}
	moves = append(moves, webMoves...)
	moved := strings.Join(moves, ", ")
	if moved == "" {
		moved = fmt.Sprintf("step %d", number)
	}
	suffix, body := "", s.Note
	if names := recipeNames(s); names != "" {
		suffix = " (" + names + ")"
		body += "\n\nRecipes applied by impulse upgrade: " + strings.TrimPrefix(strings.TrimPrefix(names, "recipes "), "recipe ") + "."
	}
	subject := upgradeCommitTyp + ": " + moved + suffix
	if len(subject) > subjectLimit && len(moves) > 0 {
		// The versions go in the body when the subject would run long: the packages alone
		// name the commit.
		subject = upgradeCommitTyp + ": " + strings.Join(modulesOf(moves), ", ") + suffix
		body += "\n\nPins moved: " + moved + "."
	}
	commit := &commit{label: fmt.Sprintf("step %d (%s)", number, moved), subject: subject, body: body, changes: changes, meanings: meanings, release: s.Impulse, moved: moves, note: s.Note}

	return u.finish(ctx, f, repo, commit)
}

// modulesOf names what the moves move ("impulse v0.2.0" names impulse, "@impulseframework/resource
// 0.2.0" the package).
func modulesOf(moves []string) []string {
	modules := make([]string, 0, len(moves))
	for _, move := range moves {
		modules = append(modules, strings.Fields(move)[0])
	}

	return modules
}

// commit is what a step commits and how the step is named when its check fails.
type commit struct {
	label   string
	subject string
	body    string
	// changes and meanings are what the step's recipes did and what it means, for the
	// brief when the check fails.
	changes  []string
	meanings []string
	// release is the impulse release that added the step, "" for the pin-only move.
	// When it is set and go.mod's pin is not the running release, the pinned impulse
	// renders the owned files and checks the step, through go tool impulse.
	release string
	// moved are the pins the step moved ("impulse v0.2.0", "@impulseframework/resource
	// 0.2.0") and note what the step changes, for the brief when go generate fails after
	// the move.
	moved []string
	note  string
}

// afterGenerateFailed is the commit's account for the brief when go generate ./... failed
// after the move: what the step moved and that the generation, not the check, is what
// failed, added to the recipes' changes, and what the step changes added to their
// meanings, so the application's author learns what the step needs of the generator
// program and of what it generates from.
func (c *commit) afterGenerateFailed() *commit {
	after := *c
	did := "ran the step's edits, with no pin to move,"
	if len(c.moved) > 0 {
		did = "moved " + strings.Join(c.moved, ", ")
	}
	after.changes = append(slices.Clone(c.changes), fmt.Sprintf("`impulse upgrade` %s and staged the step; then `%s` failed, so `%s` did not run. The generation's last lines are under \"The failing checks\". The generator program, and what it generates from, build against the moved pins, so what they declare may need the form the step brings, described under \"What it means\".", did, generateCommand, handoff.CheckCommand))
	after.meanings = append(slices.Clone(c.meanings), "What the step changes for the application: "+c.note+".")

	return &after
}

// outcome is how a step's render, regeneration and check ended: the results, whether they
// failed, and whether the regeneration is what failed, so the check never ran and the
// results hold the generation's failure as the regen check reports it.
type outcome struct {
	results  []check.Result
	failed   bool
	generate bool
}

// finish ends a step after its edits: the application is read again, the owned files are
// rendered, go generate runs, the checks run (by the pinned impulse when the step is not
// the running release's), everything is staged, and a clean check is committed; a failing
// one, or a generation that failed before it, is handed off and stops the walk.
func (u *upgrader) finish(ctx context.Context, f *transitionFlags, repo handoff.Repo, c *commit) error {
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	pinned := ""
	if pin := ledger.Pin(a.GoMod); c.release != "" && pin != u.release() {
		pinned = pin
	}
	var o *outcome
	if pinned != "" {
		o, err = u.checkPinned(ctx, f, a, pinned)
	} else {
		o, err = u.check(ctx, f, a)
	}
	if err != nil {
		return err
	}
	if err := repo.StageAll(ctx); err != nil {
		return err
	}
	if o.failed {
		return u.handoff(ctx, f, repo, a, c, o, pinned)
	}
	if out, err := u.exec.Run(ctx, a.Root, nil, gitCommand, "commit", "-q", "-m", c.subject, "-m", c.body); err != nil {
		return errors.Wrapf(err, "git commit: %s", lastLine(out))
	}
	fmt.Fprintf(u.out, "Committed: %s.\n\n", c.subject)

	return nil
}

// check renders the owned files, regenerates, and runs the checks in process, by the
// running impulse, and reports them; a generation that failed is the outcome, reported as
// the regen check reports it, and the checks do not run.
func (u *upgrader) check(ctx context.Context, f *transitionFlags, a *app.App) (*outcome, error) {
	written, err := u.owned(a)
	if err != nil {
		return nil, err
	}
	if len(written) > 0 {
		fmt.Fprintf(u.out, "Rewrote %s from the code.\n", ci.List(written))
	}
	if !f.skipGenerate {
		o, err := u.regenerate(ctx, a)
		if err != nil || o != nil {
			return o, err
		}
	}
	results := u.verify(ctx, u.env(f, a))
	check.Report(u.out, results)

	return &outcome{results: results, failed: check.Failed(results)}, nil
}

// checkPinned renders the owned files and runs the check through the impulse go.mod pins
// (go tool impulse), so a step is rendered and checked by the release that added it, and
// reports how it ended. The regeneration is the application's own generator program,
// built against its pins, and runs as always; one that failed is the outcome, and the
// check does not run. A pinned release that writes its own brief (handoffStaged) says how
// its check ended by its exit status, failedExit for a failing check, and its report is
// printed and not read, since the brief comes from that release's own check; an earlier
// release's report is read back into results for the brief the walk writes. A check the
// pinned impulse could not run at all is an error, not a failure.
func (u *upgrader) checkPinned(ctx context.Context, f *transitionFlags, a *app.App, pinned string) (*outcome, error) {
	fmt.Fprintf(u.out, "The pinned impulse %s renders the owned files and checks the step.\n", pinned)
	out, err := u.exec.Run(ctx, a.Root, nil, goCommand, "tool", impulseTool, "render")
	if err != nil {
		return nil, errors.Wrapf(err, "go tool impulse render: %s", lastLine(out))
	}
	fmt.Fprint(u.out, string(out))
	if !f.skipGenerate {
		o, err := u.regenerate(ctx, a)
		if err != nil || o != nil {
			return o, err
		}
	}
	args := []string{"tool", impulseTool, "check", "--fix"}
	if f.skipGenerate {
		args = append(args, "--skip-generate")
	}
	out, err = u.exec.Run(ctx, a.Root, nil, goCommand, args...)
	var (
		results []check.Result
		failed  bool
	)
	if handoffStaged(pinned) {
		failed = err != nil && exitCode(err) == failedExit
	} else {
		results = check.ParseReport(out)
		failed = check.Failed(results)
	}
	if err != nil && !failed {
		return nil, errors.Wrapf(err, "go tool impulse check: %s", lastLine(out))
	}
	fmt.Fprint(u.out, string(out))

	return &outcome{results: results, failed: failed}, nil
}

// handoffStaged reports whether the pinned impulse writes the brief for the walk's staged
// tree: a release at or after handoffStagedSince. A pseudo-version names a commit between
// two releases and sorts before the later one, so it reads as a release without the flag.
func handoffStaged(pinned string) bool {
	return semver.IsValid(pinned) && semver.Compare(pinned, handoffStagedSince) >= 0
}

// exitCode is the status a command that ran exited with, read through the error its
// runner returned, or -1 for a command that did not run.
func exitCode(err error) int {
	var exited interface{ ExitCode() int }
	if errors.As(err, &exited) {
		return exited.ExitCode()
	}

	return -1
}

// env is the check environment of a step: the checks apply their mechanical remedies.
func (u *upgrader) env(f *transitionFlags, a *app.App) *check.Env {
	return &check.Env{App: a, Exec: u.exec, SkipGenerate: f.skipGenerate, Fix: true, Out: u.err, Ledger: ledger.Current{}}
}

// handoff stops the step with its changes staged and the brief written for the failing
// check, or for the generation that failed before it, when the brief says so and names
// what the step moved. At a step checked in process the brief is written from the
// results, and the agent is launched when asked (--agent). At a step checked by a pinned
// impulse that writes its own brief (handoffStaged), that impulse does it all from the
// staged tree, through go tool impulse handoff --staged: the brief, the agent and the
// verification after it are the release that added the step's (its check runs the
// generation again, so its brief quotes the failure). An earlier pinned release's brief
// is written here from its report, and the agent is left to the user, since the
// verification after it would be the running impulse's. With --agent, a step the agent
// cleared is committed here and the walk continues; the walk stops only when the agent's
// work did not verify, or when no agent ran.
func (u *upgrader) handoff(ctx context.Context, f *transitionFlags, repo handoff.Repo, a *app.App, c *commit, o *outcome, pinned string) error {
	left := "the check"
	if o.generate {
		c, left = c.afterGenerateFailed(), generateCommand
	}
	var err error
	switch {
	case pinned == "":
		err = u.handoffInProcess(ctx, f, repo, a, c, o.results, f.agent)
	case handoffStaged(pinned):
		err = u.handoffPinned(ctx, f, a, c, pinned)
	default:
		if f.agent {
			fmt.Fprintf(u.out, "The agent is not launched at a step checked by the pinned impulse %s, a release before %s; run it on the brief by hand.\n", pinned, handoffStagedSince)
		}
		err = u.handoffInProcess(ctx, f, repo, a, c, o.results, false)
	}
	if err != nil {
		return err
	}
	if f.agent && (pinned == "" || handoffStaged(pinned)) {
		// The agent ran on the brief and its work verified clean (a handoff that did not
		// verify returned its error), so the step is committed and the walk goes on.
		return u.commitCleared(ctx, repo, a, c, left)
	}

	return errors.Newf("%s left %s failing; its changes are staged and the brief is at %s. When the check is clean, commit and run impulse upgrade again: it resumes from go.mod", c.label, left, handoff.File)
}

// commitCleared commits a step the agent cleared: with --agent, the handoff ran the agent
// on the brief and verified its work clean, so the step is committed under its own
// subject, the body saying the agent did the wiring, and the walk continues to the next
// step instead of stopping for a commit and a second run.
func (u *upgrader) commitCleared(ctx context.Context, repo handoff.Repo, a *app.App, c *commit, left string) error {
	if err := repo.StageAll(ctx); err != nil {
		return err
	}
	body := c.body + "\n\nThe step's move left " + left + " failing; the agent did the wiring under the brief, and its work verified clean."
	if out, err := u.exec.Run(ctx, a.Root, nil, gitCommand, "commit", "-q", "-m", c.subject, "-m", body); err != nil {
		return errors.Wrapf(err, "git commit: %s", lastLine(out))
	}
	fmt.Fprintf(u.out, "Committed: %s (the agent cleared the step).\n\n", c.subject)

	return nil
}

// handoffInProcess writes the brief from the results, with the staged paths as the
// change under review, and launches and verifies the agent when launch says so.
func (u *upgrader) handoffInProcess(ctx context.Context, f *transitionFlags, repo handoff.Repo, a *app.App, c *commit, results []check.Result, launch bool) error {
	guard, err := handoff.Take(a, handoff.FromTree(a))
	if err != nil {
		return err
	}
	staged, err := repo.Staged(ctx)
	if err != nil {
		return err
	}
	brief := &handoff.Brief{App: a, Change: strings.Join(c.changes, "\n"), Meaning: strings.Join(c.meanings, "\n\n"), Results: results, Guard: guard, Staged: staged}
	ag := &handoff.Agent{Command: f.agentCommand, ExtraArgs: f.agentArgs}

	return completeHandoff(ctx, u.out, f.appDir, u.env(f, a), repo, brief, ag, launch)
}

// handoffPinned hands the staged step to the pinned impulse's handoff (go tool impulse
// handoff --staged), which runs its check again for the brief, writes it with each
// recipe's change and meaning passed through, and launches and verifies the agent when
// asked. Its output is streamed, since the agent talks as it works.
func (u *upgrader) handoffPinned(ctx context.Context, f *transitionFlags, a *app.App, c *commit, pinned string) error {
	does := "writes the brief"
	if f.agent {
		does = "writes the brief, runs the agent and verifies its work"
	}
	fmt.Fprintf(u.out, "The pinned impulse %s %s, through go tool impulse handoff --staged.\n", pinned, does)
	args := []string{"tool", impulseTool, "handoff", "--staged"}
	for _, change := range c.changes {
		args = append(args, "--change", change)
	}
	for _, meaning := range c.meanings {
		args = append(args, "--meaning", meaning)
	}
	if f.skipGenerate {
		args = append(args, "--skip-generate")
	}
	if f.agent {
		args = append(args, "--agent", "--agent-command", f.agentCommand)
		for _, arg := range f.agentArgs {
			args = append(args, "--agent-arg", arg)
		}
	}
	if out, err := u.exec.Stream(ctx, a.Root, u.out, goCommand, args...); err != nil {
		return errors.Wrapf(err, "go tool impulse handoff: %s", lastLine(out))
	}

	return nil
}

// recipes runs the step's recipes in order, each on the tree the one before it left, and
// answers what they changed and what the changes mean, for the brief.
func (u *upgrader) recipes(ctx context.Context, appDir string, s *ledger.Step, number int) (changes, meanings []string, err error) {
	for _, recipe := range s.Recipes {
		a, err := app.Discover(appDir)
		if err != nil {
			return nil, nil, err
		}
		found, err := recipe.Detect(ctx, a)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "recipe %s", recipe.Name())
		}
		if len(found) == 0 {
			fmt.Fprintf(u.out, "Recipe %s: nothing in the old form.\n", recipe.Name())

			continue
		}
		fmt.Fprintf(u.out, "Recipe %s finds %d place(s) in the old form:\n", recipe.Name(), len(found))
		for _, line := range found {
			fmt.Fprintf(u.out, "  - %s\n", line)
		}
		change, err := recipe.Apply(ctx, a, u.exec)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "recipe %s", recipe.Name())
		}
		change.Command = fmt.Sprintf("impulse upgrade (recipe %s, step %d)", recipe.Name(), number)
		writeChange(u.out, change)
		changes = append(changes, change.Text())
		meanings = append(meanings, recipe.Meaning())
	}

	return changes, meanings, nil
}

// bumpPin moves the framework pin to the release with go get -tool on impulse's package:
// the tool and the libraries are one module, so one requirement moves both, and go
// resolves the release against the application's whole requirement list, refusing in its
// own words a release no list holds. Then go mod tidy settles the requirements and go.mod
// is read back (holdPin).
func (u *upgrader) bumpPin(ctx context.Context, a *app.App, release string) error {
	fmt.Fprintf(u.out, "Pin %s to %s.\n", ledger.FrameworkModule, release)
	if out, err := u.exec.Run(ctx, a.Root, nil, goCommand, "get", "-tool", check.ImpulseModule+"@"+release); err != nil {
		return errors.Wrapf(err, "go get -tool %s@%s: %s", check.ImpulseModule, release, lastLine(out))
	}
	if err := u.tidy(ctx, a); err != nil {
		return err
	}

	return u.holdPin(a, release)
}

// holdPin reads go.mod after the move and holds the framework pin to the release. One
// below it was held back by another requirement of the application's, and one missing was
// dropped by the tidy, so nothing in the application needs the framework; either is
// refused, naming what go.mod holds and what the step pins, since committed, the walk
// would announce the version it asked for and read a position the application never
// reached. A pin beyond the release is carried by another requirement and stays, noted,
// as a pin bumped by hand ahead of its step does.
func (u *upgrader) holdPin(a *app.App, release string) error {
	data, err := os.ReadFile(a.Abs("go.mod"))
	if err != nil {
		return errors.Wrap(err, "os.ReadFile()")
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return errors.Wrap(err, "modfile.Parse()")
	}
	have := ledger.Pin(mod)
	switch {
	case have == "":
		return errors.Newf("the step pins %s at %s but go.mod holds no requirement for it after the move: go mod tidy dropped it, so nothing in the application needs the framework, and the step cannot be read from the pin", ledger.FrameworkModule, release)
	case semver.Compare(have, release) < 0:
		return errors.Newf("the step pins %s at %s but go.mod holds %s after the move: another requirement holds the pin back", ledger.FrameworkModule, release, have)
	case semver.Compare(have, release) > 0:
		fmt.Fprintf(u.out, "%s moved to %s, beyond the step's %s: another requirement carries it.\n", ledger.FrameworkModule, have, release)
	}

	return nil
}

// bumpWebPins moves every web root's pins of the browser packages to the release's, after
// the Go pin: each package.json naming the library has the versions it holds behind the step's
// rewritten in place (a spec at or beyond the step's stays, as a Go pin bumped ahead of its
// step does, and nothing else in the file changes), then bun install runs in the root so
// bun.lock records the registry versions, and the step's commit carries both files. It
// answers the pins moved in any root, "@impulseframework/resource-angular 0.0.16" each, for the
// commit. A bun install that fails stops the step with the edit in the tree and a sentence
// saying what to do.
func (u *upgrader) bumpWebPins(ctx context.Context, a *app.App, web map[string]string) ([]string, error) {
	if len(web) == 0 {
		return nil, nil
	}
	roots, err := check.WebRoots(a, ledger.WebPackages)
	if err != nil {
		return nil, err
	}
	tree, err := os.OpenRoot(a.Root)
	if err != nil {
		return nil, errors.Wrap(err, "os.OpenRoot()")
	}
	defer tree.Close()
	moved := map[string]bool{}
	for i := range roots {
		r := &roots[i]
		data, err := tree.ReadFile(filepath.FromSlash(r.File))
		if err != nil {
			return nil, errors.Wrap(err, "os.Root.ReadFile()")
		}
		edited, moves := rewriteWebPins(data, web)
		for _, name := range ledger.WebNames(web) {
			if _, ok := moves[name]; ok {
				fmt.Fprintf(u.out, "Pin %s in %s to %s.\n", name, r.File, web[name])
				moved[name] = true
			} else if have := r.Spec(name); have != "" {
				fmt.Fprintf(u.out, "%s in %s is at %s already.\n", name, r.File, have)
			}
		}
		if len(moves) == 0 {
			continue
		}
		if err := tree.WriteFile(filepath.FromSlash(r.File), edited, 0o600); err != nil {
			return nil, errors.Wrap(err, "os.Root.WriteFile()")
		}
		fmt.Fprintf(u.out, "Running bun install in %s\n", r.Dir)
		if out, err := u.exec.Run(ctx, a.Abs(r.Dir), nil, bunCommand, "install"); err != nil {
			return nil, errors.Wrapf(err, "bun install in %s failed: %s; %s holds the step's web pins and bun.lock does not yet: fix what the install reports, run bun install there, and run impulse upgrade again once the tree is committed or restored", r.Dir, lastLine(out), r.File)
		}
	}
	webMoves := make([]string, 0, len(moved))
	for _, name := range ledger.WebNames(web) {
		if moved[name] {
			webMoves = append(webMoves, name+" "+web[name])
		}
	}

	return webMoves, nil
}

// rewriteWebPins moves a package.json's pins of the packages to the versions, in place:
// every "<package>": "<spec>" whose spec stands behind the version (an earlier release, a
// range, a tag, a yalc attachment) takes the version, in whichever section it appears
// (dependencies, peerDependencies, overrides), and nothing else in the file changes, so
// the file keeps its formatting. It answers the packages moved and the spec each moved
// from (the first found).
func rewriteWebPins(data []byte, pins map[string]string) (edited []byte, moved map[string]string) {
	moved = map[string]string{}
	edited = data
	for _, name := range ledger.WebNames(pins) {
		re := regexp.MustCompile(`("` + regexp.QuoteMeta(name) + `"\s*:\s*")([^"]*)(")`)
		edited = re.ReplaceAllFunc(edited, func(m []byte) []byte {
			sub := re.FindSubmatch(m)
			spec := string(sub[2])
			if !check.WebBehind(spec, pins[name]) {
				return m
			}
			if _, ok := moved[name]; !ok {
				moved[name] = spec
			}

			return append(append(append([]byte{}, sub[1]...), pins[name]...), sub[3]...)
		})
	}

	return edited, moved
}

// tidy runs go mod tidy after the pins moved.
func (u *upgrader) tidy(ctx context.Context, a *app.App) error {
	if out, err := u.exec.Run(ctx, a.Root, nil, goCommand, "mod", "tidy"); err != nil {
		return errors.Wrapf(err, "go mod tidy: %s", lastLine(out))
	}

	return nil
}

// regenerate runs go generate and prints the schema warnings it raised; nil when it
// succeeded. A generation that ran and failed is the step's outcome, reported as the regen
// check reports it (the failing result, with the output's last lines), for the handoff;
// one that could not run at all is an error.
func (u *upgrader) regenerate(ctx context.Context, a *app.App) (*outcome, error) {
	fmt.Fprintf(u.out, "Running %s\n", generateCommand)
	out, err := u.exec.Run(ctx, a.Root, nil, goCommand, "generate", "./...")
	if err != nil {
		if exitCode(err) < 0 {
			return nil, errors.Wrapf(err, "%s: %s", generateCommand, lastLine(out))
		}
		results := []check.Result{check.GenerateFailed(out)}
		check.Report(u.out, results)

		return &outcome{results: results, failed: true, generate: true}, nil
	}
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(line, transition_.WarningPrefix) {
			fmt.Fprintf(u.out, "  %s", line)
		}
	}

	return nil, nil
}

// lastLine is the last non-empty line of a command's output, for an error.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	return strings.TrimSpace(lines[len(lines)-1])
}
