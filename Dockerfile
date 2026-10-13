# The candidate base is immutable so rebuilding cannot silently change its
# operating-system contents. This digest is reused from the current downstream
# benchmark image; productization must approve it for the unified component.
ARG BASE_IMAGE=registry.redhat.io/ubi9/ubi-minimal-pqc@sha256:8a842ac769de709143e4edeace516f2008dfdc431b64670ad3353fa323b44736
FROM ${BASE_IMAGE}

# This directory is created only by the blocking release-candidate validator.
# It contains catalog runtime files and a qualified benchmark handoff; generator
# indexes, source inputs, reports, credentials, and sample-data are excluded.
ARG RELEASE_CANDIDATE_DIR=build/release-candidate
COPY --chown=1001:0 ${RELEASE_CANDIDATE_DIR}/app/ /app/

# Fail closed if either payload is absent or a producer-only catalog index
# reaches the image. OpenShift may assign any non-root UID, so every payload
# entry is world-readable and every directory is traversable.
RUN test -d /app/data && \
    test -d /app/benchmarks && \
    test -s /app/benchmarks/manifest.json && \
    /bin/sh -c 'for file in /app/data/*; do \
      test -f "${file}" || exit 1; \
      case "${file}" in *-catalog.yaml) ;; *) echo "unexpected runtime catalog file: ${file}" >&2; exit 1;; esac; \
    done' && \
    chmod -R a-w,a+rX /app

USER 1001
WORKDIR /app
VOLUME ["/app/data", "/app/benchmarks"]
CMD ["sleep", "infinity"]

LABEL name="ai-hub-catalog" \
      version="2.0" \
      description="Validated Model Catalog and qualified benchmark runtime data" \
      io.k8s.description="Provides validated Model Catalog and qualified benchmark data" \
      io.k8s.display-name="Model Catalog Data" \
      io.openshift.tags="ai,models,metadata,catalog,benchmarks"
