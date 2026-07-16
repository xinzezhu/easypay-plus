FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/easypay-plus ./cmd/server

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates tzdata
USER app
COPY --from=builder /out/easypay-plus /usr/local/bin/easypay-plus
EXPOSE 8080
ENTRYPOINT ["easypay-plus"]

