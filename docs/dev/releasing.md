# Publishing a release

Server image publication and GitHub Release publication intentionally run in separate workflows from
the same `v*` tag. The image workflow owns package write and keyless-signing permissions. The release
notes workflow owns only `contents: write`; it cannot build, sign, or promote an image.

## One-time repository setup

Add a dedicated OpenRouter key as the masked Actions secret used only by release notes:

```sh
gh secret set OPENROUTER_RELEASE_API_KEY --repo loomarr/loomarr
```

The default model is `openai/gpt-5-mini`. To change it without editing the workflow, set the optional
repository variable `RELEASE_NOTES_MODEL` to an OpenRouter model that supports strict structured
outputs. The release process is designed for a small classification call, not model-authored prose.

## Preview before tagging

Authenticate `gh`, export the release-specific key locally, and generate a preview for the proposed
tag. The tag may already exist or be a commit-ish accepted by GitHub's generated-notes endpoint.

```sh
export OPENROUTER_RELEASE_API_KEY='...'
make release-notes-preview TAG=v0.2.0 PREVIOUS_TAG=v0.1.0-beta.1
```

The default output is `.artifacts/release-notes-v0.2.0.md`. Set `OUTPUT=/path/to/notes.md` to choose
another destination. Do not commit or paste the key. A tag-specific
`docs/release/<tag>.md` file, when present, is prepended for human-authored framing and known
limitations.

## What the model can and cannot do

The helper first asks GitHub to generate the exact merged-PR list, contributor list, and compare link.
It sends only each PR number and title to OpenRouter. The structured response is one closed object:
every exact PR number is a required property, and its value must be one of these fixed sections. That
shape encodes one assignment per PR even for unusually large releases instead of asking the model to
repeat PR numbers across seven independent arrays:

- New Features
- Improvements
- Bug Fixes
- Security Fixes
- Documentation
- Dependencies
- Maintenance

Repository code renders GitHub's original bullet for each assignment. It independently rejects unknown
JSON fields, invented PRs, duplicate keys, omitted PRs, invalid categories, malformed output, and
unrecognized GitHub change lines. It retries inference three times and then fails without creating a
GitHub Release. It never silently publishes uncategorized or model-authored notes.

## Tag and verify

Before requesting remote release certification, test the exact current `main` commit on the
maintainer's local machine. Record the commit, commands, and results with the release evidence. At
minimum, run every release-relevant gate this host supports: Go/Rust contracts and tests, Postgres,
web unit/build, visual/e2e/tuner browser evidence, shared clients, both Expo Android app builds,
legacy Android TV including its release bundle contract, image-worker certification, and release
policy verification. Host-incompatible evidence such as iOS/tvOS and native arm64 image builds must
be named explicitly and remain required in protected CI; a local Linux pass cannot stand in for
them. Agent sessions still never run the maintainer's live-stack `make smoke*` targets.

Push the protected version tag only after that local evidence is green, the required commit gates
are green, and the exact current `main` commit has passed the proportional release-candidate scope:

```sh
gh workflow run ci.yml --repo loomarr/loomarr --ref main -f scope=release-candidate
```

That scope runs repository contracts, real-codec image-worker certification, and the native amd64
and arm64 image builds. It deliberately leaves unrelated platform and UI matrices to their normal
change-based CI. A normal push run or `-f scope=full` run is never accepted as Docker-release
evidence, even when green, because neither proves that publication is isolated from those unrelated
jobs. Use `-f scope=full` only when a complete manual rerun is independently required.

Both release workflows start from the tag. If OpenRouter is temporarily unavailable, rerun the
failed **Release notes** workflow after service recovers; the separately hardened image publication
is unaffected.

After both workflows finish, verify the GitHub Release body, the GHCR manifest, signature, SBOM, and
provenance against the tagged commit. The tag-specific header remains the place to state limitations
that cannot be derived from pull requests.
