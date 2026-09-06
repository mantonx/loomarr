package fillerstructurewindowopenrouter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	structureSchemaName = "filler_temporal_structure_window"
	structureTitle      = "Loomarr filler structure window assessment"
	settlementTimeout   = 5 * time.Second
)

var errReservationHeld = errors.New("filler structure window OpenRouter reservation held by budget")

type Assessor struct {
	config Config
}

func New(config Config) (*Assessor, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Assessor{config: config}, nil
}

func (a *Assessor) Profile() fillerstructure.AssessorProfile { return a.config.Profile }

func (a *Assessor) AssessWindow(ctx context.Context, set fillerstructurewindow.MediaSet, media filler.StructureAssessmentWindowMedia) (fillerstructurewindow.RecordedAssessment, error) {
	video, err := readBoundWindowVideo(ctx, set, media)
	if err != nil {
		return fillerstructurewindow.RecordedAssessment{}, err
	}
	durationMS := media.Window.MediaEndMS - media.Window.MediaStartMS
	requestedAt := a.config.Now().UTC().Round(0)
	var reservationState fillerstructurewindow.CallReservationState
	callResult, callErr := openroutermedia.Call(ctx, a.config.Client, a.config.BaseURL, openroutermedia.Config{
		APIKey: a.config.APIKey, Model: a.config.Model, ResolvedModel: a.config.ResolvedModel,
		UpstreamProvider: a.config.UpstreamProvider, ProviderSlug: a.config.UpstreamProviderSlug,
		SchemaName: structureSchemaName, Schema: fillerstructurewindow.DirectVideoSchema(durationMS),
		SystemPrompt: fillerstructurewindow.DirectVideoSystemPrompt, Content: fillerstructurewindow.DirectVideoContent(durationMS),
		Videos:    []openroutermedia.Video{{MIMEType: "video/mp4", Base64: base64.StdEncoding.EncodeToString(video)}},
		MaxTokens: a.config.MaxTokens, ReservationNanoUSD: a.config.ReservationNanoUSD,
		DisableReasoning: a.config.DisableReasoning, EnableReasoning: a.config.EnableReasoning,
		Title: structureTitle,
		Reserve: func(requestSHA256 string) error {
			reservation, reservationErr := fillerstructurewindow.NewCallReservation(fillerstructurewindow.CallReservationInput{
				RequestSHA256: requestSHA256, MediaSet: set, WindowOrdinal: media.Window.Ordinal, Assessor: a.config.Profile,
				MetadataSnapshotSHA256: a.config.MetadataSnapshotSHA256,
				PromptSHA256:           fillerstructurewindow.DirectVideoPromptSHA256(durationMS),
				SchemaSHA256:           fillerstructurewindow.DirectVideoSchemaSHA256(durationMS),
				ExpectedResolvedModel:  a.config.ResolvedModel, UpstreamProvider: a.config.UpstreamProvider,
				UpstreamProviderSlug: a.config.UpstreamProviderSlug, RequestedNanoUSD: a.config.ReservationNanoUSD,
				MaximumChargeNanoUSD: a.config.MaximumChargeNanoUSD, RequestedAt: requestedAt,
			})
			if reservationErr != nil {
				return reservationErr
			}
			state, reserveErr := a.config.Ledger.Reserve(ctx, reservation)
			if reserveErr != nil {
				return reserveErr
			}
			reservationState = state
			switch state {
			case fillerstructurewindow.CallReservationAccepted:
				return nil
			case fillerstructurewindow.CallReservationHeldBudget:
				return errReservationHeld
			default:
				return fmt.Errorf("filler structure window OpenRouter ledger returned invalid reservation state %q", state)
			}
		},
	})
	if callResult.RequestSHA256 == "" || reservationState == "" {
		return fillerstructurewindow.RecordedAssessment{}, fmt.Errorf("filler structure window OpenRouter call acquired no durable reservation: %w", callErr)
	}
	recorded, err := a.recordAssessment(set, media.Window.Ordinal, a.config.Now().UTC().Round(0), reservationState, callResult, callErr)
	if err != nil {
		return fillerstructurewindow.RecordedAssessment{}, err
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settlementTimeout)
	defer cancel()
	if err := a.config.Ledger.Settle(settleCtx, recorded.Record); err != nil {
		return fillerstructurewindow.RecordedAssessment{}, fmt.Errorf("settle filler structure window OpenRouter assessment: %w", err)
	}
	return recorded, nil
}

func validateConfig(config Config) error {
	parsed, err := url.Parse(config.BaseURL)
	secure := err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == ""
	testOnly := config.AllowInsecureTestURL && err == nil && parsed.Scheme == "http" && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == ""
	if fillerstructure.ValidateAssessorProfile(config.Profile) != nil || config.Profile.Provider != "openrouter" ||
		!validMetadataSnapshotSHA256(config.MetadataSnapshotSHA256) ||
		config.Profile.Model != config.Model || config.Profile.PromptVersion != fillerstructurewindow.DirectVideoPromptVersion ||
		config.Profile.EvidenceContract != fillerstructurewindow.CallRecordContractVersion ||
		strings.TrimSpace(config.APIKey) == "" || !secure && !testOnly || strings.TrimRight(config.BaseURL, "/") != config.BaseURL ||
		strings.TrimSpace(config.ResolvedModel) != config.ResolvedModel || config.ResolvedModel == "" ||
		strings.TrimSpace(config.UpstreamProvider) != config.UpstreamProvider || config.UpstreamProvider == "" ||
		strings.TrimSpace(config.UpstreamProviderSlug) != config.UpstreamProviderSlug || config.UpstreamProviderSlug == "" ||
		config.ReservationNanoUSD <= 0 || config.MaximumChargeNanoUSD <= 0 || config.MaximumChargeNanoUSD > config.ReservationNanoUSD ||
		config.MaxTokens <= 0 || config.MaxTokens > 4096 || config.DisableReasoning && config.EnableReasoning ||
		config.Client == nil || config.Ledger == nil || config.Now == nil {
		return errors.New("filler structure window OpenRouter assessor configuration is invalid")
	}
	return nil
}

func validMetadataSnapshotSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func readBoundWindowVideo(ctx context.Context, set fillerstructurewindow.MediaSet, media filler.StructureAssessmentWindowMedia) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fillerstructurewindow.ValidateMediaSet(set) != nil || media.Window.Ordinal < 0 || media.Window.Ordinal >= len(set.Windows) ||
		!reflect.DeepEqual(media.Window, set.Plan.Windows[media.Window.Ordinal]) || !reflect.DeepEqual(media.Media, set.Windows[media.Window.Ordinal]) ||
		!filepath.IsAbs(media.FullPath) || filepath.Clean(media.FullPath) != media.FullPath || strings.ToLower(filepath.Ext(media.FullPath)) != ".mp4" {
		return nil, errors.New("filler structure window OpenRouter media identity is invalid")
	}
	pathInfo, err := os.Lstat(media.FullPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, errors.New("filler structure window OpenRouter video is not a retained regular file")
	}
	file, err := os.Open(media.FullPath)
	if err != nil {
		return nil, fmt.Errorf("open filler structure window video: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	maximumBytes := set.Plan.Profile.MaximumWindowBytes
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != media.Media.Media.Bytes || info.Size() > maximumBytes {
		return nil, errors.New("filler structure window OpenRouter video is unavailable or outside its byte ceiling")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) != info.Size() || int64(len(raw)) > maximumBytes {
		return nil, errors.New("read filler structure window OpenRouter video")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != media.Media.Media.SHA256 {
		return nil, errors.New("filler structure window OpenRouter video bytes drifted")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

var _ filler.CompleteWindowStructureAssessor = (*Assessor)(nil)
