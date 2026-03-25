FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o /backend .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /backend /backend
ENTRYPOINT ["/backend"]
