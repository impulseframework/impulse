package authpkg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestSignInSteps holds the sign-in steps the generated server asks the login page for
// together: the login page's declaration in its package.json (impulseframework.signInSteps
// in web/projects/resource-angular) equals SignInSteps, the list names the framework's
// confirmation, and its second-factor entries are exactly the factors requireSecondFactor
// requires in the auth template, so a factor added to the policy is added to the list and
// to the page's declaration in the same change.
func TestSignInSteps(t *testing.T) {
	t.Parallel()

	file := filepath.Join("..", "..", "..", "..", "web", "projects", "resource-angular", "package.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Impulseframework struct {
			SignInSteps []string `json:"signInSteps"`
		} `json:"impulseframework"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if !slices.Equal(pkg.Impulseframework.SignInSteps, SignInSteps) {
		t.Errorf("%s declares the sign-in steps %v under impulseframework.signInSteps and SignInSteps is %v: the login page and the server's policy move together", file, pkg.Impulseframework.SignInSteps, SignInSteps)
	}

	if !slices.Contains(SignInSteps, "confirmation") {
		t.Errorf("SignInSteps = %v names no confirmation, the framework's password confirmation every server may hold a sign-in for", SignInSteps)
	}

	// The factors the policy requires, as the template names them, to the step names the
	// server's pending status reports them under.
	factorNames := map[string]string{"sessioninfo.MethodEmailCode": "email-code"}
	policy := regexp.MustCompile(`func requireSecondFactor\([^{]*\{\s*return signin\.RequireMFA\(([^)]*)\), nil`).FindStringSubmatch(authTemplate)
	if policy == nil {
		t.Fatal("authTemplate writes no requireSecondFactor returning signin.RequireMFA(...)")
	}
	var required []string
	for _, arg := range strings.Split(policy[1], ",") {
		arg = strings.TrimSpace(arg)
		name, ok := factorNames[arg]
		if !ok {
			t.Fatalf("requireSecondFactor requires %s, which this test does not know the pending status's name of: add it to factorNames", arg)
		}
		required = append(required, "mfa:"+name)
	}
	var listed []string
	for _, step := range SignInSteps {
		if strings.HasPrefix(step, "mfa:") {
			listed = append(listed, step)
		}
	}
	if !slices.Equal(listed, required) {
		t.Errorf("SignInSteps names the second-factor steps %v and requireSecondFactor requires %v", listed, required)
	}
}
