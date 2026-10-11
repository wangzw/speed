// Command crm-host is a multi-tenant B2B CRM assembled from this repository's
// modules. It exists to evaluate them: a defect this host runs into is fixed in
// the module, and the host is written the way each module's contract says it
// should work, never around it.
//
// This is the skeleton the CRM's feature slices build on: the users, their
// sessions and memberships, the platform operators' admin surface, the
// operations endpoint and the command line, on five deployment stacks.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. What each
// run is made of is the command tree below, and the host's own modules take
// their places in it from their own registrations.
//
// What a run demonstrates is listed below by area, each claim with a case in
// e2e_test.go — or in wiring_test.go, where the property leaves no trace in a
// run. A feature slice lists its claims under its own area and keeps its cases
// in a test file of its own.
//
// # The platform and the skeleton
//
//   - One binary runs on five stacks, and the stack is a level of the command
//     tree: inclusion is inherited down the command path, so the stack node
//     names its database engine once and every command under it runs on it.
//     Every stack goes through the same path — migrate, register tenants and
//     users and grant memberships at the command line, serve, sign in — and
//     answers the same way, and a served request's span leaves through the
//     export backend the stack names.
//   - On the stack whose metrics leave through standard output, the request
//     duration is the instrumentation library's histogram: seconds, the
//     library's bucket bounds, its whole attribute surface (method, status,
//     scheme, server address, the matched route and others) and nothing of the
//     tenant or the user, a method outside the standard nine folded into
//     _OTHER; the request and response body sizes are exported beside it, all
//     under the library's scope.
//   - `migrate` runs the engine alone and reaches every other module for its
//     declarations, and every table is built. The commands that drive past
//     Migrate carry the same declarations, so each applies the same migration
//     set rather than a smaller one.
//   - A table that references another module's table declares a requirement on
//     the capability that module stands for, so its migration is applied
//     after the one it references; the SQLite stack enforces the reference as
//     PostgreSQL does.
//   - The command-line inputs are flags a host module declares. They exist only
//     on the commands that take that module in, and the command line takes no
//     positional argument.
//   - A command-line write is recorded through the audit seam with its purpose,
//     at a depth where the journal accepts records.
//   - The command groups run no event backend, so a one-shot command never pays
//     for a bus; a configuration that asks the journal to capture events stops
//     them at startup, naming the journal's event list.
//   - A CRM user logs in through the login flow on the account endpoint, under
//     `authn.gate.mount.base-path`, and carries the session cookie the
//     completion set; the cookie reaches the guarded routes, and the user's
//     Basic credential reaches no backend there, since the one scheme belongs
//     to the operators' table. The identity the cookie authenticates is the
//     account the flow resolved, so the journal and the rate limiter name the
//     user by it. The account endpoint answers who the caller is and which
//     tenants they belong to. A logout ends the session.
//   - A CRM user can take a login as a token pair instead of a cookie: a login
//     asked for `delivery: token` answers an access token and a refresh token,
//     and the access token reaches the guarded routes as an `Authorization:
//     Bearer` credential, stamped by the gateway as the account the login
//     resolved. A credential that is absent or forged is refused before
//     resolution. The refresh token rotates with each use and a reuse past the
//     grace window ends the login session the family hangs on, so the rotated
//     token is refused too. `GET /auth/jwks.json` publishes the verification
//     keys as a JWK set, and the signing key comes from a file.
//   - A session lives as long as the session module's settings say, read from
//     the settings surface, and is refused once it lapses. The account and the
//     session are the identity system's, so the host keeps neither a users
//     table nor a password derivation of its own.
//   - A member reaches their own tenant. A caller that is not a member of the
//     tenant its request names, the operator included, is answered the same 404
//     the admission layer answers an unknown tenant, and the refusal is in the
//     journal: the layer sits inside the journal and the rate limiter, which the
//     chain listing shows. The host keeps only the role (`member_roles`), which
//     admission does not know. The only constraints the startup reports as
//     landing on no layer are the journal's anchors on resolution, on the two
//     endpoints that resolve no tenant, and it reports them at Info, since no
//     layer on those endpoints stands for resolution.
//   - A tenant's owner grants an existing user a role from the API, inside the
//     tenant the request names: the row the grant writes names no tenant, and
//     the filter fills it from the request. The grant is an upsert by the
//     membership's key, so a later grant rewrites the role rather than adding
//     a second membership; the grant reaches no other tenant, and a member in
//     a lesser role is refused.
//   - A request with no credential is refused before resolution, so it never
//     reaches a roster lookup. A tenant that is disabled and one that was never
//     registered get the same answer.
//   - What the HTTP module and the layers refuse is answered as problem
//     details: the gateway's 401, resolution's 404 and the limiter's 429, a
//     path no route serves and a method a path does not take. The layers run
//     before the module answers a request no route matches, so a caller with
//     no credential asking for a path nothing serves reads 401, not 404, and
//     the methods a path takes, which the module lists with its 405, reach no
//     caller a layer turned away. A handler of this host answers in the
//     host's own error body.
//   - An operator on the admin endpoint registers a tenant, creates a user and
//     grants a membership, and the tenant is usable at once; the cross-tenant
//     grant states its purpose through the seam. Disabling the tenant there
//     refuses it on the next request, and enabling it brings it back. A CRM
//     user on the admin endpoint is refused, and so is another subject of the
//     operators' table: the check serves one account, and the refusal is in the
//     journal, because the operator check sits inside it. The operator's
//     account belongs to no tenant, so the admission layer refuses it as a
//     stranger to the tenant it names, with the same 404 it answers a
//     non-member.
//   - Two creations of one email at once are answered with one success and one
//     conflict: the unique constraint decides, and the look-up before the
//     insert only answers the ordinary case sooner.
//   - A disabled account is refused on the next request with a session issued
//     before, and at a new login.
//   - A CRM user's password is derived at the policy the account module holds.
//     The served deployment's policy comes from the settings store, so a user
//     created at the admin endpoint stores the combination the deployment
//     configured; the hash carries its parameters, so a user created under one
//     policy signs in once the deployment gives another. A command-line creation
//     runs no settings store and derives at the module's declared defaults. A
//     login naming no account is checked against a decoy, so it costs what a
//     login of an account does.
//   - A CRM user's password hash is written to the account store's credential
//     column and nowhere else: a creation the database refuses leaves neither
//     the hash nor the address in the serving process's output, because the
//     module's logger writes a refused statement without the values bound into
//     it.
//   - A configuration file and an environment variable reach the same input
//     item, and the environment wins. The configuration written as JSON
//     reaches the same items as the YAML one, with its locator given on the
//     command line or through CRM_CONFIG.
//   - A configuration that says several replicas share the in-process
//     key-value store or bus stops the serving command at startup, naming the
//     item.
//   - A request body past the endpoint's max-body-bytes is refused as too
//     large and writes nothing.
//   - The operations endpoint serves one OpenAPI document merged from the
//     fragment every module declares.
//   - `version` and `hash` assemble nothing, so they run with no
//     configuration source at all, and an operator whose subject-table entry
//     is what hash wrote signs in on the admin endpoint.
//   - Every module whose code issues statements on a scoped model requires the
//     roster, so a command that forgot it fails at resolution instead of
//     running unscoped, and every scoped model in the program is listed by
//     the module that issues statements on it; every module that holds a
//     logger requires the logging capability.
//
// # Accounts and contacts
//
//   - Accounts and contacts are scoped tables whose keys lead with the tenant,
//     so two tenants hold records under one identifier. Every statement form
//     the two modules issue from a request names no tenant and reaches only the
//     tenant the request names: a create, a first, a find, a count, a pluck, a
//     single-row read through Row, a scan into another struct over a nested
//     association join that names the path through the account to its owner
//     alone, so the scoped account is its middle level, a preload, an update
//     from the model with a selection and one from a map, an update of one
//     column, a delete addressed by the model's key, a soft delete, a
//     find-or-create, a transaction, a result set streamed through Rows and
//     ScanRows, and a read that locks the rows it reads.
//   - Concurrent writers in one tenant each commit, on every stack: a burst of
//     creations at one account and one of finds-or-creates at another, each
//     checking its account and then writing in one transaction, all succeed,
//     and an account's delete racing creations at it takes each along or has
//     it refused. The database module begins every SQLite transaction
//     holding the write lock, with no transaction mode in the locator, so a
//     check followed by a write waits out a concurrent writer there as it
//     does on PostgreSQL.
//   - A second account under one identifier in one tenant is refused by the
//     primary key and answered as a conflict: the database module marks a
//     key's refusal alike on both engines, and that mark is what the host
//     tells it from any other failure by.
//   - The account listing pages by the accounts.page-size setting as the
//     tenant's layer answers it — the tenant's own row, else the global row —
//     with its total from a count, in an order that holds across pages, and a
//     search matches its wildcard characters and the escape character
//     literally.
//   - A delete of an account is soft and takes the account's contacts along in
//     one transaction: the rows stay in their tables and every read leaves them
//     out. A delete that finds no account to delete after its contacts'
//     statement has run rolls that statement back, and the journal's statement
//     rows go with it. An owner's purge removes the rows through Unscoped,
//     which lifts the soft delete and leaves the tenant condition in place.
//   - A contact's email and phone are ciphertext at rest, sealed afresh on
//     every write, so one address is stored as a different text in each row
//     and after each write that holds it; they read back in the clear, and a
//     change through a map seals them again. The email's blind index, which
//     the host fills over the normalised address, finds only the caller's
//     tenant's contact, although two tenants' rows for one address carry the
//     same digest. An update whose body spells a member in another case is
//     read as the member it resembles, as the HTTP module's binder reads a
//     body.
//   - The export streams through Rows and ScanRows and selects the columns it
//     writes, none of them sealed; the autocomplete plucks distinct names and
//     nothing else.
//   - A contact names a live account of its own tenant, checked in the
//     transaction that writes it. On PostgreSQL the check and an account's
//     delete both lock the account's row, the delete before it touches the
//     contacts: a contact written while the delete is uncommitted waits for
//     it and is refused; a delete that meets a contact being written, checked
//     and not yet inserted or inserted and not yet committed, waits for it
//     and takes it along; and the delete's lock takes the tenant's own row
//     and no other tenant's.
//   - A second contact with one email in one tenant, whether created with it
//     or given it by an update, is refused by the unique index over the
//     tenant and the blind index and answered as a conflict; the same email
//     in another tenant, and the email of a deleted contact, are free.
//   - A rotation of the database's root key, with the old key listed as
//     retired, leaves every contact readable and none found by email: the
//     digest is taken under the current key alone. `contact rekey` writes
//     each tenant's contacts again inside that tenant, deleted ones included,
//     and states it through the seam there; a lookup then finds each tenant's
//     own contact, the unique email refuses a second one again as a conflict,
//     and with the retired key dropped every contact still reads in the clear.
//     A tenant disabled through the rekey is entered for it all the same,
//     through the roster's entry for work that still belongs to a tenant it
//     does not serve, so its contacts read in the clear and are found by email
//     once it is served again.
//   - A contact's email and phone are logged under keys the contacts module
//     registers with the logging module's redaction, so the record of a
//     contact written carries neither.
//   - A viewer reads, a rep writes, an admin deletes and an owner purges, and
//     a refusal is a 403 with the error body. Every write route is journalled,
//     the refusals included, beside the statement rows each write issued, in
//     the tenant that made it.
//
// # Deals, the pipeline, activities and events
//
//   - Every statement form the pipeline, deals, activities, revenue and flow
//     modules issue keeps to the tenant the request or the event names, and
//     none names a tenant: a stage found or created, a one-column update, a
//     creation, a read with its associations preloaded, a look-up that sees
//     deleted rows, a listing counted and joined in one statement to its
//     stages and its accounts, both scoped, and to its owners, who are not, a
//     change made with a map and a move made with a struct inside a
//     transaction, a soft and a hard delete, a read through a model into a
//     type of its own, and an upsert. Two tenants holding stages, accounts,
//     deals and activities under the same identifiers each read and write
//     their own, and a change, a move or a deletion of an identifier only the
//     other tenant holds finds no deal.
//   - A deal's stage is one of its own tenant's stages, its account one of its
//     tenant's live accounts and its owner one of its tenant's members: a stage
//     only another tenant defines, or none does, is refused on a creation and
//     on a move; an account only another tenant holds, a deleted one, or none,
//     on a creation and on a change; and an owner from another tenant on a
//     creation and on a change.
//   - An account a deal names keeps its row: the account's delete is soft, so
//     the deal keeps its account while a new deal at it is refused, and a
//     purge, which the deals table's reference refuses, is answered as a
//     conflict with the account and the deal left as they were. A deleted
//     deal names its account as well, since its delete leaves its row, so an
//     account any deal was written at is never purged. Nor is one whose
//     contact a deal names as a stakeholder, whatever account the deal is at:
//     the join table's reference to the contact refuses the purge, answered
//     as the same conflict, and the account, the contact and the link stay.
//   - A deleted deal keeps its identifier: a creation under it is a conflict,
//     and the identifier stays free in every other tenant. Of a burst of
//     creations of a deal, or of an activity, under one identifier, one is
//     written and every other is a conflict: the ones the look-up before the
//     insert lets through are refused by the primary key, and that refusal is
//     answered as the look-up's is.
//   - A deal names its stakeholders among its tenant's live contacts through
//     a many-to-many association between two scoped models, whose tag writes
//     both keys' tenant halves into one column of the join table, and whose
//     join model is the one GORM builds: the tenant filter takes that column
//     for the join table's, as the tenant design has it, with no model of the
//     host's. Two tenants holding a deal and contacts under the same
//     identifiers each replace, add to and read their own set, through the
//     deal and through the listing, the join table holds each tenant's links
//     alone, and one stakeholder removed by its identifier takes that
//     tenant's link alone. A contact only another tenant holds, a
//     deleted one or none is refused, and a change of the set moves no
//     version and publishes nothing.
//   - A deal's timeline is its tenant's activities on it, in the order they
//     occurred.
//   - Every write the API takes is in the journal, under the tenant it wrote in
//     and the user who wrote it.
//   - A creation under an Idempotency-Key is claimed with one atomic
//     create-if-absent in the key-value store, for the lifetime the tenant's
//     deals.idempotency-ttl setting gives: a replay answers the stored
//     response, a duplicate while the first is in flight is a conflict, of
//     each burst of duplicates exactly one creates, the key with another body
//     is refused, a key is its caller's and its tenant's own, and it lapses
//     when its lifetime does, on every key-value backend. The bursts tell the
//     one atomic claim from a read followed by a write on the backends reached
//     over a connection; the in-process store answers both halves too quickly
//     for a burst to land between them.
//   - A change and a move name the version they read in If-Match, and the
//     update is conditioned on it: of two changes, and of two moves, naming
//     one version exactly one succeeds and the other's precondition fails, and
//     a write naming no version is refused.
//   - A change reads its members as the HTTP module's binder reads a body: a
//     member the change does not have is passed over, one spelled in another
//     case is read as the member it resembles, and of one given twice the last
//     value stands. A null body and a body carrying no member the change has
//     are a change naming nothing, refused as invalid like a value the deal
//     does not take, and those leave the deal as it was.
//   - A stage move and its stage-change activity are one transaction, both
//     ways round: a move whose activity cannot be written leaves the deal as
//     it was, publishes nothing and leaves no statement row; a move whose
//     commit fails after its activity was written leaves neither the update
//     nor the activity; and the same move then goes through once.
//   - Each write to a deal stages its events in its own transaction through
//     the outbox, by the one deal event contract (dealevents.go), and the
//     outbox's relay publishes them once the write has committed: a creation,
//     a change and a deletion publish deal.created, deal.changed and
//     deal.deleted, and a move publishes deal.stage-changed, and deal.won as
//     well into a won stage. Each invalidates the tenant's report under its
//     own type, and the journal records each from its event path with the
//     deal, the outcome and the writer its payload names. A move into a won
//     stage and its deal.won stand or fall together: a move whose deal.won
//     the bus refuses stands, the relay records the refusal and publishes the
//     event again, and the win is counted once the bus takes events again. A
//     move's two identifiers are minted by the move, written on its activity,
//     and are the identifiers the events' subscribers are handed, and the
//     journal records both events with an amount past what a float64 holds
//     kept exact, on every bus.
//   - A deal created without a currency takes the deals.default-currency
//     setting of its tenant, whose own row wins over the global one. The
//     settings module takes any text for it, and the deals module judges it
//     when it reads it: a value that is no currency code writes no deal, and
//     the record names the setting.
//   - Two subscribers act inside the event's tenant, entered through the
//     roster with no request behind them: the revenue projection adds a won
//     deal to its tenant's line for its currency, exactly, and the flow module
//     adds a move to its tenant's line for the two stages. Each writes its
//     records with the tenant Enter bound, on every bus.
//   - Two replicas on a transport bus each hear every event. The identifier
//     is the same on both, so for each subscriber one replica claims it in the
//     key-value store and counts it and the other finds it claimed, and the
//     journal records the event once.
//
// # Reports, the revenue projection, webhooks and the worker
//
// The deal.won a case needs is won through the deals module. The deal feed on
// the admin endpoint, off unless a deployment turns it on, publishes the ones
// no flow of the deals module produces: one identifier twice, one identifier
// in two tenants, and a tenant the consumers do not serve.
//
//   - The deal feed is served where a deployment turns it on and nowhere
//     else: left off, the operator's publish finds no route and nothing is
//     counted; turned on, a CRM user is refused at the admin endpoint's
//     operator check, and the operator's publish is counted.
//   - The revenue line and the reports answer two questions. The revenue
//     projection is the running total of its tenant's deal.won events: each
//     is counted once, with the amount the deal had when it was won, and
//     nothing else moves it. The leaderboard and the pipeline report's won
//     rows read the deals as they now are. A deal won, moved back and won
//     again is two wins in the revenue line and one deal on the leaderboard;
//     a change of its amount and its deletion move the reports and leave the
//     revenue line.
//   - The worker runs the platform modules with no HTTP entry point. The
//     journal's and the telemetry layer's endpoint lists are process-wide
//     configuration, so a worker started on the serving configuration is
//     started with both emptied through the environment — and without that it
//     does not start.
//   - The pipeline report groups the tenant's deals by stage and currency, in
//     the order of the tenant's stages, and scans the sums into a row type of
//     its own; the leaderboard adds up, by owner, the tenant's deals in a stage
//     of kind won, the owners' names read through the account store. Both join
//     the deals to their stages through the relation, and the filter scopes each
//     scan and each join, so neither counts the other tenant's twin; a deleted
//     deal leaves both.
//   - The pipeline report is served from the key-value store under a key
//     carrying its tenant: on a stack whose store several processes share, a
//     report one replica cached is the one another replica serves while the
//     table has moved. Every deal event invalidates its tenant's report on each
//     serving replica that handles it — so a creation, a change, a move and a
//     deletion each reach the report once their events are handled — and none
//     reaches another tenant's cached report.
//   - A cached report lapses once the reports.cache-ttl setting read for its
//     tenant has passed, on every key-value backend and, where replicas share
//     the store, on a replica other than the one that cached it; another
//     tenant's report cached at the same moment under the default lifetime
//     does not.
//   - The generation a tenant's cached report is filed under carries a
//     lifetime of its own, and losing it — to that lifetime, or to a store
//     evicting it — brings back no report an invalidation superseded.
//   - The revenue projection and the webhooks run on the serving replica and
//     on the worker. On a bus that crosses processes each process receives
//     every deal.won, and a claim stored with SetIfAbsent under the tenant and
//     the event's identifier lets one of them act: each deal.won is counted
//     once in its tenant's revenue line and posted once to its tenant's
//     webhook, with its delivery row found or created by the event's
//     identifier inside the tenant. A second delivery of an event is
//     recognised, and the same identifier in two tenants is two events. On the
//     in-process bus the serving replica does both alone.
//   - A consumer acts for the event's tenant by entering it through the roster.
//     A disabled tenant and one never registered are refused there, and the
//     consumer records the refusal and neither counts nor posts the deal. A
//     tenant registered after the consumers last read the roster has its won
//     deal counted at once, with no reread of the roster: an entry into a
//     tenant the snapshot lacks reads the roster's table.
//   - A webhook carries the event's identifier, the event's payload as the
//     deal event contract has it — an amount past what a float64 holds kept
//     exact — and an HMAC-SHA256 signature of its body under the tenant's own
//     secret, and the trace context of the span it was posted in: the client's
//     transport is the telemetry module's, and on every stack the span the
//     header names is one the process exports, read back from standard output
//     or, on the stack that exports through OTLP, decoded by the case's
//     receiver.
//   - A tenant's owner sets its webhook up from the settings API, on every
//     stack: a won deal is posted to the URL written, signed with the secret
//     written, and once the owner rotates the secret the next one is signed
//     with the new secret alone. Neither secret is in any settings answer, any
//     journal row or any log record.
//   - A delivery the receiver refuses is attempted again, and each attempt is
//     recorded on the tenant's delivery row by an update the filter keeps in
//     that tenant as the attempt ends, so the row read while the next attempt
//     is in flight shows the one before it; the row says whether the delivery
//     ended delivered or failed. A delivery that used every attempt is not
//     attempted again when its event returns.
//   - A delivery runs beside the bus rather than on the goroutine the bus calls
//     its subscribers on: while one tenant's receiver holds a delivery, another
//     tenant's won deal is posted and counted. Stopping the process cuts the
//     held delivery short, and its row records the attempt and stays pending.
//   - A tenant's webhook settings past their bounds — a timeout above 30
//     seconds, more than 10 attempts, a retry delay above a minute — send
//     nothing, and the record says which setting it was.
//   - Both claims last as long as their settings, revenue.claim-ttl and
//     webhooks.claim-ttl, say for the event's tenant, on every key-value
//     backend. An event delivered again after they lapse is counted again,
//     since the projection's claim is all it remembers an event by, and is not
//     posted again, since the delivery row already says it was delivered: the
//     webhooks module records finding its claim and finding the row apart, and
//     the later delivery is stopped by the row.
//
// # Settings and audit administration
//
// Cases in administration_test.go.
//
//   - A tenant's owner or admin lists the settings the host lets a tenant
//     edit — settings the product's modules read for the tenant: the account
//     listing's page size, the currency and the idempotency lifetime its deals
//     take, and where and how its webhook is posted, the signing secret
//     included — each with its declared type and default, the text the
//     tenant's layer holds and the value a read resolves to, and writes and
//     deletes them from the API; a member in a lesser role is refused on each
//     of the three routes. A write lands in the tenant's own layer and is
//     served at once: two tenants writing one key each read their own value,
//     and a delete falls back to the global value, then to the declared
//     default.
//   - The host chooses which keys it serves — every key its modules and the
//     platform's read for a tenant — and which of them a tenant edits, and
//     nothing else about a key: its type, its description, its default and
//     whether it is sensitive are read from the settings module's listing of
//     the declarations, so each listing entry says what the declaration of
//     the module that reads the key says, and a sensitive key's default is
//     withheld. A key the host serves and no module declares fails the
//     preferences module's construction, and the startup with it, naming the
//     key.
//   - A sensitive setting is shown by no answer: the tenant's listing and the
//     operator's show that it is set and not what it is, the answer to its
//     write does too, and its seam row names the key alone. The settings
//     module stores the text and keeps it out of its change event, so the
//     journal's capture of that event carries no value either.
//   - A value is handed to the settings module as it was decoded, so the type
//     discipline is the module's: a duration is written as text, and a bare
//     number for one is refused by the settings module, which the API answers
//     with 422 and the stored value unchanged. So is the domain a declaring
//     module states: an operator's rate limit of zero or below is refused the
//     same way, the limit in force stays in force, and the host restates
//     nothing of the limiter's domain. A key the host does not list for
//     tenants is not found, whichever module declares it, for a write and a
//     delete alike: a tenant's owner neither raises nor removes the rate limit
//     an operator set in the tenant's layer.
//   - An operator writes and deletes in the global layer and in any tenant's
//     layer from the admin endpoint, the platform's own keys among them: the
//     rate limit, the consumers' claim lifetimes and the report cache's
//     lifetime, none of which a tenant's owner writes. A delete from one
//     tenant's layer leaves the global row and every other tenant's. A change
//     to a tenant's layer states its purpose through the seam, and a seam row
//     carries the tenant of the scope its work ran in: this change runs in
//     none, since the settings table is not scoped, so its row is among the
//     platform's rows, names the layer and the tenant in its detail, and is
//     not in that tenant's own trail. The admin routes answer a malformed
//     tenant identifier with 422 and a body they do not read with 400, and
//     the merged document lists both.
//   - A tenant's audit trail is read through the read-only model the tenant
//     design sanctions over a table another module writes: it maps to the
//     journal's table and embeds the scoping convention, so the filter scopes
//     every form of the read — the page, its count, the per-action counts and
//     a single row. With the same actions taken in two tenants, each trail
//     holds exactly its own rows, newest first, and narrows by action and by
//     time; a row of the other tenant is not found. It shows the tenant's
//     writes, the admission layer's refusal of a stranger, the seam rows and
//     the settings changes the journal captured from the bus. A member in a
//     lesser role is refused the page and a single row. Nothing is written
//     through that model, nor through the one an operator's read uses: each
//     refuses every write GORM runs model hooks for, so the journal is the one
//     writer of its table.
//   - An operator reads every tenant's audit rows on the admin endpoint across
//     tenants, and one tenant's by entering that tenant through the roster, so
//     the filter scopes that read as it scopes a request of the tenant's own.
//     Both go through one model, whose tenant field takes the NULL a platform
//     row holds and which embeds the scoping convention, so the same read with
//     a tenant in its context is conditioned on that tenant and one with none
//     is refused. Each read is recorded through the seam with its purpose
//     before it runs, and the record of a read of one tenant ran in that
//     tenant's scope, so it is in that tenant's own trail. A disabled tenant's
//     trail is read the same way: its trail is still its own, and the read
//     enters it through the roster's entry for work that belongs to a tenant
//     it does not serve.
//   - An operator lowers one tenant's rate limit through its settings layer.
//     Driving past it answers 429 with Retry-After, another tenant is
//     unaffected, and the admission layer's refusals count toward the limit,
//     since that layer sits inside the limiter.
//   - A setting written through one replica is read on another within a bound,
//     and so is its delete, with the settings module's periodic reload off: the
//     change event is what reloads the other replica's snapshot. Each change is
//     in the trail once, however many replicas receive its event, and two
//     changes whose events carry the same payload are two rows.
//   - Two replicas count one rate limit: the counters live in the shared
//     key-value store, so alternating replicas refuses the request past the
//     limit.
//   - An operator disabling a tenant has the API answer it as it answers an
//     unknown tenant, at once on the replica that wrote the status and on
//     another replica after its roster refresh; enabling it brings it back on
//     both.
//
// # Replicas and lifecycle
//
//   - Replicas on a stack whose backends several processes share are started
//     together, as a rollout starts them, and share their sessions: a session
//     one replica issues is honoured by another, and a logout on one ends it
//     on both.
//   - An operator's status change is made against the roster's table, so it
//     reaches a tenant from a replica that has not yet reread the roster; a
//     registration on such a replica refuses an identifier another replica
//     registered rather than rewriting it, since the roster's create asks the
//     table whether the identifier is free.
//   - An alias names one tenant, and an identifier is registered once: a
//     registration under an alias another tenant holds is answered as a
//     conflict and leaves the roster as it was, and of two registrations
//     through two replicas at once, under one alias or of one identifier, one
//     succeeds and the other is a conflict.
//   - A deployment that names the tenant by the first label of the host
//     reaches each tenant by its alias in either case and with or without a
//     port; a label no tenant holds is answered as an unknown alias, and the
//     tenant header is not read.
//   - An interrupt stops every stack's serving command with exit status 0, and
//     every application module that serves writes its release records.
//   - Requests in flight when the interrupt arrives are answered in full, on
//     every stack: a deal creation writes its deal with its announcement
//     staged beside it, and its journal row, and a settings write its change
//     and its seam row. The
//     entry point is stopped before any other module and waits for them, so
//     every application module is stopped after they are answered, wherever
//     it was constructed.
//
// # Outages, shared consumers and telemetry
//
// Cases in outages_test.go, consumers_test.go and telemetry_test.go. An outage
// is a cut of one link between the host and a backend it reaches over a
// connection, through a proxy the case holds (links_test.go), on every stack
// where that link exists.
//
//   - A session presented while the database is cut is answered 503, attributed
//     to the session backend, and not 401: the backend cannot say whether the
//     session is live, which is not the session failing. The operator's Basic
//     credential, checked against a table in memory, is answered all along, and
//     the session again once the database is back.
//   - While the key-value store is cut the rate limiter lets requests past the
//     limit through, and says so once on standard error however many it lets
//     through; once the store is back and a minute has passed since the last
//     of them, it says once that it is limiting again, and refuses a request
//     past the limit.
//   - A deal moved into won while the bus is cut is answered at once; its
//     deal.won stays staged in the outbox and uncounted while the bus is away,
//     and once the bus is back it is published, counted once with its amount
//     exact, and leaves the outbox.
//   - A publish the bus refuses reaches no consumer once the bus is back: the
//     deal feed answers 503, and no process counts the event. A publish the
//     NATS bus's down link cannot carry fails at once and is not sent after
//     the reconnect, so it is counted there neither.
//   - The PostgreSQL bus records its listening session ended from the
//     server, holds a new one at once, and delivers a win made afterwards.
//   - The journal fails open on a request and closed on a statement. With the
//     database cut, the operator's tenant listing, which the roster answers
//     from its snapshot, is answered in full while its rows are lost, with one
//     line on standard error for the fault and one counting them at its end.
//     With the journal's table refusing rows, an account's creation fails and
//     writes nothing, while a member listing is answered in full.
//   - A setting written through one replica while the bus is cut is answered
//     and served there at once, and reaches the other replica with the next
//     change once the bus is back, with the periodic reload off: the reload an
//     event starts reads the whole table.
//   - Two workers sharing a consumer — a durable consumer's name on NATS, a
//     consumer group on Redis — split the deal.won events between them while
//     the serving replica hears all of them: each is handled by one worker,
//     counted once and posted once. On NATS a worker killed while it holds a
//     deal.won unacknowledged has it handed, once the server's acknowledgement
//     wait has passed, to the worker started under the same name, which finds
//     the win counted.
//   - Decoded from the OTLP export of two replicas: every export names the
//     service and each replica its own version 4 instance identifier; a served
//     route is a server span named for its method and route, carrying the
//     method, the status, the route and the endpoint; a webhook's traceparent
//     names an exported client span of its delivery; and the request duration
//     is a histogram in seconds with the fourteen bounds the instrumentation
//     library fixes, with the library's attribute surface, under the library's
//     scope.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/login"
	loginpassword "github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/authn/token"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant/admission"
	"github.com/vislake/speed/pkg/tenant/catalog"
	"github.com/vislake/speed/pkg/tenant/resolve"

	_ "github.com/vislake/speed/pkg/config/format/json"
	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"

	// The engines and the backends register themselves in init, so importing
	// a package is what puts it in the available set. They are aliased
	// rather than blank-imported because the command tree writes their names:
	// each claims its capability exclusively, and the dependency closure
	// never picks one implementation of an exclusive capability on the host's
	// behalf. Several share a package name.
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	eventnats "github.com/vislake/speed/pkg/event/nats"
	outboxrelay "github.com/vislake/speed/pkg/event/outbox/relay"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
	kvmemcached "github.com/vislake/speed/pkg/kv/memcached"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	kvnats "github.com/vislake/speed/pkg/kv/nats"
	kvpostgres "github.com/vislake/speed/pkg/kv/postgres"
	kvredis "github.com/vislake/speed/pkg/kv/redis"

	// The telemetry assembler is an exclusive provider too, and the export
	// backends are not: the assembler names no destination, so the stack says
	// which one its runs export through.
	"github.com/vislake/speed/pkg/observability/otel"
	"github.com/vislake/speed/pkg/observability/otlp"
	"github.com/vislake/speed/pkg/observability/stdout"
)

// What the version command answers with, and what the OpenAPI document calls
// this program.
const (
	hostName          = "crm-host"
	hostVersionNumber = "0.1.0"
	hostVersion       = hostName + " " + hostVersionNumber
)

// The long-running commands, by the word the command tree gives them and the
// ready record carries.
const (
	serveCommand  = "serve"
	workerCommand = "worker"
)

// stack is one deployment stack: the database engine, the key-value backend,
// the event backend and the telemetry export backend a set of commands runs on.
// Each field is a module name, and the stack's name says what it contains.
type stack struct {
	name     string
	engine   string
	kv       string
	event    string
	exporter string
}

// stacks is every stack this binary runs on. Each is built by the same
// function, so what differs between two of them is this table and nothing
// else.
var stacks = []stack{
	{
		name:   "sqlite-memory",
		engine: sqlite.ModuleName, kv: kvmemory.ModuleName, event: eventmemory.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres-redis-nats",
		engine: dbpostgres.ModuleName, kv: kvredis.ModuleName, event: eventnats.ModuleName,
		exporter: otlp.ModuleName,
	},
	{
		name:   "postgres",
		engine: dbpostgres.ModuleName, kv: kvpostgres.ModuleName, event: eventpostgres.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres-nats-redis",
		engine: dbpostgres.ModuleName, kv: kvnats.ModuleName, event: eventredis.ModuleName,
		exporter: stdout.ModuleName,
	},
	{
		name:   "postgres-memcached-nats",
		engine: dbpostgres.ModuleName, kv: kvmemcached.ModuleName, event: eventnats.ModuleName,
		exporter: stdout.ModuleName,
	},
}

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so that the deferred stop runs before the
// process leaves: os.Exit skips deferred calls.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := commandTree(core.ProcessRegistry).Execute(ctx); err != nil {
		// A failure has already been written to standard error by the time
		// it arrives here; printing it again would put the same line on the
		// stream twice.
		return 1
	}
	return 0
}

// commandTree writes this host's command line.
//
// The root takes in nothing, so that the program's name on its own prints the
// list of commands and the two commands that assemble nothing can go on
// assembling nothing. Everything else hangs under a stack.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)
	root := app.Root()

	// hash turns a password into what the operators' subject table holds. It
	// assembles nothing: deriving a hash reads no configuration and needs no
	// module, and an operator writing the first account into a file has
	// nothing running yet to ask.
	root.Sub("hash").Do(core.StagePrepare, hashCommand)
	root.Sub("version").Do(core.StagePrepare, printVersion)

	for _, s := range stacks {
		addStack(root, s)
	}
	return app
}

// addStack writes one stack's subtree. Every stack goes through here, which is
// what keeps the five from drifting apart.
//
// The stack node takes in two things, and every command under it inherits
// both. The engine at run level, because every one of those commands runs it.
// And every module's declarations at static level: migrate needs them to build
// every table, and each of the other commands drives past Migrate, so it
// applies migrations too — the database design asks that such a command carry
// the same static inclusion as migrate, because a command that carried a
// smaller one would apply a smaller migration set, and running it before
// migrate would apply one set in two batches whose order between them is not
// the dependency order. Putting it on the stack node makes that the same
// inclusion by construction rather than by keeping five copies alike.
//
// The key-value, event and export backends are not on the stack node, and that
// is the migrate command's doing: inclusion is inherited, so a backend named on
// the stack node would run in migrate as well, and the command that applies
// migrations runs the engine alone — anything more puts that module's input
// items and its reachability into a pre-deployment job that has no use for
// them. Each backend is named instead on the commands that run a module
// requiring it, from the same stack value.
//
// Every command names each module it runs, one by one. The platform modules are
// written here; the host's own modules come from their registrations, each
// naming the commands it runs on (see application). IncludeAll would make the
// participating set a function of the import graph — every imported backend at
// once — and on a command carrying tenant scoping a module nobody wrote down
// could bring a statement filter into the run.
func addStack(root *config.Command, s stack) {
	node := root.Sub(s.name)
	node.Include(s.engine)
	node.IncludeAllDeclarations()

	serve := node.Sub(serveCommand)
	serve.Include(
		speedhttp.ModuleName,
		gate.ModuleName,
		static.ModuleName,
		// The identity system's own modules, each named one by one. Every
		// exclusive provider a command runs is named, since the dependency
		// closure never picks one: mount carries the route mounter the login,
		// session and multi-factor modules mount through; account delivers the
		// account store, its write surface and the membership store; session
		// delivers the session store and the cookie backend; login and its
		// password method deliver the flow routes; mfa delivers the factor
		// register and policy; token delivers the access and refresh tokens and
		// registers the bearer verifier; admission holds the member check on the
		// API.
		authnmount.ModuleName,
		account.ModuleName,
		session.ModuleName,
		login.ModuleName,
		loginpassword.ModuleName,
		mfa.ModuleName,
		token.ModuleName,
		admission.ModuleName,
		resolve.ModuleName,
		catalog.ModuleName,
		journal.ModuleName,
		ratelimit.ModuleName,
		settings.ModuleName,
		s.kv,
		s.event,
		// The outbox's relay publishes what the deals module stages, and is
		// named here because serve is the command that runs the deals
		// module: the capability is exclusive, so nothing picks it for the
		// host. Its table is built by migrate through the stack node's
		// declarations, as every module's is.
		outboxrelay.ModuleName,
		log.ModuleName,
		otel.ModuleName,
		s.exporter,
	)
	serve.Include(modulesOn(serveCommand)...)
	serve.Do(core.StageServe, untilCancelled(serveCommand))

	// worker runs the platform modules a background process needs and no
	// HTTP entry point, and the host's modules that name it.
	//
	// The journal and the telemetry assembler take the entry point up in
	// their Init when their endpoint lists name endpoints, and serve's
	// configuration writes names into both. A worker started on it is
	// therefore given both lists empty through the environment; with them
	// left as serve's it stops at startup, naming the capability it has no
	// provider for. The assembler's default list, "*", would not stop it, but
	// the list serve writes is not the default.
	worker := node.Sub(workerCommand)
	worker.Include(
		catalog.ModuleName,
		journal.ModuleName,
		settings.ModuleName,
		s.kv,
		s.event,
		log.ModuleName,
		otel.ModuleName,
		s.exporter,
	)
	worker.Include(modulesOn(workerCommand)...)
	worker.Do(core.StageServe, untilCancelled(workerCommand))

	// migrate runs the engine the stack node names and nothing else, and
	// reaches every other module through the declarations the node carries.
	node.Sub("migrate").Do(core.StageMigrate, nil)

	for _, g := range commandGroups {
		group := node.Sub(g.word)
		group.Include(groupPlatform...)
		group.Include(g.platform...)
		group.Include(modulesOn(g.word)...)
		for _, c := range g.commands {
			group.Sub(c.use).Do(c.depth, c.action)
		}
	}
}

// groupPlatform is what every command group takes in besides the host modules
// that name it.
//
// The command-line module carries the flags every group's actions share, and
// its requirements hold each group to what those actions use. The writes go
// through the roster and are recorded through the seam, so both the roster and
// the journal run.
//
// The HTTP entry module runs as well, and no port is ever bound: these commands
// stop at Init or Start, and binding happens in Serve. It is here because the
// journal takes the entry point up in its Init when its endpoint list is not
// empty, and that list is the serving configuration's.
//
// No event backend is here. The journal takes the bus up only when its event
// list is not empty, and a one-shot command on a transport bus pays for a bus
// on every run — a consumer left behind on NATS and Redis, a copy of the outbox
// on PostgreSQL. A configuration that asks the journal to capture events
// therefore stops every command-line write at startup, naming the bus, and the
// choice between emptying the list for one-shot commands (as the worker does
// with the endpoint lists) and naming the bus here is made where that
// configuration is written.
var groupPlatform = []string{
	catalog.ModuleName,
	journal.ModuleName,
	speedhttp.ModuleName,
	log.ModuleName,
	cliModuleName,
}

// untilCancelled writes the ready record once every module has started, then
// does nothing until the run is asked to stop. A non-nil action at the serving
// depth replaces the drive's own blocking, so this is what keeps the process
// up.
func untilCancelled(command string) config.Action {
	return func(ctx context.Context, _ *core.Registry) error {
		announceReady(command)
		<-ctx.Done()
		announceStopped(command)
		return nil
	}
}

// hashCommand reads a password from standard input and writes its hash to
// standard output, for pasting into the operators' subject table. The hash is
// the static backend's, a PHC string, since that backend holds the operators; a
// CRM user's password is hashed by the account module (authn.account) when the
// account is created.
func hashCommand(_ context.Context, _ *core.Registry) error {
	password, err := readSecret(os.Stdin)
	if err != nil {
		return err
	}
	encoded, err := static.Hash(password)
	if err != nil {
		return err
	}
	fmt.Println(encoded)
	return nil
}

// printVersion answers with the version. It goes to standard output directly:
// it is what the caller asked for, not a record of what the program is doing.
func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}
