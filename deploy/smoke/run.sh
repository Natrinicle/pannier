#!/usr/bin/env bash
# MinIO + tiny Postgres/Adminer fixture + pannierd. Restore into
# pannier-verify-* only. Never docker rm originals. Never kind delete "kind".
set -euo pipefail

SMOKE="$(cd "$(dirname "$0")" && pwd)"
export DOCKER_HOST="${DOCKER_HOST:-unix:///run/user/${UID:-1000}/docker.sock}"
# Rootless maps the host user to container uid 0. A user: 1000 override
# cannot read host 0600 binds. Rootful cells keep PANNIER_UID=$(id -u).
if docker info -f '{{.SecurityOptions}}' 2>/dev/null | grep -q rootless; then
	export PANNIER_UID="${PANNIER_UID:-0}"
	export PANNIER_GID="${PANNIER_GID:-0}"
else
	export PANNIER_UID="${PANNIER_UID:-$(id -u)}"
	export PANNIER_GID="${PANNIER_GID:-$(id -g)}"
fi

DATA="${HOME}/.var/pannier"
FIX="${HOME}/.var/pannier-fixture"
MINIO_DATA="${HOME}/.local/share/pannier-minio"
MINIO_KEYS="${HOME}/.var/minio"
COMPOSE=(docker compose -f "${SMOKE}/docker-compose.yaml" --project-name pannier-smoke)

DENY_PARCEL='traefik|n8n|icinga|open-webui|kind-|mempalace|speakr|whisper|wud|ollama'

log() { printf '%s\n' "$*"; }
die() {
	printf 'smoke: %s\n' "$*" >&2
	exit 1
}

wait_http() {
	local url="$1" n="${2:-60}"
	local i
	for i in $(seq 1 "${n}"); do
		if curl -fsS -o /dev/null --max-time 2 "${url}"; then
			return 0
		fi
		sleep 2
	done
	return 1
}

umask 077
mkdir -p "${DATA}/repo" "${DATA}/cache/kopia" \
	"${FIX}/secrets" \
	"${MINIO_DATA}" "${MINIO_KEYS}"
# Existing rootless Postgres binds may be subuid-owned (1777). Do not chmod them.
if [[ ! -d "${FIX}/db" ]]; then
	mkdir -p "${FIX}/db"
fi

if [[ ! -f "${MINIO_KEYS}/root-user" ]]; then
	openssl rand -hex 8 | tr -d '\n' >"${MINIO_KEYS}/root-user"
	openssl rand -hex 20 | tr -d '\n' >"${MINIO_KEYS}/root-password"
fi
if [[ ! -f "${FIX}/secrets/password" ]]; then
	openssl rand -hex 12 | tr -d '\n' >"${FIX}/secrets/password"
fi
if [[ ! -f "${DATA}/repo.password" ]]; then
	openssl rand -hex 16 | tr -d '\n' >"${DATA}/repo.password"
fi
if [[ ! -s "${DATA}/console.password" ]]; then
	openssl rand -hex 12 | tr -d '\n' >"${DATA}/console.password"
fi
# MinIO rejects keys that still have a trailing newline from an earlier run.
python3 -c 'from pathlib import Path
for p in Path("'"${MINIO_KEYS}"'").glob("root-*"):
    p.write_text(p.read_text().strip())
for p in [Path("'"${FIX}/secrets/password"'"), Path("'"${DATA}/repo.password"'"), Path("'"${DATA}/console.password"'")]:
    if p.is_file():
        p.write_text(p.read_text().strip())
'
cp -f "${MINIO_KEYS}/root-user" "${DATA}/ingest-access"
cp -f "${MINIO_KEYS}/root-password" "${DATA}/ingest-secret"
cp -f "${FIX}/secrets/password" "${DATA}/pg.password"
chmod 600 "${DATA}/ingest-access" "${DATA}/ingest-secret" "${DATA}/repo.password" \
	"${DATA}/console.password" "${DATA}/pg.password" \
	"${MINIO_KEYS}/root-user" "${MINIO_KEYS}/root-password" "${FIX}/secrets/password"

export PANNIER_PASSWORD
PANNIER_PASSWORD="$(tr -d '\n' <"${DATA}/console.password")"

cat >"${DATA}/pannier.yaml" <<EOF
engine: kopia
listen: 0.0.0.0:3477
dataDir: /data
selfInclude: true
localRepo:
  path: /data/repo
  passwordFile: /data/repo.password
  kopiaConfig: /data/kopia.config
  binary: kopia
remotes:
  - name: minio-smoke
    endpoint: minio:9000
    bucket: pannier-packs
    prefix: smoke/
    region: us-east-1
    disableTLS: true
    objectLock: true
    liveRepo: false
    ingestCreds:
      accessKeyFile: /data/ingest-access
      secretKeyFile: /data/ingest-secret
selection:
  docker: true
  k8s: false
  lxc: false
  bareMetal: false
  controlPlane: false
  containers:
    - pannier-fixture-db
    - pannier-fixture-web
  skipContainers:
    - pannier-minio
    - pannierd
    - pannier-verify-db
    - pannier-verify-web
dumps:
  - name: appdb
    kind: postgres
    filename: app.dump
    command:
      - docker
      - exec
      - pannier-fixture-db
      - pg_dump
      - -U
      - pannier
      - --no-owner
      - --no-acl
      - pannier
retention:
  keepLast: 7
  prune: false
EOF

mapfile -t BEFORE < <(docker ps -a --format '{{.Names}}' | sort)
log "pre-existing containers: ${#BEFORE[@]}"

"${COMPOSE[@]}" up -d --build --force-recreate

wait_http "http://127.0.0.1:19000/minio/health/live" 40 || die "MinIO did not become ready on 127.0.0.1:19000"
wait_http "http://127.0.0.1:3477/healthz" 40 || {
	docker logs pannierd 2>&1 | tail -n 80
	die "pannierd did not become ready on 127.0.0.1:3477"
}

python3 "${SMOKE}/create-bucket.py" "http://127.0.0.1:19000" pannier-packs \
	"${DATA}/ingest-access" "${DATA}/ingest-secret"

for i in $(seq 1 40); do
	if docker exec pannier-fixture-db pg_isready -U pannier -d pannier >/dev/null 2>&1; then
		break
	fi
	if [[ "${i}" -eq 40 ]]; then
		docker logs pannier-fixture-db 2>&1 | tail -n 80
		die "fixture-db did not become ready"
	fi
	sleep 2
done

docker exec -e PGPASSWORD="$(tr -d '\n' <"${FIX}/secrets/password")" pannier-fixture-db \
	psql -U pannier -d pannier -c "INSERT INTO canary (id, note) VALUES (1, 'pannier-smoke-canary') ON CONFLICT (id) DO UPDATE SET note = EXCLUDED.note;"

PARCELS="$(curl -fsS -H "Authorization: Bearer ${PANNIER_PASSWORD}" http://127.0.0.1:3477/v1/parcels)"
printf '%s\n' "${PARCELS}"
if printf '%s' "${PARCELS}" | grep -Eiq "${DENY_PARCEL}"; then
	die "selection leaked live stacks; refusing to bag. parcels=${PARCELS}"
fi
printf '%s' "${PARCELS}" | grep -q 'pannier-fixture-db' || die "fixture-db missing from parcels"

curl -fsS -H "Authorization: Bearer ${PANNIER_PASSWORD}" -X POST http://127.0.0.1:3477/v1/backup >/dev/null
log "backup started"
for i in $(seq 1 180); do
	STATUS="$(curl -fsS -H "Authorization: Bearer ${PANNIER_PASSWORD}" http://127.0.0.1:3477/v1/status)"
	if printf '%s' "${STATUS}" | grep -q '"running": false'; then
		break
	fi
	sleep 2
done
printf '%s\n' "${STATUS}"
if printf '%s' "${STATUS}" | grep -q '"running": true'; then
	docker logs pannierd 2>&1 | tail -n 120
	die "backup still running after wait"
fi
if printf '%s' "${STATUS}" | grep -E '"lastErr": "[^"]+"'; then
	LASTERR="$(printf '%s' "${STATUS}" | sed -n 's/.*"lastErr": "\([^"]*\)".*/\1/p')"
	if [[ -n "${LASTERR}" ]]; then
		docker logs pannierd 2>&1 | tail -n 120
		die "backup lastErr=${LASTERR}"
	fi
fi

BAGS="$(curl -fsS -H "Authorization: Bearer ${PANNIER_PASSWORD}" http://127.0.0.1:3477/v1/bags)"
printf '%s\n' "${BAGS}"
BAG_ID="$(python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["id"] if d else "")' <<<"${BAGS}")"
[[ -n "${BAG_ID}" ]] || die "no bag id"
log "bag ${BAG_ID}"

RESTORE="$(curl -fsS -H "Authorization: Bearer ${PANNIER_PASSWORD}" \
	-H "Content-Type: application/json" \
	-d "{\"bagId\":\"${BAG_ID}\",\"items\":[\"appdb\"]}" \
	http://127.0.0.1:3477/v1/restore)"
printf '%s\n' "${RESTORE}"
DUMP="$(find "${DATA}/restore/${BAG_ID}" -type f \( -name 'app.dump' -o -name '*.dump' -o -name '*appdb*' \) | head -n 1 || true)"
if [[ -z "${DUMP}" ]]; then
	find "${DATA}/restore/${BAG_ID}" -type f | head -n 40
	die "restored dump not found under ${DATA}/restore/${BAG_ID}"
fi
log "restored dump ${DUMP} ($(wc -c <"${DUMP}") bytes)"

docker rm -f pannier-verify-db pannier-verify-web >/dev/null 2>&1 || true
docker network inspect pannier-verify >/dev/null 2>&1 || docker network create pannier-verify >/dev/null

# Throwaway verify DB: anonymous volume, image USER (postgres uid). A
# host bind at 0700 plus --user 0 in rootless still drops to subuid 70
# and cannot mkdir PGDATA.
docker run -d --name pannier-verify-db --network pannier-verify \
	-e POSTGRES_USER=pannier \
	-e POSTGRES_DB=pannier \
	-e POSTGRES_PASSWORD="$(tr -d '\n' <"${FIX}/secrets/password")" \
	-p 127.0.0.1:18433:5432 \
	postgres:16.6-alpine >/dev/null

for i in $(seq 1 40); do
	if docker exec pannier-verify-db pg_isready -U pannier -d pannier >/dev/null 2>&1; then
		break
	fi
	if [[ "${i}" -eq 40 ]]; then
		docker logs pannier-verify-db 2>&1 | tail -n 80
		die "verify-db did not become ready"
	fi
	sleep 2
done

docker exec -i -e PGPASSWORD="$(tr -d '\n' <"${FIX}/secrets/password")" pannier-verify-db \
	psql -U pannier -d pannier <"${DUMP}"
NOTE="$(docker exec -e PGPASSWORD="$(tr -d '\n' <"${FIX}/secrets/password")" pannier-verify-db \
	psql -U pannier -d pannier -Atc "SELECT note FROM canary WHERE id = 1;")"
[[ "${NOTE}" == "pannier-smoke-canary" ]] || die "canary mismatch: ${NOTE}"
log "verify-db canary ok"

docker run -d --name pannier-verify-web --network pannier-verify \
	-e ADMINER_DEFAULT_SERVER=pannier-verify-db \
	-p 127.0.0.1:18481:8080 \
	adminer:4.8.1 >/dev/null
wait_http "http://127.0.0.1:18481/" 20 || log "verify-web HTTP not ready (non-fatal after canary)"

log "tearing down verify containers only"
docker rm -fv pannier-verify-db pannier-verify-web
docker network rm pannier-verify >/dev/null 2>&1 || true

docker inspect -f '{{.State.Status}}' pannier-fixture-db pannier-fixture-web pannier-minio pannierd | grep -qx running
docker inspect -f '{{.State.Status}}' pannier-fixture-db | grep -qx running
[[ -d "${FIX}/db" ]] || die "original fixture bind missing"

mapfile -t AFTER < <(docker ps -a --format '{{.Names}}' | sort)
for name in "${BEFORE[@]}"; do
	docker inspect "${name}" >/dev/null 2>&1 || die "pre-existing container ${name} disappeared"
done
for name in pannier-verify-db pannier-verify-web; do
	docker inspect "${name}" >/dev/null 2>&1 && die "${name} still present after teardown"
done
log "originals intact; verify containers gone (${#AFTER[@]} names now, ${#BEFORE[@]} before plus smoke stack)"

TRAEFIK_OK=0
if docker inspect traefik >/dev/null 2>&1; then
	docker inspect -f '{{.State.Status}}' traefik | grep -qx running && TRAEFIK_OK=1
fi
ss -ltn | grep -q ':80 ' || log "warning: nothing listening on :80"
ss -ltn | grep -q ':443 ' || log "warning: nothing listening on :443"

if command -v kind >/dev/null 2>&1; then
	if kind get clusters 2>/dev/null | grep -qx pannier-smoke; then
		log "kind cluster pannier-smoke already exists; not recreating"
	else
		# Reuse a local kindest/node digest when present so smoke does
		# not pull a second copy beside the existing 'kind' cluster.
		KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-$(docker images --digests --format '{{.Repository}}\t{{.Digest}}' | awk -F'\t' '$1=="kindest/node" && $2 ~ /^sha256:/ { print "kindest/node@" $2; exit }')}"
		if [[ -n "${KIND_NODE_IMAGE}" ]]; then
			log "kind create pannier-smoke --image ${KIND_NODE_IMAGE}"
			kind create cluster --name pannier-smoke --config "${SMOKE}/kind.yaml" --image "${KIND_NODE_IMAGE}"
		else
			kind create cluster --name pannier-smoke --config "${SMOKE}/kind.yaml"
		fi
	fi
	kubectl --context kind-pannier-smoke get nodes
	kubectl --context kind-pannier-smoke apply -f "${SMOKE}/k8s-fixture.yaml"
	kubectl --context kind-pannier-smoke -n pannier-smoke rollout status deploy/web --timeout=120s
	kubectl --context kind-pannier-smoke -n pannier-smoke get svc web
	if [[ "${TRAEFIK_OK}" -eq 1 ]]; then
		docker inspect -f '{{.State.Status}}' traefik | grep -qx running || die "Traefik stopped after kind create"
	fi
	if ss -ltn | grep -E ':80 |:443 ' >/dev/null; then
		log "host :80/:443 still bound (Traefik path unchanged)"
	fi
	kind delete cluster --name pannier-smoke
	log "deleted kind cluster pannier-smoke (left cluster 'kind' alone)"
else
	log "kind not on PATH; skipped k8s smoke"
fi

if docker inspect kind-worker2 >/dev/null 2>&1; then
	docker inspect -f '{{.State.Status}} {{.Name}}' kind-worker2
fi

log "SMOKE PASS"
log "console http://127.0.0.1:3477/  (password in ${DATA}/console.password)"
log "fixture web http://127.0.0.1:18480/  minio console http://127.0.0.1:19001/"
log "left running: pannierd pannier-minio pannier-fixture-db pannier-fixture-web"
