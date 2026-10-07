# Sprint 0: bootstrap, devops and platform registration

Goal: three repos building and deploying through CI, every dependency registered, docs complete.
SRDD week 1 (discovery, data audit, architecture approval, environments).

## Scope

| Item | Detail | Demo slice |
|---|---|---|
| Docs | README, plan, architecture, integrations, ERD, API spec, events, data performance, backlog, sprints for all three repos | Yes |
| maskani-api scaffold | `cmd/{api,migrate,seed,seed-tenant,hashmigrations}`, config, logger, database, events (outbox), tenant syncer, identity, RBAC, audit log, auth-api client, subscriptions gate, tenant guard, health, Swagger | Yes |
| maskani-ui and maskani-commerce scaffolds | Next 16, React 19, Tailwind v4, shadcn, shared-ui-lib pinned tag, brand assets from `shared-docs/brand/maskani` | Yes |
| GitHub | `Bengo-Hub/maskani-api`, `maskani-ui`, `maskani-commerce` (public), default branch `main`, deploy workflow, secrets synced through devops-k8s `sync-secrets.yml` | Yes |
| devops-k8s | `apps/maskani-{api,ui,commerce}`, namespace `maskani`, pgbouncer `maskani` and `maskani_ro`, `create-service-secrets.sh` case for `INTERNAL_SERVICE_KEY`, network policies, Cloudflare origin hosts, fleet-health-watcher URLs | Yes |
| auth-api | OAuth client `maskani-ui` (prod host `maskaniapp.codevertexafrica.com`) | Yes |
| subscriptions-api | `plans_maskani.go`, feature catalog codes, plan feature matrix doc | Yes |
| notifications-api | Subscribe to `maskani.>`; SMS templates | Yes |
| shared-ui-lib | App switcher entry `maskani` (new tag; local copy in maskani-ui until it ships) | Yes |

## Progress

| Date | Item | State |
|---|---|---|
| 2026-10-07 | Docs for all three repos | Done |
| 2026-10-07 | SRDD moved to the private docs repo | Done |
| 2026-10-07 | go.mod, config, migrate (lock 727271007), entrypoint, Dockerfile, logger, database, NATS stream | Done |
| 2026-10-07 | Full R1 Ent schema with the tenant guard mixin; `go generate` clean | Done |
| 2026-10-07 | Tenant syncer, RBAC catalogue and service | Done |
| 2026-10-07 | Access middleware, module gate, handlers, first migration, build | In progress |
| | UI scaffolds, devops-k8s apps, GitHub repos, platform registration | Pending |

## Acceptance

- `go build`, `go vet`, `go test` green; `pnpm build` green for both UIs.
- First migration applied by the migrate binary in the cluster; `/healthz` and `/readyz` 200.
- `https://maskaniapi.codevertexafrica.com/healthz`, `https://maskaniapp.codevertexafrica.com`,
  `https://maskani.codevertexafrica.com` respond through Cloudflare with valid TLS.
- `/auth/me` returns the signed-in staff user with tenant and modules.

## Rules to apply in this sprint

Standing backend rules in [README.md](README.md), plus:

| Scenario | Rule | Memory file |
|---|---|---|
| Choosing env names | Treasury and hospital naming (`POSTGRES_URL`, `POSTGRES_MIGRATE_URL`, `AUTH_*`, `*_SERVICE_URL`); never `DATABASE_URL` | `feedback_workflow_rules.md` |
| Shared Go modules | Pinned tags only; auth-client through the module `replace`; a library change needs a new tag before services consume it | `reference_shared_go_module_release.md` |
| Image tags | Set only by CI (`update_helm_values`); never edit `image.tag` or run `build.sh` by hand | `feedback_platform_level_configs.md` |
| Secrets | Names only in workflows (`${{ secrets.X }}`); values never in tracked files; `.env` and `KubeSecrets/` ignored | `feedback_no_secrets_in_code.md` |
| Network policies | Before restricting ingress, map every consumer from values.yaml and `config.go` defaults | `feedback_network_policy_consumer_mapping.md` |
| Hostnames | Verify live (DNS, ingress) before using them in config; never from memory | `feedback_verify_hostnames_live_not_from_memory.md` |
| Node headroom | Single node near 75% CPU requests; lean requests (50m), check `kubectl top` with the user's permission before adding replicas | `ha-min-2-pods-and-pdb.md` |
| devops-k8s pushes | Rebase on the release bot's commits; ArgoCD self-heal means commit before apply | `reference_devops_k8s_release_bot_ci_race.md` |
| Commits | Explicit paths, attribution line, push to `main` once builds are green | `dev-workflow-and-deploy.md`, `feedback_git_no_approval.md` |
