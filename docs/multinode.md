# Multi-node services

A server service can run on several nodes. Hoist deploys it to each of
them and treats the first node as the one that stands for the service.

## Config

A server environment lists its nodes under `nodes`, in deploy order. One
entry is the common case.

```yaml
services:
  backend:
    type: server
    image: 533267421080.dkr.ecr.us-east-1.amazonaws.com/demoagent-backend
    port: 3000
    healthcheck: /status
    env:
      staging:
        nodes: [staging]
        host: backend-staging.svc.naoma.internal
        envfile: /etc/demoagent/backend-staging.env
      prod:
        nodes: [backend-prod, backend-prod-2]
        host: backend-prod.svc.naoma.internal
        envfile: /etc/demoagent/backend-prod.env
```

Every node in the list must be declared under `nodes` at the top of the
config. `host` and `envfile` are the same on every node: each node's
Traefik matches the same host, and each node reads its envfile from the
same path. What differs between nodes lives inside the envfile.

A cronjob environment keeps `node`, a single name. A cron job runs in one
place.

## Deploy

A deploy runs the same sequence on each node, in list order: pull the
image, start the new container, wait for its health check, stop and
remove the old containers. The next node starts when the previous one is
done.

A failure stops the sequence. Nodes before the failed one run the new
tag; the failed one keeps its old container, since the new one is
removed on a failed health check; nodes after it are untouched. The
rollback prompt then deploys the previous tag to every node of the
service.

Log lines carry the node name when a service has more than one node.

## Current and previous

The first node stands for the service. The build picker's live tag, the
rollback's previous tag, `status`, and `logs` all read it. A rollout
writes the first node first and a rollback reads it, so it is the node
whose state the rest are meant to match.

## Prune

After all deploys, images unused for over a week are pruned once on
every node a deploy touched.
