package fillersafety

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxSherpaResultLineBytes = 16 << 10

type sherpaKeywordResult struct {
	StartTime  float64   `json:"start_time"`
	Keyword    string    `json:"keyword"`
	Timestamps []float64 `json:"timestamps"`
	Tokens     []string  `json:"tokens"`
}

func parseSherpaResults(raw []byte, durationMS int64, variants map[string][][]string) ([]proposedInterval, error) {
	if durationMS <= 0 || durationMS > maxSherpaSourceMS || len(raw) > maxSherpaStdoutBytes || len(variants) == 0 {
		return nil, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
	}
	intervals := make([]proposedInterval, 0)
	seen := make(map[proposedInterval]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), maxSherpaResultLineBytes)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" || len(intervals) >= maxProposedCandidates {
			return nil, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
		}
		var result sherpaKeywordResult
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
		}
		interval, err := validateSherpaResult(result, durationMS, variants)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[interval]; duplicate {
			continue
		}
		seen[interval] = struct{}{}
		intervals = append(intervals, interval)
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
	}
	slices.SortFunc(intervals, func(first, second proposedInterval) int {
		if first.StartMS != second.StartMS {
			return intCompare(first.StartMS, second.StartMS)
		}
		return intCompare(first.EndMS, second.EndMS)
	})
	return intervals, nil
}

func validateSherpaResult(result sherpaKeywordResult, durationMS int64, variants map[string][][]string) (proposedInterval, error) {
	allowed, known := variants[result.Keyword]
	if !known || !ValidPolicyRuleID(result.Keyword) || !finiteNonnegative(result.StartTime) || result.StartTime*1000 > float64(durationMS)+float64(sherpaResultFrameMS) || len(result.Timestamps) == 0 || len(result.Timestamps) > maxAcousticTokensPerVariant || len(result.Timestamps) != len(result.Tokens) || !matchesAcousticVariantLength(result.Tokens, allowed) {
		return proposedInterval{}, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
	}
	for index, timestamp := range result.Timestamps {
		if !finiteNonnegative(timestamp) || index > 0 && timestamp < result.Timestamps[index-1] {
			return proposedInterval{}, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
		}
	}
	startMS := int64(math.Round(result.Timestamps[0] * 1000))
	endMS := int64(math.Round(result.Timestamps[len(result.Timestamps)-1]*1000)) + sherpaResultFrameMS
	if startMS < 0 || startMS >= durationMS || endMS <= startMS || endMS > durationMS+sherpaResultFrameMS {
		return proposedInterval{}, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
	}
	if endMS > durationMS {
		endMS = durationMS
	}
	if endMS-startMS > maxProposedIntervalMS {
		return proposedInterval{}, fmt.Errorf("spoken-safety acoustic proposer output is invalid")
	}
	return proposedInterval{StartMS: startMS, EndMS: endMS}, nil
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func matchesAcousticVariantLength(tokens []string, variants [][]string) bool {
	for _, token := range tokens {
		if token == "" || len(token) > 128 || !utf8.ValidString(token) || strings.IndexFunc(token, unicode.IsControl) >= 0 {
			return false
		}
	}
	for _, variant := range variants {
		if len(tokens) == len(variant) {
			return true
		}
	}
	return false
}
