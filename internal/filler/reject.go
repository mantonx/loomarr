package filler

// Rejection, as a first-class vocabulary (§10 V51b).
//
// ⚠ **The three-way is the whole point.** Before this, rejection was binary (`removed_at`) and
// holding was binary (`held`), decided in different files by different jobs with no shared
// language: `LanguageJob` tombstoned, `Tagger` filed, `ScanDir` silently skipped. Adding a
// criterion meant editing three jobs and hoping they agreed. One enumeration every rule answers
// makes a new criterion a new rule, and makes "what happened to my clip?" answerable in one place.
//
// ⚠ **Not to be confused with `AutoSplitReject` (autosplit.go), and the two stay separate.** That
// one says why a split PROPOSAL was not auto-confirmed and is operator-facing PROSE; this one says
// why a CLIP was refused from the catalog and is a stable CODE the UI matches on. Different
// subject, different audience, different lifetime — merging them would force one of the two to
// give up the property that makes it useful. Hence `Reason*` here and `Reject*` there.

// Verdict is what a stage concludes about a clip.
type Verdict int

const (
	// VerdictContinue — nothing to say; the clip proceeds.
	VerdictContinue Verdict = iota
	// VerdictReject — a hard fault. Never asked of a human, because there is no answer a human
	// could give: a clip with no audio track is not a judgement call.
	VerdictReject
	// VerdictReview — we could not decide. A human sees it.
	VerdictReview
	// VerdictDefer — the stage MADE PROGRESS and is not finished. Not a refusal and not a
	// failure: no attempt is spent, the clip stays `running`, and it resumes next pass.
	//
	// ⚠ This is the deadline-deferral rule (see `deferYield`) made available to a rung that KNOWS
	// it is unfinished, rather than only to one the clock interrupted. It exists because a
	// per-pass budget over a per-reel job had no way to say "60 of 142 done, ask me again" — so a
	// budget that was meant to bound COST behaved as a ceiling on capability, and any reel larger
	// than the budget could never be completed at all (§10 V54).
	//
	// ⚠ A rung must only return this when it actually advanced something. Deferring on a pass that
	// achieved nothing is an infinite loop; the caller's own guard is `Looked > 0`.
	VerdictDefer
)

// RejectReason is a STABLE CODE for why a clip was refused.
//
// ⚠ A code, never prose. The frontend owns the wording (the §11 refusal-code precedent that
// `FitReason` already follows): a server sentence cannot be translated, cannot link to the setting
// that caused it, and freezes our copy into an API response. The measured detail travels alongside
// it in `ClipPipeline.RejectDetail` — "8.2s; floor is 10s" — which is what makes a reject arguable
// rather than an assertion.
type RejectReason string

const (
	// ReasonUnprobeable — ffprobe could not measure the file after retries. A file we cannot
	// measure is not a clip.
	ReasonUnprobeable RejectReason = "unprobeable"
	ReasonTooShort    RejectReason = "too_short"
	ReasonNoAudio     RejectReason = "no_audio"
	ReasonNoVideo     RejectReason = "no_video"
	// ReasonBlackContent / ReasonSilentContent are valid-stream files whose measured CONTENT is
	// near-total dead air. Distinct from no_video/no_audio: ffprobe sees both streams just fine.
	ReasonBlackContent  RejectReason = "black_content"
	ReasonSilentContent RejectReason = "silent_content"
	// ReasonUnplayable — the transcode stage failed after retries, so nothing downstream can
	// play it.
	ReasonUnplayable RejectReason = "unplayable"
	ReasonLanguage   RejectReason = "language"
	ReasonDuplicate  RejectReason = "duplicate"
	// ReasonScreening is an authority-bound rendered-child refusal. Details name only the failed
	// axis; restricted phrases and visual descriptions stay in the private evidence repository.
	ReasonScreening RejectReason = "screening"
	// ReasonSliver — a sub-floor split fragment with no speech (§10 V45): dropped, not reviewed.
	ReasonSliver RejectReason = "sliver"
	// ReasonUnidentified — every signal tier ran and grounded NOTHING: no era, audience, tag,
	// brand, transcript or on-screen text.
	//
	// ⚠ This is the only reason whose verdict is CONFIGURABLE (`filler.reject.unidentified`),
	// because "we could not identify it" is not the same claim as "it is not a commercial" — and a
	// wordless station ident is exactly that case, while §10 calls a silent advert some of the
	// best filler there is. It is also why the rejected list is not optional: an operator has to be
	// able to see what this caught and put it back.
	ReasonUnidentified RejectReason = "unidentified"
)

// Soft reports whether an operator may override this rejection.
//
// ⚠ A hard reject offers NO override, and that is a kindness rather than a restriction: a control
// that cannot work is worse than no control. Restoring a clip with no audio track puts silence in
// a break; restoring one ffprobe cannot read hands playout a file it will fail on. The soft cases
// are the judgement calls — we could not identify it, we thought we had it already, we heard the
// wrong language — and those are exactly the ones a person can settle.
func (r RejectReason) Soft() bool {
	switch r {
	case ReasonUnidentified, ReasonDuplicate, ReasonLanguage:
		return true
	default:
		return false
	}
}
