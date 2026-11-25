-- The development tenants, under fixed ids: a tenant's key is an id the application
-- generates, and the development data fixes the two so the suites can name them. The
-- development logins name a tenant by its name (cmd/bootstrap/users.json).
INSERT INTO Tenants (Id, Name, CreatedAt) VALUES ('11111111-1111-4111-8111-111111111111', 'North', PENDING_COMMIT_TIMESTAMP());
INSERT INTO Tenants (Id, Name, CreatedAt) VALUES ('22222222-2222-4222-8222-222222222222', 'South', PENDING_COMMIT_TIMESTAMP());
