# Build Stage
FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS builder

# Set go mod installation source and proxy
ARG GO111MODULE=on
ARG GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE=$GO111MODULE
ENV GOPROXY=$GOPROXY

# Set up the working directory
WORKDIR /openim/openim-server

COPY go.mod go.sum ./
COPY pkg/protocol/go.mod pkg/protocol/go.sum ./pkg/protocol/
RUN go mod download

# Copy all files to the container
ADD . .

RUN make clean
RUN make build

FROM ghcr.io/openim-sigs/openim-ubuntu-image:latest

WORKDIR ${SERVER_WORKDIR}

# Copy scripts and binary files to the production image
COPY --from=builder ${OPENIM_SERVER_BINDIR} /openim/openim-server/_output/bin
COPY --from=builder ${OPENIM_SERVER_CMDDIR} /openim/openim-server/scripts
COPY --from=builder ${SERVER_WORKDIR}/config /openim/openim-server/config
COPY --from=builder ${SERVER_WORKDIR}/deployments /openim/openim-server/deployments

CMD ["/openim/openim-server/scripts/docker-start-all.sh"]
