# syntax=docker/dockerfile:1.7
#
# The default build includes the v6 page renderer: librm_lines is built from
# pinned sources and statically linked into a fully static musl binary, so
# the runtime image is still FROM scratch. Build without it (no cgo, no C
# toolchain) with:  docker build --build-arg RMLINES=0 .
ARG VERSION=0.0.0
ARG RMLINES=1

########## pinned librm_lines sources (fetched once, on the build host) ##########
FROM --platform=$BUILDPLATFORM alpine:3.22 AS deps-src
RUN apk add --no-cache curl tar xz
COPY build/rmlines/VERSION build/rmlines/SOURCES build/rmlines/SHA256SUMS build/rmlines/build.sh /rmlines/
# build.sh checks every archive against SHA256SUMS before unpacking it.
RUN --mount=type=cache,target=/deps/dl RMLINES_DL_DIR=/deps/dl /rmlines/build.sh fetch /deps/src

########## librm_lines static libraries (per target platform) ##########
FROM alpine:3.22 AS rmlines-1
# python3 + py3-fonttools split the embedded fonts at configure time; they never leave this stage.
RUN apk add --no-cache cmake samurai g++ patch python3 py3-fonttools
COPY --from=deps-src /deps/src /deps/src
COPY build/rmlines/VERSION build/rmlines/SOURCES build/rmlines/SHA256SUMS build/rmlines/build.sh /rmlines/
COPY build/rmlines/patches /rmlines/patches
RUN RMLINES_BUILD_DIR=/tmp/rmlines-build /rmlines/build.sh build /deps/src /opt/rmlines \
 && rm -rf /tmp/rmlines-build /tmp/rmlines-build.src

# RMLINES=0: nothing is built; BuildKit skips deps-src and rmlines-1 entirely.
FROM scratch AS rmlines-0
COPY build/rmlines/VERSION /opt/rmlines/DISABLED

FROM rmlines-${RMLINES} AS rmlines

########## UI ##########
FROM --platform=$BUILDPLATFORM node:lts AS uibuilder
ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
RUN corepack enable pnpm && corepack install -g pnpm@latest-9

WORKDIR /src
#COPY ui/package.json ui/pnpm-lock.yaml /src
#RUN pnpm fetch 

COPY ui .
RUN pnpm install && pnpm build

########## Go ##########
# Same Alpine release as rmlines-1, so libstdc++.a matches the compiler that built the .a files.
FROM golang:1.26-alpine3.22 AS gobuilder
ARG VERSION
ARG RMLINES
RUN if [ "$RMLINES" = 1 ]; then apk add --no-cache g++; fi
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY --from=rmlines /opt/rmlines /opt/rmlines
COPY . .
COPY --from=uibuilder /src/dist ./ui/dist
# RMLINES=1: cgo with the rmlines tags and a fully static musl link (netgo,
# osusergo: no NSS/dlopen), then the link probe renders a fixture to prove the
# library links and draws. RMLINES=0: the same pure-Go build as before.
RUN go generate ./... \
 && if [ "$RMLINES" = 1 ]; then \
      export CGO_ENABLED=1 CGO_LDFLAGS="-L/opt/rmlines/lib" \
      && go build -buildvcs=false -tags rmlines,rmlines_static,netgo,osusergo \
           -ldflags "-s -w -X main.version=${VERSION} -linkmode external -extldflags -static" \
           -o rmfakecloud-docker ./cmd/rmfakecloud/ \
      && cd build/rmlines/probe \
      && go build -buildvcs=false -tags rmlines,rmlines_static \
           -ldflags "-s -w -linkmode external -extldflags -static" -o /tmp/rmlines-probe . \
      && /tmp/rmlines-probe /src/testdata/rm/v6/Test_all_tools.rm /tmp/rmlines-probe.png; \
    else \
      CGO_ENABLED=0 go build -buildvcs=false -ldflags "-s -w -X main.version=${VERSION}" \
        -o rmfakecloud-docker ./cmd/rmfakecloud/; \
    fi

########## release binary: docker buildx build --target binary --output type=local,dest=out . ##########
FROM scratch AS binary
COPY --from=gobuilder /src/rmfakecloud-docker /rmfakecloud

########## runtime ##########
FROM scratch
EXPOSE 3000
ADD ./docker/rootfs.tar /
COPY --from=gobuilder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=gobuilder /src/rmfakecloud-docker /
COPY THIRD_PARTY_NOTICES.md /
ENTRYPOINT ["/rmfakecloud-docker"]
