package fillersafetycorpus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const maximumPreparedSourceDurationMS = int64(60_000)

type vctkProvenance struct {
	SchemaVersion          int           `json:"schemaVersion"`
	ContractVersion        string        `json:"contractVersion"`
	PreparedAt             time.Time     `json:"preparedAt"`
	ReleaseAuthoritySHA256 string        `json:"releaseAuthoritySha256"`
	RecipeSHA256           string        `json:"recipeSha256"`
	SpeakerID              string        `json:"speakerId"`
	UtteranceID            string        `json:"utteranceId"`
	Microphone             string        `json:"microphone"`
	Audio                  FileAuthority `json:"audio"`
	Transcript             FileAuthority `json:"transcript"`
	ScreeningEvidence      FileAuthority `json:"screeningEvidence"`
	OutputSHA256           string        `json:"outputSha256"`
	OutputBytes            int64         `json:"outputBytes"`
	DurationMS             int64         `json:"durationMs"`
}

// PrepareVCTK verifies and packages an already-acquired VCTK release. The
// resulting cohort contains clean candidates, never established clean truth.
func PrepareVCTK(ctx context.Context, config PrepareVCTKConfig) (PrepareVCTKResult, error) {
	return prepareVCTK(ctx, config, &ffmpegWrapper{ffmpegPath: config.FFmpegPath, ffprobePath: config.FFprobePath})
}

func prepareVCTK(ctx context.Context, config PrepareVCTKConfig, wrapper mediaWrapper) (PrepareVCTKResult, error) {
	if ctx == nil || ctx.Err() != nil || wrapper == nil {
		return PrepareVCTKResult{}, fmt.Errorf("VCTK preparation requires an active context and media wrapper")
	}
	if err := validatePrepareConfig(config); err != nil {
		return PrepareVCTKResult{}, err
	}
	boundedContext, cancel := context.WithTimeout(ctx, config.MaximumWallTime)
	defer cancel()
	ctx = boundedContext
	started := time.Now()
	loaded, err := loadVCTK(config)
	if err != nil {
		return PrepareVCTKResult{}, err
	}
	if err := validateRelease(loaded.authority, config.PreparedAt); err != nil {
		return PrepareVCTKResult{}, err
	}
	ffmpeg, ffprobe, recipeSHA, err := wrapper.Identity(ctx)
	if err != nil || recipeSHA != hashBytes([]byte(VCTKNeutralVideoRecipe)) {
		return PrepareVCTKResult{}, fmt.Errorf("VCTK media wrapper identity is invalid")
	}
	verified, inputBytes, err := verifyVCTKInputs(ctx, loaded, config)
	if err != nil {
		return PrepareVCTKResult{}, err
	}
	selected := selectVCTK(loaded.seed, loaded.authority.Members, config.ExpectedSpeakers)
	stage, err := beginPrivateStage(config.OutputDirectory)
	if err != nil {
		return PrepareVCTKResult{}, err
	}
	defer stage.cleanup()
	if err := os.Mkdir(filepath.Join(stage.path, "evidence"), 0o700); err != nil {
		return PrepareVCTKResult{}, err
	}
	rightsRelative := "evidence/release-authority.json"
	if err := writePrivate(filepath.Join(stage.path, filepath.FromSlash(rightsRelative)), loaded.authorityRaw); err != nil {
		return PrepareVCTKResult{}, err
	}
	rightsSHA := hashBytes(loaded.authorityRaw)
	outputBytes := int64(len(loaded.authorityRaw))
	cohort := PreparedCohort{
		SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: PreparedCohortContractVersion,
		PreparedAt: config.PreparedAt.UTC(), Kind: PreparedCohortKindCleanCandidate, Dataset: VCTKDatasetID,
		ReleaseAuthoritySHA256: loaded.authoritySHA, RecipeSHA256: recipeSHA, FFmpeg: ffmpeg, FFprobe: ffprobe,
	}
	owner := VCTKOwnerMap{
		SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: VCTKOwnerMapContractVersion,
		PreparedAt: config.PreparedAt.UTC(),
	}
	for index, selectedCase := range selected {
		if err := checkPrepareProgress(ctx, started, config.MaximumWallTime); err != nil {
			return PrepareVCTKResult{}, err
		}
		member := selectedCase.member
		memberKey := vctkMemberKey(member)
		input := verified[memberKey]
		caseRelative := filepath.ToSlash(filepath.Join("cases", selectedCase.caseID))
		caseRoot := filepath.Join(stage.path, filepath.FromSlash(caseRelative))
		if err := os.MkdirAll(caseRoot, 0o700); err != nil {
			return PrepareVCTKResult{}, err
		}
		transcriptRelative := caseRelative + "/transcript.txt"
		if err := writePrivate(filepath.Join(stage.path, filepath.FromSlash(transcriptRelative)), input.transcriptRaw); err != nil {
			return PrepareVCTKResult{}, fmt.Errorf("write VCTK transcript %d: %w", index+1, err)
		}
		sourceRelative := caseRelative + "/source.mp4"
		sourcePath := filepath.Join(stage.path, filepath.FromSlash(sourceRelative))
		audioSnapshot := filepath.Join(caseRoot, ".verified-audio")
		if err := snapshotVerifiedMember(loaded.root, member.Audio, audioSnapshot, config.MaximumInputBytes); err != nil {
			return PrepareVCTKResult{}, fmt.Errorf("snapshot VCTK source %d: %w", index+1, err)
		}
		wrapped, err := wrapper.Wrap(ctx, audioSnapshot, sourcePath)
		removeErr := os.Remove(audioSnapshot)
		if err != nil || !validSHA256(wrapped.SHA256) || wrapped.Bytes <= 0 || wrapped.DurationMS <= 0 ||
			wrapped.DurationMS > maximumPreparedSourceDurationMS {
			return PrepareVCTKResult{}, fmt.Errorf("wrap VCTK source %d failed or returned invalid media", index+1)
		}
		if removeErr != nil {
			return PrepareVCTKResult{}, fmt.Errorf("remove VCTK source %d snapshot: %w", index+1, removeErr)
		}
		if err := verifyWrappedOutput(sourcePath, wrapped); err != nil {
			return PrepareVCTKResult{}, fmt.Errorf("verify VCTK source %d: %w", index+1, err)
		}
		provenance := vctkProvenance{
			SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: PreparedCohortContractVersion,
			PreparedAt: config.PreparedAt.UTC(), ReleaseAuthoritySHA256: loaded.authoritySHA, RecipeSHA256: recipeSHA,
			SpeakerID: member.SpeakerID, UtteranceID: member.UtteranceID, Microphone: member.Microphone,
			Audio: member.Audio, Transcript: member.Transcript, ScreeningEvidence: member.ScreeningEvidence,
			OutputSHA256: wrapped.SHA256, OutputBytes: wrapped.Bytes, DurationMS: wrapped.DurationMS,
		}
		provenanceRelative := caseRelative + "/provenance.json"
		provenanceRaw, err := writePrivateJSON(filepath.Join(stage.path, filepath.FromSlash(provenanceRelative)), provenance)
		if err != nil {
			return PrepareVCTKResult{}, err
		}
		caseOutputBytes := int64(len(input.transcriptRaw)+len(provenanceRaw)) + wrapped.Bytes
		if caseOutputBytes > config.MaximumOutputBytes-outputBytes {
			return PrepareVCTKResult{}, fmt.Errorf("VCTK prepared artifacts exceed output byte ceiling")
		}
		outputBytes += caseOutputBytes
		cohort.Cases = append(cohort.Cases, PreparedCohortCase{
			CaseID: selectedCase.caseID, SourcePath: sourceRelative,
			SourceAuthority: fillersafety.SourceAuthority{
				SchemaVersion: fillersafety.SourceAuthoritySchemaVersion, PolicySHA256: config.PolicySHA256,
				Implementation: config.Implementation, SourceID: selectedCase.caseID, SourceSHA256: wrapped.SHA256,
				SourceBytes: wrapped.Bytes, DurationMS: wrapped.DurationMS, HasAudio: true, HasVideo: true,
				MeasuredAt: config.PreparedAt.UTC(), FFmpeg: ffmpeg, FFprobe: ffprobe,
			},
			SourceFamily: selectedCase.sourceFamily, TranscriptPath: transcriptRelative,
			TranscriptSHA256: hashBytes(input.transcriptRaw), TranscriptBytes: int64(len(input.transcriptRaw)),
			TruthProvenancePath: provenanceRelative, TruthProvenanceSHA256: hashBytes(provenanceRaw),
			TruthProvenanceBytes: int64(len(provenanceRaw)), RightsPath: rightsRelative,
			RightsSHA256: rightsSHA, RightsBytes: int64(len(loaded.authorityRaw)), Claim: PreparedCohortKindCleanCandidate,
			Locale: member.Locale, Slices: []string{VCTKTargetLocaleSlice},
		})
		owner.Entries = append(owner.Entries, VCTKOwnerMapEntry{
			CaseID: selectedCase.caseID, SourceFamily: selectedCase.sourceFamily, SpeakerID: member.SpeakerID,
			UtteranceID: member.UtteranceID, Microphone: member.Microphone,
			AudioPath: member.Audio.Path, TranscriptPath: member.Transcript.Path,
		})
	}
	finalFFmpeg, finalFFprobe, finalRecipeSHA, err := wrapper.Identity(ctx)
	if err != nil || finalFFmpeg != ffmpeg || finalFFprobe != ffprobe || finalRecipeSHA != recipeSHA {
		return PrepareVCTKResult{}, fmt.Errorf("VCTK media wrapper identity changed during preparation")
	}
	if err := validatePreparedCohort(cohort, config.ExpectedSpeakers); err != nil {
		return PrepareVCTKResult{}, err
	}
	cohortRaw, err := writePrivateJSON(filepath.Join(stage.path, "cohort.json"), cohort)
	if err != nil {
		return PrepareVCTKResult{}, err
	}
	cohortSHA := hashBytes(cohortRaw)
	owner.CohortSHA256 = cohortSHA
	if err := validateOwnerMap(owner, cohort, cohortSHA); err != nil {
		return PrepareVCTKResult{}, err
	}
	ownerRaw, err := writePrivateJSON(filepath.Join(stage.path, "owner-map.json"), owner)
	if err != nil {
		return PrepareVCTKResult{}, err
	}
	if int64(len(cohortRaw)+len(ownerRaw)) > config.MaximumOutputBytes-outputBytes {
		return PrepareVCTKResult{}, fmt.Errorf("VCTK prepared documents exceed output byte ceiling")
	}
	outputBytes += int64(len(cohortRaw) + len(ownerRaw))
	if err := checkPrepareProgress(ctx, started, config.MaximumWallTime); err != nil {
		return PrepareVCTKResult{}, err
	}
	if err := stage.publish(); err != nil {
		return PrepareVCTKResult{}, err
	}
	return PrepareVCTKResult{
		Speakers: len(cohort.Cases), CohortSHA256: cohortSHA, OwnerMapSHA256: hashBytes(ownerRaw),
		InputBytes: inputBytes, OutputBytes: outputBytes,
	}, nil
}

func checkPrepareProgress(ctx context.Context, started time.Time, maximum time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Since(started) > maximum {
		return fmt.Errorf("VCTK preparation exceeded wall-time ceiling")
	}
	return nil
}
