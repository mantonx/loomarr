package fillervisualsafety

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
)

type visualCorpusBoardCase struct {
	Alias     string `json:"alias"`
	Asset     string `json:"asset"`
	Rights    string `json:"rights"`
	MediaType string `json:"mediaType"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type visualCorpusBoardData struct {
	AuthoritySHA256 string
	AuthorityJSON   template.JS
	PolicyMatches   []CandidateBlindReviewPolicyMatch
	CasesJSON       template.JS
}

func renderVisualCorpusReviewBoard(authoritySHA256 string, policy CandidateBlindReviewPolicy, cases []VisualCorpusDraftReviewCase) ([]byte, error) {
	if !validDigest(authoritySHA256) || len(cases) == 0 || len(policy.PolicyMatches) == 0 {
		return nil, errors.New("visual corpus review board input is invalid")
	}
	boardCases := make([]visualCorpusBoardCase, len(cases))
	for index, candidate := range cases {
		boardCases[index] = visualCorpusBoardCase{
			Alias: candidate.Alias, Asset: candidate.Asset.RelativePath,
			Rights: candidate.RightsEvidence.RelativePath, MediaType: candidate.MediaType,
			Width: candidate.Width, Height: candidate.Height,
		}
	}
	caseBytes, err := json.Marshal(boardCases)
	if err != nil {
		return nil, errors.New("encode visual corpus review board cases")
	}
	authorityBytes, err := json.Marshal(authoritySHA256)
	if err != nil {
		return nil, errors.New("encode visual corpus review board authority")
	}
	value := visualCorpusBoardData{
		AuthoritySHA256: authoritySHA256, AuthorityJSON: template.JS(authorityBytes), //nolint:gosec // encoding/json escapes the fixed validated digest
		PolicyMatches: policy.PolicyMatches,
		CasesJSON:     template.JS(caseBytes), //nolint:gosec // encoding/json escapes HTML-significant candidate text
	}
	var output bytes.Buffer
	if err := visualCorpusBoardTemplate.Execute(&output, value); err != nil {
		return nil, errors.New("render visual corpus review board")
	}
	return output.Bytes(), nil
}

var visualCorpusBoardTemplate = template.Must(template.New("visual-corpus-review").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Loomarr visual corpus review</title>
<style>
:root { color-scheme: dark; font-family: ui-sans-serif, system-ui, sans-serif; background: #111318; color: #f5f7fa; }
body { margin: 0; min-height: 100vh; display: grid; grid-template-rows: auto 1fr auto; }
header, footer { padding: 14px 20px; background: #1a1e26; border-color: #343a46; }
header { border-bottom: 1px solid #343a46; }
footer { border-top: 1px solid #343a46; display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
main { display: grid; grid-template-columns: minmax(280px, 1fr) minmax(260px, 380px); gap: 18px; padding: 18px; min-height: 0; }
.image-shell { display: grid; place-items: center; min-height: 55vh; background: #08090c; border-radius: 10px; overflow: hidden; }
img { max-width: 100%; max-height: 72vh; object-fit: contain; }
aside { overflow: auto; }
h1 { margin: 0 0 6px; font-size: 18px; }
h2 { font-size: 14px; margin-top: 18px; }
p, li { line-height: 1.45; }
.muted { color: #aeb7c7; font-size: 13px; }
.alias { font-family: ui-monospace, monospace; }
.actions { display: grid; grid-template-columns: repeat(2, minmax(120px, 1fr)); gap: 8px; }
button, a.button { border: 1px solid #515b6d; border-radius: 7px; background: #252b36; color: #f5f7fa; padding: 10px 12px; cursor: pointer; text-decoration: none; text-align: center; }
button:hover, button:focus, a.button:hover, a.button:focus { border-color: #9eb6dc; outline: none; }
button[data-value="positive"] { border-color: #d38888; }
button[data-value="clean"] { border-color: #75b995; }
.selected { box-shadow: 0 0 0 2px #f3c969 inset; }
code { overflow-wrap: anywhere; }
@media (max-width: 760px) { main { grid-template-columns: 1fr; } .image-shell { min-height: 42vh; } }
</style>
</head>
<body>
<header>
  <h1>Loomarr visual corpus review</h1>
  <div id="progress" class="muted"></div>
</header>
<main>
  <section class="image-shell"><img id="candidate" alt="Candidate source work"></section>
  <aside>
    <div id="alias" class="alias"></div>
    <p class="muted">Candidate nomination and all model outputs are withheld. Decide only from the exact policy and complete source work.</p>
    <h2>Policy</h2>
    <ul>{{range .PolicyMatches}}<li><code>{{.ID}}</code>: {{.Definition}}</li>{{end}}</ul>
    <h2>Decision</h2>
    <div class="actions">
      <button data-value="positive">P · Positive</button>
      <button data-value="clean">C · Clean</button>
      <button data-value="uncertain">U · Uncertain</button>
      <button data-value="exclude">X · Exclude</button>
    </div>
    <h2>Bound evidence</h2>
    <a id="rights" class="button" target="_blank" rel="noopener">Open rights review</a>
    <p class="muted">Uncertain and excluded cases never enter either certification denominator.</p>
  </aside>
</main>
<footer>
  <button id="previous">← Previous</button>
  <button id="next">Next →</button>
  <button id="save">S · Download decisions</button>
  <span class="muted">Authority <code>{{.AuthoritySHA256}}</code></span>
</footer>
<script>
"use strict";
const authority = {{.AuthorityJSON}};
const cases = {{.CasesJSON}};
const decisions = Object.create(null);
const storageKey = "loomarr-visual-corpus-review:" + authority;
let index = 0;
const image = document.getElementById("candidate");
const alias = document.getElementById("alias");
const rights = document.getElementById("rights");
const progress = document.getElementById("progress");
const buttons = [...document.querySelectorAll("button[data-value]")];
function show() {
  const item = cases[index];
  image.src = item.asset;
  image.width = item.width;
  image.height = item.height;
  alias.textContent = item.alias;
  rights.href = item.rights;
  const completed = Object.keys(decisions).length;
  progress.textContent = ` + "`" + `Case ${index + 1} of ${cases.length} · ${completed} decided` + "`" + `;
  for (const button of buttons) button.classList.toggle("selected", decisions[item.alias] === button.dataset.value);
}
function decide(value) {
  decisions[cases[index].alias] = value;
  persist();
  if (index < cases.length - 1) index++;
  show();
}
function persist() {
  try { localStorage.setItem(storageKey, JSON.stringify(decisions)); } catch (_) { /* private-file storage may be unavailable */ }
}
function restore() {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) || "{}");
    const valid = new Set(["positive", "clean", "uncertain", "exclude"]);
    for (const item of cases) if (valid.has(saved[item.alias])) decisions[item.alias] = saved[item.alias];
  } catch (_) { /* start empty when storage is unavailable or damaged */ }
}
function move(delta) { index = Math.max(0, Math.min(cases.length - 1, index + delta)); show(); }
function save() {
  const ordered = cases.filter(item => decisions[item.alias]).map(item => ({alias: item.alias, decision: decisions[item.alias]}));
  const payload = {schemaVersion: 1, kind: "loomarr-visual-corpus-review-decisions-v1", authoritySha256: authority,
    decidedAt: new Date().toISOString(), complete: ordered.length === cases.length, decisions: ordered};
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob([JSON.stringify(payload, null, 2) + "\n"], {type: "application/json"}));
  link.download = "visual-corpus-review-decisions.json";
  link.click();
  URL.revokeObjectURL(link.href);
}
for (const button of buttons) button.addEventListener("click", () => decide(button.dataset.value));
document.getElementById("previous").addEventListener("click", () => move(-1));
document.getElementById("next").addEventListener("click", () => move(1));
document.getElementById("save").addEventListener("click", save);
document.addEventListener("keydown", event => {
  if (event.key.toLowerCase() === "p") decide("positive");
  else if (event.key.toLowerCase() === "c") decide("clean");
  else if (event.key.toLowerCase() === "u") decide("uncertain");
  else if (event.key.toLowerCase() === "x") decide("exclude");
  else if (event.key.toLowerCase() === "s") save();
  else if (event.key === "ArrowLeft") move(-1);
  else if (event.key === "ArrowRight") move(1);
});
restore();
show();
</script>
</body>
</html>
`))
