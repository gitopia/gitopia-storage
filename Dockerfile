ARG IMG_TAG=latest
ARG GITOPIA_ENV=prod

# Compile the gitopia-storage binary
FROM golang:1.23-alpine AS gitopia-storage-builder
WORKDIR /src/app/
ENV PACKAGES="curl make git libc-dev bash file gcc linux-headers eudev-dev"
RUN apk add --no-cache $PACKAGES

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN LINK_STATICALLY=true GITOPIA_ENV=${GITOPIA_ENV} make build
RUN echo "Ensuring binary is statically linked ..."  \
    && file /src/app/build/gitopia-storaged | grep "statically linked"

FROM alpine:$IMG_TAG
WORKDIR /src/app/
RUN apk add --no-cache build-base supervisor git
ARG IMG_TAG
COPY --from=gitopia-storage-builder /src/app/build/gitopia-storaged /usr/local/bin/
COPY --from=gitopia-storage-builder /src/app/build/gitopia-pre-receive /usr/local/bin/
COPY --from=gitopia-storage-builder /src/app/build/gitopia-post-receive /usr/local/bin/
EXPOSE 5000

COPY config_local.toml /src/app/config_local.toml
COPY config_dev.toml /src/app/config_dev.toml
COPY config_prod.toml /src/app/config_prod.toml
COPY scripts/startup.sh /usr/local/bin/startup.sh
COPY scripts/supervisord.conf /etc/supervisord.conf

ENTRYPOINT ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
