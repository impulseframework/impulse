package persona

import (
	"context"

	"github.com/go-playground/errors/v5"
	"google.golang.org/api/idtoken"
)

var _ DeployerVerifier = &GoogleDeployers{}

// GoogleDeployers verifies the identity tokens Google issues to service accounts: the
// token a Cloud Build step or a Cloud Run job fetches from the metadata server for the
// issuer as its audience. The deploy identity is the token's verified email address.
type GoogleDeployers struct {
	validator *idtoken.Validator
}

// NewGoogleDeployers returns the verifier, reading Google's public keys as it needs them.
func NewGoogleDeployers(ctx context.Context) (*GoogleDeployers, error) {
	validator, err := idtoken.NewValidator(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "idtoken.NewValidator()")
	}

	return &GoogleDeployers{validator: validator}, nil
}

// VerifyDeployer verifies the token for the audience and returns the service account's
// verified email address.
func (g *GoogleDeployers) VerifyDeployer(ctx context.Context, token, audience string) (string, error) {
	payload, err := g.validator.Validate(ctx, token, audience)
	if err != nil {
		return "", errors.Wrap(err, "idtoken.Validator.Validate()")
	}
	email, _ := payload.Claims["email"].(string)
	verified, _ := payload.Claims["email_verified"].(bool)
	if email == "" || !verified {
		return "", errors.New("the identity token carries no verified email address")
	}

	return email, nil
}
