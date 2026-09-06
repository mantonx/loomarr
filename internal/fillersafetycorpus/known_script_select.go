package fillersafetycorpus

import (
	"encoding/hex"
	"slices"
	"strings"
)

type selectedKnownScript struct {
	member       KnownScriptMember
	caseID       string
	sourceFamily string
}

func selectKnownScript(seed []byte, members []KnownScriptMember) []selectedKnownScript {
	result := make([]selectedKnownScript, 0, len(members))
	for _, member := range members {
		result = append(result, selectedKnownScript{
			member: member,
			caseID: "known-case-" + hex.EncodeToString(keyedRank(seed, "known-case", strings.Join([]string{
				member.ParticipantID, member.SessionID, member.TakeID,
			}, "\x00")))[:24],
			sourceFamily: "known-family-" + hex.EncodeToString(keyedRank(seed, "known-family", member.ParticipantID))[:24],
		})
	}
	slices.SortFunc(result, func(a, b selectedKnownScript) int { return strings.Compare(a.caseID, b.caseID) })
	return result
}
