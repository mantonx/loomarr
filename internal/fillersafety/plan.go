package fillersafety

import (
	"path/filepath"
	"strings"
)

// SourceRequest combines immutable authority with the machine-local source
// path. The path is intentionally excluded from authority hashing.
type SourceRequest struct {
	Authority SourceAuthority
	Path      string `json:"-"`
}

// Span is one complete source-relative media interval.
type Span struct {
	StartMS int64 `json:"startMs"`
	EndMS   int64 `json:"endMs"`
}

// CompleteMediaPlan binds the authority digest to the full audio and video
// spans that every adapter must preserve.
type CompleteMediaPlan struct {
	AuthoritySHA256 string `json:"authoritySha256"`
	SourceSHA256    string `json:"sourceSha256"`
	SourceBytes     int64  `json:"sourceBytes"`
	SourcePath      string `json:"-"`
	Audio           Span   `json:"audio"`
	Video           Span   `json:"video"`
}

// PlanCompleteMedia validates source authority before media is opened or
// inference spend is reserved.
func PlanCompleteMedia(request SourceRequest) (CompleteMediaPlan, error) {
	if err := validateSourceAuthority(request.Authority); err != nil {
		return CompleteMediaPlan{}, err
	}
	clean := filepath.Clean(request.Path)
	if strings.TrimSpace(request.Path) == "" || !filepath.IsAbs(clean) || clean != request.Path {
		return CompleteMediaPlan{}, &AuthorityError{Code: AuthoritySourceInvalid}
	}
	authoritySHA256, err := sourceAuthoritySHA256(request.Authority)
	if err != nil {
		return CompleteMediaPlan{}, &AuthorityError{Code: AuthorityIdentityInvalid}
	}
	complete := Span{StartMS: 0, EndMS: request.Authority.DurationMS}
	return CompleteMediaPlan{
		AuthoritySHA256: authoritySHA256,
		SourceSHA256:    request.Authority.SourceSHA256,
		SourceBytes:     request.Authority.SourceBytes,
		SourcePath:      request.Path,
		Audio:           complete,
		Video:           complete,
	}, nil
}
