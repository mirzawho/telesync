# Graph Report - telsync  (2026-11-09)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 8803 nodes · 36886 edges · 236 communities (197 shown, 39 thin omitted)
- Extraction: 93% EXTRACTED · 7% INFERRED · 0% AMBIGUOUS · INFERRED: 2504 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `887910fd`
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
- Administrators Route Bundle
- Administrators Route Bundle
- Categories Route Bundle
- Categories Route Bundle
- Channels Route Bundle
- Channels Route Bundle
- Login Route Bundle
- Login Route Bundle
- Client Manifest
- WordPress Route Bundle
- WordPress Route Bundle
- Telesync Go Package
- output.go
- Service
- embed.go
- Store
- compilerOptions
- newUpdateHandler
- newWorld
- NormalizePermissions
- prompt_test.go
- exchange
- .requireChannelView
- webhooktest/config_test.go
- resolvingDial
- compilerOptions
- .app
- .buildSend
- localEnv
- spec
- ports
- ref_node_fs
- images_test.go
- newEngine
- worker/send_test.go
- k8s.yaml
- APIConfig
- ci.yml
- PersistentVolumeClaim telesync-sessdata
- Secret telesync-credentials
- ref_node_path
- test-responsive.mjs
- docker
- steps
- He
- Listen
- Secret telesync-jwt
- securityContext
- scripts
- test-media.mjs
- test-panel.mjs
- test-publish.mjs
- test-webhook.mjs
- compilerOptions
- App
- frontend
- on
- execProcess
- filedEnv
- Secret telesync-wordpress
- ref_node_child_process
- woff2-cmap.mjs
- anonymous
- operatorCaller
- autoTransport
- permissionsRequest
- .servePanel
- mediahttp.go
- scripts
- scripts
- exports
- pipeTransport
- Error
- .RoundTrip
- scriptedSite
- publishError
- go
- mediaReader
- RemoveStaleSocket
- floodWait
- pipeAddr
- FloodWait
- fakeEnqueuer
- byteReader
- github.com/mirza/telesync

## God Nodes (most connected - your core abstractions)
1. `readyWorker()` - 162 edges
2. `Button()` - 105 edges
3. `newAccountEnv()` - 97 edges
4. `Server` - 96 edges
5. `U()` - 90 edges
6. `newEnv()` - 89 edges
7. `i()` - 82 edges
8. `v()` - 81 edges
9. `react` - 76 edges
10. `Manager` - 73 edges

## Surprising Connections (you probably didn't know these)
- `runAuth()` --references--> `Result`  [EXTRACTED]
  cmd/telesync/runtime.go → internal/login/login.go
- `accountCaller` --references--> `PermissionSet`  [EXTRACTED]
  cmd/docsgen/main.go → internal/channelstore/permissions_store.go
- `createAdministrator()` --references--> `AdminResult`  [EXTRACTED]
  cmd/telesync/admin.go → internal/cli/admin.go
- `docWebhookService()` --references--> `Store`  [EXTRACTED]
  cmd/docsgen/main.go → internal/channelstore/store.go
- `openStore()` --references--> `Store`  [EXTRACTED]
  cmd/telesync/admin_test.go → internal/channelstore/store.go

## Import Cycles
- 2-file cycle: `internal/apidocs/docs_test.go -> internal/apidocs/table_test.go -> internal/apidocs/docs_test.go`
- 2-file cycle: `internal/apidocs/docs_test.go -> internal/apidocs/probe_test.go -> internal/apidocs/docs_test.go`
- 2-file cycle: `internal/cli/admin_test.go -> internal/cli/configresolve_test.go -> internal/cli/admin_test.go`
- 2-file cycle: `internal/cli/configresolve_test.go -> internal/cli/daemon_test.go -> internal/cli/configresolve_test.go`
- 2-file cycle: `internal/cli/admin_test.go -> internal/cli/daemon_test.go -> internal/cli/admin_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/auth_e2e_test.go`
- 2-file cycle: `internal/runtime/e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/e2e_test.go`
- 2-file cycle: `internal/runtime/e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/e2e_test.go`
- 2-file cycle: `internal/gotdclient/entities_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/entities_test.go`
- 2-file cycle: `internal/gotdclient/manager_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/manager_test.go`
- 2-file cycle: `internal/gotdclient/discover_test.go -> internal/gotdclient/metrics_test.go -> internal/gotdclient/discover_test.go`
- 2-file cycle: `internal/gotdclient/metrics_test.go -> internal/gotdclient/reconnect_test.go -> internal/gotdclient/metrics_test.go`
- 3-file cycle: `internal/apidocs/docs_test.go -> internal/apidocs/probe_test.go -> internal/apidocs/table_test.go -> internal/apidocs/docs_test.go`
- 3-file cycle: `internal/cli/admin_test.go -> internal/cli/daemon_test.go -> internal/cli/configresolve_test.go -> internal/cli/admin_test.go`
- 3-file cycle: `internal/cli/admin_test.go -> internal/cli/configresolve_test.go -> internal/cli/daemon_test.go -> internal/cli/admin_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/e2e_test.go -> internal/runtime/strict_test.go -> internal/runtime/auth_e2e_test.go`
- 3-file cycle: `internal/runtime/auth_e2e_test.go -> internal/runtime/runtime_test.go -> internal/runtime/e2e_test.go -> internal/runtime/auth_e2e_test.go`

## Communities (236 total, 39 thin omitted)

### Community 0 - "Raw API Clients"
Cohesion: 0.01
Nodes (255): TokenOptions, wfRun, brokenBody, accepted(), TestRouteTableMatchesGateway(), fakeServers(), newProbe(), ScriptedSite() (+247 more)

### Community 1 - "Minified Frontend Bundle"
Cohesion: 0.02
Nodes (233): react-router, canManageWebhooks(), testOutcomeOf(), withDestinations(), emptySiteTerms(), Crumb, PageHeader(), EmptyState() (+225 more)

### Community 2 - "Minified Frontend Bundle"
Cohesion: 0.03
Nodes (229): Q(), t(), ae(), ai(), an(), ar(), at(), bn() (+221 more)

### Community 3 - "Worker Test Doubles"
Cohesion: 0.03
Nodes (185): AdministratorPasswordContent(), ChangeAdministratorPasswordDialog(), CommonProps, CreateAdministratorContent(), CreateAdministratorDialog(), EditAdministratorContent(), EditAdministratorDialog(), FormError() (+177 more)

### Community 4 - "Config Loading Tests"
Cohesion: 0.04
Nodes (147): i(), r(), R(), V(), z(), a(), c(), o() (+139 more)

### Community 5 - "Minified Frontend Bundle"
Cohesion: 0.02
Nodes (59): fakeWorker, documentedChannel(), documentedMessage(), documentedWorker, blockingWorker, fakeWorker, listedChannel, perms (+51 more)

### Community 6 - "Docs Static Server"
Cohesion: 0.03
Nodes (179): fakeStore, findBody, ManagedRecord, testEnv, bodyOf(), stripRequestID(), TestAChannelWithoutAGrantIsForbidden(), TestAMissingOrDuplicateTokenDoesNotRevealWhetherAClientExists() (+171 more)

### Community 7 - "Gateway Authorization Tests"
Cohesion: 0.03
Nodes (157): at(), Be(), bt(), Ct(), Dt(), et(), Fe(), ft() (+149 more)

### Community 8 - "Minified Frontend Bundle"
Cohesion: 0.07
Nodes (60): chatMessage, chatRequest, liveAnswer, responseFormat, rawAI, rawAPI, rawAutomation, rawClient (+52 more)

### Community 9 - "Gateway Test Fixtures"
Cohesion: 0.03
Nodes (121): RFC-7230, @base-ui/react, cn, react, AutomationFormActions(), AutomationFormLayout(), ComboOption, OptionCombobox() (+113 more)

### Community 10 - "Asset File Dialog Bundle"
Cohesion: 0.02
Nodes (34): operator, migration, newProber(), anonymousCaller, fakeJobs, fakeOutbound, fakeSubs, fanOutFailingJobs (+26 more)

### Community 11 - "SQLite Store Queries"
Cohesion: 0.04
Nodes (122): lucide-react, ContentEditorPanel(), ContentEditorPanelProps, FeaturedImageSelector(), MediaAttachmentEditor(), PublishErrors, TelegramMessageEditor(), toMessageMedia() (+114 more)

### Community 12 - "Stale Cache Refresh Tests"
Cohesion: 0.03
Nodes (59): WebhookDeliveryCounts, WebhookDeliverySummary, querier, webhookRow, DeliveryListOptions, Webhook, WebhookAttempt, WebhookDelivery (+51 more)

### Community 13 - "Minified Frontend Bundle"
Cohesion: 0.05
Nodes (127): ot(), c(), l(), an(), ar(), at(), bn(), br() (+119 more)

### Community 14 - "Runtime CLI Harness"
Cohesion: 0.04
Nodes (101): f(), Qt(), ae(), F(), fe(), ie(), j(), le() (+93 more)

### Community 15 - "Runtime Startup Arguments"
Cohesion: 0.03
Nodes (48): Assignment, queryer, scanner, brokenCategoryStore, CategoryByIDs, categoryIn(), Category, ListQuery (+40 more)

### Community 16 - "Channel Store Queries"
Cohesion: 0.05
Nodes (99): RFC-3339, AIStatusBadge(), BadgeVariant, CodeBlock(), CopyButton(), DeliveryStatusBadge(), EnabledBadge(), HeaderBlock() (+91 more)

### Community 17 - "Worker RPC Types"
Cohesion: 0.03
Nodes (75): react-dom, Inline(), MessageText(), Outbound(), APK, MP3, StatCardSkeleton(), ChannelFilter (+67 more)

### Community 18 - "Channel Permission Checks"
Cohesion: 0.04
Nodes (95): af(), ah(), ai(), an(), ap(), At(), Bi(), bt() (+87 more)

### Community 19 - "Minified Frontend Bundle"
Cohesion: 0.28
Nodes (22): autoProbe, categoryJSON, categoryOne, categoryPage, channelCategoriesRequest, channelDetailJSON, createUserRequest, dashboardStatsView (+14 more)

### Community 20 - "Administrators UI Dialogs"
Cohesion: 0.18
Nodes (34): attemptView, deliveryView, publicationView, requestView, runView, sourceChannelView, statusFailureView, statusView (+26 more)

### Community 21 - "Minified Frontend Bundle"
Cohesion: 0.04
Nodes (25): NormalizePage(), JoinResult, AvatarFile, ChannelRef, SendRequest, SendResult, Channel, EmojiFile (+17 more)

### Community 22 - "React Router Runtime"
Cohesion: 0.04
Nodes (36): WorkflowRunSummary, WorkflowSourceChannel, RunListOptions, Workflow, WorkflowDestination, WorkflowInput, WorkflowListOptions, WorkflowPage (+28 more)

### Community 23 - "Permission Evaluation Logic"
Cohesion: 0.06
Nodes (93): Processor, requestRecord, AIConfig, New(), asJSON(), chatAnswerWith(), messages(), mustPolish() (+85 more)

### Community 24 - "Layout Navigation Menu"
Cohesion: 0.05
Nodes (83): class-variance-authority, ChannelMessageItem(), CustomEmojiImage(), fetchCustomEmoji(), ReactionChip(), reactionLabel(), ReactionMark(), Views() (+75 more)

### Community 25 - "Message Display Formatting"
Cohesion: 0.04
Nodes (33): AutomationJobQuery, docWebhookService(), autoServices, probeWebhookService(), Job, JobHandler, JobKind, JobStatus (+25 more)

### Community 26 - "WordPress Gateway Tests"
Cohesion: 0.04
Nodes (44): RuntimeConfig, Server, AdminDeps, Deps, newPromptReader(), newTerminalPrompter(), automationSocketPath(), runAuth() (+36 more)

### Community 27 - "Web Page Shell Types"
Cohesion: 0.04
Nodes (53): m(), bn(), cn(), constructor(), create(), Dn(), e(), fn() (+45 more)

### Community 28 - "Server Bootstrap Config"
Cohesion: 0.10
Nodes (28): probeWebhookTransport, scriptedReceiver, Server, webhookRequest, visible(), TestValidatePasswordEnforcesALength(), ValidatePassword(), anyVisible() (+20 more)

### Community 29 - "WordPress Credential Encryption"
Cohesion: 0.05
Nodes (48): b(), c(), ce(), d(), de(), _e(), fe(), ge() (+40 more)

### Community 30 - "Channel Detail Route Tests"
Cohesion: 0.05
Nodes (54): BottomNav(), BottomNavItem(), BottomNavLink(), BottomNavOverflow(), itemClassName(), ALL, appDir, classOf() (+46 more)

### Community 31 - "WordPress Site Management"
Cohesion: 0.06
Nodes (46): dispatchState(), NewStateBackend(), NewStatePersistence(), snapshotToState(), stateToSnapshot(), bridged(), TestChannelPointsAndPeerHashesCrossTheBridge(), TestUpdateStatePersistsAndReloadsAfterARestart() (+38 more)

### Community 32 - "Site Probe Tests"
Cohesion: 0.08
Nodes (61): publishCrossBody(), publishCrossEnv(), TestPublishClassifiesASendFailureTheSameHoweverTheWorkerReportedIt(), TestPublishHonoursAnExplicitDetachWithoutReadingTheSourceMedia(), TestPublishLogsTheSourceConnectionWhenTheAttachmentCannotBeRead(), TestPublishNamesAFailedMediaSendAsADestinationFailure(), TestPublishNeverUsesTheDestinationConnectionToResolveAMediaID(), TestPublishOmitsMediaOnlyWhenTheSourceMessageHasNone() (+53 more)

### Community 33 - "Minified Frontend Bundle"
Cohesion: 0.07
Nodes (69): ae(), al(), b(), bh(), c(), cc(), cm(), Du() (+61 more)

### Community 34 - "MTProto Test Servers"
Cohesion: 0.09
Nodes (67): ai(), aiConfig(), TestAnAIKeyFromAVariableIsAcceptedAndNotMarkedAsInFile(), TestAnAIKeyFromBothSourcesIsRefused(), TestAnAIKeyInTheFileIsAcceptedAndMarkedAsSuch(), TestAnAIKeyIsNeverMarshalledWithTheConfiguration(), TestAnAIRefusalNeverEchoesTheKeyItWasGiven(), TestAnAmbiguousAIKeyDoesNotAlsoComplainThatTheVariableIsUnset() (+59 more)

### Community 35 - "Message Card Rendering"
Cohesion: 0.09
Nodes (62): Ye(), a(), ae(), ee(), ie(), l(), o(), oe() (+54 more)

### Community 36 - "Admin Bootstrap Command"
Cohesion: 0.06
Nodes (57): explainWebhookError(), isBaseURLRequired(), isConflictRefusal(), secretFieldErrorOf(), baseURLRequired(), conflict(), refuse(), WebhookRefusal (+49 more)

### Community 37 - "MTProto Test Fixtures"
Cohesion: 0.06
Nodes (26): ChannelListQuery, documentedRecord(), managedStore, CategoryMembers, ChannelExcluder, ChannelScope, failingDeleteStore, siteStore (+18 more)

### Community 38 - "Minified Frontend Bundle"
Cohesion: 0.06
Nodes (55): at(), Bt(), ct(), dn(), dt(), Et(), F(), r() (+47 more)

### Community 39 - "SPA Prerender Script"
Cohesion: 0.07
Nodes (54): H(), he(), W(), x(), _t(), be(), de(), E() (+46 more)

### Community 40 - "API Credential Scanner"
Cohesion: 0.05
Nodes (61): channelIDs(), openStore(), TestListChannelsByIDsPagesOverExactlyThoseRows(), TestListChannelsFiltersByAtLeastOneSelectedCategory(), TestListChannelsForUserIsScopedByTheAssignment(), TestListChannelsIsEmptyWithoutRows(), TestListChannelsPaginatesWithATotal(), TestListChannelsSearchesTitleUsernameAndClientKey() (+53 more)

### Community 41 - "Telegram Message Types"
Cohesion: 0.11
Nodes (49): autoMeta, TestTheIncrementalSettersRefuseAnUnknownAccount(), autoEnv, TestAuthMeReportsAutomationAccess(), TestAutomationAreaIsClosedWithoutAutomationView(), TestAutomationExplicitDenyBeatsTheGlobalView(), TestAutomationPermissionsDoNotBreakOtherRoutes(), TestAutomationRefusesAnUnreachableSourceOrDestination() (+41 more)

### Community 42 - "Telegram Channel Lookup"
Cohesion: 0.06
Nodes (18): X(), Y(), z(), d(), g(), l(), u(), c() (+10 more)

### Community 43 - "Channel List Store Tests"
Cohesion: 0.09
Nodes (47): siteListPage, siteView, createSite(), wpEnv, siteIDsOf(), TestALimitedAccountSeesAndReachesOnlyItsOwnWordPressSite(), TestSavingChannelPermissionsKeepsTheStoredSiteGrants(), siteTitles() (+39 more)

### Community 44 - "Dashboard State Cards"
Cohesion: 0.06
Nodes (34): chatAnswer, AutomationConfig, RecoveryConfig, pacedBody, AutomationOptions, backfillAcceptor, backfiller, backfillReader (+26 more)

### Community 45 - "Process Supervisor"
Cohesion: 0.07
Nodes (15): Manager, mediaFetcher, historyMessages(), classifyEmojiError(), sharedFileReference(), withFileReference(), findMessage(), mediaLocation() (+7 more)

### Community 46 - "Daemon Main Bootstrap"
Cohesion: 0.08
Nodes (56): TestWriteConfigIsNotPartOfTheWorker(), NewService(), mustCoded(), newService(), readyFake(), startedAt(), TestAvatarReturnsBytesAndMapsAMissingPhoto(), TestChannelDTOOmitsUnavailableUnreadCount() (+48 more)

### Community 47 - "Minified Frontend Bundle"
Cohesion: 0.07
Nodes (18): categoryChange, WordPressCategories, wordPressCategoryMembers, wordpressRequest, WordPressSiteService, wordpressSiteView, Actor, WordPressSite (+10 more)

### Community 48 - "Channel View Permissions"
Cohesion: 0.18
Nodes (19): TestManagerDefaultReconnectIsTenSeconds(), JoinOutcome, TestEntityTypesAreTheOnesTelegramDefines(), TestUTF16LenCountsCodeUnitsNotBytesOrRunes(), UTF16Len(), cutInvite(), cutLink(), FormatChannelID() (+11 more)

### Community 49 - "Minified Frontend Bundle"
Cohesion: 0.06
Nodes (42): be(), m(), t(), ve(), ye(), Se(), Bt(), Ct() (+34 more)

### Community 50 - "Worker Supervisor Tests"
Cohesion: 0.08
Nodes (20): docsSite, scriptTransport, leaveRequest, leaverOf, LocalChannelStore, pageSite, acquireLeaveLock(), codeOfAuthError() (+12 more)

### Community 51 - "Worker Registry Fakes"
Cohesion: 0.07
Nodes (54): Options, createAdministrator(), adminRequest(), openStore(), TestCreateAdministratorCreatesTheSchemaOnFirstRun(), TestCreateAdministratorIsIdempotentAndResetsThePassword(), TestCreateAdministratorNeverRemovesItsOwnAccess(), TestCreateAdministratorRejectsInvalidInputBeforeTouchingAnything() (+46 more)

### Community 52 - "Telegram Dialog Types"
Cohesion: 0.09
Nodes (21): ChannelKind(), ClassifyError(), chatPhotoID(), hasChatPhoto(), indexChats(), splitDialogs(), toInputPeer(), channelAccess() (+13 more)

### Community 53 - "DOM Event Helpers"
Cohesion: 0.09
Nodes (14): credentialCheck, handlerRef, project, routeBranch, visitor, callOf(), handlerNamed(), jsonTypeOf() (+6 more)

### Community 54 - "Worker RPC Service"
Cohesion: 0.09
Nodes (50): cloneRequest(), signedRequest(), testAuth(), TestBearerChecksTheAuthorizationHeaderAndNothingElse(), TestCustomHeaderChecksTheConfiguredHeaderName(), TestCustomHeaderHonoursAConfiguredName(), TestHMACAcceptsASecretInTheStandardWebhooksForm(), TestHMACAcceptsATimestampJustInsideTheTolerance() (+42 more)

### Community 55 - "Scripted Backend Test Sites"
Cohesion: 0.05
Nodes (27): MessagePayload, fakeStore, LocalIDsFunc, ManagedChannelResolver, Outcome, publishResponse, dedupStore, EventStore (+19 more)

### Community 56 - "MTProto Client Factory"
Cohesion: 0.10
Nodes (27): Effect, Grant, globalOf(), Action, Authorizer, GlobalPermission, ResourceKind, leaveResourceAlone() (+19 more)

### Community 57 - "Gateway Probe Auth Tests"
Cohesion: 0.07
Nodes (39): Unread, documentType(), HasMore(), HistoryLimit(), HistoryOffset(), largestPhoto(), MediaFileName(), MediaLabel() (+31 more)

### Community 58 - "Publish Operation Flow"
Cohesion: 0.12
Nodes (42): panelServer, requestLog, customEmojiServer(), TestDownloadCustomEmojiFetchesTheSetOnce(), TestDownloadCustomEmojiOfARecycledDocumentIsNotFound(), TestDownloadCustomEmojiReturnsItsBytes(), TestStatusRecordsTheLastSuccessfulProbe(), TestUnauthorizedClientHasNoLastSuccess() (+34 more)

### Community 59 - "Minified Frontend Bundle"
Cohesion: 0.06
Nodes (38): NewClient(), NewClientWithDialer(), NewHandler(), firstChunk(), newPipeListener(), readMedia(), rpcClient(), TestAvatarRoundTripCarriesBytes() (+30 more)

### Community 60 - "Minified Frontend Bundle"
Cohesion: 0.10
Nodes (39): ChannelAvatar(), loadAvatar(), loaded, ChannelMessagesPane(), dayOf(), DaySeparator(), HistoryBottom(), HistoryTop() (+31 more)

### Community 61 - "Reusable UI Pickers"
Cohesion: 0.09
Nodes (47): am(), au(), bm(), bn(), cn(), dn(), fn(), gn() (+39 more)

### Community 62 - "API Docs Comparison"
Cohesion: 0.12
Nodes (35): pipeline, receiver, JobHandlers, newPipeline(), TestAFailedDeliveryIsRecordedAndRetried(), TestAnObservedMessageDeliversToTheWebhookAndRecordsTheExchange(), TestOurOwnPublicationNeverReachesThePipeline(), TestReplayingAnObservationDoesNotDeliverTwice() (+27 more)

### Community 63 - "Route Schema AST"
Cohesion: 0.11
Nodes (34): clock, flight, meta, Options, Cache, FetchFunc, Image, Open() (+26 more)

### Community 64 - "Safe Dialer Policy"
Cohesion: 0.08
Nodes (39): accepted(), refused(), TestANameThatMerelyContainsLocalhostIsNotLocalhost(), TestCredentialsAndSchemesAreStillRefused(), TestDisguisedLoopbackSpellingsAreNotAccepted(), TestLocalhostIsRefusedByDefault(), TestPublicWebhooksKeepTheHTTPSRule(), TestTheExceptionIsNotAPrivateNetworkAllowance() (+31 more)

### Community 65 - "Publish Authorization Tests"
Cohesion: 0.10
Nodes (42): Aa(), Ao(), as(), Bc(), da(), dl(), E(), gl() (+34 more)

### Community 66 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (16): fakeRegistry, workerRegistry, blockingRegistry, byteRange, rangeKind, classify(), Registry, Worker (+8 more)

### Community 67 - "Telegram Client Lifecycle"
Cohesion: 0.06
Nodes (16): autoAccounts, identity, scriptTransport, accountEnv, accountEnv, newPanelEnv(), TestAPIAnswersUnderTheBasePath(), TestPagesNeedNoCredential() (+8 more)

### Community 68 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (35): testSecretBox, wpBox, keyFromFile(), itoa(), testChannel(), TestDeletingAWebhookRemovesItsDeliveriesAndAttempts(), testEvent(), testSeal() (+27 more)

### Community 69 - "HTTP API Client"
Cohesion: 0.08
Nodes (9): account, probe, accountCaller, ExampleCallers, prober, Credential, Prober, Callers() (+1 more)

### Community 70 - "Login and JWT Auth"
Cohesion: 0.08
Nodes (17): Signers, actor, operatorTokens, TestBurnPasswordCheckCompletes(), TestHashPasswordUsesAFreshSaltEachTime(), BurnPasswordCheck(), HashPassword(), Missing() (+9 more)

### Community 71 - "Quoted Route Manifest"
Cohesion: 0.07
Nodes (33): isbot, @react-router/dev, @react-router/node, @react-router/serve, tailwindcss, @tailwindcss/vite, @types/node, @types/react (+25 more)

### Community 72 - "Login Page UI"
Cohesion: 0.13
Nodes (35): testClock, automationStore(), eventFor(), TestADuplicateMessageIsNotStoredTwiceButKeepsItsObserver(), TestAnEventForAnUnmanagedChannelIsRefused(), TestAnEventIsStoredWithItsIdentityAndPayload(), TestAnEventWithoutAValidIdentityIsRefused(), TestTheSameMessageIDInAnotherChannelIsADistinctEvent() (+27 more)

### Community 73 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (36): _a(), ba(), bl(), cl(), dm(), em(), ep(), fa() (+28 more)

### Community 74 - "Channel Route Auth"
Cohesion: 0.11
Nodes (32): newRawRequest(), TestTheLocalChannelListKeepsItsOwnListingContract(), TestLocalChannelsListFiltersBySelectedCategories(), TestLocalChannelsListRefusesAMalformedCategoryFilter(), channelsEnv(), decodeBody(), failingDeleteConfig(), accountEnv (+24 more)

### Community 75 - "Minified Frontend Bundle"
Cohesion: 0.13
Nodes (21): discoveryBackend, channelFull(), ctxTimeout(), managerWithPeers(), memberChannel(), TestAvatarDownloadsTheProfilePhoto(), TestAvatarReportsAChannelWithoutAPhoto(), TestFindByInviteDescribesAChannelTheAccountHasNotJoined() (+13 more)

### Community 76 - "Publish Dialog UI"
Cohesion: 0.08
Nodes (15): eventLog, pipeAddr, pipeListener, recordedDial, newPipeListener(), specForCustom(), mustGenerateKey(), TestManagerRequestsFailFastWhenNeverConnected() (+7 more)

### Community 77 - "Minified Frontend Bundle"
Cohesion: 0.08
Nodes (23): ae(), ce(), constructor(), le(), U(), X(), en(), l() (+15 more)

### Community 78 - "UI Package Dependencies"
Cohesion: 0.17
Nodes (32): Ce(), de(), g(), gi(), go(), Hi(), Ji(), la() (+24 more)

### Community 79 - "Docs Endpoint Rendering"
Cohesion: 0.19
Nodes (29): runtimeHarness, New(), docsTree(), TestAuthFailsWhenTheSessionIsStillUnauthorized(), TestAuthRejectsANonPositiveTimeout(), TestAuthRejectsAnUnknownKey(), TestAuthRunsTheInteractiveFlowForOneClient(), TestAuthTimesOutWhenTheConnectionNeverBecomesReady() (+21 more)

### Community 80 - "Minified Frontend Bundle"
Cohesion: 0.13
Nodes (21): crumbPart, docsHandler, navItem, pageData, admissable(), crumb(), crumbName(), DocURL() (+13 more)

### Community 81 - "Worker Service RPC"
Cohesion: 0.15
Nodes (26): fmtHeaders(), headerValue(), newEnv(), TestAMalformedSigningSecretIsRefusedWhenItIsSaved(), TestAMissingChannelStillDeliversWithTheIdentifiersAlone(), TestARetryExtendsTheSameDeliveryAndAppendsAnAttempt(), TestASecretCanBeRemovedWithoutEverReadingItBack(), TestBearerAndHeaderModesCarryTheSecretInTheRightPlace() (+18 more)

### Community 82 - "Route Table Tests"
Cohesion: 0.12
Nodes (25): g(), B(), ee(), F(), f(), p(), H(), L() (+17 more)

### Community 83 - "Channel Peer Lookup"
Cohesion: 0.15
Nodes (12): main(), Backoff(), exitReason(), Process, newProcHandle(), TestBackoffIsJittered(), Launcher, Phase (+4 more)

### Community 84 - "Worker HTTP Handler Tests"
Cohesion: 0.11
Nodes (10): newDialListener(), startMTProtoServer(), newOneShot(), pipeListener, dialListener, dialRecorder, pipeDialer, pipeListener (+2 more)

### Community 85 - "Route Coverage Index"
Cohesion: 0.12
Nodes (20): Bp(), dp(), fp(), Gp(), h(), ie(), jp(), lp() (+12 more)

### Community 86 - "Web App Dependencies"
Cohesion: 0.13
Nodes (16): categoryRequest, categoryView, localChannelView, categoryViewOf(), decodeCategoryRequest(), Server, parseCategoryQuery(), parseID() (+8 more)

### Community 87 - "Password Hash Command"
Cohesion: 0.10
Nodes (23): publishingRegistry, accountsFor(), TestDaemonLoginEndToEnd(), newPipeDialer(), TestDaemonToWorkerToMTProtoEndToEnd(), listenHostOf(), Registry(), RunDaemon() (+15 more)

### Community 88 - "Web Component Config"
Cohesion: 0.15
Nodes (14): publicURL(), TestSafeDialAllowsLoopbackOnlyUnderTheTestPolicy(), NormalizeBaseURL(), refuse(), TestNormalizeBaseURLAcceptsHTTPS(), TestNormalizeBaseURLRefusesInternalHostnames(), TestNormalizeBaseURLRefusesWhatMustNotBeFetched(), TestNormalizeBaseURLRejectsOversizedComponents() (+6 more)

### Community 89 - "UI Component Config"
Cohesion: 0.15
Nodes (13): ValidAuthHeaderName(), constantTimeEqual(), Auth, AuthType, displayName(), Options, Server, isLoopbackHost() (+5 more)

### Community 90 - "MTProto Login Flow"
Cohesion: 0.11
Nodes (26): dedupStore, resolver, NewIngestor(), at(), intPtr(), source(), TestADuplicateObservationSchedulesNothing(), TestAFanOutFailureStillSchedulesTheOthers() (+18 more)

### Community 91 - "Web Dev Dependencies"
Cohesion: 0.29
Nodes (20): Options, New(), clients(), newProber(), TestAHungWorkerIsRestartedAfterRepeatedFailedProbes(), TestAStartupThatNeverBecomesReadyIsRestartedWithinBudget(), TestATransientProbeFailureDoesNotRestart(), TestDoesNotStartDisabledClients() (+12 more)

### Community 92 - "UI Dev Dependencies"
Cohesion: 0.19
Nodes (23): Exchange, Facts, Group, result, RouteFacts, Unresolved, renderEndpoints(), componentOf() (+15 more)

### Community 93 - "Web TypeScript Config"
Cohesion: 0.10
Nodes (21): AutomationAddButton(), AutomationContent(), AutomationPagination(), AutomationView(), Listing, TAB_WEBHOOKS, TAB_WORKFLOWS, answerAll() (+13 more)

### Community 94 - "Minified Frontend Bundle"
Cohesion: 0.09
Nodes (20): chatResponse, storedPayload(), ordered(), envelope, RequestRecord, bodyValue(), fileName(), Record (+12 more)

### Community 95 - "Docker Compose Setup"
Cohesion: 0.19
Nodes (18): newEnv(), TestAnAIFailureBlocksPublicationUnlessTheWorkflowAllowsTheFallback(), TestAPublishedDestinationIsNeverPublishedAgain(), TestAPublishedMessageIsRecordedUnderTheDestinationsNetwork(), TestAQueuedRunKeepsTheDestinationsItWasQueuedWith(), TestARetryReusesThePolishedOutputInsteadOfCallingTheAIAgain(), TestARunIsCancelledWhenTheWorkflowIsDisabledAfterEnqueue(), TestARunPublishesTheSourceContentToEveryDestination() (+10 more)

### Community 96 - "Docs Site Tests"
Cohesion: 0.15
Nodes (23): Snapshot, exampleFiles(), exercisesSome(), handledAmong(), handlerRoutes(), readExamples(), readIndex(), routeKey() (+15 more)

### Community 97 - "Root App Layout"
Cohesion: 0.17
Nodes (9): checkJSON(), contentTypeOf(), describeRequestError(), Client, requestTarget(), isTimeout(), headerInt(), quoteDisposition() (+1 more)

### Community 98 - "API Examples Tests"
Cohesion: 0.17
Nodes (17): ChannelDeleteDialog(), ChannelDeleteDialogContent(), DeleteChannelOptions, CATEGORIES, CHANNEL, DeleteBody(), MEDIA_ONLY, PHOTO_MESSAGE (+9 more)

### Community 99 - "Peer Store Persistence"
Cohesion: 0.18
Nodes (8): claims, hashParams, header, changePasswordRequest, loginRequest, principalKey, hashWithParams(), parseHash()

### Community 100 - "Docker Build CI"
Cohesion: 0.13
Nodes (19): Destination, DestinationType, PublishOptions, PublishOutcome, SourceRef, PublishingService, PublishRequest, PublishResult (+11 more)

### Community 101 - "Turbo Build Config"
Cohesion: 0.13
Nodes (19): entityValid(), utf16Len(), Validate(), ValidateWordPress(), mediaHasBytes(), randomIDFor(), requestHash(), sourceRequestHash() (+11 more)

### Community 102 - "UI TypeScript Config"
Cohesion: 0.16
Nodes (10): gotdFlow, LoginRunner, loginRunner, describeAuthError(), NewLoginForSpec(), Result, Run(), RunWithAttempts() (+2 more)

### Community 103 - "Test Server Helpers"
Cohesion: 0.14
Nodes (22): bs(), co(), cs(), dc(), ec(), Es(), fs(), hs() (+14 more)

### Community 104 - "Publish Idempotency"
Cohesion: 0.09
Nodes (20): @fontsource-variable/geist, @fontsource-variable/outfit, radix-ui, shadcn, tw-animate-css, zod, dependencies, @base-ui/react@^1.9.0 (+12 more)

### Community 105 - "Lint TypeScript Config"
Cohesion: 0.19
Nodes (9): channelView, avatarURLFor(), channelRefOf(), channelViewOf(), Server, isNoAvatar(), joinResultOf(), peerTypeOf() (+1 more)

### Community 106 - "Telegram Channel Discovery"
Cohesion: 0.12
Nodes (8): leaveLock, logSink, Launch, newFakeProcess(), fakeLauncher, fakeProcess, execLauncher, countingDial

### Community 107 - "Emoji Document Cache"
Cohesion: 0.11
Nodes (16): mediaView, messageView, AbsoluteMediaURL(), APIMount(), mediaHasBytes(), MediaURL(), nonNilEntities(), nonNilReactions() (+8 more)

### Community 108 - "Message Send Service"
Cohesion: 0.15
Nodes (21): Bd(), d(), ds(), ef(), f(), fm(), fo(), Gd() (+13 more)

### Community 109 - "Auth Prompt Input"
Cohesion: 0.17
Nodes (10): accessView, permissionsView, resourcePermissionsView, userView, PermissionSet, containsGlobal(), permissionSetOf(), permissionsViewOf() (+2 more)

### Community 110 - "CI Workflow Config"
Cohesion: 0.12
Nodes (17): Entities(), entityOf(), InputEntities(), inputEntity(), TestEntitiesCarriesTheTypeSpecificFields(), TestEntitiesIsNeverNil(), TestEntitiesMapsTheRemainingTelegramTypesByName(), TestEntitiesReadsEverySpanOfAMessage() (+9 more)

### Community 111 - "Frontend Build Config"
Cohesion: 0.24
Nodes (4): peerKey(), ptsKey(), stateKey(), fakeState

### Community 112 - "Docker Image Retag"
Cohesion: 0.10
Nodes (19): aliases, components, hooks, lib, ui, utils, iconLibrary, menuAccent (+11 more)

### Community 113 - "Workflow Step Config"
Cohesion: 0.10
Nodes (19): aliases, components, hooks, lib, ui, utils, iconLibrary, menuAccent (+11 more)

### Community 114 - "Package Scripts"
Cohesion: 0.16
Nodes (15): panelBuilt(), panelHandler(), Options, FS(), TestEmbeddedTreeIsTheDocumentationRoot(), gatewaySite(), serve(), TestEmbeddedCopyIsServed() (+7 more)

### Community 115 - "Web TypeScript Config"
Cohesion: 0.23
Nodes (17): Gn(), ae(), c(), D(), ee(), g(), K(), oe() (+9 more)

### Community 116 - "Frontend CI Job"
Cohesion: 0.22
Nodes (15): currentVersion(), hasCommit(), isRepository(), main(), packageVersion(), panelPackagePath, releaseCommand(), repoRoot (+7 more)

### Community 117 - "Workflow Triggers"
Cohesion: 0.13
Nodes (18): prettier, prettier-plugin-tailwindcss, turbo, typescript@^6.0.2, devDependencies, prettier@^3.9.10, prettier-plugin-tailwindcss@^0.8.1, turbo@^2.11.10 (+10 more)

### Community 118 - "Channel Category Filing"
Cohesion: 0.11
Nodes (18): compilerOptions, esModuleInterop, jsx, lib, module, moduleResolution, noEmit, paths (+10 more)

### Community 119 - "Anonymous Auth Context"
Cohesion: 0.18
Nodes (8): promptSet, readPassword(), runHashPassword(), TestHashPasswordCommandPrintsAVerifiableHash(), TestHashPasswordCommandRefusesAnEmptyPassword(), stdinFD(), decodeOptionalBody(), terminalPrompter

### Community 120 - "Operator Auth Context"
Cohesion: 0.18
Nodes (15): manualPage, composePage(), readComponents(), readManual(), renderComponents(), splitList(), startsWithMarker(), titleOf() (+7 more)

### Community 121 - "Account Auth Context"
Cohesion: 0.28
Nodes (18): TestAnOriginlessDeploymentStillCreatesAndSendsAWebhook(), TestDeliveryBoundsTheResponseBodyAndRecordsTruncation(), TestDeliveryDialsTheAddressItResolvedAndNotTheName(), TestDeliveryRecordsTimeoutsAndConnectionFailuresAsRetryable(), TestDeliveryRefusesABlockedTargetAtDeliveryTime(), TestDeliveryRefusesAHostThatResolvesSomewherePrivate(), testOpener(), testSecret() (+10 more)

### Community 122 - "Package Scripts"
Cohesion: 0.15
Nodes (12): @turbo/gen, devDependencies, tailwindcss@^4, @tailwindcss/vite@^4, @turbo/gen@^2.11.10, @types/node@^22, @types/react@^19, @types/react-dom@^19 (+4 more)

### Community 123 - "Package Exports Map"
Cohesion: 0.17
Nodes (10): main(), run(), Plan, Build(), legacyRoutes(), routeRows(), write(), DocsDir() (+2 more)

### Community 124 - "Go CI Job"
Cohesion: 0.12
Nodes (16): VERSION, args, context, TELESYNC_CONFIG, services, telesync, build, env_file (+8 more)

### Community 125 - "Pipe Address Resolution"
Cohesion: 0.19
Nodes (16): docsHandlerForTest, docsSite(), TestDocsLandingPageRenders(), TestDocsMarkdownRendersTablesCodeAndLinks(), TestDocsMethodNotAllowed(), TestDocsMissingAndUnsupportedFilesAre404(), TestDocsNestedPagesAndRelativeLinks(), TestDocsRawHTMLAndDangerousLinksAreInert() (+8 more)

### Community 126 - "FloodWait Error"
Cohesion: 0.18
Nodes (17): Cp(), eu(), Fu(), gm(), iu(), kp(), ks(), Nd() (+9 more)

### Community 127 - "Pipe Address Resolution"
Cohesion: 0.15
Nodes (9): App(), ToastCard(), ToastContext, ToastContextValue, ToastOptions, ToastProvider(), ToastRecord, ToastVariant (+1 more)

### Community 128 - "FloodWait Error"
Cohesion: 0.19
Nodes (5): runtimeArgs, runtimeFlags, DefaultPath(), TestDefaultPathUsesTheHomeDirectory(), displayName()

### Community 129 - "Format Scripts"
Cohesion: 0.37
Nodes (13): homeEnv, newHomeEnv(), TestBareInvocationCreatesTheDefaultConfig(), TestBareInvocationDoesNotCreateAnExplicitPath(), TestBareInvocationReportsAnExistingConfig(), TestDaemonCreatesTheDefaultConfigToo(), TestDefaultConfigIsValidJSONWithExpectedShape(), TestExplicitConfigPathIsNeverCreated() (+5 more)

### Community 130 - "Administrators Route Bundle"
Cohesion: 0.23
Nodes (13): dashboardStats, assertListTotals(), accountEnv, num(), ptrInt(), reportUserCount(), statsOf(), TestDashboardAdministratorsCountNeedsAGlobalUserRight() (+5 more)

### Community 131 - "Administrators Route Bundle"
Cohesion: 0.18
Nodes (16): indexKey(), matchesSomeRoute(), TestEveryRouteIsDocumented(), TestRemovedRouteIsDetected(), Concrete(), DocRoutes(), isNamespace(), Matches() (+8 more)

### Community 132 - "Categories Route Bundle"
Cohesion: 0.23
Nodes (15): buildSystemPrompt(), mustContain(), TestCustomPromptCanOnlySupplementTheRules(), TestCustomPromptIsNotWrappedWhenAbsent(), TestDefaultPromptClosesEveryEscalationChannel(), TestDefaultPromptDeclaresTheJSONContract(), TestDefaultPromptForbidsInventingFacts(), TestDefaultPromptImprovesWithoutChangingMeaning() (+7 more)

### Community 133 - "Categories Route Bundle"
Cohesion: 0.19
Nodes (7): AutomationFailure, AutomationStatus, AutomationStatusSource, Status, AutomationJobCounts, fakeQueue, QueueStatus

### Community 134 - "Channels Route Bundle"
Cohesion: 0.16
Nodes (11): documentedServers(), serverUser, serverView, formatTime(), requestIDFrom(), safeID(), Server, ServerInfo (+3 more)

### Community 135 - "Channels Route Bundle"
Cohesion: 0.13
Nodes (15): with, build-args, cache, cache-dependency-path, cache-from, cache-to, context, file (+7 more)

### Community 136 - "Login Route Bundle"
Cohesion: 0.19
Nodes (13): NewClient(), safeDial(), systemLookup(), TestTheRestrictedClientDoesNotFollowRedirects(), WithDial(), WithLookup(), WithPolicy(), WithTimeout() (+5 more)

### Community 137 - "Login Route Bundle"
Cohesion: 0.19
Nodes (15): metadata, apiVersion, kind, metadata, Service telesync, Deployment telesync, app.kubernetes.io/name, app.kubernetes.io/part-of (+7 more)

### Community 138 - "Client Manifest"
Cohesion: 0.13
Nodes (15): args, env, image, imagePullPolicy, livenessProbe, name, ports, readinessProbe (+7 more)

### Community 139 - "WordPress Route Bundle"
Cohesion: 0.20
Nodes (11): acquire(), claim(), download(), entries, Entry, loadMediaBlob(), release(), releaseMediaBlob() (+3 more)

### Community 140 - "WordPress Route Bundle"
Cohesion: 0.15
Nodes (13): appDir, builtDir, copyTree(), embedDir, embedOnly, ensurePage(), indexPath, repoRoot (+5 more)

### Community 141 - "Telesync Go Package"
Cohesion: 0.13
Nodes (14): dependsOn, inputs, outputs, cache, persistent, dependsOn, $schema, tasks (+6 more)

### Community 142 - "output.go"
Cohesion: 0.21
Nodes (12): rawOutput, isASCIIAlphaNumeric(), isTokenBoundary(), jsonFailure(), normalizeTokens(), readToken(), rejectControlRunes(), rejectUnsafeToken() (+4 more)

### Community 143 - "Service"
Cohesion: 0.19
Nodes (7): newBackgroundPublisher(), TestBackgroundPublisherResolvesTheAccountItPublishesUnder(), Registry, SecretBox, Service, ActorResolver, Sites

### Community 144 - "embed.go"
Cohesion: 0.20
Nodes (6): Embed(), listTree(), readTree(), sortedKeys(), TestEmbedDirMirrorsDocsAPIAndIsIdempotent(), writeFile()

### Community 145 - "Store"
Cohesion: 0.22
Nodes (8): Store, Open(), TestACorruptedFileIsRecoveredAsEmpty(), TestAnEmptyPathIsRejected(), TestSaveAndFindRoundTrip(), TestSaveRejectsZeroValues(), TestTheFileIsRestricted(), TestTheStoreSurvivesAReopen()

### Community 146 - "compilerOptions"
Cohesion: 0.14
Nodes (13): compilerOptions, jsx, lib, module, moduleResolution, noEmit, paths, skipLibCheck (+5 more)

### Community 147 - "newUpdateHandler"
Cohesion: 0.26
Nodes (12): IngestSink, IngestSinkFunc, updateHandler, newUpdateHandler(), batch(), collectSink(), TestANewChannelMessageBecomesOneBoundedObservation(), TestAnUnstorableMessageIsReportedSoTheCounterCannotSkipIt() (+4 more)

### Community 148 - "newWorld"
Cohesion: 0.33
Nodes (11): boolPtr(), newWorld(), TestAWorkflowWithNoOwningAccountIsRefusedRatherThanElevated(), TestBackgroundPublishingKeepsTheSourceMediaUnlessItIsDropped(), TestBackgroundPublishingRefusesAnAccountThatIsGone(), TestBackgroundPublishingRefusesAnInactiveAccount(), TestBackgroundPublishingRefusesASourceTheAccountCannotRead(), TestBackgroundPublishingRunsUnderTheAccountsCurrentPermissions() (+3 more)

### Community 149 - "NormalizePermissions"
Cohesion: 0.26
Nodes (13): ChannelAccess, Permissions, NormalizePermissions(), member(), TestNormalizePermissionsForADefaultRestrictedSupergroup(), TestNormalizePermissionsForAnAdminWithoutPostingRights(), TestNormalizePermissionsForAnAdminWithPostingRights(), TestNormalizePermissionsForAnInaccessibleOrNonMemberChannel() (+5 more)

### Community 150 - "prompt_test.go"
Cohesion: 0.24
Nodes (9): sentCodeHint(), newTestPrompter(), TestPromptCodeDoesNotEchoTheCodeItself(), TestPromptCodeHintsDelivery(), TestPromptEOFIsAnErrorNotAHang(), TestPromptPasswordFromPipeIsRead(), TestPromptPhoneReadsLine(), TestSentCodeHintVariants() (+1 more)

### Community 151 - "exchange"
Cohesion: 0.27
Nodes (11): Caller, Observation, Route, ProbePath(), ProbePaths(), exchange(), fillPattern(), ObserveProbe() (+3 more)

### Community 152 - ".requireChannelView"
Cohesion: 0.24
Nodes (5): channelAccess, channelTarget, TargetsByTelegramID, access(), Server

### Community 153 - "webhooktest/config_test.go"
Cohesion: 0.50
Nodes (11): LoadAuth(), completeEnv(), envOf(), TestLoadAuthAcceptsAHeaderNameWithSurroundingWhitespace(), TestLoadAuthAcceptsAStandardWebhooksSecret(), TestLoadAuthReadsEveryVariableItVerifiesAgainst(), TestLoadAuthRefusesAHeaderNameTheSenderWouldNeverSend(), TestLoadAuthRefusesAnUnusableHMACSecret() (+3 more)

### Community 154 - "resolvingDial"
Cohesion: 0.26
Nodes (11): resolvingDial(), safeDial(), systemLookup(), neverDial(), TestSafeDialConnectsToTheAddressesItChecked(), TestSafeDialRefusesAPrivateAnswer(), TestSafeDialRefusesWhenAnyAnswerIsPrivate(), TestSafeDialReportsANameThatDoesNotResolve() (+3 more)

### Community 155 - "compilerOptions"
Cohesion: 0.17
Nodes (11): compilerOptions, jsx, lib, module, moduleResolution, outDir, skipLibCheck, strict (+3 more)

### Community 156 - ".app"
Cohesion: 0.18
Nodes (9): adminHarness, TestAdminAsksForCredentialsAndCreatesTheAccount(), TestAdminDefaultsTheFullNameToTheUsername(), TestAdminRefusesMismatchedPasswords(), TestAdminRejectsUnknownArguments(), TestAdminReportsAMissingDatabaseAsAFailure(), TestAdminReportsAnInvalidAccountAsAUsageError(), TestAdminSaysWhenItResetAnExistingAccount() (+1 more)

### Community 157 - ".buildSend"
Cohesion: 0.25
Nodes (8): mustChannelID(), namedAttachment(), sanitiseFileName(), sendFileName(), imageMedia(), insertEvent(), Media, sourceMessage

### Community 158 - "localEnv"
Cohesion: 0.45
Nodes (10): deliveryJob(), localEnv(), localWebhook(), loopbackTarget(), TestAClosedLoopbackPortIsRecordedAsAConnectionFailure(), TestALocalhostWebhookIsDeliveredToALoopbackServer(), TestALocalhostWebhookIsRefusedWhenTheDeploymentForbidsIt(), TestAPolicyRefusalIsRecordedAndNotRetried() (+2 more)

### Community 159 - "spec"
Cohesion: 0.18
Nodes (11): spec, replicas, strategy, template, volumes, type, metadata, spec (+3 more)

### Community 160 - "ports"
Cohesion: 0.18
Nodes (11): app.kubernetes.io/name, name, port, protocol, targetPort, app.kubernetes.io/name, matchLabels, spec (+3 more)

### Community 161 - "ref_node_fs"
Cohesion: 0.20
Nodes (6): appDir, esbuild, here, named, outDir, webDir

### Community 162 - "images_test.go"
Cohesion: 0.31
Nodes (9): imageMediaType(), selectImages(), TestImageContentPartUsesADataURL(), TestImageMediaTypeAcceptsOnlyRealImages(), TestSelectImagesBoundsTheCount(), TestSelectImagesBoundsTheTotalBytes(), TestSelectImagesDropsEverythingThatIsNotAnImage(), TestSelectImagesNeverMutatesTheCallerBytes() (+1 more)

### Community 163 - "newEngine"
Cohesion: 0.36
Nodes (8): boxOf(), newEngine(), newTestReceiver(), TestADeliveryIsRecordedAgainstTheReceiverThatAnsweredIt(), TestTheDeliveryEngineIsRefusedWhenTheSecretDoesNotMatch(), TestTheDeliveryEngineSendsWhatTheTestReceiverAccepts(), engine, testReceiver

### Community 164 - "worker/send_test.go"
Cohesion: 0.51
Nodes (9): codeOf(), readyStatus(), sendService(), TestServiceSendMessagePassesThePreparedMessageThrough(), TestServiceSendMessageRefusesWorkWithoutAnAuthorizedAccount(), TestServiceSendMessageReportsAFloodWait(), TestServiceSendMessageReportsAnUnresolvableDestination(), TestServiceSendMessageReportsARefusedDestination() (+1 more)

### Community 165 - "k8s.yaml"
Cohesion: 0.20
Nodes (9): apiVersion, data, kind, telesync.json, Namespace telesync, ConfigMap telesync-config, apiVersion, kind (+1 more)

### Community 166 - "APIConfig"
Cohesion: 0.28
Nodes (6): JWTConfig, WordPressConfig, APIConfig, isLoopback(), CheckWordPressListener(), checkWordPressUnconfigured()

### Community 167 - "ci.yml"
Cohesion: 0.22
Nodes (8): concurrency, cancel-in-progress, group, jobs, name, permissions, contents, packages

### Community 168 - "PersistentVolumeClaim telesync-sessdata"
Cohesion: 0.22
Nodes (9): PersistentVolumeClaim telesync-sessdata, apiVersion, kind, metadata, spec, storage, requests, accessModes (+1 more)

### Community 169 - "Secret telesync-credentials"
Cohesion: 0.25
Nodes (8): Secret telesync-credentials, apiVersion, kind, metadata, stringData, type, OFFICIAL_API_HASH, OFFICIAL_PHONE

### Community 170 - "ref_node_path"
Cohesion: 0.25
Nodes (6): appDir, esbuild, here, outDir, tests, webDir

### Community 171 - "test-responsive.mjs"
Cohesion: 0.25
Nodes (6): appDir, esbuild, here, outDir, roots, webDir

### Community 172 - "docker"
Cohesion: 0.29
Nodes (7): env, name, runs-on, OWNER, RETAG, TAG, docker

### Community 173 - "steps"
Cohesion: 0.48
Nodes (7): steps, steps, steps, if, name, run, uses

### Community 174 - "He"
Cohesion: 0.38
Nodes (7): Ge(), He(), a(), c(), r(), Ke(), j()

### Community 175 - "Listen"
Cohesion: 0.38
Nodes (6): isLive(), Listen(), isSandboxDenied(), TestListenPreparesTheSocketPath(), TestListenRefusesANonSocketFile(), TestListenRemovesADeadSocket()

### Community 176 - "Secret telesync-jwt"
Cohesion: 0.29
Nodes (7): Secret telesync-jwt, apiVersion, kind, metadata, stringData, type, TELESYNC_JWT_SECRET

### Community 177 - "securityContext"
Cohesion: 0.29
Nodes (7): type, fsGroup, runAsGroup, runAsNonRoot, runAsUser, seccompProfile, securityContext

### Community 178 - "scripts"
Cohesion: 0.29
Nodes (7): scripts, build, dev, format, start, test, typecheck

### Community 179 - "test-media.mjs"
Cohesion: 0.29
Nodes (6): appDir, esbuild, here, outDir, tests, webDir

### Community 180 - "test-panel.mjs"
Cohesion: 0.29
Nodes (6): appDir, esbuild, here, outDir, tests, webDir

### Community 181 - "test-publish.mjs"
Cohesion: 0.29
Nodes (6): appDir, esbuild, here, outDir, tests, webDir

### Community 182 - "test-webhook.mjs"
Cohesion: 0.29
Nodes (6): appDir, esbuild, here, outDir, tests, webDir

### Community 183 - "compilerOptions"
Cohesion: 0.29
Nodes (6): compilerOptions, module, moduleResolution, skipLibCheck, strict, target

### Community 185 - "frontend"
Cohesion: 0.33
Nodes (6): run, defaults, name, runs-on, frontend, working-directory

### Community 186 - "on"
Cohesion: 0.33
Nodes (6): on, pull_request, push, workflow_dispatch, branches, tags

### Community 188 - "filedEnv"
Cohesion: 0.47
Nodes (5): categoryChannelsPath(), channelCategoriesPath(), filedEnv(), TestAChannelIsFiledAndListedFromBothSides(), TestFilingNeedsTheChannelAndTheCategory()

### Community 189 - "Secret telesync-wordpress"
Cohesion: 0.33
Nodes (6): Secret telesync-wordpress, apiVersion, kind, stringData, type, TELESYNC_WORDPRESS_KEY

### Community 190 - "ref_node_child_process"
Cohesion: 0.33
Nodes (4): appDir, here, out, tsc

### Community 191 - "woff2-cmap.mjs"
Cohesion: 0.40
Nodes (3): KNOWN_TABLE_TAGS, parseWoff2Tables(), readBase128()

### Community 195 - "permissionsRequest"
Cohesion: 0.40
Nodes (4): categoryPermissionsEntry, channelPermissionsEntry, permissionsRequest, wordpressPermissionsEntry

### Community 197 - "mediahttp.go"
Cohesion: 0.60
Nodes (3): contentDisposition(), isASCII(), rfc5987()

### Community 198 - "scripts"
Cohesion: 0.30
Nodes (5): @remixicon/react, @remixicon/react@^4.9.0, scripts, format, typecheck

### Community 199 - "scripts"
Cohesion: 0.40
Nodes (5): scripts, build, dev, format, typecheck

### Community 200 - "exports"
Cohesion: 0.40
Nodes (5): exports, ./components/*, ./globals.css, ./hooks/*, ./lib/*

### Community 206 - "go"
Cohesion: 0.67
Nodes (3): name, runs-on, go

### Community 208 - "RemoveStaleSocket"
Cohesion: 0.67
Nodes (3): RemoveStaleSocket(), TestStaleSocketFilesAreRemovedButOtherFilesAreNot(), TestStaleSocketIsRefusedOutsideTheRunDirectory()

## Knowledge Gaps
- **9 isolated node(s):** `accountEnv`, `wfEnv`, `accountEnv`, `accountEnv`, `wpEnv` (+4 more)
  These have ≤1 connection - possible missing edges. (Counts symbols only; 1121 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **39 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `ctxTimeout()` connect `Minified Frontend Bundle` to `Raw API Clients`, `Asset File Dialog Bundle`?**
  _High betweenness centrality (0.015) - this node is a cross-community bridge._
- **Are the 91 inferred relationships involving `readyWorker()` (e.g. with `TestAChannelWithoutAGrantIsForbidden()` and `TestAMissingOrDuplicateTokenDoesNotRevealWhetherAClientExists()`) actually correct?**
  _`readyWorker()` has 91 INFERRED edges - model-reasoned connections that need verification._
- **What connects `accountEnv`, `wfEnv`, `accountEnv` to the rest of the system?**
  _9 weakly-connected nodes found - possible missing edges._
- **Should `Raw API Clients` be split into smaller, more focused modules?**
  _Cohesion score 0.01409422694094227 - nodes in this community are weakly interconnected._
- **Why does `Manager` connect `Process Supervisor` to `Telegram Channel Discovery`, `Minified Frontend Bundle`, `Dashboard State Cards`, `Channel View Permissions`, `Store`, `Telegram Dialog Types`, `Minified Frontend Bundle`, `Message Display Formatting`, `WordPress Gateway Tests`, `Gateway Probe Auth Tests`?**
  _High betweenness centrality (0.007) - this node is a cross-community bridge._
- **Should `Minified Frontend Bundle` be split into smaller, more focused modules?**
  _Cohesion score 0.02134754680880142 - nodes in this community are weakly interconnected._
- **Why does `Spec` connect `WordPress Gateway Tests` to `Minified Frontend Bundle`, `Publish Dialog UI`, `Process Supervisor`, `UI TypeScript Config`?**
  _High betweenness centrality (0.006) - this node is a cross-community bridge._