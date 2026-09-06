// Package fillerstructurewindowopenrouter adapts the bounded OpenRouter media transport to one
// complete planned-window assessment call.
package fillerstructurewindowopenrouter

import (
	"context"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type Ledger interface {
	Reserve(context.Context, fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error)
	Settle(context.Context, fillerstructurewindow.CallRecord) error
}

type Config struct {
	Profile                fillerstructure.AssessorProfile
	MetadataSnapshotSHA256 string
	APIKey                 string
	BaseURL                string
	Model                  string
	ResolvedModel          string
	UpstreamProvider       string
	UpstreamProviderSlug   string
	ReservationNanoUSD     int64
	MaximumChargeNanoUSD   int64
	MaxTokens              int
	DisableReasoning       bool
	EnableReasoning        bool
	AllowInsecureTestURL   bool
	Client                 *http.Client
	Ledger                 Ledger
	Now                    func() time.Time
}
