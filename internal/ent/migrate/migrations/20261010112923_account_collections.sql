-- Create "account_collections" table
CREATE TABLE "account_collections" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_account_id" uuid NOT NULL, "unit_id" uuid NOT NULL, "property_id" uuid NOT NULL, "fund_id" uuid NOT NULL, "day" date NOT NULL, "amount" numeric(18,2) NOT NULL, "payments_count" bigint NOT NULL DEFAULT 0, PRIMARY KEY ("id"));
-- Create index "accountcollection_tenant_id_property_id_day" to table: "account_collections"
CREATE INDEX "accountcollection_tenant_id_property_id_day" ON "account_collections" ("tenant_id", "property_id", "day");
-- Create index "accountcollection_tenant_id_unit_account_id_day" to table: "account_collections"
CREATE UNIQUE INDEX "accountcollection_tenant_id_unit_account_id_day" ON "account_collections" ("tenant_id", "unit_account_id", "day");
