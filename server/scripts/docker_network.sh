#!/usr/bin/env bash

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


export DOCKER_BRIDGE_SUBNET="172.30.0.0/16"
export DOCKER_BRIDGE_GATEWAY="172.30.0.1"
export MYSQL_NETWORK_ADDRESS="172.30.0.2"
export MONGO_NETWORK_ADDRESS="172.30.0.3"
export REDIS_NETWORK_ADDRESS="172.30.0.4"
export KAFKA_NETWORK_ADDRESS="172.30.0.5"
export ZOOKEEPER_NETWORK_ADDRESS="172.30.0.6"
export MINIO_NETWORK_ADDRESS="172.30.0.7"
export OPENIM_WEB_NETWORK_ADDRESS="172.30.0.8"
export OPENIM_SERVER_NETWORK_ADDRESS="172.30.0.9"
export OPENIM_CHAT_NETWORK_ADDRESS="172.30.0.10"
export PROMETHEUS_NETWORK_ADDRESS="172.30.0.11"
export GRAFANA_NETWORK_ADDRESS="172.30.0.12"
export OPENIM_WEB_PORT=15001