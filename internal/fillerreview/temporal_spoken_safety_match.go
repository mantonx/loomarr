package fillerreview

import (
	"sort"
	"strings"
	"unicode"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type temporalSpokenSafetyTimedWord struct {
	text           string
	startMS, endMS int64
	segment        int
}

func temporalSpokenSafetyWords(value string) []string {
	value = cases.Fold().String(norm.NFKC.String(value))
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
			current = current[:0]
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func matchTemporalSpokenSafety(policy TemporalSpokenSafetyPolicy, segments []fillerbakeoff.TranscriptSegment) []TemporalSpokenSafetyMatch {
	var timed []temporalSpokenSafetyTimedWord
	for index, segment := range segments {
		for _, word := range temporalSpokenSafetyWords(segment.Text) {
			timed = append(timed, temporalSpokenSafetyTimedWord{text: word, startMS: segment.StartMS, endMS: segment.EndMS, segment: index})
		}
	}
	seen := map[TemporalSpokenSafetyMatch]struct{}{}
	var matches []TemporalSpokenSafetyMatch
	for _, rule := range policy.Rules {
		for _, variant := range rule.Variants {
			words := temporalSpokenSafetyWords(variant)
			for start := 0; start+len(words) <= len(timed); start++ {
				matched := true
				for offset, word := range words {
					wordMatches := timed[start+offset].text == word
					if rule.MatchMode == TemporalSpokenSafetyModeTokenPrefix && offset == len(words)-1 {
						wordMatches = strings.HasPrefix(timed[start+offset].text, word)
					}
					if !wordMatches {
						matched = false
						break
					}
					if offset > 0 && timed[start+offset-1].segment != timed[start+offset].segment && timed[start+offset].startMS-timed[start+offset-1].endMS > policy.MaximumInterSegmentGapMS {
						matched = false
						break
					}
				}
				if !matched {
					continue
				}
				match := TemporalSpokenSafetyMatch{RuleID: rule.ID, Class: rule.Class, StartMS: timed[start].startMS, EndMS: timed[start+len(words)-1].endMS}
				if _, duplicate := seen[match]; !duplicate {
					seen[match] = struct{}{}
					matches = append(matches, match)
				}
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].StartMS != matches[j].StartMS {
			return matches[i].StartMS < matches[j].StartMS
		}
		if matches[i].EndMS != matches[j].EndMS {
			return matches[i].EndMS < matches[j].EndMS
		}
		if matches[i].Class != matches[j].Class {
			return matches[i].Class < matches[j].Class
		}
		return matches[i].RuleID < matches[j].RuleID
	})
	return matches
}
