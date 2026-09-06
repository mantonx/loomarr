package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

const (
	TemporalStructureWindowPreflightSchemaVersion   = 1
	TemporalStructureWindowPreflightContractVersion = "filler-temporal-structure-window-preflight-v1"
	TemporalStructureWindowPreflightFamilies        = 2
	TemporalStructureWindowPreflightEdgeCases       = 2

	TemporalStructureWindowPreflightReady   = "ready"
	TemporalStructureWindowPreflightBlocked = "blocked"
)

type TemporalStructureWindowPreflightConfig struct {
	WindowSetManifestPath       string
	SuitePath                   string
	ShortSourceCeilingMS        int64
	IntendedLongSourceCeilingMS int64
	OutputPath                  string
}

// TemporalStructureWindowPreflight is a deterministic, provider-free paid-run plan. It binds
// aggregate topology only: construction truth and case identifiers remain in the private suite.
type TemporalStructureWindowPreflight struct {
	SchemaVersion                        int    `json:"schemaVersion"`
	ContractVersion                      string `json:"contractVersion"`
	WindowSetManifestSHA256              string `json:"windowSetManifestSha256"`
	SuiteSHA256                          string `json:"suiteSha256"`
	SuiteFileSHA256                      string `json:"suiteFileSha256"`
	AssessmentMediaProfileSHA256         string `json:"assessmentMediaProfileSha256"`
	WindowProfileSHA256                  string `json:"windowProfileSha256"`
	ShortSourceCeilingMS                 int64  `json:"shortSourceCeilingMs"`
	IntendedLongSourceCeilingMS          int64  `json:"intendedLongSourceCeilingMs"`
	MinimumObservedSourceDurationMS      int64  `json:"minimumObservedSourceDurationMs"`
	MaximumObservedSourceDurationMS      int64  `json:"maximumObservedSourceDurationMs"`
	MaximumObservedSourceBytes           int64  `json:"maximumObservedSourceBytes"`
	MinimumObservedWindowBytes           int64  `json:"minimumObservedWindowBytes"`
	MaximumObservedWindowBytes           int64  `json:"maximumObservedWindowBytes"`
	MinimumObservedWindowsPerSource      int    `json:"minimumObservedWindowsPerSource"`
	MaximumObservedWindowsPerSource      int    `json:"maximumObservedWindowsPerSource"`
	LowerEnvelopeEdgeCases               int    `json:"lowerEnvelopeEdgeCases"`
	UpperEnvelopeEdgeCases               int    `json:"upperEnvelopeEdgeCases"`
	MinimumRequiredEnvelopeEdgeCases     int    `json:"minimumRequiredEnvelopeEdgeCases"`
	Cases                                int    `json:"cases"`
	AssessorFamilies                     int    `json:"assessorFamilies"`
	WindowRequestsPerFamily              int    `json:"windowRequestsPerFamily"`
	CompleteVideoRequestsPerFamily       int    `json:"completeVideoRequestsPerFamily"`
	TotalProviderRequests                int    `json:"totalProviderRequests"`
	ProtocolMaximumSourceDurationMS      int64  `json:"protocolMaximumSourceDurationMs"`
	ProtocolMaximumWindowsPerSource      int    `json:"protocolMaximumWindowsPerSource"`
	ContinuousProductionEnvelope         bool   `json:"continuousProductionEnvelope"`
	IntendedProductionCeilingRepresented bool   `json:"intendedProductionCeilingRepresented"`
	ReadyForPaidCertification            bool   `json:"readyForPaidCertification"`
	TrainingAllowed                      bool   `json:"trainingAllowed"`
	ProductionAdmissionAllowed           bool   `json:"productionAdmissionAllowed"`
	AutomaticMaterializationAllowed      bool   `json:"automaticMaterializationAllowed"`
	Status                               string `json:"status"`
	NextAction                           string `json:"nextAction"`
	SHA256                               string `json:"sha256"`
}

// PublishTemporalStructureWindowPreflight validates all local media before publishing one
// immutable run plan. It has no provider client and cannot reserve or spend money.
func PublishTemporalStructureWindowPreflight(config TemporalStructureWindowPreflightConfig) (TemporalStructureWindowPreflight, string, error) {
	if strings.TrimSpace(config.WindowSetManifestPath) == "" || strings.TrimSpace(config.SuitePath) == "" ||
		strings.TrimSpace(config.OutputPath) == "" || config.ShortSourceCeilingMS <= 0 ||
		config.IntendedLongSourceCeilingMS <= config.ShortSourceCeilingMS+fillerstructurewindowcert.BoundaryToleranceMS ||
		config.IntendedLongSourceCeilingMS > fillerstructurewindow.MaximumSourceDurationMS {
		return TemporalStructureWindowPreflight{}, "", errors.New("window preflight requires manifest, suite, output, and distinct bounded source ceilings")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureWindowPreflight{}, "", err
	}
	suite, suiteFileSHA, err := LoadTemporalStructureWindowCertificationSuite(config.SuitePath)
	if err != nil {
		return TemporalStructureWindowPreflight{}, "", fmt.Errorf("load private window certification suite: %w", err)
	}
	if err := validateTemporalStructureWindowPreflightJoin(manifest, suite); err != nil {
		return TemporalStructureWindowPreflight{}, "", err
	}
	report := buildTemporalStructureWindowPreflight(config, manifest, manifestSHA, suite, suiteFileSHA)
	if err := ValidateTemporalStructureWindowPreflight(report); err != nil {
		return TemporalStructureWindowPreflight{}, "", err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalStructureWindowPreflight{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureWindowPreflight{}, "", fmt.Errorf("publish window preflight: %w", err)
	}
	return report, hashBytes(raw), nil
}

func buildTemporalStructureWindowPreflight(config TemporalStructureWindowPreflightConfig, manifest TemporalStructureWindowSetManifest, manifestSHA string, suite fillerstructurewindowcert.Suite, suiteFileSHA string) TemporalStructureWindowPreflight {
	profile := fillerstructurewindow.CanonicalProfile()
	report := TemporalStructureWindowPreflight{
		SchemaVersion: TemporalStructureWindowPreflightSchemaVersion, ContractVersion: TemporalStructureWindowPreflightContractVersion,
		WindowSetManifestSHA256: manifestSHA, SuiteSHA256: suite.SHA256, SuiteFileSHA256: suiteFileSHA,
		AssessmentMediaProfileSHA256: manifest.AssessmentMediaProfileSHA256, WindowProfileSHA256: profile.SHA256,
		ShortSourceCeilingMS: config.ShortSourceCeilingMS, IntendedLongSourceCeilingMS: config.IntendedLongSourceCeilingMS,
		MinimumRequiredEnvelopeEdgeCases: TemporalStructureWindowPreflightEdgeCases,
		Cases:                            len(manifest.Cases), AssessorFamilies: TemporalStructureWindowPreflightFamilies,
		CompleteVideoRequestsPerFamily: len(manifest.Cases), ProtocolMaximumSourceDurationMS: profile.MaximumSourceDurationMS,
		ProtocolMaximumWindowsPerSource: profile.MaximumWindows,
	}
	for caseIndex, item := range manifest.Cases {
		duration := item.Source.DurationMs
		windows := len(item.Windows)
		if caseIndex == 0 || duration < report.MinimumObservedSourceDurationMS {
			report.MinimumObservedSourceDurationMS = duration
		}
		if duration > report.MaximumObservedSourceDurationMS {
			report.MaximumObservedSourceDurationMS = duration
		}
		if item.Source.Bytes > report.MaximumObservedSourceBytes {
			report.MaximumObservedSourceBytes = item.Source.Bytes
		}
		if caseIndex == 0 || windows < report.MinimumObservedWindowsPerSource {
			report.MinimumObservedWindowsPerSource = windows
		}
		if windows > report.MaximumObservedWindowsPerSource {
			report.MaximumObservedWindowsPerSource = windows
		}
		if duration > config.ShortSourceCeilingMS && duration <= config.ShortSourceCeilingMS+fillerstructurewindowcert.BoundaryToleranceMS {
			report.LowerEnvelopeEdgeCases++
		}
		if duration >= config.IntendedLongSourceCeilingMS-fillerstructurewindowcert.BoundaryToleranceMS && duration <= config.IntendedLongSourceCeilingMS {
			report.UpperEnvelopeEdgeCases++
		}
		for _, window := range item.MediaSet.Windows {
			bytes := window.Media.Bytes
			if report.MinimumObservedWindowBytes == 0 || bytes < report.MinimumObservedWindowBytes {
				report.MinimumObservedWindowBytes = bytes
			}
			if bytes > report.MaximumObservedWindowBytes {
				report.MaximumObservedWindowBytes = bytes
			}
			report.WindowRequestsPerFamily++
		}
	}
	report.TotalProviderRequests = report.AssessorFamilies * (report.WindowRequestsPerFamily + report.CompleteVideoRequestsPerFamily)
	report.ContinuousProductionEnvelope = report.MinimumObservedSourceDurationMS > report.ShortSourceCeilingMS &&
		report.LowerEnvelopeEdgeCases >= report.MinimumRequiredEnvelopeEdgeCases
	report.IntendedProductionCeilingRepresented = report.UpperEnvelopeEdgeCases >= report.MinimumRequiredEnvelopeEdgeCases &&
		report.MaximumObservedSourceDurationMS <= report.IntendedLongSourceCeilingMS
	report.ReadyForPaidCertification = report.ContinuousProductionEnvelope && report.IntendedProductionCeilingRepresented
	if report.ReadyForPaidCertification {
		report.Status = TemporalStructureWindowPreflightReady
		report.NextAction = "run_two_truth_blind_window_families"
	} else {
		report.Status = TemporalStructureWindowPreflightBlocked
		report.NextAction = "extend_and_rerender_sealed_window_corpus"
	}
	report.SHA256 = temporalStructureWindowPreflightSHA256(report)
	return report
}

// LoadTemporalStructureWindowPreflight replays a coordinator-produced run plan without opening
// private construction truth. Paid family commands use it before snapshots or provider work.
func LoadTemporalStructureWindowPreflight(path, windowSetManifestSHA256 string) (TemporalStructureWindowPreflight, string, error) {
	if strings.TrimSpace(path) == "" || !reviewSHA256(windowSetManifestSHA256) {
		return TemporalStructureWindowPreflight{}, "", errors.New("window preflight load requires a report and public manifest identity")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureWindowPreflight{}, "", fmt.Errorf("read window preflight: %w", err)
	}
	report, err := readStrictJSON[TemporalStructureWindowPreflight](path)
	if err != nil {
		return TemporalStructureWindowPreflight{}, "", fmt.Errorf("decode window preflight: %w", err)
	}
	if err := ValidateTemporalStructureWindowPreflight(report); err != nil {
		return TemporalStructureWindowPreflight{}, "", err
	}
	if !report.ReadyForPaidCertification || report.WindowSetManifestSHA256 != windowSetManifestSHA256 {
		return TemporalStructureWindowPreflight{}, "", errors.New("window preflight does not authorize this paid certification run")
	}
	return report, hashBytes(raw), nil
}

func validateTemporalStructureWindowPreflightJoin(manifest TemporalStructureWindowSetManifest, suite fillerstructurewindowcert.Suite) error {
	if len(manifest.Cases) != len(suite.Cases) {
		return errors.New("window preflight suite does not contain the complete public manifest")
	}
	privateByMediaSet := make(map[string]fillerstructurewindowcert.Case, len(suite.Cases))
	for _, item := range suite.Cases {
		privateByMediaSet[item.MediaSet.SHA256] = item
	}
	for _, item := range manifest.Cases {
		private, ok := privateByMediaSet[item.MediaSet.SHA256]
		if !ok || !reflect.DeepEqual(private.MediaSet, item.MediaSet) {
			return errors.New("window preflight suite media sets drifted from the public manifest")
		}
		delete(privateByMediaSet, item.MediaSet.SHA256)
	}
	if len(privateByMediaSet) != 0 {
		return errors.New("window preflight suite contains an unknown media set")
	}
	return nil
}

func ValidateTemporalStructureWindowPreflight(report TemporalStructureWindowPreflight) error {
	profile := fillerstructurewindow.CanonicalProfile()
	validStatus := report.Status == TemporalStructureWindowPreflightReady && report.NextAction == "run_two_truth_blind_window_families" ||
		report.Status == TemporalStructureWindowPreflightBlocked && report.NextAction == "extend_and_rerender_sealed_window_corpus"
	if report.SchemaVersion != TemporalStructureWindowPreflightSchemaVersion || report.ContractVersion != TemporalStructureWindowPreflightContractVersion ||
		!reviewSHA256(report.WindowSetManifestSHA256) || !reviewSHA256(report.SuiteSHA256) || !reviewSHA256(report.SuiteFileSHA256) ||
		report.AssessmentMediaProfileSHA256 != profile.AssessmentMediaProfileSHA256 || report.WindowProfileSHA256 != profile.SHA256 ||
		report.ShortSourceCeilingMS <= 0 || report.IntendedLongSourceCeilingMS <= report.ShortSourceCeilingMS+fillerstructurewindowcert.BoundaryToleranceMS ||
		report.IntendedLongSourceCeilingMS > profile.MaximumSourceDurationMS || report.MinimumObservedSourceDurationMS <= 0 ||
		report.MaximumObservedSourceDurationMS < report.MinimumObservedSourceDurationMS || report.MaximumObservedSourceDurationMS > profile.MaximumSourceDurationMS ||
		report.MaximumObservedSourceBytes <= 0 || report.MaximumObservedSourceBytes > TemporalStructureWindowMaximumSourceBytes ||
		report.MinimumObservedWindowBytes <= 0 || report.MaximumObservedWindowBytes < report.MinimumObservedWindowBytes || report.MaximumObservedWindowBytes > profile.MaximumWindowBytes ||
		report.MinimumObservedWindowsPerSource < 2 || report.MaximumObservedWindowsPerSource < report.MinimumObservedWindowsPerSource ||
		report.MaximumObservedWindowsPerSource > profile.MaximumWindows || report.MinimumRequiredEnvelopeEdgeCases != TemporalStructureWindowPreflightEdgeCases ||
		report.LowerEnvelopeEdgeCases < 0 || report.LowerEnvelopeEdgeCases > report.Cases || report.UpperEnvelopeEdgeCases < 0 || report.UpperEnvelopeEdgeCases > report.Cases ||
		report.Cases != TemporalStructureWindowCorpusCases || report.AssessorFamilies != TemporalStructureWindowPreflightFamilies ||
		report.WindowRequestsPerFamily < report.Cases*2 || report.WindowRequestsPerFamily > report.Cases*profile.MaximumWindows ||
		report.CompleteVideoRequestsPerFamily != report.Cases ||
		report.TotalProviderRequests != report.AssessorFamilies*(report.WindowRequestsPerFamily+report.CompleteVideoRequestsPerFamily) ||
		report.ProtocolMaximumSourceDurationMS != profile.MaximumSourceDurationMS || report.ProtocolMaximumWindowsPerSource != profile.MaximumWindows ||
		report.ContinuousProductionEnvelope != (report.MinimumObservedSourceDurationMS > report.ShortSourceCeilingMS && report.LowerEnvelopeEdgeCases >= report.MinimumRequiredEnvelopeEdgeCases) ||
		report.IntendedProductionCeilingRepresented != (report.UpperEnvelopeEdgeCases >= report.MinimumRequiredEnvelopeEdgeCases && report.MaximumObservedSourceDurationMS <= report.IntendedLongSourceCeilingMS) ||
		report.ReadyForPaidCertification != (report.ContinuousProductionEnvelope && report.IntendedProductionCeilingRepresented) ||
		(report.ReadyForPaidCertification != (report.Status == TemporalStructureWindowPreflightReady)) || !validStatus ||
		report.TrainingAllowed || report.ProductionAdmissionAllowed || report.AutomaticMaterializationAllowed ||
		!reviewSHA256(report.SHA256) || report.SHA256 != temporalStructureWindowPreflightSHA256(report) {
		return errors.New("window certification preflight identity, envelope, topology, or disposition is invalid")
	}
	return nil
}

func temporalStructureWindowPreflightSHA256(report TemporalStructureWindowPreflight) string {
	report.SHA256 = ""
	return temporalTruthJSONSHA(report)
}
