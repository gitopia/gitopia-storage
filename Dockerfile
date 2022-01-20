FROM golang:1.16-buster

ARG USER
ARG PERSONAL_ACCESS_TOKEN
ARG ENV

WORKDIR /app

ADD scripts/install_libgit2.sh /app

RUN apt-get update && apt-get -y install cmake libssl-dev
RUN ./install_libgit2.sh
RUN git config --global url."https://${USER}:${PERSONAL_ACCESS_TOKEN}@github.com".insteadOf "https://github.com"
RUN git config --global gc.auto 0

ADD . /app

RUN make build

EXPOSE 5000

ENTRYPOINT ["./scripts/startup.sh"]
CMD ["${ENV}"]