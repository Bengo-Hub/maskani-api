# maskani-api

Go service for **Maskani by Codevertex**, the multi-tenant property management and real estate
platform. It owns the property domain: portfolios, properties, blocks, units, parties, charge
rules, meters and readings, billing runs, unit sales and instalments, works and vendors, gate and
visitors, notices, documents, listings and statistics.

It does **not** move money. Invoices, payments, payouts, ledgers and tax live in treasury-api;
identity, tenants and branches in auth-api; staff and payroll in erp-api; messaging in
notifications-api; plans and limits in subscriptions-api. maskani-api stores their IDs and never
keeps a second copy.

| Item | Value |
|---|---|
| Public host | `https://maskaniapi.codevertexafrica.com` |
| In-cluster | `http://maskani-api.maskani.svc.cluster.local:4000` |
| Base path | `/api/v1/{tenant}/maskani/...` |
| S2S path | `/api/v1/s2s/{tenant}/maskani/...` (header `X-API-Key`) |
| Public market path | `/api/v1/market/...` (rate limited, no auth) |
| Swagger | `/v1/docs/` |
| Event stream | NATS JetStream `maskani`, subjects `maskani.{event}` |
| Migration lock key | `727271007` |
| Namespace | `maskani` |

## Documents

| File | Content |
|---|---|
| [plan.md](plan.md) | Product plan, releases, demo slice |
| [docs/architecture.md](docs/architecture.md) | Components, tenancy, isolation, module gating, jobs |
| [docs/integrations.md](docs/integrations.md) | Contracts with auth, treasury, erp, notifications, subscriptions |
| [docs/erd.md](docs/erd.md) | Entities, fields, keys and relationships |
| [docs/api-spec.md](docs/api-spec.md) | Endpoints by area, access rules |
| [docs/events.md](docs/events.md) | Published and consumed events |
| [docs/data-performance.md](docs/data-performance.md) | Indexes, pagination, aggregates, retention |
| [docs/backlog.md](docs/backlog.md) | Deferred items and known gaps |
| [docs/sprints/](docs/sprints/README.md) | Sprint plans with rules to apply |

## Local development

```bash
cp .env.example .env            # fill local values, never commit .env
go generate ./internal/ent/...  # after any schema change
go run ./cmd/migrate            # applies migrations (advisory locked)
go run ./cmd/seed               # platform default catalogues, roles
go run ./cmd/api
```

Generating a migration after a schema change uses the local PG17 `ent_dev` schema:

```bash
go run -mod=mod internal/ent/migrate/main.go <migration_name>
go run ./cmd/hashmigrations     # only after hand-writing a .sql file
```

## Deployment

Push to `main` runs `.github/workflows/deploy.yml`: secrets check, image build and push
(`docker.io/codevertex/maskani-api:<sha8>`), database and secret provisioning through devops-k8s
scripts, then the Helm values tag bump in `devops-k8s/apps/maskani-api/values.yaml`. ArgoCD rolls
it out. Never run `build.sh` by hand against production.
