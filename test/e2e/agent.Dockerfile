# Builds the Agent from the current checkout so the suite tests this working tree,
# not a published release.
FROM golang:1.27-alpine AS build

# The Server refuses any Agent below its minimum version, so this has to be a real
# semantic version rather than a label like "harness".
ARG AGENT_VERSION=0.1.0

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# The e2e tag compiles in the protocol-version override, which stays inert unless
# OMJ_TEST_PROTOCOL_VERSION is set. A release binary is built without the tag, so the
# override cannot exist in one.
RUN CGO_ENABLED=0 go build -trimpath -tags e2e \
    -ldflags "-s -w -X github.com/ohmyjob/omj-agent/internal/version.Version=${AGENT_VERSION}" \
    -o /out/omj-agent ./cmd/omj-agent

FROM alpine:3.20

RUN addgroup -S ohmyjob \
    && adduser -S -G ohmyjob -h /var/lib/ohmyjob -s /sbin/nologin ohmyjob \
    && mkdir -p /etc/ohmyjob /var/lib/ohmyjob \
    && chown ohmyjob:ohmyjob /etc/ohmyjob /var/lib/ohmyjob \
    && chmod 0750 /etc/ohmyjob \
    && chmod 0700 /var/lib/ohmyjob

COPY --from=build /out/omj-agent /usr/local/bin/omj-agent

CMD ["sleep", "infinity"]
