# Graph Report - telsync  (2026-11-02)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 5364 nodes · 22073 edges · 142 communities (122 shown, 20 thin omitted)
- Extraction: 93% EXTRACTED · 7% INFERRED · 0% AMBIGUOUS · INFERRED: 1502 edges (avg confidence: 0.85)
- Token cost: 67,196 input · 1,715 output

## Graph Freshness
- Built from commit: `4a23673b`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Raw API Clients
- Minified Frontend Bundle
- Minified Frontend Bundle
- Worker Test Doubles
- Config Loading Tests
- Minified Frontend Bundle
- Docs Static Server
- Gateway Authorization Tests
- Minified Frontend Bundle
- Gateway Test Fixtures
- Asset File Dialog Bundle
- SQLite Store Queries
- Stale Cache Refresh Tests
- Minified Frontend Bundle
- Runtime CLI Harness
- Runtime Startup Arguments
- Channel Store Queries
- Worker RPC Types
- Channel Permission Checks
- Minified Frontend Bundle
- Administrators UI Dialogs
- Minified Frontend Bundle
- React Router Runtime
- Permission Evaluation Logic
- Layout Navigation Menu
- Message Display Formatting
- WordPress Gateway Tests
- Web Page Shell Types
- Server Bootstrap Config
- WordPress Credential Encryption
- Channel Detail Route Tests
- WordPress Site Management
- Site Probe Tests
- Minified Frontend Bundle
- MTProto Test Servers
- Message Card Rendering
- Admin Bootstrap Command
- MTProto Test Fixtures
- Minified Frontend Bundle
- SPA Prerender Script
- API Credential Scanner
- Telegram Message Types
- Telegram Channel Lookup
- Channel List Store Tests
- Dashboard State Cards
- Process Supervisor
- Daemon Main Bootstrap
- Minified Frontend Bundle
- Channel View Permissions
- Minified Frontend Bundle
- Worker Supervisor Tests
- Worker Registry Fakes
- Telegram Dialog Types
- DOM Event Helpers
- Worker RPC Service
- Scripted Backend Test Sites
- MTProto Client Factory
- Gateway Probe Auth Tests
- Publish Operation Flow
- Minified Frontend Bundle
- Minified Frontend Bundle
- Reusable UI Pickers
- API Docs Comparison
- Route Schema AST
- Safe Dialer Policy
- Publish Authorization Tests
- Minified Frontend Bundle
- Telegram Client Lifecycle
- Minified Frontend Bundle
- HTTP API Client
- Login and JWT Auth
- Quoted Route Manifest
- Login Page UI
- Minified Frontend Bundle
- Channel Route Auth
- Minified Frontend Bundle
- Publish Dialog UI
- Minified Frontend Bundle
- UI Package Dependencies
- Docs Endpoint Rendering
- Minified Frontend Bundle
- Worker Service RPC
- Route Table Tests
- Channel Peer Lookup
- Worker HTTP Handler Tests
- Route Coverage Index
- Web App Dependencies
- Password Hash Command
- Web Component Config
- UI Component Config
- MTProto Login Flow
- Web Dev Dependencies
- UI Dev Dependencies
- Web TypeScript Config
- Minified Frontend Bundle
- Docker Compose Setup
- Docs Site Tests
- Root App Layout
- API Examples Tests
- Peer Store Persistence
- Docker Build CI
- Turbo Build Config
- UI TypeScript Config
- Test Server Helpers
- Publish Idempotency
- Lint TypeScript Config
- Telegram Channel Discovery
- Emoji Document Cache
- Message Send Service
- Auth Prompt Input
- CI Workflow Config
- Frontend Build Config
- Docker Image Retag
- Workflow Step Config
- Package Scripts
- Web TypeScript Config
- Frontend CI Job
- Workflow Triggers
- Channel Category Filing
- Anonymous Auth Context
- Operator Auth Context
- Account Auth Context
- Package Scripts
- Package Exports Map
- Go CI Job
- Pipe Address Resolution
- FloodWait Error
- Pipe Address Resolution
- FloodWait Error
- Format Scripts
- Telesync Go Package

## God Nodes (most connected - your core abstractions)
1. `readyWorker()` - 155 edges
2. `newAccountEnv()` - 94 edges
3. `v()` - 91 edges
4. `newEnv()` - 85 edges
5. `i()` - 82 edges
6. `Ce()` - 72 edges
7. `ut()` - 72 edges
8. `D()` - 68 edges
9. `g()` - 61 edges
10. `U()` - 58 edges

## Surprising Connections (you probably didn't know these)
- `accountCaller` --references--> `PermissionSet`  [EXTRACTED]
  cmd/docsgen/main.go → internal/channelstore/permissions_store.go
- `createAdministrator()` --references--> `AdminRequest`  [EXTRACTED]
  cmd/telesync/admin.go → internal/cli/admin.go
- `adminRequest()` --references--> `AdminRequest`  [EXTRACTED]
  cmd/telesync/admin_test.go → internal/cli/admin.go
- `createAdministrator()` --references--> `AdminResult`  [EXTRACTED]
  cmd/telesync/admin.go → internal/cli/admin.go
- `runtimeDeps()` --references--> `RuntimeDeps`  [EXTRACTED]
  cmd/telesync/runtime.go → internal/cli/cli.go

## Import Cycles
- 2-file cycle: `internal/cli/admin_test.go -> internal/cli/configresolve_test.go -> internal/cli/admin_test.go`
- 2-file cycle: `internal/cli/configresolve_test.go -> internal/cli/daemon_test.go -> internal/cli/configresolve_test.go`
- 2-file cycle: `internal/cli/admin_test.go -> internal/cli/daemon_test.go -> internal/cli/admin_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/e2e_test.go`
- 2-file cycle: `internal/runtime/runtime_test.go -> internal/runtime/strict_test.go -> internal/runtime/runtime_test.go`
- 2-file cycle: `internal/runtime/e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/e2e_test.go`
- 2-file cycle: `internal/gotdclient/discover_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/discover_test.go`
- 2-file cycle: `internal/gotdclient/entities_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/entities_test.go`
- 2-file cycle: `internal/gotdclient/manager_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/manager_test.go`
- 2-file cycle: `internal/gotdclient/metrics_test.go -> internal/gotdclient/reconnect_test.go -> internal/gotdclient/metrics_test.go`
- 3-file cycle: `internal/cli/admin_test.go -> internal/cli/daemon_test.go -> internal/cli/configresolve_test.go -> internal/cli/admin_test.go`
- 3-file cycle: `internal/cli/admin_test.go -> internal/cli/configresolve_test.go -> internal/cli/daemon_test.go -> internal/cli/admin_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/e2e_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/strict_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/e2e_test.go -> internal/runtime/auth_e2e_test.go`

## Communities (142 total, 20 thin omitted)

### Community 0 - "Raw API Clients"
Cohesion: 0.06
Nodes (112): rawAPI, rawClient, rawConfig, rawJWTAPI, rawRuntime, rawServer, rawWordPressAPI, claims (+104 more)

### Community 1 - "Minified Frontend Bundle"
Cohesion: 0.03
Nodes (159): De(), _e(), Ee(), G(), he(), J(), K(), ke() (+151 more)

### Community 2 - "Minified Frontend Bundle"
Cohesion: 0.03
Nodes (191): ae(), b(), c(), d(), g(), ge(), h(), ie() (+183 more)

### Community 3 - "Worker Test Doubles"
Cohesion: 0.03
Nodes (66): fakeWorker, migration, documentedChannel(), documentedMessage(), documentedWorker, blockingWorker, fakeWorker, mediaView (+58 more)

### Community 4 - "Config Loading Tests"
Cohesion: 0.03
Nodes (133): TestGeneratorIsOffline(), loadIn(), loadWith(), TestDocsDirDefaultsToTheEmbeddedCopy(), TestDocsDirFindsTheCheckoutTree(), TestDocsDirNamedMustExist(), writeConfig(), CreateIfMissing() (+125 more)

### Community 5 - "Minified Frontend Bundle"
Cohesion: 0.03
Nodes (90): Te(), we(), Se(), ye(), $e(), ai(), hr(), i() (+82 more)

### Community 6 - "Docs Static Server"
Cohesion: 0.06
Nodes (40): docsSite, scriptTransport, categoryRequest, categoryView, leaveRequest, leaverOf, pageSite, ScriptedSite() (+32 more)

### Community 7 - "Gateway Authorization Tests"
Cohesion: 0.04
Nodes (98): dashboardStats, ResourcePermission, ResourcePermissions, bodyOf(), stripRequestID(), TestAChannelWithoutAGrantIsForbidden(), TestAMissingOrDuplicateTokenDoesNotRevealWhetherAClientExists(), TestAnExplicitChannelAllowGrantsWithoutAGlobalPermission() (+90 more)

### Community 8 - "Minified Frontend Bundle"
Cohesion: 0.04
Nodes (92): ah(), ai(), an(), ap(), au(), bl(), bm(), bs() (+84 more)

### Community 9 - "Gateway Test Fixtures"
Cohesion: 0.07
Nodes (93): fakeStore, findBody, ManagedRecord, testEnv, TestCustomEmojiMissingIsNotFound(), TestCustomEmojiRejectsABadID(), TestCustomEmojiStreamsTheEmojiByDocumentID(), New() (+85 more)

### Community 10 - "Asset File Dialog Bundle"
Cohesion: 0.04
Nodes (67): acquire(), at(), br(), constructor(), dn(), dr(), e(), Ei() (+59 more)

### Community 11 - "SQLite Store Queries"
Cohesion: 0.04
Nodes (37): Assignment, queryer, brokenCategoryStore, CategoryByIDs, localChannelView, WordPressSiteService, categoryIn(), Category (+29 more)

### Community 12 - "Stale Cache Refresh Tests"
Cohesion: 0.06
Nodes (55): clock, flight, meta, Options, crumbPart, docsHandler, navItem, pageData (+47 more)

### Community 13 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (71): ae(), b(), c(), d(), Du(), ee(), eu(), f() (+63 more)

### Community 14 - "Runtime CLI Harness"
Cohesion: 0.07
Nodes (54): AdminDeps, adminHarness, homeEnv, runtimeHarness, limitedWriter, AdminRequest, AdminResult, TestAdminAsksForCredentialsAndCreatesTheAccount() (+46 more)

### Community 15 - "Runtime Startup Arguments"
Cohesion: 0.09
Nodes (57): runtimeArgs, runtimeFlags, Load(), customJSON(), env(), officialJSON(), TestAppHashAndEnvTogetherAreRejected(), TestCustomRejectsInvalidEnums() (+49 more)

### Community 16 - "Channel Store Queries"
Cohesion: 0.06
Nodes (21): scanner, documentedRecord(), managedStore, CategoryMembers, ChannelCategories, ChannelExcluder, ChannelScope, failingDeleteStore (+13 more)

### Community 17 - "Worker RPC Types"
Cohesion: 0.06
Nodes (16): JoinOutcome, JoinResult, FormatChannelID(), AvatarFile, ChannelRef, SendRequest, SendResult, EmojiFile (+8 more)

### Community 18 - "Channel Permission Checks"
Cohesion: 0.05
Nodes (22): anonymousCaller, operatorCaller, channelAccess, channelTarget, stubWPClient, TargetsByTelegramID, wpFakeClient, wpSiteHandler (+14 more)

### Community 19 - "Minified Frontend Bundle"
Cohesion: 0.12
Nodes (58): Ae(), Ae(), at(), Be(), ct(), Ee(), et(), G() (+50 more)

### Community 20 - "Administrators UI Dialogs"
Cohesion: 0.11
Nodes (50): ChangePasswordDialog(), ChangePasswordResult, PasswordField(), ConfirmDialog(), SubmitButton(), listAll(), User, Login() (+42 more)

### Community 21 - "Minified Frontend Bundle"
Cohesion: 0.07
Nodes (48): A(), ce(), de(), ee(), f(), fe(), I(), ke() (+40 more)

### Community 22 - "React Router Runtime"
Cohesion: 0.06
Nodes (45): u(), an(), at(), Be(), bi(), ci(), cr(), fi() (+37 more)

### Community 23 - "Permission Evaluation Logic"
Cohesion: 0.08
Nodes (29): Effect, Grant, globalOf(), Action, Authorizer, GlobalPermission, ResourceKind, leaveResourceAlone() (+21 more)

### Community 24 - "Layout Navigation Menu"
Cohesion: 0.08
Nodes (42): @base-ui/react, Breadcrumbs(), AccessKind, NavItem, navItemFor(), navItems, UserMenu(), AccountMenu() (+34 more)

### Community 25 - "Message Display Formatting"
Cohesion: 0.11
Nodes (45): RFC-3339, ChannelAvatar(), MessageCard(), MessageStats(), ReactionPill(), PageHeader(), dateFormat, dateOnlyFormat (+37 more)

### Community 26 - "WordPress Gateway Tests"
Cohesion: 0.10
Nodes (42): createSite(), siteTitles(), TestWordPressListRefusesAMalformedFilter(), TestWordPressListSearchesAndFiltersByCategories(), decodeResp(), firstSite(), wpEnv, newWPEnv() (+34 more)

### Community 27 - "Web Page Shell Types"
Cohesion: 0.05
Nodes (48): PageError(), pageMeta(), PageShell(), section(), SectionHandle, Category, Channel, ChannelAvailability (+40 more)

### Community 28 - "Server Bootstrap Config"
Cohesion: 0.06
Nodes (41): JWTConfig, Server, Type, build(), buildServer(), findDocsUpwards(), APIConfig, Client (+33 more)

### Community 29 - "WordPress Credential Encryption"
Cohesion: 0.08
Nodes (40): WordPressConfig, testSecretBox, wpBox, createUser(), openAt(), site(), testBox(), TestCreateWordPressSiteRefusesASecondURL() (+32 more)

### Community 30 - "Channel Detail Route Tests"
Cohesion: 0.07
Nodes (48): newRawRequest(), assertChannelObject(), assertSameKeys(), channelObjects(), channelObjectsOf(), keysOf(), TestBothChannelDetailRoutesAnswerTheSameChannelObject(), TestLocalChannelDetailIdentifiesTheChannelByItsTelegramID() (+40 more)

### Community 31 - "WordPress Site Management"
Cohesion: 0.09
Nodes (20): categoryChange, WordPressCategories, wordPressCategoryMembers, wordpressRequest, wordpressSiteView, actorAuth(), Config, Registry (+12 more)

### Community 32 - "Site Probe Tests"
Cohesion: 0.07
Nodes (40): NewClient(), newSite(), strPtr(), TestProbeDoesNotFetchAnIconURLAsOne(), TestProbeFollowsNoRedirect(), TestProbeIgnoresAnIconURLThatIsNotAPublicHTTPSURL(), TestProbeKeepsTheTitleWhenTheIconCannotBeResolved(), TestProbeLeavesNoIconWhenTheSiteHasNone() (+32 more)

### Community 33 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (41): d(), f(), at(), bt(), cn(), dt(), ee(), en() (+33 more)

### Community 34 - "MTProto Test Servers"
Cohesion: 0.07
Nodes (20): pipeListener, recordedDial, newDialListener(), startMTProtoServer(), Listen(), Serve(), isSandboxDenied(), newOneShot() (+12 more)

### Community 35 - "Message Card Rendering"
Cohesion: 0.08
Nodes (36): loadAvatar(), loaded, CustomEmojiImage(), faSize(), iconFor, inlineTypes, Linkified(), MessageMediaView() (+28 more)

### Community 36 - "Admin Bootstrap Command"
Cohesion: 0.08
Nodes (42): TokenOptions, Options, newProber(), createAdministrator(), adminRequest(), openStore(), TestCreateAdministratorCreatesTheSchemaOnFirstRun(), TestCreateAdministratorIsIdempotentAndResetsThePassword() (+34 more)

### Community 37 - "MTProto Test Fixtures"
Cohesion: 0.10
Nodes (34): eventLog, panelServer, customEmojiServer(), TestDownloadCustomEmojiFetchesTheSetOnce(), TestDownloadCustomEmojiOfARecycledDocumentIsNotFound(), TestDownloadCustomEmojiReturnsItsBytes(), newPipeListener(), specForCustom() (+26 more)

### Community 38 - "Minified Frontend Bundle"
Cohesion: 0.07
Nodes (44): af(), At(), Bd(), bt(), cf(), df(), dh(), Do() (+36 more)

### Community 39 - "SPA Prerender Script"
Cohesion: 0.08
Nodes (30): appDir, here, out, tsc, appDir, builtDir, copyTree(), embedDir (+22 more)

### Community 40 - "API Credential Scanner"
Cohesion: 0.11
Nodes (9): credentialCheck, handlerRef, project, routeBranch, visitor, handlerNamed(), branchOf(), registerSchema() (+1 more)

### Community 41 - "Telegram Message Types"
Cohesion: 0.06
Nodes (32): MediaLabel(), MediaMimeType(), MediaSize(), MediaType(), MessageDate(), ReplyToMessageID(), SenderOf(), Views() (+24 more)

### Community 42 - "Telegram Channel Lookup"
Cohesion: 0.11
Nodes (13): findRequestOf(), ClassifyError(), HasMore(), HistoryLimit(), HistoryOffset(), classifyJoinError(), inviteKind(), membershipOf() (+5 more)

### Community 43 - "Channel List Store Tests"
Cohesion: 0.08
Nodes (42): channelIDs(), openStore(), TestListChannelsByIDsPagesOverExactlyThoseRows(), TestListChannelsFiltersByAtLeastOneSelectedCategory(), TestListChannelsForUserIsScopedByTheAssignment(), TestListChannelsIsEmptyWithoutRows(), TestListChannelsPaginatesWithATotal(), TestListChannelsSearchesTitleUsernameAndClientKey() (+34 more)

### Community 44 - "Dashboard State Cards"
Cohesion: 0.09
Nodes (34): class-variance-authority, StatCardSkeleton(), connectionLabels, connectionOf(), ConnectionState, TelegramServer, serverLabel(), serverStateBadge() (+26 more)

### Community 45 - "Process Supervisor"
Cohesion: 0.09
Nodes (13): leaveLock, logSink, exitReason(), Process, newProcHandle(), Launcher, Phase, procHandle (+5 more)

### Community 46 - "Daemon Main Bootstrap"
Cohesion: 0.08
Nodes (24): main(), run(), newPromptReader(), panelBuilt(), panelHandler(), runAuth(), runDaemon(), runtimeDeps() (+16 more)

### Community 47 - "Minified Frontend Bundle"
Cohesion: 0.11
Nodes (37): S(), V(), ar(), ae(), ce(), d(), f(), G() (+29 more)

### Community 48 - "Channel View Permissions"
Cohesion: 0.09
Nodes (21): channelView, listedChannel, perms, avatarURLFor(), channelRefOf(), channelViewOf(), AvatarCache, Server (+13 more)

### Community 49 - "Minified Frontend Bundle"
Cohesion: 0.11
Nodes (38): Ao(), as(), Bc(), Bi(), da(), E(), eo(), fi() (+30 more)

### Community 50 - "Worker Supervisor Tests"
Cohesion: 0.18
Nodes (26): Launch, Options, New(), RemoveStaleSocket(), clients(), newFakeProcess(), newProber(), TestAHungWorkerIsRestartedAfterRepeatedFailedProbes() (+18 more)

### Community 51 - "Worker Registry Fakes"
Cohesion: 0.14
Nodes (10): fakeRegistry, workerRegistry, blockingRegistry, classify(), Server, Worker, messageViewOf(), pageMeta() (+2 more)

### Community 52 - "Telegram Dialog Types"
Cohesion: 0.09
Nodes (27): Unread, ChannelKind(), UnreadFromDialog(), chatPhotoID(), hasChatPhoto(), indexChats(), splitDialogs(), toInputPeer() (+19 more)

### Community 53 - "DOM Event Helpers"
Cohesion: 0.09
Nodes (27): Bp(), discover(), dispatchMismatch(), dp(), fp(), Gp(), h(), ie() (+19 more)

### Community 54 - "Worker RPC Service"
Cohesion: 0.09
Nodes (10): RunWorker(), TestRunWorkerReportsAStartupFailure(), Channel, HistoryPage, MediaFile, Message, WithLogf(), WorkerManager (+2 more)

### Community 55 - "Scripted Backend Test Sites"
Cohesion: 0.07
Nodes (11): scriptedSite, identity, scriptTransport, accountEnv, accountEnv, newPanelEnv(), TestAPIAnswersUnderTheBasePath(), TestPagesNeedNoCredential() (+3 more)

### Community 56 - "MTProto Client Factory"
Cohesion: 0.09
Nodes (19): Load(), customInstance(), TestFromClientCustomRejectsASessionFromAnotherDC(), TestFromClientCustomSetsEverythingExplicitly(), TestFromClientInstancesAreIsolated(), TestFromClientOfficialUsesLibraryDefaults(), TestFromClientValidatesItsInput(), customDCList() (+11 more)

### Community 57 - "Gateway Probe Auth Tests"
Cohesion: 0.10
Nodes (7): account, probe, ExampleCallers, Options, prober, Credential, Prober

### Community 58 - "Publish Operation Flow"
Cohesion: 0.13
Nodes (20): actor, publishRequest, sourceMessage, wpPostRequest, visible(), mediaHasBytes(), Server, mustChannelID() (+12 more)

### Community 59 - "Minified Frontend Bundle"
Cohesion: 0.13
Nodes (30): Jt(), qt(), Ai(), ar(), di(), dn(), dr(), En() (+22 more)

### Community 60 - "Minified Frontend Bundle"
Cohesion: 0.11
Nodes (32): ha(), _a(), Aa(), al(), am(), ba(), Ca(), dm() (+24 more)

### Community 61 - "Reusable UI Pickers"
Cohesion: 0.15
Nodes (16): cn, CategoryMultiSelect(), MultiPicker(), Crumb, TelegramServerCombobox(), PermissionPicker(), ComboboxChip(), ComboboxChips() (+8 more)

### Community 62 - "API Docs Comparison"
Cohesion: 0.11
Nodes (22): manualPage, Plan, result, Build(), legacyRoutes(), routeRows(), write(), composePage() (+14 more)

### Community 63 - "Route Schema AST"
Cohesion: 0.09
Nodes (18): Store, serverList, Callers(), callOf(), constValue(), httpCode(), httpStatus(), jsonTypeOf() (+10 more)

### Community 64 - "Safe Dialer Policy"
Cohesion: 0.09
Nodes (20): Client, WithPolicy(), blockedIP(), resolvingDial(), safeDial(), systemLookup(), neverDial(), TestSafeDialConnectsToTheAddressesItChecked() (+12 more)

### Community 65 - "Publish Authorization Tests"
Cohesion: 0.16
Nodes (26): errorCode(), accountEnv, grantPublish(), publishBody(), publishEnv(), TestPublishNeedsACredential(), TestPublishNeedsThePublishingPermission(), TestPublishRefusesADestinationTheAccountCannotPostTo() (+18 more)

### Community 66 - "Minified Frontend Bundle"
Cohesion: 0.19
Nodes (29): Ce(), gi(), go(), Hi(), Ji(), la(), lo(), p() (+21 more)

### Community 67 - "Telegram Client Lifecycle"
Cohesion: 0.15
Nodes (7): accountFromUser(), describe(), Manager, sleep(), waitWithin(), Account, State

### Community 68 - "Minified Frontend Bundle"
Cohesion: 0.12
Nodes (28): br(), gr(), hr(), ii(), lr(), d(), f(), m() (+20 more)

### Community 69 - "HTTP API Client"
Cohesion: 0.10
Nodes (17): RuntimeConfig, Tokens, NewClient(), NewClientWithDialer(), newPipeListener(), TestClientDecodesANullReactionListAsEmpty(), TestClientRejectsMalformedResponses(), TestClientReportsUnreachableWorker() (+9 more)

### Community 70 - "Login and JWT Auth"
Cohesion: 0.11
Nodes (12): Signers, operatorTokens, TestBurnPasswordCheckCompletes(), BurnPasswordCheck(), Missing(), TestMissingAcceptsARealSigner(), TestMissingCatchesATypedNil(), bearerOf() (+4 more)

### Community 71 - "Quoted Route Manifest"
Cohesion: 0.09
Nodes (27): bh(), cc(), cm(), de(), g(), Gh(), Gt(), hh() (+19 more)

### Community 72 - "Login Page UI"
Cohesion: 0.09
Nodes (14): react-router, appVersion, ButtonSize, ButtonVariant, setToken(), clientAction(), clientLoader(), loginError() (+6 more)

### Community 73 - "Minified Frontend Bundle"
Cohesion: 0.11
Nodes (27): bn(), cn(), dn(), ep(), fn(), gn(), hn(), hp() (+19 more)

### Community 74 - "Channel Route Auth"
Cohesion: 0.16
Nodes (24): requireSession(), requestURL(), strip(), clientLoader(), loginRedirect(), clientAction(), clientLoader(), loginRedirect() (+16 more)

### Community 75 - "Minified Frontend Bundle"
Cohesion: 0.14
Nodes (23): bn(), cn(), In(), jn(), Vn(), xn(), ce(), F() (+15 more)

### Community 76 - "Publish Dialog UI"
Cohesion: 0.12
Nodes (18): asArray(), ChannelRow, Destination, MessageLike, PublishDialogProps, SiteRow, TelegramServerRow, compose() (+10 more)

### Community 77 - "Minified Frontend Bundle"
Cohesion: 0.13
Nodes (25): ce(), de(), _e(), et(), F(), I(), Ie(), Ke() (+17 more)

### Community 78 - "UI Package Dependencies"
Cohesion: 0.11
Nodes (24): @fontsource-variable/geist, @fontsource-variable/outfit, radix-ui, @remixicon/react, shadcn, tw-animate-css, zod, dependencies (+16 more)

### Community 79 - "Docs Endpoint Rendering"
Cohesion: 0.21
Nodes (21): Exchange, Facts, Group, RouteFacts, Unresolved, renderEndpoints(), componentOf(), ComposePage() (+13 more)

### Community 80 - "Minified Frontend Bundle"
Cohesion: 0.15
Nodes (23): kn(), ln(), Me(), pn(), rt(), update(), ue(), b() (+15 more)

### Community 81 - "Worker Service RPC"
Cohesion: 0.14
Nodes (9): NormalizePage(), withPaging(), TestWriteConfigIsNotPartOfTheWorker(), channelDTO(), Service, NewService(), TestHealthReportsReadyAndUptime(), TestHealthReportsWhetherASessionFileExists() (+1 more)

### Community 82 - "Route Table Tests"
Cohesion: 0.15
Nodes (19): Caller, Observation, accepted(), serve(), TestRouteTableMatchesGateway(), newProbe(), Route, ProbePath() (+11 more)

### Community 83 - "Channel Peer Lookup"
Cohesion: 0.18
Nodes (20): channelFull(), ctxTimeout(), managerWithPeers(), memberChannel(), TestAvatarDownloadsTheProfilePhoto(), TestAvatarReportsAChannelWithoutAPhoto(), TestFindByInviteDescribesAChannelTheAccountHasNotJoined(), TestFindByInviteReportsAnExistingMembership() (+12 more)

### Community 84 - "Worker HTTP Handler Tests"
Cohesion: 0.14
Nodes (18): NewHandler(), serveRPC(), TestFindRejectsAGetRequest(), TestMediaRouteRejectsBadInput(), TestServerMapsBackendErrorsToCodes(), TestServerRejectsInvalidPagination(), TestServerRejectsUnknownQueryParameters(), TestServerRejectsUnknownRoutesAndMethods() (+10 more)

### Community 85 - "Route Coverage Index"
Cohesion: 0.13
Nodes (22): handledAmong(), handlerRoutes(), indexKey(), matchesSomeRoute(), TestEveryRouteIsDocumented(), TestIndexCoversEveryRoute(), TestRemovedRouteIsDetected(), Concrete() (+14 more)

### Community 86 - "Web App Dependencies"
Cohesion: 0.12
Nodes (21): isbot, @react-router/node, @react-router/serve, @workspace/ui, dependencies, isbot@^5, lucide-react@^1.63.0, react@^19.3.1 (+13 more)

### Community 87 - "Password Hash Command"
Cohesion: 0.15
Nodes (10): promptSet, main(), readPassword(), runHashPassword(), TestHashPasswordCommandPrintsAVerifiableHash(), TestHashPasswordCommandRefusesAnEmptyPassword(), newTerminalPrompter(), stdinFD() (+2 more)

### Community 88 - "Web Component Config"
Cohesion: 0.10
Nodes (19): aliases, components, hooks, lib, ui, utils, iconLibrary, menuAccent (+11 more)

### Community 89 - "UI Component Config"
Cohesion: 0.10
Nodes (19): aliases, components, hooks, lib, ui, utils, iconLibrary, menuAccent (+11 more)

### Community 90 - "MTProto Login Flow"
Cohesion: 0.13
Nodes (5): authenticator, gotdFlow, loginRunner, Prompter, describeAuthError()

### Community 91 - "Web Dev Dependencies"
Cohesion: 0.13
Nodes (18): prettier, prettier-plugin-tailwindcss, turbo, typescript@^6.0.2, devDependencies, prettier@^3.9.10, prettier-plugin-tailwindcss@^0.8.1, turbo@^2.11.10 (+10 more)

### Community 92 - "UI Dev Dependencies"
Cohesion: 0.13
Nodes (19): @turbo/gen, devDependencies, tailwindcss@^4, @tailwindcss/vite@^4, @types/node@^22, @types/react@^19, @types/react-dom@^19, tailwindcss (+11 more)

### Community 93 - "Web TypeScript Config"
Cohesion: 0.11
Nodes (18): compilerOptions, esModuleInterop, jsx, lib, module, moduleResolution, noEmit, paths (+10 more)

### Community 94 - "Minified Frontend Bundle"
Cohesion: 0.24
Nodes (18): cl(), dl(), fa(), hl(), hm(), je(), Oa(), qo() (+10 more)

### Community 95 - "Docker Compose Setup"
Cohesion: 0.12
Nodes (16): VERSION, args, context, TELESYNC_CONFIG, services, telesync, build, env_file (+8 more)

### Community 96 - "Docs Site Tests"
Cohesion: 0.19
Nodes (16): docsHandlerForTest, docsSite(), TestDocsLandingPageRenders(), TestDocsMarkdownRendersTablesCodeAndLinks(), TestDocsMethodNotAllowed(), TestDocsMissingAndUnsupportedFilesAre404(), TestDocsNestedPagesAndRelativeLinks(), TestDocsRawHTMLAndDangerousLinksAreInert() (+8 more)

### Community 97 - "Root App Layout"
Cohesion: 0.15
Nodes (9): App(), ToastCard(), ToastContext, ToastContextValue, ToastOptions, ToastProvider(), ToastRecord, ToastVariant (+1 more)

### Community 98 - "API Examples Tests"
Cohesion: 0.23
Nodes (16): Snapshot, exampleFiles(), exercisesSome(), readExamples(), readIndex(), routeKey(), TestExamplesAreValidAndIndexed(), TestExamplesRunAgainstTheGateway() (+8 more)

### Community 99 - "Peer Store Persistence"
Cohesion: 0.18
Nodes (10): Store, Open(), TestACorruptedFileIsRecoveredAsEmpty(), TestAnEmptyPathIsRejected(), TestSaveAndFindRoundTrip(), TestSaveRejectsZeroValues(), TestTheFileIsRestricted(), TestTheStoreSurvivesAReopen() (+2 more)

### Community 100 - "Docker Build CI"
Cohesion: 0.13
Nodes (15): with, build-args, cache, cache-dependency-path, cache-from, cache-to, context, file (+7 more)

### Community 101 - "Turbo Build Config"
Cohesion: 0.13
Nodes (14): dependsOn, inputs, outputs, cache, persistent, dependsOn, $schema, tasks (+6 more)

### Community 102 - "UI TypeScript Config"
Cohesion: 0.14
Nodes (13): compilerOptions, jsx, lib, module, moduleResolution, noEmit, paths, skipLibCheck (+5 more)

### Community 103 - "Test Server Helpers"
Cohesion: 0.19
Nodes (10): documentedServers(), serverUser, serverView, requestIDFrom(), safeID(), Server, ServerInfo, Server (+2 more)

### Community 104 - "Publish Idempotency"
Cohesion: 0.29
Nodes (12): publishOp(), publishStore(), TestAConfirmedSuccessIsReplayedNotRepeated(), TestAFailedAttemptMayBeRetried(), TestAnInFlightAttemptIsNotReRunConcurrently(), TestAnUncertainOutcomeIsNeverReRun(), TestAPublishOperationIsRecordedAsInFlight(), TestDeletingAnAccountRemovesItsPublishOperations() (+4 more)

### Community 105 - "Lint TypeScript Config"
Cohesion: 0.17
Nodes (11): compilerOptions, jsx, lib, module, moduleResolution, outDir, skipLibCheck, strict (+3 more)

### Community 107 - "Emoji Document Cache"
Cohesion: 0.24
Nodes (4): documentType(), classifyEmojiError(), sharedFileReference(), withFileReference()

### Community 108 - "Message Send Service"
Cohesion: 0.44
Nodes (9): codeOf(), readyStatus(), sendService(), TestServiceSendMessagePassesThePreparedMessageThrough(), TestServiceSendMessageRefusesWorkWithoutAnAuthorizedAccount(), TestServiceSendMessageReportsAFloodWait(), TestServiceSendMessageReportsAnUnresolvableDestination(), TestServiceSendMessageReportsARefusedDestination() (+1 more)

### Community 109 - "Auth Prompt Input"
Cohesion: 0.36
Nodes (8): sentCodeHint(), newTestPrompter(), TestPromptCodeDoesNotEchoTheCodeItself(), TestPromptCodeHintsDelivery(), TestPromptEOFIsAnErrorNotAHang(), TestPromptPasswordFromPipeIsRead(), TestPromptPhoneReadsLine(), TestSentCodeHintVariants()

### Community 110 - "CI Workflow Config"
Cohesion: 0.22
Nodes (8): concurrency, cancel-in-progress, group, jobs, name, permissions, contents, packages

### Community 111 - "Frontend Build Config"
Cohesion: 0.25
Nodes (4): @react-router/dev, vite, @react-router/dev@8.5.0, vite@^8

### Community 112 - "Docker Image Retag"
Cohesion: 0.29
Nodes (7): env, name, runs-on, OWNER, RETAG, TAG, docker

### Community 113 - "Workflow Step Config"
Cohesion: 0.48
Nodes (7): steps, steps, steps, if, name, run, uses

### Community 114 - "Package Scripts"
Cohesion: 0.29
Nodes (7): scripts, build, dev, format, start, test, typecheck

### Community 115 - "Web TypeScript Config"
Cohesion: 0.29
Nodes (6): compilerOptions, module, moduleResolution, skipLibCheck, strict, target

### Community 116 - "Frontend CI Job"
Cohesion: 0.33
Nodes (6): run, defaults, name, runs-on, frontend, working-directory

### Community 117 - "Workflow Triggers"
Cohesion: 0.33
Nodes (6): on, pull_request, push, workflow_dispatch, branches, tags

### Community 118 - "Channel Category Filing"
Cohesion: 0.47
Nodes (5): categoryChannelsPath(), channelCategoriesPath(), filedEnv(), TestAChannelIsFiledAndListedFromBothSides(), TestFilingNeedsTheChannelAndTheCategory()

### Community 122 - "Package Scripts"
Cohesion: 0.40
Nodes (5): scripts, build, dev, format, typecheck

### Community 123 - "Package Exports Map"
Cohesion: 0.40
Nodes (5): exports, ./components/*, ./globals.css, ./hooks/*, ./lib/*

### Community 124 - "Go CI Job"
Cohesion: 0.67
Nodes (3): name, runs-on, go

### Community 129 - "Format Scripts"
Cohesion: 0.67
Nodes (3): scripts, format, typecheck

## Knowledge Gaps
- **7 isolated node(s):** `accountEnv`, `accountEnv`, `accountEnv`, `wpEnv`, `accountEnv` (+2 more)
  These have ≤1 connection - possible missing edges. (Counts symbols only; 637 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **20 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `ctxTimeout()` connect `Channel Peer Lookup` to `Raw API Clients`, `Worker Test Doubles`, `Config Loading Tests`?**
  _High betweenness centrality (0.011) - this node is a cross-community bridge._
- **Are the 85 inferred relationships involving `readyWorker()` (e.g. with `TestAChannelWithoutAGrantIsForbidden()` and `TestAMissingOrDuplicateTokenDoesNotRevealWhetherAClientExists()`) actually correct?**
  _`readyWorker()` has 85 INFERRED edges - model-reasoned connections that need verification._
- **What connects `accountEnv`, `accountEnv`, `accountEnv` to the rest of the system?**
  _7 weakly-connected nodes found - possible missing edges._
- **Should `Raw API Clients` be split into smaller, more focused modules?**
  _Cohesion score 0.05967990150815636 - nodes in this community are weakly interconnected._
- **Why does `v()` connect `Minified Frontend Bundle` to `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Asset File Dialog Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `Minified Frontend Bundle`, `React Router Runtime`, `Minified Frontend Bundle`?**
  _High betweenness centrality (0.008) - this node is a cross-community bridge._
- **Should `Minified Frontend Bundle` be split into smaller, more focused modules?**
  _Cohesion score 0.03336732182412714 - nodes in this community are weakly interconnected._
- **Why does `Authorizer` connect `Permission Evaluation Logic` to `Raw API Clients`, `Docs Static Server`, `SQLite Store Queries`, `Channel Store Queries`, `Channel View Permissions`, `Channel Permission Checks`, `Publish Operation Flow`, `WordPress Site Management`?**
  _High betweenness centrality (0.007) - this node is a cross-community bridge._