package access

import (
	"net/http"

	"github.com/impulseframework/impulse/httpio"
)

// Handlers provides HTTP handlers for managing roles and their members. The
// {domain} forms address memberships and roles held in one tenant domain; the
// EveryDomain forms address memberships held in every tenant domain, which
// have no domain to name.
type Handlers interface {
	AddRole() http.HandlerFunc
	AddRoleUsers() http.HandlerFunc
	AddRoleUsersEveryDomain() http.HandlerFunc
	DeleteRole() http.HandlerFunc
	DeleteRoleUsers() http.HandlerFunc
	DeleteRoleUsersEveryDomain() http.HandlerFunc
	RolePermissions() http.HandlerFunc
	Roles() http.HandlerFunc
	RoleUsers() http.HandlerFunc
	RoleUsersEveryDomain() http.HandlerFunc
}

// LogHandler wraps handlers with logging. Converts error-returning handler to http.HandlerFunc.
type LogHandler func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc

// HandlerClient implements Handlers for access management.
type HandlerClient struct {
	manager UserManager
	handler LogHandler
}

var _ Handlers = &HandlerClient{}

func newHandler(client *Client, logHandler LogHandler) *HandlerClient {
	return &HandlerClient{
		manager: client.UserManager(),
		handler: logHandler,
	}
}

// newDecoder builds the decoder for a handler's request body. Construction fails only
// for a malformed request struct, a programming error, so it panics at startup.
func newDecoder[T any]() *httpio.StructDecoder[T] {
	decoder, err := httpio.NewStructDecoder[T]()
	if err != nil {
		panic(err)
	}

	return decoder
}
