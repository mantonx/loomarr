# Federal/public-institution audiovisual replacement candidates — 2026-09-05

**Scope.** This is metadata-only discovery for #1089. No video bytes were
downloaded, decoded, uploaded, or sent to a model/provider; no money was spent;
and this note neither clears rights nor grants corpus, training, or production
authority. It deliberately avoids the examined LOC silent-film pool, CDC *Charge
Your Phone* family, NPS *Toilet Paper Burning*, and listed USGS leads.

The three items below are exact NASA Image and Video Library records, rather
than a search/category page. The Library's [API documentation](https://images.nasa.gov/docs/images.nasa.gov_api_docs.pdf)
defines its `asset/{nasa_id}` response as the first-party asset manifest. The
listed direct MP4 URLs preserve the `~orig.mp4` paths returned by that manifest;
the metadata URLs are companion entries in the same manifest. The API returns
those asset links with `http`, while the same exact host/path serves over HTTPS
and Loomarr's inventory contract requires HTTPS. A future adapter must own and
test that one deterministic scheme upgrade rather than treating arbitrary URL
rewriting as source authority. Durations and audio facts come from those
metadata JSON documents, not a local probe.

This is not a newly qualified source lane. The earlier frozen NASA pilot reviewed
10 items and found 2 rights-approved, 7 product-relevant, and **zero that were
both**; the lane failed its common qualification gate. These three exact records
improve metadata and soundtrack preflight, but none resolves the same embedded
rights problem. That prior result is recorded in the
[pilot rights evidence](../evidence/filler-pilot-rights-review-2026-08-26.json).

## Candidate records

### 1. OSIRIS-REx TAG Trailer — NASA ID `GSFC_20200924_M13725_tagtrailer`

* **Exact item / media / metadata:** [item page](https://images.nasa.gov/details/GSFC_20200924_M13725_tagtrailer);
  [original MP4](https://images-assets.nasa.gov/video/GSFC_20200924_M13725_tagtrailer/GSFC_20200924_M13725_tagtrailer~orig.mp4);
  [first-party metadata](https://images-assets.nasa.gov/video/GSFC_20200924_M13725_tagtrailer/metadata.json).
  A 2026-09-05 HTTPS HEAD returned `video/mp4`, 112,115,372 bytes, byte ranges,
  last-modified `Wed, 09 Dec 2020 17:33:51 GMT`, and ETag
  `"05594f93ef99ba6b0bd70a326238dd2a-14"`.
* **Centre/agency and role:** the NASA record names centre `GSFC` and exposes no
  primary creator; the item calls
  itself an OSIRIS-REx Touch-And-Go-event trailer. Likely role: short
  trailer/promo (also a possible science-programme bumper), not a programme
  parent.
* **Duration and encoded-audio evidence:** `QuickTime:Duration` and
  `QuickTime:MediaDuration` are `0:01:28`. The record declares `mp4a`, 48 kHz,
  16-bit, two-channel/stereo audio. Its edit ingredients explicitly name both
  WAV files and `The_Glory_of_Victory.mp3`; therefore an audible soundtrack is
  expected, not inferred from the word “trailer.”
* **Rights statement and exceptions:** NASA says its images, audio, video, and
  other media are *generally* not subject to US copyright and may be used for
  factual educational/informational purposes with NASA acknowledged, but says
  third-party copyrighted material is separately marked and NASA's use conveys
  no rights to others. It also withholds the insignia/logotype/identifiers,
  prohibits implied endorsement, flags commercial privacy/publicity rights for
  identifiable people, and says its insignia may not appear in AI training or
  with AI-generated imagery ([NASA Images and Media Guidelines](https://www.nasa.gov/nasa-brand-center/images-and-media/)).
* **Collision/suitability:** High soundtrack-rights risk: the named MP3 is a
  direct embedded-work signal, so this is an inspect-first *negative* candidate
  unless an authorized reviewer proves its separate licence. It also has a
  common OSIRIS-REx campaign/mission family and NASA end-tag/identifier risk.
  No media pixels or audio were inspected, so the metadata supports no
  suitability conclusion.
* **Deterministic source-authority seam:** bind `nasa_id` → exact asset-manifest
  media URL → exact metadata digest → item record → versioned NASA-guidelines
  snapshot. Hold if ingredients include a non-NASA music/footage asset, an
  identifier lacks explicit use authority, or provider transfer/model
  processing is not separately authorized.

### 2. MMS Mission Trailer — NASA ID `GSFC_20140515_MMS_m11526_Trailer`

* **Exact item / media / metadata:** [item page](https://images.nasa.gov/details/GSFC_20140515_MMS_m11526_Trailer);
  [original MP4](https://images-assets.nasa.gov/video/GSFC_20140515_MMS_m11526_Trailer/GSFC_20140515_MMS_m11526_Trailer~orig.mp4);
  [first-party metadata](https://images-assets.nasa.gov/video/GSFC_20140515_MMS_m11526_Trailer/metadata.json).
  A 2026-09-05 HTTPS HEAD returned `video/mp4`, 95,703,461 bytes, byte ranges,
  last-modified `Thu, 21 Mar 2019 18:50:57 GMT`, and ETag
  `"49ae2dba98d9834a41b040de29568b9c-12"`.
* **Centre/agency and role:** the NASA record names centre `GSFC`, exposes no
  primary creator, and names Genna Duberstein, Michael Lentz, and Walt Feimer
  as secondary creators.
  Likely role: 47-second mission trailer/promo, not a programme parent.
* **Duration and encoded-audio evidence:** `QuickTime:Duration`,
  `MediaDuration`, and `TrackDuration` are `0:00:47`; audio is `mp4a`, 48 kHz,
  16-bit, stereo/two-channel. That is direct first-party encoded-track
  metadata, so an audio soundtrack is expected (its voice/music composition is
  not separately identified).
* **Rights statement and exceptions:** the same [NASA media guidelines](https://www.nasa.gov/nasa-brand-center/images-and-media/)
  apply: generally no US copyright for NASA content, but third-party material,
  protected identifiers, non-endorsement, commercial privacy/publicity, and
  NASA's AI/insignia restrictions remain exceptions.
* **Collision/suitability:** MMS is a distinct mission/campaign from the other
  entries but retains agency/visual-brand collision risk. The named individual
  creators and unidentified soundtrack make whole-work authorship and talent/
  music provenance unresolved. Metadata cannot establish suitability;
  flashing, captions, logos/end cards, human likenesses, speech, and music all
  require quarantine review.
* **Deterministic source-authority seam:** `nasa_id` and selected rendition
  must be immutable inputs; require a snapshot of both Library record and NASA
  guidelines, plus a component-credit/identifier screen. Unknown audio-credit,
  third-party flag, or provider-processing authority is a hard reject.

### 3. Parker Solar Probe Trailer — NASA ID `GSFC_20180720_Parker_m12911_trailer.en_US`

* **Exact item / media / metadata:** [item page](https://images.nasa.gov/details/GSFC_20180720_Parker_m12911_trailer.en_US);
  [original MP4](https://images-assets.nasa.gov/video/GSFC_20180720_Parker_m12911_trailer.en_US/GSFC_20180720_Parker_m12911_trailer.en_US~orig.mp4);
  [first-party metadata](https://images-assets.nasa.gov/video/GSFC_20180720_Parker_m12911_trailer.en_US/metadata.json).
  A 2026-09-05 HTTPS HEAD returned `video/mp4`, 143,523,026 bytes, byte ranges,
  last-modified `Thu, 14 Mar 2019 23:38:39 GMT`, and ETag
  `"755ee9a3556d2109857b08da4e5952b7-18"`.
* **Centre/agency and role:** the NASA record names centre `GSFC`, exposes no
  primary creator, and names Genna Duberstein and Steve Gribben as secondary
  creators. Likely role:
  65-second mission trailer/promo, potentially useful as a bright science
  interstitial but not a programme parent.
* **Duration and encoded-audio evidence:** metadata states `QuickTime:Duration`
  and `TrackDuration` `0:01:05`, with `mp4a`, 48 kHz, 16-bit, stereo/two-channel
  audio. It is thus an encoded-audio asset; the expected trailer soundtrack is
  real, but its component attribution is not exposed as a rights grant.
* **Rights statement and exceptions:** see the same [NASA guidelines](https://www.nasa.gov/nasa-brand-center/images-and-media/):
  general federal-media treatment is conditional and does not override
  third-party rights, NASA identifiers, endorsement, privacy/publicity, or the
  explicit AI/insignia restriction.
* **Collision/suitability:** separate Parker mission family, but same GSFC/NASA
  branding family as the preceding records. Check for energetic flashing,
  proper-name/personality rights, mission marks, soundtrack credit, and
  promotional endorsement before considering local use. No suitability claim
  is available from metadata alone.
* **Deterministic source-authority seam:** bind the three URLs and their
  captured bytes/digests as a single source tuple; retain the exact rendition
  name and metadata audio fields. Admission requires explicit component-rights
  and identifier/model-processing dispositions and a distinct
  `campaign_family_key=ParkerSolarProbe`; otherwise fail closed.

## Comparison and disposition

All three are modern, exact, first-party public-institution media records with
both a direct MP4 and manifest-exposed encoded audio. They are **not cleared**.
The first is the clearest rejection/hold because its metadata identifies an MP3;
the other two lack enough item-level component provenance to convert NASA's
general policy into a whole-work or provider-processing grant. Do not dedupe
them merely as “NASA”: preserve agency, centre, NASA ID, mission, rendition,
and campaign-family keys. Conversely, do not select more than one without the
family gate explicitly allowing it, because their common NASA end-card/style
can create an editorial collision.

## Acquisition disposition

**Do not acquire any of the three yet.** MMS Mission Trailer is the best future
local-quarantine candidate because it is shortest (47 seconds), has a direct
first-party audio-track declaration, names its human secondary creators, and
does **not** expose the explicit third-party music filename found in the
OSIRIS-REx candidate. But absence of that filename is not a soundtrack grant,
and the prior NASA lane already demonstrated that technical relevance without
whole-work rights produces no usable yield. Reopen MMS only after an exact
component-rights decision grants the intended local processing; provider
transfer remains a separate authority. At that point, freeze the item, asset,
metadata, HEAD facts, and NASA-guideline snapshots before fetching the exact
95,703,461-byte object.
