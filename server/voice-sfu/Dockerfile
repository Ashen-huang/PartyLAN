FROM golang:1.26.3-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server/voice-sfu ./server/voice-sfu
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=voice-sfu-v1" \
    -o /out/voice-sfu ./server/voice-sfu/cmd/voice-sfu

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/voice-sfu /voice-sfu
EXPOSE 5054/tcp 5054/udp
ENTRYPOINT ["/voice-sfu"]
