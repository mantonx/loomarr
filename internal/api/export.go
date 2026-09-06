package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// ExportOpenAPI builds the API's operation definitions and returns the OpenAPI
// 3.1 spec as YAML (§7.1). It needs no live store — only the registered
// operation schemas — so `make openapi` runs offline and deterministically.
func ExportOpenAPI(log *slog.Logger) ([]byte, error) {
	_, humaAPI := schemaOnlyAPI(log)
	return humaAPI.OpenAPI().YAML()
}

// schemaOnlyAPI registers every typed operation against a store-less Server and returns it.
//
// Shared by ExportOpenAPI and the authorization gate so the two cannot disagree about what
// "every operation" means: a route added to one list and not the other is exactly the drift
// this centralises away. Adding a register* call here reaches both.
func schemaOnlyAPI(log *slog.Logger) (*Server, huma.API) {
	mux := http.NewServeMux()
	humaAPI := humago.New(mux, humaConfig())
	// schemaOnly makes the nil-guarded register* funcs (auth, provisioning, users
	// sync) emit their operation schemas into the spec despite the bare Server —
	// handlers aren't invoked; only their schemas are read. Without it the exported
	// spec would omit /v1/auth/*, /v1/setup/bootstrap, /v1/users/{import,sync} and
	// orval couldn't type them (the FE↔BE seam that 13.0 closes).
	srv := &Server{log: log, schemaOnly: true}
	srv.registerMiddleware(humaAPI)
	srv.registerTitles(humaAPI)
	srv.registerAuth(humaAPI)
	srv.registerDeviceAuth(humaAPI)
	srv.registerUsers(humaAPI)
	srv.registerInvitations(humaAPI)
	srv.registerInvitationRedemption(humaAPI)
	srv.registerPasswordRecovery(humaAPI)
	srv.registerPasswords(humaAPI)
	srv.registerChannels(humaAPI)
	srv.registerPlayout(humaAPI) // Hidden ops — present for register-list parity, absent from the spec (§9.1)
	srv.registerGuide(humaAPI)
	srv.registerProgramming(humaAPI)
	srv.registerSetup(humaAPI)
	srv.registerProposals(humaAPI)
	srv.registerDiscoveryFeedback(humaAPI)
	srv.registerProposalJourneys(humaAPI)
	srv.registerSearch(humaAPI)
	srv.registerCollections(humaAPI)
	srv.registerFiller(humaAPI)
	srv.registerFillerDecisions(humaAPI)
	srv.registerFillerRights(humaAPI)
	srv.registerFillerSources(humaAPI)
	srv.registerFillerWatch(humaAPI)
	srv.registerFillerPulls(humaAPI)
	srv.registerFillerIncoming(humaAPI)
	srv.registerFillerBulk(humaAPI)
	srv.registerFillerFile(humaAPI)
	srv.registerTaxonomy(humaAPI)
	srv.registerImages(humaAPI)
	srv.registerJobs(humaAPI)
	srv.registerSystemLLM(humaAPI)
	srv.registerSystemDatabase(humaAPI)
	srv.registerSystemSecurity(humaAPI)
	srv.registerDiscoveryQuality(humaAPI)
	srv.registerSystemBackups(humaAPI)
	srv.registerSystemRestart(humaAPI)
	srv.registerDashboardPanels(humaAPI)
	srv.registerSettings(humaAPI)
	srv.registerNotificationDestinations(humaAPI)
	srv.registerHelp(humaAPI)
	// registerEvents is nil-guarded on the bus; schemaOnly forces the frame schemas out so
	// the generated client always has the event types even when this export ran without one.
	srv.registerEvents(humaAPI)
	// registerSSO is nil-guarded on the provider, same schemaOnly escape.
	srv.registerSSO(humaAPI)
	// pprof=false: the profiler routes are Hidden on both paths anyway (they exist only when a
	// flag is set, so advertising them would describe a surface most installs do not have), and
	// the exporter has no Options to read the flag from.
	srv.registerOps(humaAPI, false)
	srv.registerDashboard(humaAPI)
	srv.registerDiagnostics(humaAPI)
	srv.registerPlayoutStatus(humaAPI)
	// registerProvisioning is nil-guarded (like registerAuth): with no provisioner
	// wired here, /v1/setup/bootstrap + /v1/users/import stay out of the exported
	// spec, matching how /v1/auth/* is handled. Documented via §11.
	srv.registerProvisioning(humaAPI)
	return srv, humaAPI
}

// EventFrameTypes reports the SSE frame vocabulary as event-name → payload Go type name.
// Exported so the guard in api_test can compare it against what internal/app actually
// publishes without reaching into the package.
//
// ⚠ The failure this enables detection of is silent: huma names an SSE frame after the Go
// TYPE of its payload, so publishing a type absent from eventTypeMap emits a frame with no
// `event:` line. Every addEventListener keyed to that name stops firing, nothing errors, and
// the feature just looks broken.
func EventFrameTypes() map[string]string {
	out := make(map[string]string, len(eventTypeMap()))
	for name, v := range eventTypeMap() {
		out[name] = reflect.TypeOf(v).Name()
	}
	return out
}

// OperationsMissingARole reports every registered operation that declares no required role
// (§11). Exported so the test in api_test can call it without reaching into the package.
//
// ⚠ Walks the REAL registry rather than grepping source: a regex over `huma.Register` calls
// would miss the conditionally-registered ones (dev-login, users sync) and would keep
// passing if a registration moved somewhere the pattern no longer matched. What is mounted
// is the only thing worth asserting on.
//
// Returns "METHOD /path (operation-id)" so a failure names the route rather than an index.
func OperationsMissingARole() ([]string, error) {
	_, humaAPI := schemaOnlyAPI(slog.New(slog.DiscardHandler))
	spec := humaAPI.OpenAPI()
	if spec == nil || spec.Paths == nil {
		return nil, fmt.Errorf("api: no operations registered")
	}
	var missing []string
	for path, item := range spec.Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op == nil {
				continue
			}
			if _, ok := op.Metadata[roleMetaKey]; !ok {
				missing = append(missing, fmt.Sprintf("%s %s (%s)", method, path, op.OperationID))
			}
		}
	}
	sort.Strings(missing) // deterministic: map iteration order must not reorder a failure
	return missing, nil
}
