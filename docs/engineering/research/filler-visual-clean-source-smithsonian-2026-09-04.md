# Smithsonian Open Access visual clean-control source assessment

Date: 2026-09-04

Tracking: [#1011](https://github.com/loomarr/loomarr/issues/1011)

Scope: metadata-source qualification only for the V68 visual-safety certification corpus. No
Smithsonian API request, bulk-record download, media download, rights decision, model evaluation,
truth label, certification, ingestion, scheduling, broadcast, or production authority was exercised.

## Decision

**Use Smithsonian Open Access for a bounded metadata-only probe, not yet for acquisition.** The
current primary-source evidence makes it a credible, reproducible *candidate* source for a
300–350-family visual clean-control pool, but does **not** prove that the required number of
rights-reviewable, non-people, non-sensitive, source-family-distinct image candidates presently
exists. That numerical and semantic claim needs the proposed pinned metadata probe.

It is a better first clean-candidate source than a web scrape because the Smithsonian publishes an
institutional CC0 signal per Open Access asset, an official record identity, and a public bulk
metadata release. It is not clean truth: the Smithsonian expressly says CC0 concerns copyright and
that privacy, publicity, trademark, and other third-party permissions may still be needed. V68
therefore still requires private rights review, distinct-source-family review, frozen exact source
bytes, and locked clean truth before certification.

## What the official sources establish

### Scale, access, and release shape

The official AWS Open Data registry describes the release as Smithsonian Open Access media and
metadata, licensed CC0, with new/updated metadata and image files pushed weekly. It identifies a
public S3 bucket (`smithsonian-open-access`, `us-west-2`) and says anonymous listing is possible
with `aws s3 ls --no-sign-request`; it does not require an AWS account. The registry describes the
2020 release as 2.8 million CC0 2-D/3-D images and associated metadata, while the official
repository README says the bulk corpus has over 11 million metadata records. Those quantities make
a 300–350 candidate target plausible, but neither source reports the count after Loomarr's much
narrower safety, representation, and family filters. [AWS Open Data registry](https://registry.opendata.aws/smithsonian-open-access/), [official bulk-repository README](https://github.com/Smithsonian/OpenAccess/blob/master/README.md).

The repository README says the former GitHub compressed-archive distribution was retired in favor
of the AWS bucket; records are line-delimited JSON, organized by owning unit and the first two
characters of a content-serialization hash. The weekly mutable release means an API query or S3
prefix is not itself reproducible. A Loomarr probe must save the exact response/objects used,
byte-digest every source record, record its observation time and source URL/object key, and later
materialize only from the frozen inventory. [Official bulk-repository README](https://github.com/Smithsonian/OpenAccess/blob/master/README.md).

The public API requires a caller-provided API key. Smithsonian's maintained Python client rejects
every `content`, `stats`, `search`, category-search, and terms operation without one, and directs
callers to `api.data.gov/signup/`. The key is an access prerequisite, not a source or licence
signal; it must stay out of inventories and logs. [Official maintained client README](https://github.com/Smithsonian/smithsonian-openaccess/blob/main/README.md), [client implementation](https://github.com/Smithsonian/smithsonian-openaccess/blob/main/src/si_openaccess/si_openaccess.py).

### Query and identity capabilities

The official API documentation presently exposes:

- `GET /content/:id` for a record by row id or URL;
- `GET /search` and `GET /category/:cat/search`, with `q`, `start`, `rows`, `sort`, and an API key;
- `GET /terms/:category`, where categories include `data_source`, `object_type`,
  `online_media_type`, `topic`, and `unit_code`.

`q` accepts Boolean operators and fielded searches (the documentation gives `topic:Gastropoda` as
an example). Object search can restrict `type=edanmdm` and `row_group=objects`; category search
can restrict to `art_design`, `history_culture`, or `science_technology`. `rows` permits at most
1,000, and the official parameter list documents `id`, `newest`, `updated`, and `random` sorts.
These are enough to page a deterministic discovery set (`sort=id`) and to discover official term
vocabulary before constructing a query. The sources do not publish a rate limit or a complete
query grammar; the probe must keep a low declared request budget and fail rather than infer one.
[Official API parameter specification](https://edan.si.edu/openaccess/apidocs/api_data.json), [official maintained client](https://github.com/Smithsonian/smithsonian-openaccess/blob/main/src/si_openaccess/si_openaccess.py).

The API's documented response example has an outer `id`, `url`, `unitCode`, `type`, `hash`,
`docSignature`, `timestamp`, `lastTimeUpdated`, and `version`; its repository documentation says
repository content atoms receive persistent identifiers even during data refresh. That supports
record-level reproducibility when Loomarr retains all of those fields plus the raw JSON digest. It
does not establish that any one of them is an immutable work/family identifier, or that an image URL
will retain the same bytes. Treat `id` as the exact *record* key, not an independent-source-family
claim; separately bind fetched media SHA-256 and representation facts. [Official API response
example](https://edan.si.edu/openaccess/apidocs/api_data.json), [official EDAN documentation](https://edan.si.edu/openaccess/docs/).

### Rights and media representation

Smithsonian's FAQ says that Open Access items designated CC0 may be used, transformed, and shared
without permission or charge, including commercial use. It also says collection data exposed by the
API/GitHub has metadata for public-domain digital images including a corresponding image URL; when
an object may have copyright or other limitations, metadata may still be CC0 while Smithsonian does
not provide a media file. Consequently, selection must require an image-media entry carrying the
item-level CC0 access signal—not merely a CC0 metadata record or a reachable URL. [Open Access
FAQ](https://www.si.edu/openaccess/faq).

The source materials support inspecting the record's online-media structure and preserving the
media's exact delivery/resource URL(s), media type, width/height where supplied, and resource
identity. They do **not** publish a current, machine-enforced cross-unit schema promising that each
image has a stable original URL, dimensions, checksum, format, or item-level CC0 field at one fixed
path. Different Smithsonian units contribute flexible record content. The API docs say each record
type conforms to a published schema, but a probe must validate the actual selected fields in the
current source records before code assumes a path or an original representation exists. Absence,
ambiguity, unexpected type, non-image media, non-HTTPS URL, representation drift, or absent exact
CC0 signal is a hold. [Official EDAN documentation](https://edan.si.edu/openaccess/docs/),
[official API parameter specification](https://edan.si.edu/openaccess/apidocs/api_data.json),
[Open Access FAQ](https://www.si.edu/openaccess/faq).

CC0 is necessary but deliberately insufficient for Loomarr's rights screen. The FAQ cautions that
third-party rights such as privacy, publicity, and trademarks can remain; it also says an item that
is not marked CC0 has usage conditions. A clean-control source policy should therefore exclude
recognizable people, contemporary portraiture, brands/logos, living-person/event photography,
rights text or usage conditions other than exact `CC0`, and any row/media mismatch. A reviewer—not
metadata or a model—still decides whether the exact item and intended private/production uses are
permitted. [Open Access FAQ](https://www.si.edu/openaccess/faq).

## What metadata can and cannot screen

The API's fielded/Boolean search and its term endpoints can make a conservative *candidate* filter:
select image media from low-person-risk natural-history/material-culture units, require exact CC0
media access, and exclude terms found in title, topic, object type, description/freetext, creator,
and credit fields for people, portraiture, anatomy, nudity, sexual content, youth/minors, medical
imagery, and contemporary events. The official API documents fielded search and terms, not a
complete universal semantic field catalogue, so every expression must first be tested and frozen
from the actual term response.

This can reduce manual work, but it cannot demonstrate absence of people, nudity, minors, or
privacy/publicity risk. Smithsonian metadata is descriptive and heterogenous across units; an
unmentioned condition is not evidence that it is absent. Nor can metadata establish V68 clean truth
or a full-video negative. All surviving items remain `smithsonian_metadata_clean_candidate`, not
`clean`, `safe`, `approved`, or `airable`.

The safest initial lanes are static 2-D specimens and objects from explicitly selected natural
history units, for example mineral sciences, botany, entomology, fishes, and paleobiology, only if
their actual records have exact CC0 image-media entries. Do not use an entire unit as a family
boundary: it is an owning institution/department, not a work or capture identity. Exclude any
record with multiple subject objects, a repeated primary image/IDS identity, or insufficient
record-level provenance until a reviewer resolves it.

## Metadata-only probe contract

The first execution should be a small, no-media, no-model, no-rights-decision probe. It should
produce no authority outside a mode-0600 private report.

1. Register a dedicated API key, store it only in a local secret, and record neither the key nor a
   request URL containing it. Set a predeclared ceiling of **80 API requests**, serial execution,
   `rows <= 100`, no retry on an ambiguous non-2xx response, and a 250 ms minimum delay. Do not
   download a media representation or call `content/:id` until the candidate result passes the
   metadata predicates below.
2. Fetch and freeze the exact responses for `terms/unit_code`, `terms/online_media_type`,
   `terms/object_type`, `terms/topic`, and `terms/data_source`; digest each response and write the
   observation time, endpoint (without credentials), and HTTP status. Use them to select the
   actual current low-person-risk unit and image-media vocabulary. If a needed term endpoint lacks
   the expected vocabulary, stop with a hold rather than invent a field value.
3. Issue one deterministic `type=edanmdm`, `row_group=objects`, `sort=id` search per selected
   source lane, only with API-documented Boolean and fielded operands verified by the frozen terms.
   Page at most 60 results per lane (up to five pages/lane). Retain raw response bytes/digests,
   `rowCount`, cursor/start, and query *redacted of the key*. Do not use `random`.
4. From each returned row, require an outer record id/type/url/unit code and the item-level
   descriptive record identity; select exactly one `Images` media entry that has exact `CC0` access,
   an HTTPS content/delivery URL, a non-empty media identifier/guid where supplied, and a distinct
   candidate representation key. Preserve all representation URLs and dimensions/resources rather
   than assuming which is original. A missing field is a named hold.
5. Apply a closed, versioned metadata exclusion lexicon across all available title, topic,
   object-type, description/freetext, people/creator, credit, caption, and rights/usage text. It
   must exclude people/portrait/person, nude/nudity/anatomy/sexual, child/youth/minor/infant,
   event/news/contemporary photography, privacy/publicity, logo/brand/trademark, and all
   non-CC0/uncertain rights signals. A match is exclusion; missing searchable text is a hold, not
   a clean result.
6. Create provisional family keys only to prevent duplicate review: prefer a record-level stable
   work GUID when the current row shows one, otherwise the exact outer record id. Also deduplicate
   all media/IDS/representation IDs and URLs. Call these **provisional** keys; an independent
   reviewer must validate source-family distinctness before locking truth.
7. Stop after generating a 600-item maximum candidate sheet. Report lane counts at every predicate,
   duplicates, missing fields, rights holds, lexical exclusions, and the count of provisional unique
   families. A result below 350 must be reported as inadequate; no broadening, media download, or
   manual substitution is implicit. If at least 350 survive, prepare—not execute—a separate
   rights-review and representation-probe proposal.

The sheet's minimum row identity is: raw-record SHA-256; outer id/url/type/unit code; raw
`docSignature`, `hash`, and `version` when present; record ID/GUID/link; selected media index,
media ID/GUID/type/CC0 value; every source representation URL/dimension/resource; provisional
family key and collision set; query/response digest; and every exclusion/hold reason. Its internal
status vocabulary is only `candidate`, `excluded`, or `hold`.

## Explicit unknowns and blockers

- No official source inspected here provides a current count for 300–350 items after the required
  exact-CC0, image, people/nudity/minor/privacy, representation, duplicate, and family filters.
  **The numerical feasibility is unproven until the probe completes.**
- The API documentation contains no published rate/quotas or a complete field-query grammar. The
  key holder must accept the bounded probe contract; a 429, undocumented limit, or schema error is
  a stop/hold, not a reason to increase concurrency.
- The record and media schema is flexible across Smithsonian contributors. The official sources do
  not guarantee a universal item-level original-image URL, immutable representation, dimensions,
  checksum, item-level CC0 path, or work-family relationship. Implementation must be schema-guarded
  after a successful probe.
- Weekly updates mean current search results and S3 listings mutate. Reproducibility requires a
  frozen response/record inventory and later exact-byte media materialization, not rerunning a
  query.
- Metadata cannot prove no person, no nudity, adult-only status, no privacy/publicity issue, or V68
  clean truth. It only prioritizes review. A rights reviewer and the locked V68 clean-truth process
  retain those decisions.
- The bulk release is useful for a later reproducible offline selection because it avoids an API
  key and has documented line-delimited layout, but it is **not preferable for the first probe**:
  it is a large, weekly-mutating corpus with no server-side candidate filter. Use the API to measure
  field coverage and yield; move to a date- and digest-pinned bulk subset only if the probe proves
  the selected fields and candidate count are adequate.
