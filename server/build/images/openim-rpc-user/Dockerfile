# Copyright © 2023 OpenIM. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# OpenIM base image: https://github.com/openim-sigs/openim-base-image

# Set go mod installation source and proxy

FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS builder

ARG GO111MODULE=on
ARG GOPROXY=https://goproxy.cn,direct

WORKDIR /openim/openim-server

ENV GO111MODULE=$GO111MODULE
ENV GOPROXY=$GOPROXY

COPY go.mod go.sum ./
COPY pkg/protocol/go.mod pkg/protocol/go.sum ./pkg/protocol/
RUN go mod download

COPY . .

RUN make build BINS=openim-rpc-user

RUN cp /openim/openim-server/_output/bin/platforms/$(go env GOOS)/$(go env GOARCH)/openim-rpc-user /usr/bin/openim-rpc-user

# FROM ghcr.io/openim-sigs/openim-bash-image:latest
FROM ghcr.io/openim-sigs/openim-bash-image:latest

WORKDIR /openim/openim-server

COPY --from=builder /usr/bin/openim-rpc-user ./bin/openim-rpc-user

ENTRYPOINT ["./bin/openim-rpc-user"]
