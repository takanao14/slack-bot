FROM --platform=$BUILDPLATFORM golang:1.27.1 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/slack-bot ./cmd/slack-bot

FROM debian:trixie-slim AS font

# BIZ UDGothic is a universal design font designed for readability even at small sizes, and is used for
# 32-pixel LED matrices.
# Font and license files are stored in the release archive.
ARG FONT_VERSION=v1.051
ARG FONT_FILE=BIZUDPGothic-Regular.ttf
ARG FONT_SHA256=30692df621b92df13b88f1360aed1ab6ae50de441bce751a396c6439045cd759
ARG LICENSE_SHA256=e753d7155d53c747d037a445e584c8ecfca6dd79846db610417e282a736b28bc

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/bizudgothic.zip \
    "https://github.com/googlefonts/morisawa-biz-ud-gothic/releases/download/${FONT_VERSION}/BIZUDGothic.zip" \
    && echo "${FONT_SHA256}  /tmp/bizudgothic.zip" | sha256sum -c - \
    && unzip -j -o /tmp/bizudgothic.zip "*${FONT_FILE}" -d /out \
    && rm /tmp/bizudgothic.zip

RUN curl -fsSL -o /out/OFL.txt \
    "https://raw.githubusercontent.com/googlefonts/morisawa-biz-ud-gothic/${FONT_VERSION}/OFL.txt" \
    && echo "${LICENSE_SHA256}  /out/OFL.txt" | sha256sum -c -

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/slack-bot /slack-bot
COPY --from=font /out/BIZUDPGothic-Regular.ttf /usr/share/fonts/BIZUDGothic/BIZUDPGothic-Regular.ttf
COPY --from=font /out/OFL.txt /usr/share/fonts/BIZUDGothic/OFL.txt

ENV SLACK_BOT_FONT_PATH=/usr/share/fonts/BIZUDGothic/BIZUDPGothic-Regular.ttf

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/slack-bot"]
