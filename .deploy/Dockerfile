# Production image for the VPS. Build context: repository root.
# One static binary; the compose file picks the role (serve or sweep).

FROM golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${GIT_COMMIT}" -o /out/lateness ./cmd/lateness

# Distroless static: CA certificates for SMTP, webhooks, probes and sign-in; no shell.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/lateness /lateness
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/lateness"]
CMD ["serve"]
