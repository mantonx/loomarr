package fillerreview

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func bindTemporalStructureWindowCertificationCases(manifest TemporalStructureWindowSetManifest, suite fillerstructurewindowcert.Suite, first, second TemporalStructureWindowFamilyResult) ([]fillerstructurewindowcert.CaseResult, error) {
	if len(manifest.Cases) != TemporalStructureWindowCorpusCases || len(suite.Cases) != len(manifest.Cases) ||
		len(first.Cases) != len(manifest.Cases) || len(second.Cases) != len(manifest.Cases) {
		return nil, errors.New("window certification inputs do not have complete shared coverage")
	}
	suiteByMedia := make(map[string]fillerstructurewindowcert.Case, len(suite.Cases))
	for _, item := range suite.Cases {
		suiteByMedia[item.MediaSet.SHA256] = item
	}
	results := make([]fillerstructurewindowcert.CaseResult, 0, len(manifest.Cases))
	seenSuiteCases := make(map[string]struct{}, len(manifest.Cases))
	for index, public := range manifest.Cases {
		private, found := suiteByMedia[public.MediaSet.SHA256]
		if !found || !reflect.DeepEqual(private.MediaSet, public.MediaSet) {
			return nil, fmt.Errorf("window certification public case %d has no exact private-suite join", index)
		}
		if _, duplicate := seenSuiteCases[private.ID]; duplicate {
			return nil, fmt.Errorf("window certification private case %q is reused", private.ID)
		}
		if first.Cases[index].Alias != public.Alias || second.Cases[index].Alias != public.Alias ||
			!reflect.DeepEqual(first.Cases[index].Evidence.Stitch.MediaSet, public.MediaSet) ||
			!reflect.DeepEqual(second.Cases[index].Evidence.Stitch.MediaSet, public.MediaSet) {
			return nil, fmt.Errorf("window certification family case %d drifted from public input", index)
		}
		results = append(results, fillerstructurewindowcert.CaseResult{
			CaseID: private.ID, Stitches: []fillerstructurewindow.StitchResult{first.Cases[index].Evidence.Stitch, second.Cases[index].Evidence.Stitch},
		})
		seenSuiteCases[private.ID] = struct{}{}
	}
	return results, nil
}
