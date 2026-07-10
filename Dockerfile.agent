# Multi-stage build for the krew-review-agent.
# Build stage: compile the Go binary from golang:1.26.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/agent ./cmd/agent

# Runtime stage: minimal base with git and ca-certificates.
# alpine is used because distroless does not ship git by default; for a
# distroless deployment use the :debug variant or a custom base that includes git.
FROM alpine:3.20
RUN apk add --no-cache git ca-certificates && update-ca-certificates
COPY --from=build /out/agent /usr/local/bin/agent
# Cloud Run expects the binary to listen on $PORT.
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/agent"]
