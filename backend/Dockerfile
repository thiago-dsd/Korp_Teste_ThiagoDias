# One image per service, built from the same file: the three services share a
# module, so building them separately would mean three copies of the same steps
# drifting apart.
#
# Pass the service with --build-arg SERVICE=stock-service.
ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build
ARG SERVICE

WORKDIR /src

# Dependencies are their own layer, so editing code does not download the
# module cache again.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary: the runtime stage has no libc to link against.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${SERVICE}

FROM alpine:3.20 AS runtime

# Certificates are needed to reach the AI endpoint over TLS; the rest of the
# image stays empty on purpose.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 service

COPY --from=build /out/service /usr/local/bin/service

# The services never write to disk and never need root.
USER service

ENTRYPOINT ["/usr/local/bin/service"]
