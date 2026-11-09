// Package spannerstore keeps the persona sign-in's state in the host application's
// Spanner database, in the tables the migrations under schema/spanner/migrations create.
package spannerstore

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/persona"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
)

const (
	registrations = "PersonaRegistrations"
	requests      = "PersonaRequests"
	tokens        = "PersonaTokens"
	keys          = "PersonaKeys"
	signIns       = "PersonaSignIns"
)

// errNotFound is a read that matched no row; the public methods turn it into the
// persona package's sentinel for the kind of row.
var errNotFound = errors.New("spannerstore: no row")

var _ persona.Store = &Store{}

// Store is the persona sign-in's store over a Spanner database.
type Store struct {
	client *spanner.Client
}

// New returns the store over the database client, which the host hands it.
func New(client *spanner.Client) *Store {
	return &Store{client: client}
}

// registrationRow is a PersonaRegistrations row.
type registrationRow struct {
	ClientID            string    `spanner:"ClientId"`
	ApplicationClientID string    `spanner:"ApplicationClientId"`
	Environment         string    `spanner:"Environment"`
	PullRequest         int64     `spanner:"PullRequest"`
	RedirectURIs        []string  `spanner:"RedirectUris"`
	Personas            string    `spanner:"Personas"`
	RegisteredBy        string    `spanner:"RegisteredBy"`
	RegisteredAt        time.Time `spanner:"RegisteredAt"`
	ExpiresAt           time.Time `spanner:"ExpiresAt"`
}

// PutRegistration writes a registration, replacing one under the same client ID.
func (s *Store) PutRegistration(ctx context.Context, reg *persona.Registration) error {
	cast, err := json.Marshal(reg.Cast)
	if err != nil {
		return errors.Wrap(err, "json.Marshal()")
	}
	row := &registrationRow{
		ClientID: reg.ClientID, ApplicationClientID: reg.ApplicationClientID, Environment: reg.Environment, PullRequest: int64(reg.PullRequest),
		RedirectURIs: reg.RedirectURIs, Personas: string(cast), RegisteredBy: reg.RegisteredBy, RegisteredAt: reg.RegisteredAt, ExpiresAt: reg.ExpiresAt,
	}
	m, err := spanner.InsertOrUpdateStruct(registrations, row)
	if err != nil {
		return errors.Wrap(err, "spanner.InsertOrUpdateStruct()")
	}

	return s.apply(ctx, m)
}

// Registration reads the registration under the client ID.
func (s *Store) Registration(ctx context.Context, clientID string) (*persona.Registration, error) {
	var row registrationRow
	stmt := spanner.Statement{SQL: "SELECT * FROM PersonaRegistrations WHERE ClientId = @clientId", Params: map[string]any{"clientId": clientID}}
	if err := s.readOne(ctx, stmt, &row); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, persona.ErrUnknownRegistration
		}

		return nil, err
	}
	reg := &persona.Registration{
		Deployment:          persona.Deployment{ClientID: row.ClientID, Environment: row.Environment, PullRequest: int(row.PullRequest)},
		ApplicationClientID: row.ApplicationClientID,
		RedirectURIs:        row.RedirectURIs,
		RegisteredBy:        row.RegisteredBy,
		RegisteredAt:        row.RegisteredAt,
		ExpiresAt:           row.ExpiresAt,
	}
	if err := json.Unmarshal([]byte(row.Personas), &reg.Cast); err != nil {
		return nil, errors.Wrap(err, "json.Unmarshal()")
	}

	return reg, nil
}

// DeleteRegistration removes the registration under the client ID.
func (s *Store) DeleteRegistration(ctx context.Context, clientID string) error {
	return s.apply(ctx, spanner.Delete(registrations, spanner.Key{clientID}))
}

// ExpireRegistrations removes every registration whose expiry is before the time.
func (s *Store) ExpireRegistrations(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteWhere(ctx, "DELETE FROM PersonaRegistrations WHERE ExpiresAt < @before", before)
}

// requestRow is a PersonaRequests row.
type requestRow struct {
	ID                  string             `spanner:"Id"`
	ClientID            string             `spanner:"ClientId"`
	RedirectURI         string             `spanner:"RedirectUri"`
	State               string             `spanner:"State"`
	Nonce               string             `spanner:"Nonce"`
	Scopes              []string           `spanner:"Scopes"`
	ResponseType        string             `spanner:"ResponseType"`
	ResponseMode        string             `spanner:"ResponseMode"`
	CodeChallenge       string             `spanner:"CodeChallenge"`
	CodeChallengeMethod string             `spanner:"CodeChallengeMethod"`
	LoginHint           string             `spanner:"LoginHint"`
	Subject             string             `spanner:"Subject"`
	ActorID             string             `spanner:"ActorId"`
	Done                bool               `spanner:"Done"`
	AuthTime            spanner.NullTime   `spanner:"AuthTime"`
	Code                spanner.NullString `spanner:"Code"`
	CreatedAt           time.Time          `spanner:"CreatedAt"`
}

// request is the row as the persona package reads it.
func (r *requestRow) request() *persona.Request {
	req := &persona.Request{
		ID: r.ID, ClientID: r.ClientID, RedirectURI: r.RedirectURI, State: r.State, Nonce: r.Nonce, Scopes: r.Scopes,
		ResponseType: r.ResponseType, ResponseMode: r.ResponseMode, CodeChallenge: r.CodeChallenge, CodeChallengeMethod: r.CodeChallengeMethod,
		LoginHint: r.LoginHint, Subject: r.Subject, ActorID: r.ActorID, Done: r.Done, CreatedAt: r.CreatedAt,
	}
	if r.AuthTime.Valid {
		req.AuthTime = r.AuthTime.Time
	}
	if r.Code.Valid {
		req.Code = r.Code.StringVal
	}

	return req
}

// CreateRequest writes a sign-in request.
func (s *Store) CreateRequest(ctx context.Context, req *persona.Request) error {
	row := &requestRow{
		ID: req.ID, ClientID: req.ClientID, RedirectURI: req.RedirectURI, State: req.State, Nonce: req.Nonce, Scopes: req.Scopes,
		ResponseType: req.ResponseType, ResponseMode: req.ResponseMode, CodeChallenge: req.CodeChallenge, CodeChallengeMethod: req.CodeChallengeMethod,
		LoginHint: req.LoginHint, Subject: req.Subject, ActorID: req.ActorID, Done: req.Done, CreatedAt: req.CreatedAt,
		AuthTime: spanner.NullTime{Time: req.AuthTime, Valid: !req.AuthTime.IsZero()},
		Code:     spanner.NullString{StringVal: req.Code, Valid: req.Code != ""},
	}
	m, err := spanner.InsertStruct(requests, row)
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}

	return s.apply(ctx, m)
}

// Request reads a sign-in request by ID.
func (s *Store) Request(ctx context.Context, id string) (*persona.Request, error) {
	return s.request(ctx, spanner.Statement{SQL: "SELECT * FROM PersonaRequests WHERE Id = @id", Params: map[string]any{"id": id}})
}

// RequestByCode reads the sign-in request a code was issued for.
func (s *Store) RequestByCode(ctx context.Context, code string) (*persona.Request, error) {
	return s.request(ctx, spanner.Statement{SQL: "SELECT * FROM PersonaRequests WHERE Code = @code", Params: map[string]any{"code": code}})
}

// request reads the one request a statement selects.
func (s *Store) request(ctx context.Context, stmt spanner.Statement) (*persona.Request, error) {
	var row requestRow
	if err := s.readOne(ctx, stmt, &row); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, persona.ErrUnknownRequest
		}

		return nil, err
	}

	return row.request(), nil
}

// completion is the update that marks a request done.
type completion struct {
	ID       string    `spanner:"Id"`
	Subject  string    `spanner:"Subject"`
	ActorID  string    `spanner:"ActorId"`
	Done     bool      `spanner:"Done"`
	AuthTime time.Time `spanner:"AuthTime"`
}

// CompleteRequest marks a request done with the persona picked.
func (s *Store) CompleteRequest(ctx context.Context, id, subject, actorID string, at time.Time) error {
	return s.updateRequest(ctx, id, &completion{ID: id, Subject: subject, ActorID: actorID, Done: true, AuthTime: at})
}

// issuedCode is the update that records a request's code.
type issuedCode struct {
	ID   string `spanner:"Id"`
	Code string `spanner:"Code"`
}

// SaveCode records the code issued for a request.
func (s *Store) SaveCode(ctx context.Context, id, code string) error {
	return s.updateRequest(ctx, id, &issuedCode{ID: id, Code: code})
}

// updateRequest updates a request's columns from the struct's fields, refusing an ID
// with no request.
func (s *Store) updateRequest(ctx context.Context, id string, columns any) error {
	m, err := spanner.UpdateStruct(requests, columns)
	if err != nil {
		return errors.Wrap(err, "spanner.UpdateStruct()")
	}
	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if _, err := txn.ReadRow(ctx, requests, spanner.Key{id}, []string{"Id"}); err != nil {
			if spanner.ErrCode(err) == codes.NotFound {
				return persona.ErrUnknownRequest
			}

			return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
		}

		return txn.BufferWrite([]*spanner.Mutation{m})
	})
	if err != nil {
		if errors.Is(err, persona.ErrUnknownRequest) {
			return persona.ErrUnknownRequest
		}

		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

// DeleteRequest removes a request and its code.
func (s *Store) DeleteRequest(ctx context.Context, id string) error {
	return s.apply(ctx, spanner.Delete(requests, spanner.Key{id}))
}

// ExpireRequests removes every request created before the time.
func (s *Store) ExpireRequests(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteWhere(ctx, "DELETE FROM PersonaRequests WHERE CreatedAt < @before", before)
}

// tokenRow is a PersonaTokens row.
type tokenRow struct {
	ID        string    `spanner:"Id"`
	RequestID string    `spanner:"RequestId"`
	ClientID  string    `spanner:"ClientId"`
	Subject   string    `spanner:"Subject"`
	Audience  []string  `spanner:"Audience"`
	Scopes    []string  `spanner:"Scopes"`
	ExpiresAt time.Time `spanner:"ExpiresAt"`
}

// CreateToken writes an access token.
func (s *Store) CreateToken(ctx context.Context, tok *persona.Token) error {
	row := &tokenRow{ID: tok.ID, RequestID: tok.RequestID, ClientID: tok.ClientID, Subject: tok.Subject, Audience: tok.Audience, Scopes: tok.Scopes, ExpiresAt: tok.ExpiresAt}
	m, err := spanner.InsertStruct(tokens, row)
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}

	return s.apply(ctx, m)
}

// Token reads an access token by ID.
func (s *Store) Token(ctx context.Context, id string) (*persona.Token, error) {
	var row tokenRow
	stmt := spanner.Statement{SQL: "SELECT * FROM PersonaTokens WHERE Id = @id", Params: map[string]any{"id": id}}
	if err := s.readOne(ctx, stmt, &row); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, persona.ErrUnknownToken
		}

		return nil, err
	}

	return &persona.Token{ID: row.ID, RequestID: row.RequestID, ClientID: row.ClientID, Subject: row.Subject, Audience: row.Audience, Scopes: row.Scopes, ExpiresAt: row.ExpiresAt}, nil
}

// ExpireTokens removes every token whose expiry is before the time.
func (s *Store) ExpireTokens(ctx context.Context, before time.Time) (int64, error) {
	return s.deleteWhere(ctx, "DELETE FROM PersonaTokens WHERE ExpiresAt < @before", before)
}

// keyRow is a PersonaKeys row: the private key as PKCS #8 PEM.
type keyRow struct {
	ID         string    `spanner:"Id"`
	PrivateKey string    `spanner:"PrivateKey"`
	CreatedAt  time.Time `spanner:"CreatedAt"`
}

// SigningKeys reads the signing keys, newest first.
func (s *Store) SigningKeys(ctx context.Context) ([]*persona.SigningKey, error) {
	iter := s.client.Single().Query(ctx, spanner.Statement{SQL: "SELECT * FROM PersonaKeys ORDER BY CreatedAt DESC"})
	defer iter.Stop()
	var list []*persona.SigningKey
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return list, nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		var r keyRow
		if err := row.ToStruct(&r); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ToStruct()")
		}
		key, err := decodeKey(r.PrivateKey)
		if err != nil {
			return nil, err
		}
		list = append(list, &persona.SigningKey{ID: r.ID, Key: key, CreatedAt: r.CreatedAt})
	}
}

// AddSigningKey adds a signing key.
func (s *Store) AddSigningKey(ctx context.Context, key *persona.SigningKey) error {
	encoded, err := encodeKey(key.Key)
	if err != nil {
		return err
	}
	m, err := spanner.InsertStruct(keys, &keyRow{ID: key.ID, PrivateKey: encoded, CreatedAt: key.CreatedAt})
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}

	return s.apply(ctx, m)
}

// signInRow is a PersonaSignIns row.
type signInRow struct {
	ID            string    `spanner:"Id"`
	ClientID      string    `spanner:"ClientId"`
	ApplicationID string    `spanner:"ApplicationId"`
	Tenant        string    `spanner:"Tenant"`
	Environment   string    `spanner:"Environment"`
	PullRequest   int64     `spanner:"PullRequest"`
	Persona       string    `spanner:"Persona"`
	ActorID       string    `spanner:"ActorId"`
	ActorName     string    `spanner:"ActorName"`
	ActorEmail    string    `spanner:"ActorEmail"`
	SignedInAt    time.Time `spanner:"SignedInAt"`
}

// RecordSignIn appends an entry to the log.
func (s *Store) RecordSignIn(ctx context.Context, e *persona.SignIn) error {
	row := &signInRow{
		ID: e.ID, ClientID: e.Deployment.ClientID, ApplicationID: e.Application.ID, Tenant: e.Application.Tenant, Environment: e.Deployment.Environment,
		PullRequest: int64(e.Deployment.PullRequest), Persona: e.Persona, ActorID: e.Actor.ID, ActorName: e.Actor.Name, ActorEmail: e.Actor.Email, SignedInAt: e.At,
	}
	m, err := spanner.InsertStruct(signIns, row)
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}

	return s.apply(ctx, m)
}

// apply commits one mutation.
func (s *Store) apply(ctx context.Context, m *spanner.Mutation) error {
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}

// readOne runs a statement that selects at most one row and scans it into dest, or
// answers errNotFound.
func (s *Store) readOne(ctx context.Context, stmt spanner.Statement, dest any) error {
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return errNotFound
	}
	if err != nil {
		return errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	if err := row.ToStruct(dest); err != nil {
		return errors.Wrap(err, "spanner.Row.ToStruct()")
	}

	return nil
}

// deleteWhere runs a delete statement with one time parameter and returns the count.
func (s *Store) deleteWhere(ctx context.Context, sql string, before time.Time) (int64, error) {
	var count int64
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		n, err := txn.Update(ctx, spanner.Statement{SQL: sql, Params: map[string]any{"before": before}})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		count = n

		return nil
	})
	if err != nil {
		return 0, errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return count, nil
}

// encodeKey writes a private key as PKCS #8 PEM.
func encodeKey(key *rsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", errors.Wrap(err, "x509.MarshalPKCS8PrivateKey()")
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// decodeKey reads a private key from PKCS #8 PEM.
func decodeKey(encoded string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("the stored signing key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "x509.ParsePKCS8PrivateKey()")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the stored signing key is not an RSA key")
	}

	return key, nil
}
