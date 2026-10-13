# Unified data image inputs

The release candidate contains two runtime payloads:

- the generated catalog files listed in `runtime-catalog-files.txt`, installed at `/app/data`;
- the qualified private benchmark handoff, installed at `/app/benchmarks`.

Catalog indexes, files under `input/`, generator binaries, reports, credentials, and the benchmark producer are producer-only inputs. They are validated where applicable but are not copied into the runtime image. The existing models catalog remains in the runtime allowlist to preserve the current image contract.

The benchmark handoff must be the qualified output produced by
`ai-hub-benchmark-data` from its protected benchmark source. Do not point a
product release at `sample-data`, at an unqualified checkout, or at this
repository's committed fixture. Private benchmark data must never be committed
to this repository.

Create and validate the complete build context with one blocking command:

```sh
make validate-release-candidate \
  BENCHMARK_DATA_DIR=/path/to/qualified/benchmark/data \
  MODEL_CATALOG_LOADER_CHECK_DIR=/path/to/supported-version-checks
```

On success, the exact image payload is staged under `build/release-candidate/app`. Build it with:

```sh
make docker-build \
  BENCHMARK_DATA_DIR=/path/to/qualified/benchmark/data \
  MODEL_CATALOG_LOADER_CHECK_DIR=/path/to/supported-version-checks
```

`BENCHMARK_DATA_DIR` is the handoff boundary: its producer supplies the qualified
runtime payload, while this repository validates the supplied files and their
compatibility with the catalog candidate before staging either payload. Producing
or approving that benchmark input remains outside this repository.

The loader-check directory must contain exactly one executable per entry in
`supported-model-catalog-versions.txt`, named for that version, with no missing
or extra files. The dual-payload compatibility matrix contains RHOAI 3.4 and
3.5. RHOAI 2.25 is catalog-only because its Model Catalog implementation has no
benchmark-loading option; the generic release validation still checks that the
benchmark payload is present, qualified, and compatible with the catalog.
Each executable receives
`MODEL_CATALOG_VERSION`, `RELEASE_CANDIDATE_ROOT`, `CATALOG_DATA_DIR`, and
`BENCHMARK_DATA_DIR`, must recursively initialize and load the candidate, and
only then print `MODEL_CATALOG_LOADER_CHECK_OK=<version>`. The release command
fails when membership differs, an entry is not executable, a check fails, or
the attestation is absent. The release owner supplies and reviews the matching
product-version executables; they are intentionally not mocked in this data
repository. `make validate-release-candidate-fixture` uses an isolated synthetic
payload, version list, and behavior-checking loader; its CLI guard accepts only
those committed fixture paths and a separate `build/release-candidate-fixture`
output.

Each product loader check must use the Model Catalog operand pinned by the exact
released operator bundle under test, generate that release's catalog source
configuration against the staged `/app/data` files, load `/app/benchmarks`, and
assert the expected catalog and benchmark records through the consumer. Server
startup or readiness alone is not a successful compatibility check.

Validation rejects malformed YAML, JSON, and NDJSON (including duplicate
mapping/object keys); duplicate catalog, provider, model, and benchmark artifact
identifiers; missing or escaping producer references; index/catalog
inconsistency; manifest/file mismatches; missing provider or variant-group
references; unexpected benchmark files; benchmark records stored under the
wrong model path; catalog-to-benchmark incompatibility; symlinks; unsafe or
overlapping paths; and a staged tree that is not readable by an arbitrary
non-root user. The image is staged from the exact bytes that were validated,
and loader hooks are prevented from changing them. Catalog-to-benchmark matching
compares the complete namespace and model name case-insensitively; it does not
guess aliases or ignore spelling and punctuation differences.

The validator enforces syntax, identity, reference, and generic record-path
contracts for evaluation, performance, and security NDJSON. Metric-specific
field schemas and semantic ranges are not defined in this repository; that
remains an explicit producer/consumer contract to approve rather than a schema
invented by this release gate.

## Konflux handoff boundary

`Dockerfile` is the unified-image proof and consumes only the validated
`build/release-candidate/app` tree. `Dockerfile.konflux` intentionally retains
the current catalog-only behavior so RHOAIENG-97415 can merge without breaking
existing Konflux pull-request and push builds. RHOAIENG-97416 must atomically
switch the product path to the unified Dockerfile contract after it wires the
protected benchmark payload, approved loader matrix, pre-build validation, and
relevant path triggers in
`aipcc-product-management-configs`. Do not edit generated `.tekton` files
directly.

Release promotion, stable tags, ImageStreams, registry publication, provenance, and rollback metadata belong to the follow-up release-configuration story.

The digest-pinned UBI candidate base in `Dockerfile` is reused from the current
downstream benchmark component. Productization approval for using that digest
in this unified component is required before promotion.
