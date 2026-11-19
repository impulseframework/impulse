# accesstypes

The `accesstypes` package provides types used by the `access` package and other dependent packages.

## Principals and masks

`Principal` is the authorization subject a session evaluates against — a user
(`UserPrincipal`) or a role (`RolePrincipal`). Kind is structural: the
constructors set an unexported discriminator, so no username can read as a role
and no role name as a user. Whether a session is impersonated is a property of
the session's impersonation record, never of the `Principal` value.

`PermissionMask` is the attenuation an impersonated session carries, and the
control over what the session may do: a session that impersonates a person with
no mask holds everything that person holds. A mask says which permission kinds
the session keeps (List, Read, Create, Update, Delete, Execute) and, inside a
kind it keeps, what it refuses: a class of actions, or one permission on one
named action, record or field (a record's refusal covers its fields). A mask only narrows: a masked check asks the mask
before it asks policy, so nothing a mask does can grant what policy denies.

```go
accesstypes.AllowAll()                                                   // the zero mask: unrestricted
accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read) // reading only
accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential))  // everything except the credential actions
accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("ScrapShip"))   // everything except one action
accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Invoices")...) // everything except one record's writes
```

`MaskPermissions(fallback, perms...)` keeps exactly the kinds listed, and the
fallback is what an empty list means, stated where the list is built. `Refusing`
adds refusals and leaves its receiver as it was. `Refuses(perm, resource,
classes...)` is the whole question a check asks, with the reason for the log;
`Allows(perm)` answers for the kind alone.

`ActionClass` names a class of actions, a marking an action carries where it is
declared (`@class` on a generated method or on the constant of a registration
made by hand). A mask that refuses a class refuses every action that carries
it, the ones declared after the mask was written among them, so nobody keeps a
list of actions in step with the application. Two classes are the library's:
`ClassCredential` (`credential`), the actions that change how a person signs
in, and `ClassAccess` (`access`), the actions that give or take away what
someone may do, a role, a place in a group, an invitation, an account. An
application names its own beside them.

`ceiling.Narrow(request)` is the arithmetic of a grant that carries a mask: the
mask a request gets under the widest mask its grant gives. A request that names
no kinds keeps the ceiling's; one that names kinds keeps those, each of which
the ceiling must keep; the refusals of both are joined. A request for a kind
the ceiling does not keep is refused with a `*WiderMaskError`, never trimmed.
`UnknownKinds()` lists the kinds a mask keeps that are no permission kind
(`read` for `Read`): such a kind keeps nothing, so whatever takes a mask from a
person refuses it, with the kinds there are (`PermissionKinds()`).

`Text()` is the persistence form and `ParseMask` reads it back: the kinds,
sorted and joined with commas (`*` for every kind), then each refusal after a
semicolon, `-class:<class>` or `-on:<Permission>:<Resource>`. `List,Read` is a
read-only mask and `*;-class:credential` keeps everything except the credential
actions. `Words()` says the mask as a person would ("reading only", "everything
except credential actions"), for a banner and a list of impersonated sessions. A
record refused its three writes (`RefuseWrites`) is said once, "everything except
writing Invoices"; one or two of the three are each said ("creating Invoices").
`NullMaskWords` is the type of a record's field that reads the stored text as
those words: a list of impersonation records carries it, so the list says
"everything" for a NULL column and "everything except credential actions" for
`*;-class:credential`, and a column that is no mask's text reads "nothing (the
stored mask does not read)", as the session it belongs to is held. It is read
only: it refuses to be written to a database.
`ReadOnly()` is what the session library's read-only guard asks: the mask names
its kinds and keeps nothing beyond List and Read.

## Scopes: where a request is, and where policy is held

`Scope` is the partition an operation applies to: the global partition
(`GlobalScope()`) or one tenant domain (`DomainScope(domain)`). A permission check,
a permission digest and the foothold question take a `Scope`, because a request is
in exactly one partition. Global-ness is structural, an unexported flag, so no
domain string reads as the global partition: a tenant literally named `global` is
an ordinary tenant.

`PolicyScope` is where policy is held: a role membership, a custom role and its
grants live in the global partition (`GlobalPolicyScope()`), in one tenant domain
(`DomainPolicyScope(domain)`), or in every tenant domain
(`EveryDomainPolicyScope()`). The policy store, the user manager and the login's
role synchronization take a `PolicyScope`. A membership held in every domain
reaches every tenant scope, including a tenant the store holds no other row in, so
a tenant created at run time needs nothing written for its every-domain members.

The two types are kept apart on purpose. "Every domain" is not a place a request
can be in, so a `PolicyScope` cannot be passed where a `Scope` is expected and
nothing converts one into a `Scope`; an invalid call fails to compile rather than
at run time. `Scope.PolicyScope()` converts the other way, since every partition a
request can be in is also a place policy can be held, and `PolicyScope.Covers(scope)`
answers whether policy held there applies in a partition.
