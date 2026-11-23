package authpkg

// SignInSteps are the sign-in steps a generated application's server asks the login page
// for, as the server's pending status names them: the pending reason, and for the second
// factor the factor the policy requires after a colon. "confirmation" is the session
// package's password confirmation, which holds a directory identity that matched a user
// with a password; "mfa:email-code" is the mailed code the session library asks for while
// an auth's sign-in setting requires a second factor (sessioninfo.MethodEmailCode), the
// framework's own factor. The login page, @impulseframework/resource-angular, declares
// the steps it renders under impulseframework.signInSteps in its package.json
// (web/projects/resource-angular in this repository), and TestSignInSteps holds this list
// to that declaration and its second factor to sessioninfo.MethodEmailCode, so a release
// never pairs a server with a page that cannot render what the server asks: a server and
// a page of the same release are built from one tree.
var SignInSteps = []string{"confirmation", "mfa:email-code"}
