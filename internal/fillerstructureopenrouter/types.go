// Package fillerstructureopenrouter adapts the bounded OpenRouter media transport to the
// provider-neutral complete-timeline assessor port.
package fillerstructureopenrouter

import (
	"context"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
)

const MaximumVideoBytes int64 = fillerstructuremedia.MaximumVideoBytes

// Ledger must commit Reserve before returning and atomically close that request in Settle. A
// process crash between those calls intentionally leaves a discoverable open reservation.
type Ledger interface {
	Reserve(context.Context, fillerstructure.AssessmentReservation) (fillerstructure.AssessmentReservationState, error)
	Settle(context.Context, fillerstructure.AssessmentRecord) error
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
