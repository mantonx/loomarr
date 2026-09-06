package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/store"
)

type fillerScreeningInput struct {
	Hash string `query:"hash" minLength:"64" maxLength:"64" pattern:"^[0-9a-f]{64}$" doc:"Exact catalog content hash"`
}

type FillerScreeningAxisDTO struct {
	Axis           string `json:"axis" enum:"visual_safety,spoken_safety,written_safety,rights,playback_integrity"`
	Outcome        string `json:"outcome" enum:"pass,reject,hold"`
	ReasonCode     string `json:"reasonCode"`
	EvidenceSHA256 string `json:"evidenceSha256" doc:"Immutable closed axis-record identity; never raw provider evidence"`
}

type FillerAirworthinessTriggerDTO struct {
	ObservationID  string `json:"observationId"`
	Axis           string `json:"axis" enum:"visual,spoken,written"`
	Flag           string `json:"flag" enum:"adult_nudity,age_ambiguous,alcohol,animal_harm_or_death,commercial_or_brand,drug,explicit_sexual_language,frightening_or_disturbing,gambling,graphic_violence_or_gore,hate_or_extremist_symbol,hateful_targeting,human_death_or_corpse,minor_nudity_or_sexual_risk,minor_present,non_graphic_violence,profanity,regulated_product_promotion,religious_suffering,self_harm_or_suicide,severe_injury_or_medical,sexual_activity_or_presentation,slur_or_degrading_language,threat,tobacco_or_nicotine,war_or_military,weapon_depiction"`
	Severity       string `json:"severity" enum:"low,moderate,high"`
	Context        string `json:"context" enum:"presence,depiction,promotion,instruction"`
	StartMs        int64  `json:"startMs"`
	EndMs          int64  `json:"endMs"`
	Effect         string `json:"effect" enum:"reject,review"`
	EvidenceSHA256 string `json:"evidenceSha256"`
}

type FillerAirworthinessDTO struct {
	SchemaVersion     int                             `json:"schemaVersion"`
	ContractVersion   string                          `json:"contractVersion"`
	SubjectSHA256     string                          `json:"subjectSha256"`
	Profile           string                          `json:"profile" enum:"all_ages,general_audience,restricted_archive"`
	PolicyVersion     string                          `json:"policyVersion"`
	VocabularyVersion string                          `json:"vocabularyVersion"`
	Verdict           string                          `json:"verdict" enum:"pass,reject,hold"`
	ReasonCodes       []string                        `json:"reasonCodes" enum:"airworthiness_evidence_satisfied,airworthiness_prohibited_observation,airworthiness_observation_requires_review,airworthiness_coverage_incomplete,airworthiness_certification_incomplete,airworthiness_evidence_invalid,airworthiness_restricted_archive_not_playout"`
	ObservedFlags     []string                        `json:"observedFlags" enum:"adult_nudity,age_ambiguous,alcohol,animal_harm_or_death,commercial_or_brand,drug,explicit_sexual_language,frightening_or_disturbing,gambling,graphic_violence_or_gore,hate_or_extremist_symbol,hateful_targeting,human_death_or_corpse,minor_nudity_or_sexual_risk,minor_present,non_graphic_violence,profanity,regulated_product_promotion,religious_suffering,self_harm_or_suicide,severe_injury_or_medical,sexual_activity_or_presentation,slur_or_degrading_language,threat,tobacco_or_nicotine,war_or_military,weapon_depiction"`
	HeldAxes          []string                        `json:"heldAxes" enum:"visual,spoken,written"`
	Triggers          []FillerAirworthinessTriggerDTO `json:"triggers"`
	EvidenceSHA256s   []string                        `json:"evidenceSha256s"`
	AuthoritySHA256   string                          `json:"authoritySha256"`
	DecisionSHA256    string                          `json:"decisionSha256"`
}

type FillerRightsReviewDTO struct {
	SourceID           string                `json:"sourceId"`
	AcquisitionID      string                `json:"acquisitionId"`
	SourceMasterSHA256 string                `json:"sourceMasterSha256"`
	PolicySHA256       string                `json:"policySha256"`
	Use                string                `json:"use"`
	CanRecord          bool                  `json:"canRecord"`
	CurrentGrant       *fillerRightsGrantDTO `json:"currentGrant,omitempty"`
}

type FillerScreeningDTO struct {
	State          string                   `json:"state" enum:"not_screened,available,unavailable"`
	ReasonCode     string                   `json:"reasonCode,omitempty"`
	ClipHash       string                   `json:"clipHash"`
	SubjectSHA256  string                   `json:"subjectSha256,omitempty"`
	EvidenceSHA256 string                   `json:"evidenceSha256,omitempty"`
	Outcome        string                   `json:"outcome,omitempty" enum:"pass,reject,hold"`
	Axes           []FillerScreeningAxisDTO `json:"axes"`
	RightsReview   *FillerRightsReviewDTO   `json:"rightsReview,omitempty"`
	Airworthiness  *FillerAirworthinessDTO  `json:"airworthiness,omitempty"`
	AssessedAt     string                   `json:"assessedAt,omitempty" format:"date-time"`
}

type fillerScreeningOutput struct{ Body FillerScreeningDTO }

func (s *Server) registerFillerScreening(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "get-filler-screening", Method: http.MethodGet, Path: "/v1/filler/screening",
		Summary:     "Exact clip's five-axis screening state",
		Description: "Admin-only browser-safe projection of visual, spoken, written, rights, playback-integrity, and Airworthiness evidence for one exact catalog hash. Raw provider evidence, text, paths, and private rights documents never cross this boundary.",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.getFillerScreening)
}

func (s *Server) getFillerScreening(ctx context.Context, input *fillerScreeningInput) (*fillerScreeningOutput, error) {
	if s.store == nil || s.fillerScreening == nil || s.fillerLayout.ClipDir() == "" {
		return nil, huma.Error501NotImplemented("filler screening evidence is not configured")
	}
	clip, err := s.store.GetClip(ctx, input.Hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, huma.Error404NotFound("filler clip not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("read filler clip", err)
	}
	mediaPath, ok := safeFillerPath(s.fillerLayout.ClipDir(), clip.Path)
	if !ok {
		return nil, huma.Error404NotFound("filler clip not found")
	}
	summary, readErr := s.fillerScreening.ReadSegmentScreeningSummary(ctx, clip.Hash, mediaPath)
	if readErr != nil && summary.State == "" {
		return nil, huma.Error500InternalServerError("read filler screening evidence", readErr)
	}
	if readErr != nil {
		s.log.Warn("filler screening evidence is unavailable", "clip", clip.Hash, "reason", summary.ReasonCode, "err", readErr)
	}
	if filler.ValidateSegmentScreeningSummary(summary) != nil || summary.ClipHash != clip.Hash {
		return nil, huma.Error500InternalServerError("project filler screening evidence")
	}
	dto := fillerScreeningDTO(summary)
	if summary.RightsScope != nil {
		dto.RightsReview = &FillerRightsReviewDTO{
			SourceID: summary.RightsScope.SourceID, AcquisitionID: summary.RightsScope.AcquisitionID,
			SourceMasterSHA256: summary.RightsScope.SourceMasterSHA256,
			PolicySHA256:       summary.RightsScope.PolicySHA256, Use: summary.RightsScope.Use,
			CanRecord: s.fillerRights != nil,
		}
		if s.fillerRights != nil {
			grant, found, grantErr := s.fillerRights.CurrentGrant(ctx, *summary.RightsScope)
			if grantErr != nil {
				return nil, huma.Error500InternalServerError("read current filler rights authority", grantErr)
			}
			if found {
				current := fillerRightsGrantDTOFrom(grant)
				dto.RightsReview.CurrentGrant = &current
			}
		}
	}
	return &fillerScreeningOutput{Body: dto}, nil
}

func fillerScreeningDTO(summary filler.SegmentScreeningSummary) FillerScreeningDTO {
	dto := FillerScreeningDTO{
		State: string(summary.State), ReasonCode: summary.ReasonCode, ClipHash: summary.ClipHash,
		SubjectSHA256: summary.SubjectSHA256, EvidenceSHA256: summary.EvidenceSHA256,
		Outcome: string(summary.Outcome), Axes: make([]FillerScreeningAxisDTO, 0, len(summary.Axes)),
	}
	for _, axis := range summary.Axes {
		dto.Axes = append(dto.Axes, FillerScreeningAxisDTO{
			Axis: string(axis.Axis), Outcome: string(axis.Outcome), ReasonCode: axis.ReasonCode,
			EvidenceSHA256: axis.EvidenceSHA256,
		})
	}
	if !summary.AssessedAt.IsZero() {
		dto.AssessedAt = summary.AssessedAt.UTC().Format(time.RFC3339)
	}
	if summary.Airworthiness != nil {
		dto.Airworthiness = fillerAirworthinessDTO(*summary.Airworthiness)
	}
	return dto
}

func fillerAirworthinessDTO(decision fillerairworthiness.Decision) *FillerAirworthinessDTO {
	dto := &FillerAirworthinessDTO{
		SchemaVersion: decision.SchemaVersion, ContractVersion: decision.ContractVersion,
		SubjectSHA256: decision.SubjectSHA256, Profile: string(decision.Profile),
		PolicyVersion: decision.PolicyVersion, VocabularyVersion: decision.VocabularyVersion,
		Verdict:     string(decision.Verdict),
		ReasonCodes: stringsFrom(decision.ReasonCodes), ObservedFlags: stringsFrom(decision.ObservedFlags),
		HeldAxes: stringsFrom(decision.HeldAxes), EvidenceSHA256s: stringsFrom(decision.EvidenceSHA256s),
		Triggers:        make([]FillerAirworthinessTriggerDTO, 0, len(decision.Triggers)),
		AuthoritySHA256: decision.AuthoritySHA256, DecisionSHA256: decision.SHA256,
	}
	for _, trigger := range decision.Triggers {
		dto.Triggers = append(dto.Triggers, FillerAirworthinessTriggerDTO{
			ObservationID: trigger.ObservationID,
			Axis:          string(trigger.Axis), Flag: string(trigger.Flag), Severity: string(trigger.Severity),
			Context: string(trigger.Context), StartMs: trigger.StartMS, EndMs: trigger.EndMS,
			Effect: string(trigger.Effect), EvidenceSHA256: trigger.EvidenceSHA256,
		})
	}
	return dto
}

func stringsFrom[T ~string](values []T) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = string(value)
	}
	return out
}
