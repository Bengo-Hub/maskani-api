-- Create "manual_payments" table
CREATE TABLE "manual_payments" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_account_id" uuid NOT NULL, "property_id" uuid NOT NULL, "amount" numeric(18,2) NOT NULL, "method" character varying NOT NULL, "reference" character varying NOT NULL, "paid_on" date NOT NULL, "payer_name" character varying NULL, "note" text NULL, "evidence_key" character varying NULL, "status" character varying NOT NULL DEFAULT 'pending', "submitted_by" uuid NOT NULL, "submitted_by_name" character varying NULL, "reviewed_by" uuid NULL, "reviewed_by_name" character varying NULL, "reviewed_at" timestamptz NULL, "review_note" text NULL, "treasury_intent_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "manualpayment_tenant_id_method_reference" to table: "manual_payments"
CREATE UNIQUE INDEX "manualpayment_tenant_id_method_reference" ON "manual_payments" ("tenant_id", "method", "reference") WHERE ((status)::text <> 'rejected'::text);
-- Create index "manualpayment_tenant_id_status_created_at" to table: "manual_payments"
CREATE INDEX "manualpayment_tenant_id_status_created_at" ON "manual_payments" ("tenant_id", "status", "created_at");
-- Create index "manualpayment_tenant_id_unit_account_id_created_at" to table: "manual_payments"
CREATE INDEX "manualpayment_tenant_id_unit_account_id_created_at" ON "manual_payments" ("tenant_id", "unit_account_id", "created_at");
