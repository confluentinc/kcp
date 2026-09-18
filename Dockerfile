# syntax=docker/dockerfile:1
# goreleaser builds the static linux binary per-arch and provides it (named "kcp")
# in the build context. This image only packages it — no compilation, no RUN steps.
FROM gcr.io/distroless/static-debian12:nonroot
COPY kcp /usr/local/bin/kcp
ENTRYPOINT ["/usr/local/bin/kcp"]
