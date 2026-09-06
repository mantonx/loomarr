package fillersafetycert

import (
	"fmt"
	"path/filepath"
	"slices"
)

// RuntimeAuthority is the exact certification input accepted by a production evidence runtime.
// The report remains non-authorizing for admission; Passed proves only that this cascade may
// produce certified spoken-safety evidence under the authority it names.
type RuntimeAuthority struct {
	authority       Authority
	authoritySHA256 string
	report          Report
}

func (r RuntimeAuthority) Authority() Authority    { return cloneRuntimeAuthority(r.authority) }
func (r RuntimeAuthority) AuthoritySHA256() string { return r.authoritySHA256 }
func (r RuntimeAuthority) Report() Report          { return cloneRuntimeReport(r.report) }

// LoadRuntimeAuthority reopens and validates the exact private authority and its reproduced
// passing report. Hashing the original bytes prevents a semantically equivalent rewrite from
// silently becoming the certification named by production runs.
func LoadRuntimeAuthority(authorityPath, reportPath string) (RuntimeAuthority, error) {
	if authorityPath == "" || reportPath == "" {
		return RuntimeAuthority{}, fmt.Errorf("spoken-safety runtime authority and report are required")
	}
	authorityAbs, err := filepath.Abs(authorityPath)
	if err != nil {
		return RuntimeAuthority{}, fmt.Errorf("resolve spoken-safety runtime authority: %w", err)
	}
	reportAbs, err := filepath.Abs(reportPath)
	if err != nil || authorityAbs == reportAbs {
		return RuntimeAuthority{}, fmt.Errorf("spoken-safety runtime authority and report must be distinct")
	}
	authority, authorityRaw, err := readPrivateJSON[Authority](authorityAbs)
	if err != nil {
		return RuntimeAuthority{}, fmt.Errorf("read spoken-safety runtime authority: %w", err)
	}
	if err := validateAuthority(authority); err != nil {
		return RuntimeAuthority{}, fmt.Errorf("validate spoken-safety runtime authority: %w", err)
	}
	report, _, err := readPrivateJSON[Report](reportAbs)
	if err != nil {
		return RuntimeAuthority{}, fmt.Errorf("read spoken-safety runtime report: %w", err)
	}
	if err := validateReport(report); err != nil {
		return RuntimeAuthority{}, fmt.Errorf("validate spoken-safety runtime report: %w", err)
	}
	authoritySHA256 := hashBytes(authorityRaw)
	if authority.ChallengeKind != ChallengeCertification || report.CertificationStatus != StatusPassed ||
		report.ChallengeKind != authority.ChallengeKind || report.AuthoritySHA256 != authoritySHA256 ||
		report.PolicySHA256 != authority.PolicySHA256 || report.ProposerSHA256 != authority.ProposerSHA256 ||
		report.Implementation != authority.Implementation || report.ScoredAt.Before(authority.AuthoredAt) {
		return RuntimeAuthority{}, fmt.Errorf("spoken-safety runtime report does not authorize the supplied evidence cascade")
	}
	return RuntimeAuthority{
		authority: cloneRuntimeAuthority(authority), authoritySHA256: authoritySHA256,
		report: cloneRuntimeReport(report),
	}, nil
}

func cloneRuntimeAuthority(authority Authority) Authority {
	cloned := authority
	cloned.AudioRoute.Modalities = slices.Clone(authority.AudioRoute.Modalities)
	cloned.VideoRoute.Modalities = slices.Clone(authority.VideoRoute.Modalities)
	cloned.Cases = slices.Clone(authority.Cases)
	for index := range cloned.Cases {
		cloned.Cases[index].Slices = slices.Clone(authority.Cases[index].Slices)
		cloned.Cases[index].PositiveIntervals = slices.Clone(authority.Cases[index].PositiveIntervals)
		cloned.Cases[index].Reviewers = slices.Clone(authority.Cases[index].Reviewers)
	}
	return cloned
}

func cloneRuntimeReport(report Report) Report {
	cloned := report
	cloned.CleanSlices = slices.Clone(report.CleanSlices)
	cloned.Cases = slices.Clone(report.Cases)
	return cloned
}
