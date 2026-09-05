//go:build eval

package eval

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/suggest"
)

const certificationManifestPath = "testdata/planner-certification-v6.json"

//go:embed testdata/planner-certification-v1.json testdata/planner-certification-v2.json testdata/planner-certification-v3.json testdata/planner-certification-v4.json testdata/planner-certification-v5.json testdata/planner-certification-v6.json testdata/planner-certification-v6-base.json testdata/planner-catalog-v1.json testdata/planner-catalog-v2.json
var certificationFiles embed.FS

// CertificationCorpus is the immutable, held-out planner-model corpus contract.
// It names the exact fixture bytes used by every candidate model.
type CertificationCorpus struct {
	SchemaVersion         int                     `json:"schemaVersion"`
	Version               string                  `json:"version"`
	Split                 string                  `json:"split"`
	PromptVersion         string                  `json:"promptVersion"`
	ToolSchemaVersion     string                  `json:"toolSchemaVersion"`
	ScorerVersion         string                  `json:"scorerVersion"`
	HardMetrics           []string                `json:"hardMetrics"`
	QualityMetrics        []string                `json:"qualityMetrics"`
	Thresholds            CertificationThresholds `json:"thresholds"`
	Selection             CertificationSelection  `json:"selection"`
	AllowedTrainingSplits []string                `json:"allowedTrainingSplits"`
	Fixture               CertificationFixture    `json:"fixture"`
	Cases                 []CertificationCase     `json:"cases"`
}

// CertificationRunnerConfig binds Runner output to the exact corpus, fixture,
// prompt/tool contract, scorer, and pre-registered metric sets.
func CertificationRunnerConfig(config RunnerConfig) (RunnerConfig, error) {
	corpus, err := LoadEmbeddedCertificationCorpus()
	if err != nil {
		return RunnerConfig{}, err
	}
	if strings.TrimSpace(config.Generator.Provider) == "" || buildScorecardRunSnapshot(Scorecard{
		CorpusVersion: corpus.Version, GeneratedAt: time.Unix(1, 0).UTC(),
		Profile: config.Profile, Generator: config.Generator,
	}, false) == nil {
		return RunnerConfig{}, fmt.Errorf("certification scorecard requires identifier-shaped profile and generator provider/model")
	}
	config.Contract = &CertificationContract{
		CorpusVersion:        corpus.Version,
		CatalogFixtureSHA256: corpus.Fixture.SHA256,
		PromptVersion:        corpus.PromptVersion,
		ToolSchemaVersion:    corpus.ToolSchemaVersion,
		ScorerVersion:        corpus.ScorerVersion,
		HardMetrics:          append([]string(nil), corpus.HardMetrics...),
		QualityMetrics:       append([]string(nil), corpus.QualityMetrics...),
		Thresholds:           corpus.Thresholds,
		Selection:            corpus.Selection,
	}
	return config, nil
}

type CertificationFixture struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type certificationCorpusExtension struct {
	SchemaVersion       int                     `json:"schemaVersion"`
	Version             string                  `json:"version"`
	Base                CertificationFixture    `json:"base"`
	PromptVersion       string                  `json:"promptVersion"`
	ToolSchemaVersion   string                  `json:"toolSchemaVersion"`
	ScorerVersion       string                  `json:"scorerVersion"`
	QualityMetrics      []string                `json:"qualityMetrics"`
	Thresholds          CertificationThresholds `json:"thresholds"`
	Selection           CertificationSelection  `json:"selection"`
	ProposalExpectation string                  `json:"proposalExpectation"`
	PolicyCeilings      map[string]string       `json:"policyCeilings"`
	RecoveryCases       []string                `json:"recoveryCases"`
	RepairCases         []string                `json:"repairCases"`
}

type CertificationCase struct {
	ID              string                 `json:"id"`
	Split           string                 `json:"split"`
	FixtureCase     string                 `json:"fixtureCase"`
	Axes            []string               `json:"axes"`
	Description     string                 `json:"description"`
	AllowAbstention bool                   `json:"allowAbstention,omitempty"`
	Variants        []CertificationVariant `json:"variants,omitempty"`
}

type CertificationVariant struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type certificationCatalogFixture struct {
	SchemaVersion int                        `json:"schemaVersion"`
	FixtureID     string                     `json:"fixtureId"`
	Cases         []certificationFixtureCase `json:"cases"`
}

type certificationFixtureCase struct {
	ID        string                         `json:"id"`
	Responses []certificationFixtureResponse `json:"responses"`
}

type certificationFixtureResponse struct {
	Operation  string              `json:"operation"`
	Candidates []catalog.Candidate `json:"candidates"`
	Error      string              `json:"error"`
}

// LoadEmbeddedCertificationCorpus verifies and returns the corpus manifest.
// A digest mismatch or missing fixture case fails before any provider is called.
func LoadEmbeddedCertificationCorpus() (CertificationCorpus, error) {
	extensionBlob, err := certificationFiles.ReadFile(certificationManifestPath)
	if err != nil {
		return CertificationCorpus{}, fmt.Errorf("read certification manifest: %w", err)
	}
	var extension certificationCorpusExtension
	if err := json.Unmarshal(extensionBlob, &extension); err != nil {
		return CertificationCorpus{}, fmt.Errorf("decode certification manifest: %w", err)
	}
	manifestBlob, err := certificationFiles.ReadFile(extension.Base.Path)
	if err != nil {
		return CertificationCorpus{}, fmt.Errorf("read certification base manifest: %w", err)
	}
	baseDigest := sha256.Sum256(manifestBlob)
	if hex.EncodeToString(baseDigest[:]) != extension.Base.SHA256 {
		return CertificationCorpus{}, fmt.Errorf("certification base manifest digest mismatch")
	}
	var corpus CertificationCorpus
	if err := json.Unmarshal(manifestBlob, &corpus); err != nil {
		return CertificationCorpus{}, fmt.Errorf("decode certification base manifest: %w", err)
	}
	if err := validateCertificationExtension(extension, corpus); err != nil {
		return CertificationCorpus{}, err
	}
	if extension.PromptVersion != suggest.PlannerPromptVersion || extension.ToolSchemaVersion != suggest.PlannerToolSchemaVersion {
		return CertificationCorpus{}, fmt.Errorf("certification prompt/tool identity differs from production Suggester")
	}
	corpus.SchemaVersion = extension.SchemaVersion
	corpus.Version = extension.Version
	corpus.PromptVersion = extension.PromptVersion
	corpus.ToolSchemaVersion = extension.ToolSchemaVersion
	corpus.ScorerVersion = extension.ScorerVersion
	corpus.QualityMetrics = append([]string(nil), extension.QualityMetrics...)
	corpus.Thresholds = extension.Thresholds
	corpus.Selection = extension.Selection
	fixtureBlob, err := certificationFiles.ReadFile(corpus.Fixture.Path)
	if err != nil {
		return CertificationCorpus{}, fmt.Errorf("read certification fixture: %w", err)
	}
	digest := sha256.Sum256(fixtureBlob)
	if hex.EncodeToString(digest[:]) != corpus.Fixture.SHA256 {
		return CertificationCorpus{}, fmt.Errorf("certification fixture digest mismatch")
	}
	var fixture certificationCatalogFixture
	if err := json.Unmarshal(fixtureBlob, &fixture); err != nil {
		return CertificationCorpus{}, fmt.Errorf("decode certification fixture: %w", err)
	}
	fixtureCases := make(map[string]bool, len(fixture.Cases))
	for _, c := range fixture.Cases {
		fixtureCases[c.ID] = true
	}
	for _, c := range corpus.Cases {
		if !fixtureCases[c.FixtureCase] {
			return CertificationCorpus{}, fmt.Errorf("certification case %q references missing fixture case %q", c.ID, c.FixtureCase)
		}
	}
	return corpus, nil
}

func validateCertificationExtension(extension certificationCorpusExtension, corpus CertificationCorpus) error {
	if extension.SchemaVersion <= 0 || extension.Version == "" || extension.PromptVersion == "" || extension.ToolSchemaVersion == "" || extension.ScorerVersion == "" {
		return fmt.Errorf("certification extension identity is incomplete")
	}
	if extension.ProposalExpectation != "exact_fixture_candidates_or_declared_abstention" {
		return fmt.Errorf("unsupported proposal expectation %q", extension.ProposalExpectation)
	}
	if len(extension.QualityMetrics) == 0 {
		return fmt.Errorf("certification extension quality metrics are empty")
	}
	if err := validateSelection(extension.Selection); err != nil {
		return fmt.Errorf("certification extension selection: %w", err)
	}
	known := make(map[string]bool, len(corpus.Cases))
	for _, c := range corpus.Cases {
		known[c.ID] = true
	}
	validateIDs := func(label string, ids []string) error {
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if !known[id] {
				return fmt.Errorf("certification extension %s references unknown case %q", label, id)
			}
			if seen[id] {
				return fmt.Errorf("certification extension %s duplicates case %q", label, id)
			}
			seen[id] = true
		}
		return nil
	}
	policyIDs := make([]string, 0, len(extension.PolicyCeilings))
	for id := range extension.PolicyCeilings {
		policyIDs = append(policyIDs, id)
	}
	for label, ids := range map[string][]string{
		"policy ceilings": policyIDs, "recovery cases": extension.RecoveryCases, "repair cases": extension.RepairCases,
	} {
		if err := validateIDs(label, ids); err != nil {
			return err
		}
	}
	return nil
}

// CertificationCases projects the frozen manifest onto Runner's public Case
// seam. Every case starts with the two production structural bounds and the
// unsupported-id hard gate; narrower expectations can deepen individual cases
// without creating a parallel evaluator.
func CertificationCases() ([]Case, error) {
	corpus, err := LoadEmbeddedCertificationCorpus()
	if err != nil {
		return nil, err
	}
	fixtureBlob, err := certificationFiles.ReadFile(corpus.Fixture.Path)
	if err != nil {
		return nil, fmt.Errorf("read certification fixture: %w", err)
	}
	var fixture certificationCatalogFixture
	if err := json.Unmarshal(fixtureBlob, &fixture); err != nil {
		return nil, fmt.Errorf("decode certification fixture: %w", err)
	}
	operationByCase := make(map[string]string, len(fixture.Cases))
	keysByCase := make(map[string][]provision.Key, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if len(c.Responses) > 0 {
			operationByCase[c.ID] = c.Responses[0].Operation
		}
		for _, response := range c.Responses {
			for _, candidate := range response.Candidates {
				key, keyErr := candidate.Key()
				if keyErr != nil {
					return nil, fmt.Errorf("certification fixture case %q has invalid candidate: %w", c.ID, keyErr)
				}
				keysByCase[c.ID] = append(keysByCase[c.ID], key)
			}
		}
	}
	extensionBlob, err := certificationFiles.ReadFile(certificationManifestPath)
	if err != nil {
		return nil, fmt.Errorf("read certification manifest: %w", err)
	}
	var extension certificationCorpusExtension
	if err := json.Unmarshal(extensionBlob, &extension); err != nil {
		return nil, fmt.Errorf("decode certification manifest: %w", err)
	}
	recoveryCases := make(map[string]bool, len(extension.RecoveryCases))
	for _, id := range extension.RecoveryCases {
		recoveryCases[id] = true
	}
	repairCases := make(map[string]bool, len(extension.RepairCases))
	for _, id := range extension.RepairCases {
		repairCases[id] = true
	}
	cases := make([]Case, 0, len(corpus.Cases)*6)
	for _, frozen := range corpus.Cases {
		if frozen.Description == "" {
			return nil, fmt.Errorf("certification case %q has a blank Intent", frozen.ID)
		}
		base := Case{
			Name:                       frozen.ID,
			Intent:                     Intent{Description: frozen.Description},
			NoFabrication:              true,
			ExpectGroundedCompletion:   !frozen.AllowAbstention,
			ExpectedToolOperation:      operationByCase[frozen.FixtureCase],
			ExpectedPolicyCeiling:      extension.PolicyCeilings[frozen.ID],
			ExpectedProposalKeys:       append([]provision.Key(nil), keysByCase[frozen.FixtureCase]...),
			ExpectedProposalAbstention: frozen.AllowAbstention,
			RecoveryExpected:           recoveryCases[frozen.ID],
			TrackRepairRecovery:        repairCases[frozen.ID],
		}
		if frozen.AllowAbstention {
			base.ExpectedProposalKeys = nil
		}
		cases = append(cases, base)
		for _, variant := range frozen.Variants {
			if variant.ID == "" || variant.Description == "" {
				return nil, fmt.Errorf("certification case %q has a blank variant", frozen.ID)
			}
			variantCase := base
			variantCase.Name = frozen.ID + "--" + variant.ID
			variantCase.Intent.Description = variant.Description
			cases = append(cases, variantCase)
		}
	}
	return withProductionStructuralBounds(cases), nil
}

// CertificationFamilySmokeCases returns the canonical base Intent from every
// frozen semantic family. It is the bounded live-model adapter smoke set, not a
// release certification or a source of training examples.
func CertificationFamilySmokeCases() ([]Case, error) {
	cases, err := CertificationCases()
	if err != nil {
		return nil, err
	}
	smoke := make([]Case, 0, len(cases)/6)
	for _, c := range cases {
		if !strings.Contains(c.Name, "--") {
			smoke = append(smoke, c)
		}
	}
	return smoke, nil
}

type embeddedCertificationGenerator struct {
	inner        *suggest.Suggester
	fixture      *embeddedCatalogFixture
	caseByIntent map[string]string
}

func (g *embeddedCertificationGenerator) Suggest(ctx context.Context, intent suggest.Intent) (suggest.Proposal, error) {
	caseID, ok := g.caseByIntent[intent.Description]
	if !ok {
		return suggest.Proposal{}, fmt.Errorf("Intent is absent from the embedded certification corpus")
	}
	g.fixture.selectCase(caseID)
	return g.inner.Suggest(ctx, intent)
}

// NewEmbeddedCertificationGenerator wires the production Suggester to the
// digest-pinned synthetic catalog. The provider is the only live boundary, so
// every candidate model observes identical tool results.
func NewEmbeddedCertificationGenerator(provider llm.Provider) (Generator, Observer, error) {
	corpus, err := LoadEmbeddedCertificationCorpus()
	if err != nil {
		return nil, nil, err
	}
	fixtureBlob, err := certificationFiles.ReadFile(corpus.Fixture.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read certification fixture: %w", err)
	}
	var decoded certificationCatalogFixture
	if err := json.Unmarshal(fixtureBlob, &decoded); err != nil {
		return nil, nil, fmt.Errorf("decode certification fixture: %w", err)
	}
	fixture := newEmbeddedCatalogFixture(decoded)
	observed := &observedProvider{inner: provider}
	cat := catalog.New(embeddedLibraryCatalog{fixture}, embeddedTMDBCatalog{fixture}).WithPresence(fixture)
	suggester := suggest.New(observed, cat, fixture, 10).WithRatings(fixture)
	caseByIntent := make(map[string]string, len(corpus.Cases))
	for _, c := range corpus.Cases {
		caseByIntent[c.Description] = c.FixtureCase
		for _, variant := range c.Variants {
			caseByIntent[variant.Description] = c.FixtureCase
		}
	}
	return &embeddedCertificationGenerator{inner: suggester, fixture: fixture, caseByIntent: caseByIntent}, observed, nil
}

type embeddedCatalogFixture struct {
	mu      sync.RWMutex
	current string
	cases   map[string]certificationFixtureCase
	byID    map[int]catalog.Candidate
}

func newEmbeddedCatalogFixture(decoded certificationCatalogFixture) *embeddedCatalogFixture {
	f := &embeddedCatalogFixture{
		cases: make(map[string]certificationFixtureCase, len(decoded.Cases)),
		byID:  make(map[int]catalog.Candidate),
	}
	for _, c := range decoded.Cases {
		f.cases[c.ID] = c
		for _, response := range c.Responses {
			for _, candidate := range response.Candidates {
				f.byID[candidate.TMDBID] = candidate
			}
		}
	}
	return f
}

func (f *embeddedCatalogFixture) selectCase(id string) {
	f.mu.Lock()
	f.current = id
	f.mu.Unlock()
}

func (f *embeddedCatalogFixture) response(operation string) ([]catalog.Candidate, error) {
	f.mu.RLock()
	c := f.cases[f.current]
	f.mu.RUnlock()
	for _, response := range c.Responses {
		if response.Operation != operation {
			continue
		}
		if response.Error != "" {
			return nil, fmt.Errorf("%s", response.Error)
		}
		return append([]catalog.Candidate(nil), response.Candidates...), nil
	}
	return nil, nil
}

func (f *embeddedCatalogFixture) Exists(_ context.Context, _ provision.MediaType, tmdbID int) (bool, error) {
	_, ok := f.byID[tmdbID]
	return ok, nil
}

func (f *embeddedCatalogFixture) ContentRating(_ context.Context, _ provision.MediaType, tmdbID int) (string, error) {
	return f.byID[tmdbID].OfficialRating, nil
}

func (f *embeddedCatalogFixture) Present(_ context.Context, _ provision.MediaType, tmdbID, _ int) (catalog.Presence, bool, error) {
	candidate, ok := f.byID[tmdbID]
	if !ok || !candidate.InLibrary {
		return catalog.Presence{}, false, nil
	}
	return catalog.Presence{LibraryItemID: candidate.LibraryItemID, OfficialRating: candidate.OfficialRating, Genres: candidate.Genres}, true, nil
}

type embeddedLibraryCatalog struct{ fixture *embeddedCatalogFixture }

func (c embeddedLibraryCatalog) Search(context.Context, string, int) ([]library.SearchResult, error) {
	candidates, err := c.fixture.response("title")
	if err != nil {
		return nil, err
	}
	results := make([]library.SearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.InLibrary {
			continue
		}
		mediaType := library.Movie
		if candidate.MediaType == provision.Series {
			mediaType = library.Series
		}
		results = append(results, library.SearchResult{
			LibraryItemID: candidate.LibraryItemID, Name: candidate.Name, Year: candidate.Year,
			MediaType: mediaType, TMDBID: candidate.TMDBID, TVDBID: candidate.TVDBID,
			Genres: candidate.Genres, Overview: candidate.Overview, OfficialRating: candidate.OfficialRating,
		})
	}
	return results, nil
}

type embeddedTMDBCatalog struct{ fixture *embeddedCatalogFixture }

func (c embeddedTMDBCatalog) Search(context.Context, string, int) ([]catalog.Candidate, error) {
	return c.fixture.response("title")
}

func (c embeddedTMDBCatalog) Discover(_ context.Context, query catalog.DiscoveryQuery, _ int) ([]catalog.Candidate, error) {
	switch {
	case query.Network != "":
		return c.fixture.response("network")
	case len(query.Cast) > 0 && len(query.Creators) > 0:
		return c.fixture.response("people")
	case len(query.Cast) > 0:
		return c.fixture.response("cast")
	case len(query.Creators) > 0:
		return c.fixture.response("creator")
	}
	if len(query.Keywords) > 0 {
		return c.fixture.response("keyword")
	}
	return c.fixture.response("genre")
}
