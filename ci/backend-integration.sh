#!/usr/bin/env bash
# This script refuses local machines. It only manages a fresh GitHub-hosted VM.
set -euo pipefail
umask 077

if [[ ${GITHUB_ACTIONS:-} != true || ${RUNNER_ENVIRONMENT:-} != github-hosted || ${RUNNER_OS:-} != Linux ]]; then
  printf '%s\n' 'Disposable GitHub-hosted Linux runner required; local execution refused.' >&2
  exit 2
fi
[[ ${GITHUB_RUN_ID:-} =~ ^[0-9]+$ && ${GITHUB_RUN_ATTEMPT:-} =~ ^[0-9]+$ ]] || exit 2
repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
result_dir="$repo_dir/ci-results/backend-integration"
fixture_root="/tmp/luobo-it-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
socket_dir="$fixture_root/references/integration/redis8.2"
mongo_name="luobo-it-mongo-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
redis_name="luobo-it-redis-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
run_label="${GITHUB_RUN_ID}:${GITHUB_RUN_ATTEMPT}"
mongo_image='mongo:8.0.15@sha256:8332f93f6cb881db35f58caaea2e986e6044f97a3cf2629515875361b3ae359c'
redis_image='redis:8.2.2@sha256:3e3982fbc94dd878eaafd21c068aa8ed9f6246440676849cca46bf46f9c4012f'

owned_container() {
  local actual
  actual=$(docker container inspect --format '{{ index .Config.Labels "luobo.ci.run" }}' "$1" 2>/dev/null) || return 1
  [[ $actual == "$run_label" ]]
}
mongo_eval() {
  timeout --signal=TERM --kill-after=5s 20s docker exec "$mongo_name" \
    mongosh --quiet --host 127.0.0.1 --port 37017 admin --eval "$1"
}
redis_cli() {
  timeout --signal=TERM --kill-after=5s 10s docker exec "$redis_name" redis-cli --raw -h 127.0.0.1 -p 36379 "$@"
}

case ${1:-} in
  prepare)
    [[ ! -e $fixture_root && ! -L $fixture_root && ! -e $result_dir ]] || {
      printf '%s\n' 'Fresh fixture/output paths required; no resume or overwrite.' >&2; exit 2;
    }
    mkdir -p "$repo_dir/ci-results"
    mkdir -m 700 "$result_dir" "$fixture_root"
    mkdir -p "$socket_dir"
    [[ ${#socket_dir} -lt 95 ]] || exit 2
    docker info --format '{{.ServerVersion}}' >"$result_dir/docker-version.txt"
    if docker container inspect "$mongo_name" >/dev/null 2>&1 || docker container inspect "$redis_name" >/dev/null 2>&1; then
      printf '%s\n' 'Container-name collision; nothing reused.' >&2; exit 2
    fi
    # Pull exact linux/amd64 manifests; never resolve a mutable tag at runtime.
    docker pull --platform linux/amd64 "$mongo_image" >"$result_dir/mongo-pull.log" 2>&1
    docker pull --platform linux/amd64 "$redis_image" >"$result_dir/redis-pull.log" 2>&1
    docker image inspect --format '{{json .RepoDigests}}' "$mongo_image" >"$result_dir/mongo-image.json"
    docker image inspect --format '{{json .RepoDigests}}' "$redis_image" >"$result_dir/redis-image.json"
    # Host networking makes the RS advertised loopback endpoint identical for
    # host Go clients and mongod. Both daemons bind only 127.0.0.1, never 0.0.0.0.
    docker run -d --platform linux/amd64 --name "$mongo_name" --label "luobo.ci.run=$run_label" \
      --network host --memory 2g --cpus 2 \
      --mount type=tmpfs,destination=/data/db,tmpfs-size=1073741824 \
      --mount type=tmpfs,destination=/data/configdb,tmpfs-size=67108864 \
      "$mongo_image" mongod --replSet luoboIntegration --bind_ip 127.0.0.1 \
      --port 37017 --wiredTigerCacheSizeGB 0.5 >"$result_dir/mongo-container-id.txt"
    docker run -d --platform linux/amd64 --name "$redis_name" --label "luobo.ci.run=$run_label" \
      --network host --memory 256m --cpus 1 --user "$(id -u):$(id -g)" \
      --mount "type=bind,source=$socket_dir,destination=/fixture" \
      "$redis_image" redis-server --bind 127.0.0.1 --port 36379 \
      --save '' --appendonly no --unixsocket /fixture/redis.sock --unixsocketperm 700 \
      >"$result_dir/redis-container-id.txt"
    available=false
    for _ in $(seq 1 45); do
      if mongo_eval 'if (db.runCommand({ping:1}).ok !== 1) quit(1)' >/dev/null 2>&1; then available=true; break; fi
      sleep 2
    done
    [[ $available == true ]] || { printf '%s\n' 'Mongo startup not confirmed.' >&2; exit 1; }
    # Fresh instance only: initiate once. Unknown result is not replayed.
    mongo_eval 'const r=rs.initiate({_id:"luoboIntegration",members:[{_id:0,host:"127.0.0.1:37017"}]}); if(r.ok!==1) quit(1)' \
      >"$result_dir/replica-init.log" 2>&1
    available=false
    for _ in $(seq 1 60); do
      if mongo_eval 'const h=db.hello(); if(h.setName!=="luoboIntegration" || h.isWritablePrimary!==true || db.version()!=="8.0.15") quit(1)' >/dev/null 2>&1; then available=true; break; fi
      sleep 1
    done
    [[ $available == true ]] || { printf '%s\n' 'Writable replica-set primary not confirmed.' >&2; exit 1; }
    [[ $(redis_cli PING) == PONG && -S $socket_dir/redis.sock ]] || exit 1
    redis_cli INFO server | tr -d '\r' | grep -x 'redis_version:8.2.2' >"$result_dir/redis-version.txt"
    mongo_eval 'print(JSON.stringify({setName:db.hello().setName,isWritablePrimary:db.hello().isWritablePrimary,version:db.version(),userDatabases:db.adminCommand({listDatabases:1,nameOnly:true}).databases.filter(x=>!["admin","config","local"].includes(x.name)).length}))' >"$result_dir/mongo-initial.json"
    [[ $(redis_cli -n 15 DBSIZE) == 0 ]] || exit 1
    [[ ! -e $repo_dir/server/config/config.yaml && ! -L $repo_dir/server/config/config.yaml ]] || {
      printf '%s\n' 'Existing runtime config refused.' >&2; exit 2;
    }
    cp "$repo_dir/server/config/config.example.yaml" "$repo_dir/server/config/config.yaml"
    python3 -B "$repo_dir/ci/backend-integration.py" ready "$result_dir" "$run_label" "$GITHUB_SHA"
    ;;
  test)
    [[ -f $result_dir/fixture-ready.json ]] || exit 2
    owned_container "$mongo_name" && owned_container "$redis_name" || exit 2
    python3 -B "$repo_dir/ci/backend-integration.py" check-ready "$result_dir" "$run_label" "$GITHUB_SHA"
    export LUOBO_INTEGRATION_MONGO_URI='mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
    export LUOBO_BOOKING_TEST_MONGO_URI="$LUOBO_INTEGRATION_MONGO_URI"
    export LUOBO_RETENTION_TEST_MONGO="$LUOBO_INTEGRATION_MONGO_URI"
    export LUOBO_MONGO_TEST_URI="$LUOBO_INTEGRATION_MONGO_URI"
    export LUOBO_STORAGE_TEST_MONGO_URI="$LUOBO_INTEGRATION_MONGO_URI"
    export LUOBO_INTEGRATION_REDIS_SOCKET="$socket_dir/redis.sock"
    export LUOBO_BOOKING_TEST_REDIS_ADDR='127.0.0.1:36379'
    export LUOBO_RETENTION_TEST_REDIS='redis://127.0.0.1:36379/15'
    export GOWORK=off GOTOOLCHAIN=local GOMAXPROCS=2
    export LUOBO_EMAIL_INTEGRATION=0 LUOBO_RUN_LEGACY_LOAD_TESTS=0 LUOBO_RUN_LEGACY_EXAMPLES=0
    cd "$repo_dir/server"
    failed=0
    while IFS= read -r package; do
      name=${package#./}; name=${name//\//_}
      output="$result_dir/$name"; mkdir "$output"
      # -count=1 prevents a cached pass from replacing actual fixture execution.
      # No package/test concurrency; individual intentional race tests remain.
      set +e
      timeout --signal=TERM --kill-after=20s 20m go test -mod=readonly -race -json -count=1 -p=1 -parallel=1 -timeout=12m "$package" \
        >"$output/tests.jsonl" 2>"$output/tests.stderr.log"
      test_status=$?
      timeout --signal=TERM --kill-after=20s 10m go vet -mod=readonly -p=1 "$package" >"$output/vet.log" 2>&1
      vet_status=$?
      python3 -B "$repo_dir/ci/backend-integration.py" summary "$output" "$package" "$test_status" "$vet_status"
      summary_status=$?
      set -e
      if [[ $summary_status -ne 0 ]]; then failed=1; fi
    done < <(python3 -B "$repo_dir/ci/backend-integration.py" packages)
    python3 -B "$repo_dir/ci/backend-integration.py" aggregate "$result_dir" || failed=1
    exit "$failed"
    ;;
  collect)
    mkdir -p "$result_dir"
    failed=0
    for name in "$mongo_name" "$redis_name"; do
      if owned_container "$name"; then
        timeout --signal=TERM --kill-after=5s 20s docker logs "$name" >"$result_dir/$name.log" 2>&1 || failed=1
        docker container inspect --format '{{json .State}}' "$name" >"$result_dir/$name-state.json" || failed=1
      fi
    done
    if [[ -f $result_dir/fixture-ready.json ]]; then
      owned_container "$mongo_name" && owned_container "$redis_name" || exit 1
      mongo_eval 'const h=db.hello(); if(h.setName!=="luoboIntegration" || h.isWritablePrimary!==true) quit(1); const d=db.adminCommand({listDatabases:1,nameOnly:true}); if(d.ok!==1) quit(1); print(JSON.stringify({userDatabases:d.databases.filter(x=>!["admin","config","local"].includes(x.name)).map(x=>x.name)}))' >"$result_dir/mongo-final.json" || failed=1
      redis_cli -n 15 DBSIZE >"$result_dir/redis-final-count.txt" || failed=1
      redis_cli INFO keyspace >"$result_dir/redis-final-keyspace.txt" || failed=1
      python3 -B "$repo_dir/ci/backend-integration.py" residuals "$result_dir" || failed=1
    else
      printf '%s\n' '{"state":"fixture_setup_incomplete","integration_pass_claimed":false}' >"$result_dir/fixture-incomplete.json"
    fi
    exit "$failed"
    ;;
  cleanup)
    mkdir -p "$result_dir"
    failed=0
    for name in "$mongo_name" "$redis_name"; do
      present=$(docker container ls -a --filter "name=^/$name$" --format '{{.Names}}') || { failed=1; continue; }
      if [[ $present == "$name" ]] && owned_container "$name"; then
        # Remove anonymous volumes too. No host data directory is mounted.
        timeout --signal=TERM --kill-after=5s 30s docker rm -f -v "$name" >>"$result_dir/container-cleanup.log" 2>&1 || failed=1
        present=$(docker container ls -a --filter "name=^/$name$" --format '{{.Names}}') || { failed=1; continue; }
        if [[ -n $present ]]; then failed=1; fi
      elif [[ -n $present ]]; then
        printf '%s\n' 'Container ownership mismatch: no removal authorized.' >>"$result_dir/container-cleanup.log"
        failed=1
      fi
    done
    printf '{"container_cleanup_exit_code":%s,"scope":"exact_run_label_and_two_names_only"}\n' "$failed" >"$result_dir/container-cleanup.json"
    exit "$failed"
    ;;
  *) printf '%s\n' 'Expected prepare, test, collect or cleanup.' >&2; exit 2 ;;
esac
