# NFO and OMDb as Planner Data Sources

**Date:** 2026-09-03
**Scope:** Issue #966; runtime retrieval/personalization and model training/evaluation.
**Status:** Product and engineering policy recommendation, not legal advice.

## Executive decision

Loomarr may use a user's local Kodi-style NFO files as runtime context when the user explicitly authorizes the library, provided it minimizes and keeps sensitive viewing data local by default. NFO files are containers: their presence beside user-owned media does not establish rights to every embedded plot, image, rating, or other third-party value. Therefore, actual NFO contents must not enter a centrally collected training or evaluation corpus without field-level provenance and permission. Synthetic NFO fixtures are the preferred evaluation source.

Loomarr should not use OMDb content for either a generally distributed runtime feature or any training/evaluation corpus under the public terms alone. OMDb's site advertises CC BY-NC 4.0, while its specific legal terms limit Contributions to personal, non-commercial use and prohibit derivative works and several forms of copying, archiving, distribution, and indexing. Attribution does not cure those restrictions. Enable OMDb only after written permission expressly covers the intended product use, caching, commercial status, and ML use; otherwise fail closed.

## What Kodi-style NFO files contain

Kodi describes NFOs as UTF-8 XML text files that populate its music and video libraries with locally stored information; Kodi checks an NFO before an online scraper and can also export its library into NFO files for backup ([Kodi: NFO files](https://kodi.wiki/view/NFO_files)). Common movie fields include titles, plot and tagline, runtime, genres and tags, cast and crew, ratings and votes, external IDs, artwork and trailer references, stream details, and file paths. They can also include personal state: `userrating`, `playcount`, `lastplayed`, `resume/position`, and `dateadded` ([Kodi: Movie NFO](https://kodi.wiki/view/NFO_files/Movies)). TV libraries normally have a show NFO plus per-episode NFOs, with similar metadata and viewing state ([Kodi: TV show NFO](https://kodi.wiki/view/NFO_files/TV_shows), [Kodi: Episode NFO](https://kodi.wiki/view/NFO_files/Episodes)). Kodi's separate-files export explicitly writes watched state—including play count, last played, and resume points—beside the media ([Kodi: Video library import/export](https://kodi.wiki/view/Import-export_library/Video)).

These documented semantics make NFO useful for planner grounding, but do not grant a license to the values found in a particular file. The Kodi wiki itself is offered under CC BY-SA 3.0, as its page footer states; that applies to the wiki material, not automatically to metadata copied into an NFO by a scraper or another catalog.

## Use-by-use assessment

| Source and use | Assessment | Conditions |
| --- | --- | --- |
| Local NFO for runtime retrieval | **Allow** | User selects/authorizes the library; parse locally; use only fields necessary for the request; do not redistribute embedded content. |
| Local NFO for personalization | **Allow with safeguards** | Treat viewing state and user tags as sensitive preference data; local processing/default storage; explicit opt-in before sending derived context to a hosted model. |
| Local NFO for training | **Do not collect by default** | Only ingest fields whose provenance and training rights are recorded, with informed opt-in for personal fields. Exclude unverified third-party prose, images, and ratings. |
| Local NFO for evaluation | **Prefer synthetic fixtures** | Use fictitious titles and values modeled on documented schema semantics. Real samples require the same provenance, consent, minimization, and retention controls as training data. |
| OMDb for runtime retrieval | **Not under public terms for a distributed Loomarr feature** | Require written authorization or a commercial agreement covering product use, API plan, caching, attribution, and redistribution. An operator-supplied API key alone is not evidence of these rights. |
| OMDb for training or evaluation | **Prohibit without specific written permission** | Permission must expressly cover ML training/evaluation, reproduction, derivative use, storage, redistribution, and commercial use. Do not bulk crawl, snapshot responses, or publish responses as test fixtures. |

## OMDb rights and constraints

The [OMDb API home page](https://www.omdbapi.com/) describes a REST service whose content and images are contributed and maintained by users, requires an API key, makes its poster endpoint patron-only, and labels all content CC BY-NC 4.0. That license permits sharing and adaptation only for non-commercial purposes and requires attribution, a license link, and an indication of changes ([Creative Commons BY-NC 4.0 summary](https://creativecommons.org/licenses/by-nc/4.0/)). The legal code also applies these conditions to extraction or reuse of a substantial portion of a licensed database ([CC BY-NC 4.0 legal code](https://creativecommons.org/licenses/by-nc/4.0/legalcode.en)). A commercial or revenue-supporting Loomarr use therefore cannot rely on this license.

More restrictively, the [OMDb legal terms](https://www.omdbapi.com/legal.htm) grant use and copying of user Contributions solely for personal, non-commercial purposes; require retention of proprietary notices; prohibit unauthorized copying, downloading, reproduction, archiving, distribution, publication, modification, and translation; prohibit creating or distributing an index without written permission; and prohibit derivative works based on Contributions without express written permission. The terms say the restriction applies even when derivative material is distributed free of charge. They were last marked updated March 12, 2015 and may be changed by OMDb.

The public CC notice and the specific site terms are not a safe basis for inferring broad corpus rights. Fine-tuning, dataset construction, persisted evaluation fixtures, or a published benchmark would at minimum involve copying or archiving and may involve adaptation or derivative use. A live API call in a distributed product also is not clearly within “personal, non-commercial” use. Loomarr should obtain a written clarification rather than choosing the more permissive reading. If permission is granted, retain required notices, provide visible “Data from OMDb API” attribution with the applicable license link, label modifications, avoid an independent OMDb-derived index, and cache only to the extent expressly authorized.

## Privacy minimization

Viewing history linked to a user or household can reveal preferences and may constitute personal data; that is a risk-based inference from the data fields, not a claim made by Kodi. GDPR Article 5 requires purpose limitation, data minimization, accuracy, storage limitation, integrity, and accountability for personal data ([Regulation (EU) 2016/679](https://eur-lex.europa.eu/eli/reg/2016/679/2016-05-04)). Loomarr should apply those principles regardless of whether every deployment is legally subject to GDPR:

- Keep raw NFO files, paths, and viewing-state history on the user's appliance by default.
- Build planner context from the minimum necessary derived facts. Do not send filenames, directory paths, usernames, device names, free-form tags, or exact timestamps to a hosted model unless essential and disclosed.
- Use ephemeral prompts and redact logs; do not retain raw NFO payloads or prompts for model improvement by default.
- Separate runtime consent from training consent. Training/evaluation participation must be explicit, revocable, purpose-specific, and accompanied by deletion and retention controls.
- Prefer coarse derived preferences over event histories, and evaluate locally where practical.

## Recommended Loomarr policy

1. **Runtime NFO adapter:** opt-in library access, local parsing, an allowlist of planner-relevant fields, provenance labels, and local-first personalization. Send only minimized derived context to hosted inference after a clear opt-in.
2. **Corpus gate:** no production NFO upload. Accept only synthetic fixtures or deliberately contributed samples with recorded source, field-level rights, consent basis, permitted purposes, retention, and deletion status. Automatically exclude plot/outline/tagline text, artwork and image URLs, scraped ratings/votes, free-form tags, paths, and exact viewing events unless individually cleared.
3. **OMDb gate:** ship no default OMDb retrieval, cache, training import, or evaluation fixture. Record OMDb as “permission required.” Legal/product review may lift the gate only on written terms covering the exact deployment and ML uses.
4. **Evaluation design:** certify planner behavior with fictitious NFO-shaped cases and licensed or project-authored metadata. Test privacy behavior explicitly: omitted fields must never appear in prompts, logs, traces, or exported datasets.
5. **Auditability:** store the applicable source terms/version and permission record with every approved dataset. Re-review OMDb terms before release because the site reserves the ability to amend them.

## Primary sources

- [Kodi: NFO files](https://kodi.wiki/view/NFO_files)
- [Kodi: Movie NFO](https://kodi.wiki/view/NFO_files/Movies)
- [Kodi: TV show NFO](https://kodi.wiki/view/NFO_files/TV_shows)
- [Kodi: Episode NFO](https://kodi.wiki/view/NFO_files/Episodes)
- [Kodi: Video library import/export](https://kodi.wiki/view/Import-export_library/Video)
- [OMDb API](https://www.omdbapi.com/)
- [OMDb legal terms](https://www.omdbapi.com/legal.htm)
- [Creative Commons Attribution-NonCommercial 4.0](https://creativecommons.org/licenses/by-nc/4.0/)
- [CC BY-NC 4.0 legal code](https://creativecommons.org/licenses/by-nc/4.0/legalcode.en)
- [EU General Data Protection Regulation](https://eur-lex.europa.eu/eli/reg/2016/679/2016-05-04)
