package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const DirectVideoPromptVersion = "filler-temporal-structure-window-direct-video-v2"

const DirectVideoSystemPrompt = `Segment one complete identity-blind video window. Judge the supplied window's actual item boundaries, not whether its topic resembles an advertisement.

Return segments in playback order covering every millisecond from 0 through the supplied window duration. Timestamps are relative to this supplied window. Each segment supplies its exclusive endMs: the first starts at 0, every later segment starts at the preceding endMs, and the final endMs equals the supplied duration. Keep one independently bounded, self-contained item with one cohesive communicative purpose as one segment even when it contains many shots, scenes, title cards, or credits. Split whenever separately bounded items have been joined. For programme material with an inserted filler item, return the programme before the insertion, every inserted item, and the resumed programme as separate intervals. A scene change inside one continuing programme is not an item boundary.

Give every segment its own role: commercial, promo, bumper, psa, station_id, trailer, interstitial, programme_fragment, non_filler, ambiguous, or unusable. Use programme_fragment for material whose beginning or ending depends on a larger programme, including an ordinary scene, programme opening, sustained performance, credits/title fragment, or interior cut. Never copy one role across the window merely because the source appears commercial. Use ambiguous rather than guessing. Use unusable only when corruption or degradation prevents reliable temporal assessment; age, poor image quality, or recording overlays alone are insufficient.

Each segment must cite up to eight sorted, unique decisive timestamps inside its own interval and give a concise reason for its boundary and role. Do not infer source identity, mention filenames or aliases, or claim content suitability. Return only the requested JSON.`

func DirectVideoContent(durationMS int64) string {
	return fmt.Sprintf("Complete supplied window duration: %d milliseconds. Inspect the complete supplied window and return the closed temporal-structure assessment.", durationMS)
}

func DirectVideoSchema(durationMS int64) map[string]any {
	return fillerstructure.DirectVideoSchema(durationMS)
}

func DirectVideoPromptSHA256(durationMS int64) string {
	raw, err := json.Marshal(struct {
		Version string `json:"version"`
		System  string `json:"system"`
		Content string `json:"content"`
	}{DirectVideoPromptVersion, DirectVideoSystemPrompt, DirectVideoContent(durationMS)})
	if err != nil {
		return ""
	}
	return directVideoDigest(raw)
}

func DirectVideoSchemaSHA256(durationMS int64) string {
	raw, err := json.Marshal(DirectVideoSchema(durationMS))
	if err != nil {
		return ""
	}
	return directVideoDigest(raw)
}

func directVideoDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
