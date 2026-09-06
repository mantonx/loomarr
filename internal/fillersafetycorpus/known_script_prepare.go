package fillersafetycorpus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

type knownScriptProvenance struct {
	SchemaVersion     int                        `json:"schemaVersion"`
	ContractVersion   string                     `json:"contractVersion"`
	PreparedAt        time.Time                  `json:"preparedAt"`
	AuthoritySHA256   string                     `json:"authoritySha256"`
	ParticipantID     string                     `json:"participantId"`
	SessionID         string                     `json:"sessionId"`
	TakeID            string                     `json:"takeId"`
	ScriptID          string                     `json:"scriptId"`
	Script            FileAuthority              `json:"script"`
	PolicyMapping     FileAuthority              `json:"policyMapping"`
	MasterAudio       FileAuthority              `json:"masterAudio"`
	SelectedAudio     FileAuthority              `json:"selectedAudio"`
	Transformation    KnownScriptTransformation  `json:"transformation"`
	PositiveIntervals []PreparedPositiveInterval `json:"positiveIntervals"`
	OutputSHA256      string                     `json:"outputSha256"`
	OutputBytes       int64                      `json:"outputBytes"`
	DurationMS        int64                      `json:"durationMs"`
}

// PrepareKnownScript verifies and packages already acquired, consented real
// speech. It emits positive candidates, never established certification truth.
func PrepareKnownScript(ctx context.Context, config PrepareKnownScriptConfig) (PrepareKnownScriptResult, error) {
	wrapper := &ffmpegWrapper{
		ffmpegPath: config.FFmpegPath, ffprobePath: config.FFprobePath, recipe: KnownScriptPackagingRecipe,
	}
	return prepareKnownScript(ctx, config, wrapper)
}

func prepareKnownScript(
	ctx context.Context,
	config PrepareKnownScriptConfig,
	wrapper mediaWrapper,
) (PrepareKnownScriptResult, error) {
	if ctx == nil || ctx.Err() != nil || wrapper == nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("known-script preparation requires an active context and media wrapper")
	}
	if err := validateKnownScriptConfig(config); err != nil {
		return PrepareKnownScriptResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, config.MaximumWallTime)
	defer cancel()
	started := time.Now()
	loaded, err := loadKnownScript(config)
	if err != nil {
		return PrepareKnownScriptResult{}, err
	}
	if err := validateKnownScriptAuthority(loaded.authority, config); err != nil {
		return PrepareKnownScriptResult{}, err
	}
	verified, inputBytes, err := verifyKnownScriptInputs(runCtx, loaded, config)
	if err != nil {
		return PrepareKnownScriptResult{}, err
	}
	ffmpeg, ffprobe, recipeSHA256, err := wrapper.Identity(runCtx)
	if err != nil || recipeSHA256 != hashBytes([]byte(KnownScriptPackagingRecipe)) {
		return PrepareKnownScriptResult{}, fmt.Errorf("known-script media wrapper identity is invalid")
	}
	stage, err := beginPrivateStage(config.OutputDirectory)
	if err != nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("create known-script private output stage")
	}
	defer stage.cleanup()
	cohort := PreparedCohort{
		SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: PreparedCohortContractVersion,
		PreparedAt: config.PreparedAt.UTC(), Kind: PreparedCohortKindPositiveCandidate, Dataset: KnownScriptDatasetID,
		ReleaseAuthoritySHA256: loaded.authoritySHA256, RecipeSHA256: recipeSHA256, FFmpeg: ffmpeg, FFprobe: ffprobe,
	}
	owner := KnownScriptOwnerMap{
		SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: KnownScriptOwnerMapContractVersion,
		PreparedAt: config.PreparedAt.UTC(),
	}
	outputBytes := int64(0)
	for index, selected := range selectKnownScript(loaded.seed, loaded.authority.Members) {
		if err := checkKnownScriptProgress(runCtx, started, config.MaximumWallTime); err != nil {
			return PrepareKnownScriptResult{}, err
		}
		member := selected.member
		input := verified[member.ParticipantID]
		caseRelative := filepath.ToSlash(filepath.Join("cases", selected.caseID))
		caseRoot := filepath.Join(stage.path, filepath.FromSlash(caseRelative))
		if err := os.MkdirAll(caseRoot, 0o700); err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("create known-script case directory %d", index+1)
		}
		transcriptRelative := caseRelative + "/transcript.txt"
		if err := writePrivate(filepath.Join(stage.path, filepath.FromSlash(transcriptRelative)), input.scriptRaw); err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("write known-script transcript %d", index+1)
		}
		audioSnapshot := filepath.Join(caseRoot, ".verified-audio")
		if err := snapshotVerifiedMember(loaded.root, member.SelectedAudio, audioSnapshot, config.MaximumInputBytes); err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("snapshot known-script audio %d", index+1)
		}
		sourceRelative := caseRelative + "/source.mp4"
		sourcePath := filepath.Join(stage.path, filepath.FromSlash(sourceRelative))
		wrapped, wrapErr := wrapper.Wrap(runCtx, audioSnapshot, sourcePath)
		removeErr := os.Remove(audioSnapshot)
		if wrapErr != nil || !validSHA256(wrapped.SHA256) || wrapped.Bytes <= 0 || wrapped.DurationMS <= 0 ||
			wrapped.DurationMS > maximumPreparedSourceDurationMS {
			return PrepareKnownScriptResult{}, fmt.Errorf("wrap known-script source %d failed or returned invalid media", index+1)
		}
		if removeErr != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("remove known-script audio snapshot %d", index+1)
		}
		if err := verifyWrappedOutput(sourcePath, wrapped); err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("verify known-script source %d", index+1)
		}
		if !validPreparedIntervals(member.PositiveIntervals, wrapped.DurationMS) {
			return PrepareKnownScriptResult{}, fmt.Errorf("known-script source %d intervals exceed measured output", index+1)
		}
		provenance := knownScriptProvenance{
			SchemaVersion: KnownScriptAuthoritySchemaVersion, ContractVersion: KnownScriptAuthorityContractVersion,
			PreparedAt: config.PreparedAt.UTC(), AuthoritySHA256: loaded.authoritySHA256,
			ParticipantID: member.ParticipantID, SessionID: member.SessionID, TakeID: member.TakeID,
			ScriptID: member.ScriptID, Script: member.Script, PolicyMapping: member.PolicyMapping,
			MasterAudio: member.MasterAudio, SelectedAudio: member.SelectedAudio, Transformation: member.Transformation,
			PositiveIntervals: slices.Clone(member.PositiveIntervals), OutputSHA256: wrapped.SHA256,
			OutputBytes: wrapped.Bytes, DurationMS: wrapped.DurationMS,
		}
		provenanceRelative := caseRelative + "/provenance.json"
		provenanceRaw, err := writePrivateJSON(filepath.Join(stage.path, filepath.FromSlash(provenanceRelative)), provenance)
		if err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("write known-script provenance %d", index+1)
		}
		rights := knownScriptRights{
			SchemaVersion: KnownScriptRightsSchemaVersion, ContractVersion: KnownScriptRightsContractVersion,
			PreparedAt: config.PreparedAt.UTC(), AuthoritySHA256: loaded.authoritySHA256,
			ParticipantID: member.ParticipantID, Consent: member.Consent,
			ProcessorSchedule: input.processorSchedule, Assets: slices.Clone(member.Transformation.Assets),
		}
		rightsRelative := caseRelative + "/rights.json"
		rightsRaw, err := writePrivateJSON(filepath.Join(stage.path, filepath.FromSlash(rightsRelative)), rights)
		if err != nil {
			return PrepareKnownScriptResult{}, fmt.Errorf("write known-script rights %d", index+1)
		}
		caseBytes := wrapped.Bytes + int64(len(input.scriptRaw)+len(provenanceRaw)+len(rightsRaw))
		if caseBytes > config.MaximumOutputBytes-outputBytes {
			return PrepareKnownScriptResult{}, fmt.Errorf("known-script outputs exceed byte ceiling")
		}
		outputBytes += caseBytes
		cohort.Cases = append(cohort.Cases, PreparedCohortCase{
			CaseID: selected.caseID, SourcePath: sourceRelative,
			SourceAuthority: fillersafety.SourceAuthority{
				SchemaVersion: fillersafety.SourceAuthoritySchemaVersion, PolicySHA256: loaded.authority.PolicySHA256,
				Implementation: loaded.authority.Implementation, SourceID: selected.caseID, SourceSHA256: wrapped.SHA256,
				SourceBytes: wrapped.Bytes, DurationMS: wrapped.DurationMS, HasAudio: true, HasVideo: true,
				MeasuredAt: config.PreparedAt.UTC(), FFmpeg: ffmpeg, FFprobe: ffprobe,
			},
			SourceFamily: selected.sourceFamily, TranscriptPath: transcriptRelative,
			TranscriptSHA256: hashBytes(input.scriptRaw), TranscriptBytes: int64(len(input.scriptRaw)),
			TruthProvenancePath: provenanceRelative, TruthProvenanceSHA256: hashBytes(provenanceRaw),
			TruthProvenanceBytes: int64(len(provenanceRaw)), RightsPath: rightsRelative,
			RightsSHA256: hashBytes(rightsRaw), RightsBytes: int64(len(rightsRaw)),
			Claim: PreparedCohortKindPositiveCandidate, Locale: member.Locale,
			Slices: slices.Clone(member.Slices), PositiveIntervals: slices.Clone(member.PositiveIntervals),
		})
		owner.Entries = append(owner.Entries, KnownScriptOwnerMapEntry{
			CaseID: selected.caseID, SourceFamily: selected.sourceFamily, ParticipantID: member.ParticipantID,
			SessionID: member.SessionID, TakeID: member.TakeID, ScriptID: member.ScriptID,
			MasterAudioPath: member.MasterAudio.Path, SelectedAudioPath: member.SelectedAudio.Path,
		})
	}
	finalFFmpeg, finalFFprobe, finalRecipeSHA256, err := wrapper.Identity(runCtx)
	if err != nil || finalFFmpeg != ffmpeg || finalFFprobe != ffprobe || finalRecipeSHA256 != recipeSHA256 {
		return PrepareKnownScriptResult{}, fmt.Errorf("known-script media wrapper identity changed during preparation")
	}
	if err := verifyKnownScriptStability(runCtx, loaded, config, inputBytes); err != nil {
		return PrepareKnownScriptResult{}, err
	}
	if err := validatePreparedCandidateCohort(
		cohort, config.ExpectedSpeakers, PreparedCohortKindPositiveCandidate, KnownScriptDatasetID, config.PreparedAt,
	); err != nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("prepared known-script cohort: %w", err)
	}
	cohortRaw, err := writePrivateJSON(filepath.Join(stage.path, "cohort.json"), cohort)
	if err != nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("write known-script cohort")
	}
	cohortSHA256 := hashBytes(cohortRaw)
	owner.CohortSHA256 = cohortSHA256
	if err := validateKnownScriptOwnerMap(owner, cohort, cohortSHA256); err != nil {
		return PrepareKnownScriptResult{}, err
	}
	ownerRaw, err := writePrivateJSON(filepath.Join(stage.path, "owner-map.json"), owner)
	if err != nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("write known-script owner map")
	}
	if int64(len(cohortRaw)+len(ownerRaw)) > config.MaximumOutputBytes-outputBytes {
		return PrepareKnownScriptResult{}, fmt.Errorf("known-script documents exceed output byte ceiling")
	}
	outputBytes += int64(len(cohortRaw) + len(ownerRaw))
	if err := checkKnownScriptProgress(runCtx, started, config.MaximumWallTime); err != nil {
		return PrepareKnownScriptResult{}, err
	}
	if err := stage.publish(); err != nil {
		return PrepareKnownScriptResult{}, fmt.Errorf("publish known-script prepared output")
	}
	return PrepareKnownScriptResult{
		Speakers: len(cohort.Cases), CohortSHA256: cohortSHA256, OwnerMapSHA256: hashBytes(ownerRaw),
		InputBytes: inputBytes, OutputBytes: outputBytes,
	}, nil
}

func checkKnownScriptProgress(ctx context.Context, started time.Time, maximum time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Since(started) > maximum {
		return fmt.Errorf("known-script preparation exceeded wall-time ceiling")
	}
	return nil
}
