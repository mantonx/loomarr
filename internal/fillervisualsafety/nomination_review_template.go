package fillervisualsafety

import "html/template"

// The browser document stays together as one implementation artifact; the Go
// binding and validation interface live in nomination_review.go.
var visualCorpusNominationBoardTemplate = template.Must(template.New("visual-corpus-nomination-review").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Loomarr visual nomination review</title>
<style>
:root { color-scheme: dark; font-family: ui-sans-serif, system-ui, sans-serif; background: #111318; color: #f5f7fa; }
[hidden] { display: none !important; }
body { margin: 0; min-height: 100vh; display: grid; grid-template-rows: auto 1fr auto; }
header, footer { padding: 14px 20px; background: #1a1e26; border-color: #343a46; }
header { border-bottom: 1px solid #343a46; }
footer { border-top: 1px solid #343a46; display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
main { min-height: 0; }
#single-review { display: grid; grid-template-columns: minmax(280px, 1fr) minmax(280px, 420px); gap: 18px; padding: 18px; }
.image-shell { display: grid; place-items: center; min-height: 55vh; background: #08090c; border-radius: 10px; overflow: hidden; }
.image-shell img { max-width: 100%; max-height: 72vh; object-fit: contain; }
aside { overflow: auto; }
h1 { margin: 0 0 6px; font-size: 18px; }
h2 { font-size: 14px; margin-top: 18px; }
p { line-height: 1.45; }
.muted { color: #aeb7c7; font-size: 13px; }
.identity { font-family: ui-monospace, monospace; overflow-wrap: anywhere; }
.actions { display: grid; grid-template-columns: repeat(3, minmax(90px, 1fr)); gap: 8px; }
button, .button, input::file-selector-button { border: 1px solid #515b6d; border-radius: 7px; background: #252b36; color: #f5f7fa; padding: 10px 12px; cursor: pointer; text-decoration: none; text-align: center; }
button:hover, button:focus, .button:hover, .button:focus { border-color: #9eb6dc; outline: none; }
button[data-value="positive"] { border-color: #d38888; }
button[data-value="clean"] { border-color: #75b995; }
button[data-value="exclude"] { border-color: #c8a66a; }
button:disabled { cursor: not-allowed; opacity: .45; }
.selected { box-shadow: 0 0 0 2px #f3c969 inset; }
.proposal { color: #80d6aa; }
.hold { color: #f0c979; }
.review-mode-switch { margin-left: auto; }
#clean-review { padding: 18px; }
.clean-toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 14px; }
.clean-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 14px; }
.clean-card { display: grid; grid-template-rows: 220px auto; background: #1a1e26; border: 1px solid #343a46; border-radius: 10px; overflow: hidden; }
.clean-card[data-decision="clean"] { border-color: #75b995; }
.clean-card[data-decision="positive"] { border-color: #d38888; }
.clean-card[data-decision="exclude"] { border-color: #c8a66a; }
.clean-card-media { display: grid; place-items: center; background: #08090c; position: relative; }
.clean-card img { display: block; width: 100%; height: 220px; object-fit: contain; }
.clean-card-body { padding: 10px; min-width: 0; }
.clean-card-body strong { overflow-wrap: anywhere; }
.clean-card-body p { margin: 5px 0; }
.clean-card-actions { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6px; margin-top: 9px; }
.clean-card-actions.required { grid-template-columns: 1fr; }
.clean-card-actions button { padding: 7px; }
.load-state { position: absolute; inset: auto 6px 6px; padding: 3px 6px; border-radius: 4px; background: rgba(17,19,24,.88); font-size: 12px; }
.attestation { display: flex; gap: 8px; align-items: flex-start; max-width: 980px; }
.attestation input { margin-top: 4px; }
@media (max-width: 760px) { #single-review { grid-template-columns: 1fr; } .image-shell { min-height: 42vh; } }
</style>
</head>
<body>
<header>
  <h1>Loomarr visual nomination review</h1>
  <div id="progress" class="muted"></div>
</header>
<main>
<section id="single-review">
  <section class="image-shell"><img id="candidate" alt="Institutional source work"></section>
  <aside>
    <div id="identity" class="identity"></div>
    <p id="creator"></p>
    <p id="subjects" class="muted"></p>
    <a id="source" class="button" target="_blank" rel="noopener">Open institution record</a>
    <a id="exact-image" class="button" target="_blank" rel="noopener">Open exact image at full resolution</a>
    <h2>Optional model assistance</h2>
    <input id="assistance" type="file" accept="application/json,.json">
    <p id="assistance-status" class="muted">No assistance loaded. Its suggestions are never decisions.</p>
    <div id="suggestion"></div>
    <button id="exclude-non-proposals" disabled>Exclude all non-proposals</button>
    <h2>Your decision</h2>
    <div class="actions">
      <button data-value="positive">P · Positive</button>
      <button data-value="clean">C · Clean</button>
      <button data-value="exclude">X · Exclude</button>
    </div>
    <p id="decision-help" class="muted">Positive and clean always require an individual click or key. Exclusion publishes no candidate or semantic assertion.</p>
  </aside>
</section>
<section id="clean-review" hidden>
  <div class="clean-toolbar">
    <button id="clean-previous">← Previous page</button>
    <button id="clean-next">Next page →</button>
    <strong id="clean-page-progress"></strong>
    <label class="button" for="clean-assistance">Load model assistance</label>
    <input id="clean-assistance" type="file" accept="application/json,.json" hidden>
    <button id="single-mode" class="review-mode-switch">Inspect one image</button>
  </div>
  <p id="clean-assistance-status" class="muted">No assistance loaded. Its suggestions are never decisions.</p>
  <div id="clean-grid" class="clean-grid"></div>
  <p class="attestation">
    <input id="clean-attestation" type="checkbox">
    <label for="clean-attestation">I reviewed every exact image on this page for sexual content; minor or age-ambiguous sexual risk; violence, gore, death, self-harm, animal harm, weapons, or frightening imagery; tobacco, alcohol, drugs, gambling, or regulated-product promotion; hateful or extremist material; prohibited visible written language; and any other content unsuitable for the intended audience.</label>
  </p>
  <button id="confirm-page-clean" disabled>Confirm eligible undecided images as clean</button>
  <p id="clean-page-status" class="muted"></p>
</section>
</main>
<footer>
  <button id="previous">← Previous</button>
  <button id="next">Next →</button>
  <button id="clear">U · Clear</button>
  <button id="save">S · Download review.csv</button>
  <button id="contact-sheet" hidden>Contact sheet</button>
  <span class="muted">Worksheet <span class="identity">{{.WorksheetSHA256}}</span></span>
</footer>
<script>
"use strict";
const worksheet = {{.WorksheetJSON}};
const header = {{.HeaderJSON}};
const cases = {{.CasesJSON}};
const cleanReviewMode = {{.CleanReviewJSON}};
const decisions = Object.create(null);
const suggestions = Object.create(null);
const storageKey = "loomarr-visual-nomination:" + worksheet;
const assistanceContracts = new Set(["filler-visual-corpus-model-assistance-v1", "filler-visual-corpus-model-assistance-v2"]);
const cleanAssistanceContracts = new Set(["filler-visual-corpus-clean-assistance-v1", "filler-visual-corpus-clean-assistance-v2"]);
const allowedActions = new Set(["propose_positive_candidate", "exclude_age_risk", "exclude_no_visible_nudity", "hold_cross_batch_creator_overlap", "targeted_human_review"]);
const suitabilityVocabulary = [
  "adult_nudity", "minor_nudity_or_sexual_risk", "minor_present", "age_ambiguous",
  "weapon_depiction", "non_graphic_violence", "graphic_violence_or_gore", "human_death_or_corpse",
  "animal_harm_or_death", "self_harm_or_suicide", "tobacco_or_nicotine", "alcohol", "drug",
  "gambling", "regulated_product_promotion", "hate_or_extremist_symbol", "slur_or_degrading_language",
  "profanity", "explicit_sexual_language", "frightening_or_disturbing", "religious_suffering",
  "war_or_military", "commercial_or_brand",
];
const suitabilityFlags = new Set(suitabilityVocabulary);
const suitabilitySeverities = new Set(["low", "moderate", "high"]);
const suitabilityContexts = new Set(["presence", "depiction", "promotion", "instruction"]);
const suitabilityConfidences = new Set(["low", "medium", "high"]);
let order = cases.map((_, index) => index);
let cursor = 0;
const cleanPageSize = 12;
let cleanPage = 0;
let cleanPageLoads = new Map();
let showingContactSheet = cleanReviewMode;
let assistanceLoaded = false;
const image = document.getElementById("candidate");
const identity = document.getElementById("identity");
const creator = document.getElementById("creator");
const subjects = document.getElementById("subjects");
const source = document.getElementById("source");
const exactImage = document.getElementById("exact-image");
const progress = document.getElementById("progress");
const suggestion = document.getElementById("suggestion");
const assistanceStatus = document.getElementById("assistance-status");
const cleanAssistanceStatus = document.getElementById("clean-assistance-status");
const excludeNonProposals = document.getElementById("exclude-non-proposals");
const singleReview = document.getElementById("single-review");
const cleanReview = document.getElementById("clean-review");
const cleanGrid = document.getElementById("clean-grid");
const cleanAttestation = document.getElementById("clean-attestation");
const confirmPageClean = document.getElementById("confirm-page-clean");
const cleanPageStatus = document.getElementById("clean-page-status");
const cleanPageProgress = document.getElementById("clean-page-progress");
const contactSheet = document.getElementById("contact-sheet");
const buttons = [...document.querySelectorAll("button[data-value]")];
function current() { return cases[order[cursor]]; }
function counts() {
  const values = Object.values(decisions);
  return {positive: values.filter(value => value === "positive").length,
    clean: values.filter(value => value === "clean").length,
    exclude: values.filter(value => value === "exclude").length};
}
function needsIndividualReview(item) {
  const proposed = suggestions[item.caseId];
  return proposed && (proposed.action !== "exclude_no_visible_nudity" ||
    proposed.controlEligibility === "individual_review_required" ||
    (Array.isArray(proposed.suitabilityObservations) && proposed.suitabilityObservations.length > 0));
}
function suggestionText(proposed) {
  if (!proposed) return "No model assistance loaded";
  const labels = {
    propose_positive_candidate: "Possible adult nudity — inspect individually",
    exclude_age_risk: "Possible minor or age uncertainty — inspect individually",
    exclude_no_visible_nudity: "No policy signal proposed — your page review is still required",
    hold_cross_batch_creator_overlap: "Source-family overlap — inspect individually",
    targeted_human_review: "Uncertain or possible policy signal — inspect individually",
  };
  const ocr = Number.isInteger(proposed.ocrTextCount) && proposed.ocrTextCount > 0 ? " · OCR text detected" : "";
  const flags = Array.isArray(proposed.suitabilityObservations) && proposed.suitabilityObservations.length > 0
    ? " · Suitability: " + proposed.suitabilityObservations.map(value => value.flag.replaceAll("_", " ")).join(", ")
    : "";
  return labels[proposed.action] + " · " + proposed.reason + flags + ocr;
}
function cleanPageItems() {
  const start = cleanPage * cleanPageSize;
  return order.slice(start, start + cleanPageSize).map(index => cases[index]);
}
function setContactSheet(showContactSheet) {
  showingContactSheet = cleanReviewMode && showContactSheet;
  singleReview.hidden = showingContactSheet;
  cleanReview.hidden = !showingContactSheet;
  contactSheet.hidden = !cleanReviewMode || showingContactSheet;
  for (const id of ["previous", "next", "clear"]) document.getElementById(id).hidden = showingContactSheet;
  if (showingContactSheet) renderCleanPage();
  else show();
}
function updateCleanPageControls() {
  const pageItems = cleanPageItems();
  const totals = counts();
  const allLoaded = pageItems.length > 0 && pageItems.every(item => cleanPageLoads.get(item.caseId) === true);
  const failed = pageItems.filter(item => cleanPageLoads.get(item.caseId) === false).length;
  const loading = pageItems.filter(item => !cleanPageLoads.has(item.caseId)).length;
  const individual = pageItems.filter(item => !decisions[item.caseId] && needsIndividualReview(item)).length;
  const eligible = pageItems.filter(item => !decisions[item.caseId] && !needsIndividualReview(item)).length;
  progress.textContent = (totals.positive + totals.clean + totals.exclude) + " of " + cases.length + " decided · " +
    totals.positive + " positive · " + totals.clean + " clean · " + totals.exclude + " excluded";
  confirmPageClean.disabled = !assistanceLoaded || !allLoaded || !cleanAttestation.checked || eligible === 0;
  confirmPageClean.textContent = "Confirm " + eligible + " eligible undecided image" + (eligible === 1 ? "" : "s") + " as clean";
  cleanPageStatus.textContent = !assistanceLoaded ? "Load a bound machine-assistance manifest before confirming a page."
    : loading ? loading + " exact image" + (loading === 1 ? " is" : "s are") + " still loading."
    : failed ? failed + " image" + (failed === 1 ? " failed" : "s failed") + " to load; page confirmation is disabled."
    : individual ? individual + " model-routed image" + (individual === 1 ? " requires" : "s require") + " an individual decision."
    : "All exact images on this page loaded.";
}
function updateCleanCardDecision(item) {
  const card = cleanGrid.querySelector('[data-case-id="' + CSS.escape(item.caseId) + '"]');
  if (!card) return;
  card.dataset.decision = decisions[item.caseId] || "";
  const label = card.querySelector("[data-decision-label]");
  label.textContent = decisions[item.caseId] ? "Decision: " + decisions[item.caseId] : needsIndividualReview(item) ? "Individual review required" : "Undecided";
}
function cleanCardButton(label, action, item) {
  const button = document.createElement("button");
  button.textContent = label;
  button.disabled = true;
  button.dataset.loadRequired = "true";
  button.addEventListener("click", () => action(item));
  return button;
}
function renderCleanPage() {
  const pageItems = cleanPageItems();
  cleanPageLoads = new Map();
  cleanAttestation.checked = false;
  cleanGrid.replaceChildren();
  cleanPageProgress.textContent = "Page " + (cleanPage + 1) + " of " + Math.ceil(cases.length / cleanPageSize);
  document.getElementById("clean-previous").disabled = cleanPage === 0;
  document.getElementById("clean-next").disabled = cleanPage >= Math.ceil(cases.length / cleanPageSize) - 1;
  for (const item of pageItems) {
    const card = document.createElement("article");
    card.className = "clean-card";
    card.dataset.caseId = item.caseId;
    const media = document.createElement("div");
    media.className = "clean-card-media";
    const candidate = document.createElement("img");
    candidate.alt = "Institutional source work " + item.rank;
    candidate.width = item.width;
    candidate.height = item.height;
    const loadState = document.createElement("span");
    loadState.className = "load-state";
    loadState.textContent = "Loading exact image…";
    const body = document.createElement("div");
    body.className = "clean-card-body";
    const identityLine = document.createElement("strong");
    identityLine.textContent = "#" + item.rank + " · " + item.caseId;
    const creatorLine = document.createElement("p");
    creatorLine.textContent = (item.creator || []).join(", ");
    const subjectLine = document.createElement("p");
    subjectLine.className = "muted";
    subjectLine.textContent = (item.subjectTerms || []).join(" · ");
    const suggestionLine = document.createElement("p");
    suggestionLine.className = needsIndividualReview(item) ? "hold" : "muted";
    suggestionLine.textContent = suggestionText(suggestions[item.caseId]);
    const decisionLine = document.createElement("p");
    decisionLine.dataset.decisionLabel = "true";
    const actions = document.createElement("div");
    actions.className = "clean-card-actions";
    const positive = cleanCardButton("P · Positive", value => { decisions[value.caseId] = "positive"; persist(); updateCleanCardDecision(value); updateCleanPageControls(); }, item);
    const exclude = cleanCardButton("X · Exclude", value => { decisions[value.caseId] = "exclude"; persist(); updateCleanCardDecision(value); updateCleanPageControls(); }, item);
    const inspect = cleanCardButton("Inspect", value => { cursor = order.findIndex(index => cases[index].caseId === value.caseId); setContactSheet(false); }, item);
    if (needsIndividualReview(item)) {
      actions.classList.add("required");
      positive.hidden = true;
      exclude.hidden = true;
      inspect.textContent = "Inspect required";
    }
    actions.append(positive, exclude, inspect);
    body.append(identityLine, creatorLine, subjectLine, suggestionLine, decisionLine, actions);
    media.append(candidate, loadState);
    card.append(media, body);
    cleanGrid.append(card);
    updateCleanCardDecision(item);
    candidate.addEventListener("load", () => {
      cleanPageLoads.set(item.caseId, true);
      loadState.textContent = "Exact image loaded";
      for (const button of actions.querySelectorAll("button[data-load-required]")) button.disabled = false;
      updateCleanPageControls();
    }, {once: true});
    candidate.addEventListener("error", () => {
      cleanPageLoads.set(item.caseId, false);
      loadState.textContent = "Load failed";
      updateCleanPageControls();
    }, {once: true});
    candidate.src = item.assetUrl;
  }
  updateCleanPageControls();
}
function confirmCleanPage() {
  const pageItems = cleanPageItems();
  if (!assistanceLoaded || !cleanAttestation.checked || !pageItems.every(item => cleanPageLoads.get(item.caseId) === true)) return;
  for (const item of pageItems) {
    if (!decisions[item.caseId] && !needsIndividualReview(item)) decisions[item.caseId] = "clean";
    updateCleanCardDecision(item);
  }
  persist();
  updateCleanPageControls();
}
function show() {
  const item = current();
  image.src = item.assetUrl;
  image.width = item.width;
  image.height = item.height;
  identity.textContent = "#" + item.rank + " · " + item.caseId;
  creator.textContent = (item.creator || []).join(", ");
  subjects.textContent = (item.subjectTerms || []).join(" · ");
  source.href = item.objectUrl;
  exactImage.href = item.assetUrl;
  const totals = counts();
  progress.textContent = "Case " + (cursor + 1) + " of " + cases.length + " · " +
    (totals.positive + totals.clean + totals.exclude) + " decided · " + totals.positive + " positive · " +
    totals.clean + " clean · " + totals.exclude + " excluded";
  const proposed = suggestions[item.caseId];
  if (proposed) {
    const className = proposed.action === "propose_positive_candidate" ? "proposal" : "hold";
    suggestion.className = className;
    suggestion.textContent = suggestionText(proposed);
  } else {
    suggestion.className = "";
    suggestion.textContent = "";
  }
  for (const button of buttons) button.classList.toggle("selected", decisions[item.caseId] === button.dataset.value);
}
function decide(value) {
  decisions[current().caseId] = value;
  persist();
  if (cursor < cases.length - 1) cursor++;
  show();
}
function persist() {
  try { localStorage.setItem(storageKey, JSON.stringify(decisions)); } catch (_) { /* private-file storage may be unavailable */ }
}
function restore() {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) || "{}");
    const valid = new Set(["positive", "clean", "exclude"]);
    for (const item of cases) if (valid.has(saved[item.caseId])) decisions[item.caseId] = saved[item.caseId];
  } catch (_) { /* start empty when storage is unavailable or damaged */ }
}
function move(delta) { cursor = Math.max(0, Math.min(cases.length - 1, cursor + delta)); show(); }
function csvCell(value) {
  const text = String(value);
  return /[",\r\n]/.test(text) ? "\"" + text.replaceAll("\"", "\"\"") + "\"" : text;
}
function save() {
  const rows = [header];
  for (const item of cases) {
    const decision = decisions[item.caseId];
    let mutable = ["", "", "", ""];
    if (decision === "positive") mutable = ["positive_candidate", "historical_art_adult_only", "not_generated", "[\"historical_graphics\"]"];
    else if (decision === "clean") mutable = ["clean_candidate", "no_sensitive_subject_identified", "not_generated", "[\"historical_graphics\"]"];
    else if (decision === "exclude") mutable = ["exclude", "", "", "[]"];
    rows.push(item.immutableCsv.concat(mutable));
  }
  const raw = rows.map(row => row.map(csvCell).join(",")).join("\r\n") + "\r\n";
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob([raw], {type: "text/csv"}));
  link.download = "review.csv";
  link.click();
  URL.revokeObjectURL(link.href);
}
function canonicalJSON(value) {
  if (Array.isArray(value)) return "[" + value.map(canonicalJSON).join(",") + "]";
  if (value !== null && typeof value === "object") {
    return "{" + Object.keys(value).sort().map(key => JSON.stringify(key) + ":" + canonicalJSON(value[key])).join(",") + "}";
  }
  return JSON.stringify(value);
}
async function sha256Hex(raw) {
  if (!globalThis.crypto || !globalThis.crypto.subtle) throw new Error("browser SHA-256 support is unavailable");
  const digest = await globalThis.crypto.subtle.digest("SHA-256", new TextEncoder().encode(raw));
  return [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join("");
}
async function sealedDigest(value) {
  return sha256Hex(canonicalJSON({...value, sha256: ""}));
}
function validNonAuthorityRecord(value) {
  return value.candidateModelOutput === true && value.truthAuthorityCreated === false &&
    value.trainingAllowed === false && value.productionUseAllowed === false && value.ingestionAllowed === false &&
    value.schedulingAllowed === false && value.broadcastAllowed === false;
}
function validateSuitabilityObservations(observations) {
  if (!Array.isArray(observations)) throw new Error("frontier suitability observations are invalid");
  const seen = new Set();
  let previous = "";
  for (const observation of observations) {
    const keys = observation && typeof observation === "object" ? Object.keys(observation).sort().join(",") : "";
    if (keys !== "confidence,context,flag,severity" || !suitabilityFlags.has(observation.flag) ||
        !suitabilitySeverities.has(observation.severity) || !suitabilityContexts.has(observation.context) ||
        !suitabilityConfidences.has(observation.confidence) || seen.has(observation.flag) ||
        (previous && observation.flag < previous)) {
      throw new Error("frontier suitability observation is invalid");
    }
    seen.add(observation.flag);
    previous = observation.flag;
  }
  return observations;
}
async function validateFrontierAudienceReview(manifest) {
  if (manifest.frontierAudienceReviewer !== "openai-codex-frontier-session" ||
      !/^[0-9a-f]{64}$/.test(manifest.frontierAudienceReviewLedgerSha256) ||
      !Array.isArray(manifest.suitabilityVocabulary) ||
      canonicalJSON(manifest.suitabilityVocabulary) !== canonicalJSON(suitabilityVocabulary) ||
      !manifest.suitabilityFlagCounts || Array.isArray(manifest.suitabilityFlagCounts) ||
      !Array.isArray(manifest.frontierAudienceReviewRecords) ||
      manifest.frontierAudienceReviewRecords.length !== cases.length) {
    throw new Error("frontier audience-review coverage is invalid");
  }
  const records = new Map();
  const observedFlags = Object.create(null);
  const allowedRecordKeys = new Set([
    "schemaVersion", "contractVersion", "worksheetSha256", "rank", "caseId", "sourceSha256",
    "reviewer", "reviewMethod", "observations", "absenceIsTruth", "candidateModelOutput",
    "truthAuthorityCreated", "trainingAllowed", "productionUseAllowed", "ingestionAllowed",
    "schedulingAllowed", "broadcastAllowed", "sha256",
  ]);
  for (let index = 0; index < manifest.frontierAudienceReviewRecords.length; index++) {
    const record = manifest.frontierAudienceReviewRecords[index];
    const item = cases[index];
    if (!record || typeof record !== "object" || Object.keys(record).length !== allowedRecordKeys.size ||
        Object.keys(record).some(key => !allowedRecordKeys.has(key)) || record.schemaVersion !== 1 ||
        record.contractVersion !== "filler-frontier-audience-review-record-v1" ||
        record.worksheetSha256 !== worksheet || record.rank !== item.rank || record.caseId !== item.caseId ||
        record.sourceSha256 !== item.contentSha256 || record.reviewer !== manifest.frontierAudienceReviewer ||
        !new Set(["complete_contact_sheet", "exact_source_image_plus_complete_contact_sheet"]).has(record.reviewMethod) ||
        record.absenceIsTruth !== false || !validNonAuthorityRecord(record) || !/^[0-9a-f]{64}$/.test(record.sha256) ||
        records.has(record.caseId)) {
      throw new Error("frontier audience-review case binding is invalid");
    }
    const observations = validateSuitabilityObservations(record.observations);
    if (await sealedDigest(record) !== record.sha256) throw new Error("frontier audience-review record digest is invalid");
    for (const observation of observations) observedFlags[observation.flag] = (observedFlags[observation.flag] || 0) + 1;
    records.set(record.caseId, record);
  }
  const ledgerRaw = manifest.frontierAudienceReviewRecords.map(record => canonicalJSON(record) + "\n").join("");
  if (await sha256Hex(ledgerRaw) !== manifest.frontierAudienceReviewLedgerSha256) {
    throw new Error("frontier audience-review ledger digest is invalid");
  }
  const flagKeys = new Set([...Object.keys(manifest.suitabilityFlagCounts), ...Object.keys(observedFlags)]);
  for (const key of flagKeys) {
    if (!suitabilityFlags.has(key) || manifest.suitabilityFlagCounts[key] !== (observedFlags[key] || 0)) {
      throw new Error("frontier suitability flag counts are invalid");
    }
  }
  return records;
}
async function validateAssistance(manifest) {
  const first = cases[0].immutableCsv;
  const cleanV2 = cleanReviewMode && manifest && manifest.contractVersion === "filler-visual-corpus-clean-assistance-v2";
  const contractAccepted = cleanReviewMode ? manifest && cleanAssistanceContracts.has(manifest.contractVersion)
    : manifest && assistanceContracts.has(manifest.contractVersion);
  if (!manifest || manifest.schemaVersion !== (cleanV2 ? 2 : 1) || !contractAccepted ||
      !/^[0-9a-f]{64}$/.test(manifest.sha256) || manifest.worksheetSha256 !== worksheet ||
      manifest.inventorySha256 !== first[1] || manifest.materializationSha256 !== first[2] ||
      manifest.candidateModelOutput !== true || manifest.truthAuthorityCreated !== false ||
      manifest.trainingAllowed !== false || manifest.productionUseAllowed !== false ||
      manifest.ingestionAllowed !== false || manifest.schedulingAllowed !== false || manifest.broadcastAllowed !== false ||
      !manifest.counts || Array.isArray(manifest.counts) || !Array.isArray(manifest.proposals) ||
      manifest.proposals.length !== cases.length) throw new Error("manifest authority or worksheet binding is invalid");
  const expectedMachinePolicy = cleanV2 ? "two_local_vlm_plus_local_ocr_text_plus_frontier_audience_review_v2"
    : "two_local_vlm_plus_local_ocr_text_v1";
  if (cleanReviewMode && (manifest.machineReviewPolicy !== expectedMachinePolicy ||
      typeof manifest.leftModel !== "string" || typeof manifest.rightModel !== "string" || manifest.leftModel === manifest.rightModel ||
      !/^[0-9a-f]{64}$/.test(manifest.leftLedgerSha256) || !/^[0-9a-f]{64}$/.test(manifest.rightLedgerSha256) ||
      !/^[0-9a-f]{64}$/.test(manifest.ocrLedgerSha256) || !/^[0-9a-f]{64}$/.test(manifest.textSafetyLedgerSha256))) {
    throw new Error("clean assistance coverage is invalid");
  }
  const byCase = new Map(cases.map(item => [item.caseId, item]));
  const frontierRecords = cleanV2 ? await validateFrontierAudienceReview(manifest) : null;
  const seen = new Set();
  const observedCounts = Object.create(null);
  for (const proposed of manifest.proposals) {
    const item = byCase.get(proposed.caseId);
    if (!item || seen.has(proposed.caseId) || proposed.rank !== item.rank || proposed.sourceSha256 !== item.contentSha256 ||
        !allowedActions.has(proposed.action) || proposed.candidateModelOutput !== true || proposed.truthAuthorityCreated !== false ||
        proposed.trainingAllowed !== false || proposed.productionUseAllowed !== false || typeof proposed.reason !== "string" ||
        proposed.reason.length === 0 || proposed.reason.length > 256) {
      throw new Error("manifest case binding is invalid");
    }
    const positive = proposed.action === "propose_positive_candidate";
    if (positive !== (proposed.proposedNomination === "positive_candidate") ||
        positive !== (proposed.proposedSubjectStatus === "historical_art_adult_only") ||
        positive !== (proposed.proposedGeneratedStatus === "not_generated") ||
        positive !== (Array.isArray(proposed.proposedSlices) && proposed.proposedSlices.length === 1 && proposed.proposedSlices[0] === "historical_graphics")) {
      throw new Error("manifest proposal fields are invalid");
    }
    if (cleanV2) {
      const record = frontierRecords.get(proposed.caseId);
      const observations = validateSuitabilityObservations(proposed.suitabilityObservations);
      const expectedEligibility = proposed.action !== "exclude_no_visible_nudity" || observations.length > 0
        ? "individual_review_required" : "page_confirmable_clean";
      if (!record || proposed.frontierReviewRecordSha256 !== record.sha256 ||
          canonicalJSON(observations) !== canonicalJSON(record.observations) ||
          proposed.controlEligibility !== expectedEligibility) {
        throw new Error("manifest frontier proposal fields are invalid");
      }
    }
    seen.add(proposed.caseId);
    observedCounts[proposed.action] = (observedCounts[proposed.action] || 0) + 1;
  }
  const countKeys = new Set([...Object.keys(manifest.counts), ...Object.keys(observedCounts)]);
  for (const key of countKeys) if (!allowedActions.has(key) || manifest.counts[key] !== (observedCounts[key] || 0)) throw new Error("manifest counts are invalid");
  if (cleanV2 && await sealedDigest(manifest) !== manifest.sha256) throw new Error("manifest sealed digest is invalid");
  return manifest.proposals;
}
async function loadAssistance(event) {
  try {
    const proposals = await validateAssistance(JSON.parse(await event.target.files[0].text()));
    for (const key of Object.keys(suggestions)) delete suggestions[key];
    for (const proposed of proposals) suggestions[proposed.caseId] = proposed;
    assistanceLoaded = true;
    if (cleanReviewMode) {
      for (const item of cases) if (decisions[item.caseId] === "clean" && needsIndividualReview(item)) delete decisions[item.caseId];
      persist();
    }
    order = cases.map((_, index) => index).sort((left, right) => {
      const leftProposal = needsIndividualReview(cases[left]) ? 0 : 1;
      const rightProposal = needsIndividualReview(cases[right]) ? 0 : 1;
      return leftProposal - rightProposal || cases[left].rank - cases[right].rank;
    });
    cursor = 0;
    cleanPage = 0;
    const loadedMessage = "Bound assistance loaded. Model-routed cases are first and require individual review; suggestions remain non-authoritative.";
    assistanceStatus.textContent = loadedMessage;
    cleanAssistanceStatus.textContent = loadedMessage;
    excludeNonProposals.disabled = cleanReviewMode;
    if (showingContactSheet) renderCleanPage(); else show();
  } catch (error) {
    assistanceLoaded = false;
    for (const key of Object.keys(suggestions)) delete suggestions[key];
    order = cases.map((_, index) => index);
    cleanPage = 0;
    const rejectedMessage = "Assistance rejected: " + error.message;
    assistanceStatus.textContent = rejectedMessage;
    cleanAssistanceStatus.textContent = rejectedMessage;
    excludeNonProposals.disabled = true;
    if (showingContactSheet) renderCleanPage(); else show();
  }
}
function excludeOthers() {
  if (cleanReviewMode) return;
  if (!confirm("Explicitly exclude every model row that is not a positive proposal? This makes no positive or clean decision.")) return;
  for (const item of cases) if (suggestions[item.caseId].action !== "propose_positive_candidate") decisions[item.caseId] = "exclude";
  persist();
  show();
}
for (const button of buttons) button.addEventListener("click", () => decide(button.dataset.value));
document.getElementById("previous").addEventListener("click", () => move(-1));
document.getElementById("next").addEventListener("click", () => move(1));
document.getElementById("clear").addEventListener("click", () => { delete decisions[current().caseId]; persist(); show(); });
document.getElementById("save").addEventListener("click", save);
document.getElementById("assistance").addEventListener("change", loadAssistance);
document.getElementById("clean-assistance").addEventListener("change", loadAssistance);
excludeNonProposals.addEventListener("click", excludeOthers);
document.getElementById("clean-previous").addEventListener("click", () => { if (cleanPage > 0) { cleanPage--; renderCleanPage(); } });
document.getElementById("clean-next").addEventListener("click", () => { if (cleanPage < Math.ceil(cases.length / cleanPageSize) - 1) { cleanPage++; renderCleanPage(); } });
document.getElementById("single-mode").addEventListener("click", () => setContactSheet(false));
contactSheet.addEventListener("click", () => setContactSheet(true));
cleanAttestation.addEventListener("change", updateCleanPageControls);
confirmPageClean.addEventListener("click", confirmCleanPage);
document.addEventListener("keydown", event => {
  if (event.target && event.target.tagName === "INPUT") return;
  if (showingContactSheet) {
    if (event.key.toLowerCase() === "s") save();
    else if (event.key === "ArrowLeft" && cleanPage > 0) { cleanPage--; renderCleanPage(); }
    else if (event.key === "ArrowRight" && cleanPage < Math.ceil(cases.length / cleanPageSize) - 1) { cleanPage++; renderCleanPage(); }
    return;
  }
  if (event.key.toLowerCase() === "p") decide("positive");
  else if (event.key.toLowerCase() === "c") decide("clean");
  else if (event.key.toLowerCase() === "x") decide("exclude");
  else if (event.key.toLowerCase() === "u") { delete decisions[current().caseId]; persist(); show(); }
  else if (event.key.toLowerCase() === "s") save();
  else if (event.key === "ArrowLeft") move(-1);
  else if (event.key === "ArrowRight") move(1);
});
restore();
excludeNonProposals.hidden = cleanReviewMode;
if (cleanReviewMode) document.getElementById("decision-help").textContent = "Positive decisions require an individual click or key. Clean may be decided individually here or by explicit exact-image page confirmation; exclusion publishes no semantic assertion.";
setContactSheet(cleanReviewMode);
</script>
</body>
</html>
`))
