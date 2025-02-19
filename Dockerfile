ARG IMG_TAG=latest

# Compile the git-server binary
FROM golang:1.23-alpine AS git-server-builder
WORKDIR /src/app/
ENV PACKAGES="curl make git libc-dev bash file gcc linux-headers eudev-dev"
RUN apk add --no-cache $PACKAGES

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN LINK_STATICALLY=true GITOPIA_ENV=testing make build
RUN echo "Ensuring binary is statically linked ..."  \
    && file /src/app/build/git-server | grep "statically linked"

FROM alpine:$IMG_TAG
WORKDIR /src/app/
RUN apk add --no-cache build-base supervisor git
ARG IMG_TAG
COPY --from=git-server-builder /src/app/build/git-server /usr/local/bin/
COPY --from=git-server-builder /src/app/build/git-server-events /usr/local/bin/
COPY --from=git-server-builder /src/app/build/gitopia-pre-receive /usr/local/bin/
COPY --from=git-server-builder /src/app/build/gitopia-post-receive /usr/local/bin/
EXPOSE 5000

COPY config_local.toml /src/app/config_local.toml
COPY scripts/startup.sh /usr/local/bin/startup.sh
COPY scripts/supervisord.conf /etc/supervisord.conf

ENTRYPOINT ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
