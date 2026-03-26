FROM golang:1.25-alpine AS builder
WORKDIR /app

# Build backend
COPY backend/ ./backend/
RUN cd backend && go mod tidy && CGO_ENABLED=0 go build -o /backend .

# Build client binaries for major platforms
COPY client/ ./client/
RUN cd client && go mod tidy && \
    CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o /dl/otiv-client-linux-amd64      ./cmd/otiv-client && \
    CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o /dl/otiv-client-linux-arm64      ./cmd/otiv-client && \
    CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -o /dl/otiv-client-darwin-amd64     ./cmd/otiv-client && \
    CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o /dl/otiv-client-darwin-arm64     ./cmd/otiv-client && \
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dl/otiv-client-windows-amd64.exe ./cmd/otiv-client

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /backend /backend
COPY --from=builder /dl/ /downloads/
ENTRYPOINT ["/backend"]
