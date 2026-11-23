package authpkg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestSignInSteps holds the sign-in steps the generated server asks the login page for
// together: the login page's declaration in its package.json (impulseframework.signInSteps
// in web/projects/resource-angular) equals SignInSteps, the list names the framework's
// confirmation, and its second-factor entry is exactly the factor the session library
// asks for while an auth's sign-in setting requires one (sessioninfo.MethodEmailCode), so
// a factor the library asks for is added to the list and to the page's declaration in the
// same change.
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

	var listed []string
	for _, step := range SignInSteps {
		if strings.HasPrefix(step, "mfa:") {
			listed = append(listed, step)
		}
	}
	if required := []string{"mfa:" + string(sessioninfo.MethodEmailCode)}; !slices.Equal(listed, required) {
		t.Errorf("SignInSteps names the second-factor steps %v and the session library asks for %v", listed, required)
	}
}
