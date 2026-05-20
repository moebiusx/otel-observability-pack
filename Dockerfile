# syntax=docker/dockerfile:1.7

# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache deps separately for fast incremental builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the operator binary statically so it runs in a distroless-like image.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags "-s -w" -o /out/operator ./cmd/operator

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
