FROM golang:1.24.2-bookworm AS build
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG VERSION="dev"
RUN apt-get update && apt-get install git
RUN go build -v -ldflags "-X main.version=${VERSION}" ./cmd/firestarknet

ARG FIRECORE_VERSION=v1.9.8
FROM ghcr.io/streamingfast/firehose-core:${FIRECORE_VERSION}

COPY --from=build /app/firestarknet /app/firestarknet