# Met Open Access rights pre-screen

Date: 2026-09-04
Tracking: [#993](https://github.com/loomarr/loomarr/issues/993)

## Decision

Loomarr may use the Met's item metadata to remove repetitive work from the development-corpus rights
review, but the result is only `met_metadata_prescreen_pass`. It is never called cleared, licensed,
approved, or safe to air. The pre-screen creates no download, training, truth, certification,
production, or broadcast authority.

The machine-checkable conjunction is:

1. the exact cached Met object response still has the inventory's SHA-256 and object id;
2. `isPublicDomain` is exactly `true`;
3. `rightsAndReproduction` exists and is empty after trimming;
4. the selected `primaryImage`, official object URL, creator, title, date, subject terms, repository,
   credit line, and source-work identity reproduce the frozen inventory; and
5. the selected representation remains an HTTPS JPEG or PNG on the exact Met image host.

The selected representation is the Met image URL with `download=1` and a cache-isolation key derived
from the already frozen item-metadata SHA-256, and that exact URL is frozen before rights review. A
real acquisition attempt found that the CDN could advertise the origin byte length to `HEAD` while
returning a differently sized cached body to a bare `GET`; a shared `download=1` cache key could also
retain that split across HTTP variants. The item-bound URL returned the same byte length to both
methods. The adapter therefore probes and later materializes that exact download representation; it
does not relax the downloader's exact-byte check.

Any disagreement is a named hold for the independent reviewer. A passing row is a mechanically
consistent nomination, not the rights decision consumed by the downloader.

## Primary-source findings

The [official Collection API documentation](https://metmuseum.github.io/) describes the API as
providing Open Access data and corresponding high-resolution JPEGs that are in the public domain.
Its object schema defines `isPublicDomain: true` as indicating an artwork in the public domain,
defines `primaryImage` and `additionalImages` as image URLs, describes `creditLine` as source/origin
acknowledgement, and exposes `rightsAndReproduction` for rights-related text. The HTML retrieved for
this review was 71,948 bytes at SHA-256
`037f875cd22180ecb31a67cb38707ce2ea88eb7087c2f81edd27a0a1aa56dd6a`.

The Met's [Image and Data Resources policy](https://www.metmuseum.org/policies/image-resources)
states that images of works the Met believes are public domain, or for which it waives its own image
copyright, are made available under CC0. Its
[image and data FAQ](https://www.metmuseum.org/policies/frequently-asked-questions-image-and-data-resources)
explains that Open Access does not require attribution while recommending a Met/object citation. It
also identifies reasons an item may not be Open Access that extend beyond copyright, including
privacy, publicity, ownership, contractual, and photography constraints. The
[terms and conditions](https://www.metmuseum.org/policies/terms-and-conditions) disclaim a general
non-infringement warranty and preserve third-party trademark and publicity concerns. These three
pages returned HTTP 429 to the bounded Loomarr client during the supervisor's reproduction, so they
are citations for independent review rather than live machine dependencies of the pre-screen.

The official [`metmuseum/openaccess` repository README](https://github.com/metmuseum/openaccess/blob/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/README.md)
was pinned at commit `6fa206f0df6cf349d4fe558028d4c08e95f44eb6`. The README blob is
`689b9325f6016e7f0ecb80f7fd7ac0a579c7e0ef`; its 4,979 downloaded bytes are SHA-256
`26f24c669b3eb888a02498113dc94feb2674ee9d007a1d470c13be36413a29c2`. The repository's
[CC0 file](https://github.com/metmuseum/openaccess/blob/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/LICENSE)
has Git blob `670154e3538863b2d9891fd5483160fbdfc89164` and downloaded SHA-256
`36ffd9dc085d529a7e60e1276d73ae5a030b020313e6c5408593a6ae2af39673`.

That repository licenses its selected metadata dataset; its README explicitly says image files are
not part of the dataset. The dataset's CC0 file therefore cannot independently grant rights to the
selected object image. The item-level API assertion and Met image policy must remain distinct
evidence.

## Mandatory anomaly holds

The pre-screen holds a row when any of the following is true:

- the raw metadata is missing, changed, symlinked, oversized, malformed, or does not reproduce the
  inventory-bound item;
- `isPublicDomain` is absent or false;
- `rightsAndReproduction` is missing or non-empty;
- the object id, official object URL, selected image URL, title, creator, date, subject terms,
  repository, credit line, source work, media type, or exact host differs;
- the representation is missing or no longer matches the frozen record; or
- the reviewer detects a trademark, recognizable person or likeness, privacy/publicity concern,
  embedded third-party work, location/contractual issue, or item-page caveat not represented in the
  API response.

Blank `rightsAndReproduction` is necessary for this conservative pre-screen but is not affirmative
clearance. Object dates are catalog context, not copyright calculations. A retrievable image URL is
availability evidence, not a standalone licence.

## Human decision that remains

One independent reviewer still decides whether the exact policy evidence is adequate for Loomarr's
private development use, copying, storage, transformations, evidence extraction, and retention;
whether the recorded non-copyright limitations are acceptable for that use; and whether a caveat,
attribution requirement, or restriction requires the ordinary exception path. The batch-completion
aid records that policy decision once, but expands it into the unchanged item-bound rights worksheet
and leaves the existing locker as the only producer of downloader authority. It refuses every batch
with a held pre-screen case, changed artifact, mixed authority, stale review, widened use,
attribution requirement, or restriction. The reviewer therefore inspects the policy and named
anomalies rather than manually rechecking 120 hashes and duplicated fields.

The successful private development cohort has 120 present `rightsAndReproduction` fields, all blank;
120 `isPublicDomain: true` records; 120 non-empty selected primary images; and 120 distinct creator,
source-work, source-family, and exact-content identities. Those counts are development evidence only
and are reproduced by the implementation rather than committed as source metadata.

The final metadata inventory is SHA-256
`3a663b77d675fa209cffb496a3f27505664989629b03f9f38cd8bf70fff49847`. Its item, work,
creator, metadata, representation-name, media-type, and byte-size projection is identical to the
original candidate pool; the selected URL now names the exact item-bound download representation.
The offline pre-screen again reproduced all 120 inventory-bound raw responses with 120
`met_metadata_prescreen_pass` results and zero holds. Its mode-`0600`, path-free report is SHA-256
`d1e85d3e35ab6188200f4930b68f4740785f8b4cf041345072a0dc9d2b0e8896`.

The maintainer accepted the frozen development-only limitations in attestation SHA-256
`848b8258c6f2606eb30221e6cd0f3cd6413697202c57b2f155b7db6bac57a039`; the ordinary
item-level locker then produced 120 approvals at SHA-256
`df84668b51afd7bdfa26237f688d8dc5e301b9b69cc84275244c6b1d27a5ce49`. The bounded,
serial materializer downloaded and completely decoded all 120 JPEGs: exactly 292,769,745 bytes in
120 requests, with no duplicate content. The schema-3 ledger is SHA-256
`9bda3154575635af954058d3f86a40744ad3820d5e91a9e4989e6546025e0152`.

The model-blind nomination preparation reopened every downloaded image and emitted 120 inert rows.
Its worksheet file is SHA-256
`04710cb604da6f28f4ff28b38ada65b95caa459bb1a79cf2b8fe9e3f8a70939c`; its four-field
review CSV is SHA-256 `601889a02e80f4efa4fa34a91bb41159cd238a68ca2c2352683ca7d1f5285942`.
No nomination, truth, training, certification, provider-transfer, production, ingestion, scheduling,
or broadcast authority was created. Earlier future-dated and shared-CDN-key attempts are superseded;
both failed closed before publishing a ledger.
