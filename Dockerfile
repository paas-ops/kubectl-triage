# Build stage: compile the static kubectl-triage binary.
FROM golang:1.26 AS build

# Build metadata injected via -ldflags (main.version/commit/date).
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

# Download modules in their own layer so source edits do not refetch them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/kubectl-triage ./cmd/kubectl-triage

# Runtime stage: distroless static, nonroot. No shell, package manager,
# kubeconfig or credentials are included; only the static binary.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/kubectl-triage /usr/local/bin/kubectl-triage

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/kubectl-triage"]
