package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowopenrouter"
	"github.com/loomarr/loomarr/internal/store"
)

type productionStructureWindowLedger struct {
	store  store.FillerStructureAssessmentStore
	budget store.InferenceBudget
}

func (l productionStructureWindowLedger) Reserve(ctx context.Context, reservation fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error) {
	return l.store.ReserveStructureWindowCall(ctx, reservation, l.budget)
}

func (l productionStructureWindowLedger) Settle(ctx context.Context, record fillerstructurewindow.CallRecord) error {
	return l.store.SettleStructureWindowCall(ctx, record)
}

func buildCertifiedWindowStructureRuntime(st store.Store, set resolved, layout filler.Layout,
	authority *fillerstructurewindow.MaterializationAuthority,
	deployment *fillerstructurewindowopenrouter.Deployment,
) (filler.CompleteTimelineStructureDecisioner, error) {
	if authority == nil || deployment == nil {
		return nil, nil
	}
	if layout.ClipDir() == "" {
		return nil, errors.New("certified long-reel runtime requires filler storage")
	}
	apiKey := openRouterStructureAPIKey(set)
	if apiKey == "" {
		return nil, errors.New("certified long-reel runtime requires the OpenRouter provider secret")
	}
	root := filepath.Join(layout.ClipDir(), ".loomarr", "structure-window")
	return fillerstructurewindowopenrouter.NewCertifiedRuntime(fillerstructurewindowopenrouter.CertifiedRuntimeConfig{
		Authority: *authority, Deployment: *deployment, APIKey: apiKey,
		SourceRoot: layout.ClipDir(), MediaRoot: filepath.Join(root, "media"), EvidenceRoot: filepath.Join(root, "evidence"),
		FFmpegPath: resolveTool(set.str("playout.ffmpeg_path"), "ffmpeg"),
		Ledger: productionStructureWindowLedger{store: st, budget: store.InferenceBudget{
			PerClipNanoUSD: deployment.PerSourceBudgetNanoUSD, PerDayNanoUSD: deployment.PerDayBudgetNanoUSD,
		}},
	})
}

func openRouterStructureAPIKey(set resolved) string {
	if value, err := set.svc.LoadRaw(setLLMAPIKey + ".openrouter"); err == nil && value != "" {
		return value
	}
	selection := resolveSelection(set)
	if selection.Provider == "openrouter" {
		return selection.APIKey
	}
	return ""
}
