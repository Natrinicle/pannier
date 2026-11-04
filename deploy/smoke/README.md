# Pannier smoke

Loopback MinIO (`sourcemation/minio`, Docker Hub `minio/minio` is gone) + a tiny Postgres/Adminer fixture + pannierd in rootless
Docker. Bags **only** `pannier-fixture-db` and `pannier-fixture-web`.
Restores the dump into `pannier-verify-*`, checks the canary row, then
removes **only** those verify containers.

Does not bag the rest of the engine. Does not `compose down -v` live
stacks. Kind uses [kind.yaml](kind.yaml) with no host 80/443 mappings.

```sh
export DOCKER_HOST=unix:///run/user/1000/docker.sock
bash deploy/smoke/run.sh
# rootless: the script sets PANNIER_UID=0 so binds stay readable
```

Tear down the smoke originals later (not the script default):

```sh
docker compose -f deploy/smoke/docker-compose.yaml --project-name pannier-smoke down
# bind data stays under ~/.var/pannier, ~/.var/pannier-fixture, ~/.local/share/pannier-minio
kind delete cluster --name pannier-smoke   # only if this script created it; never delete 'kind'
```
