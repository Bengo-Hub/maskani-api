-- Create "vendor_invoices" table
CREATE TABLE "vendor_invoices" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "vendor_id" uuid NOT NULL, "contract_id" uuid NOT NULL, "property_id" uuid NOT NULL, "invoice_number" character varying NOT NULL, "etims_number" character varying NULL, "invoice_date" timestamptz NOT NULL, "period_start" timestamptz NOT NULL, "period_end" timestamptz NOT NULL, "amount" numeric(18,2) NOT NULL, "vat_amount" numeric(18,2) NOT NULL, "attachments" jsonb NULL, "status" character varying NOT NULL DEFAULT 'submitted', "decided_at" timestamptz NULL, "paid_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "vendorinvoice_tenant_id_contract_id_period_start" to table: "vendor_invoices"
CREATE INDEX "vendorinvoice_tenant_id_contract_id_period_start" ON "vendor_invoices" ("tenant_id", "contract_id", "period_start");
-- Create index "vendorinvoice_tenant_id_property_id_status" to table: "vendor_invoices"
CREATE INDEX "vendorinvoice_tenant_id_property_id_status" ON "vendor_invoices" ("tenant_id", "property_id", "status");
-- Create index "vendorinvoice_tenant_id_vendor_id_invoice_number" to table: "vendor_invoices"
CREATE UNIQUE INDEX "vendorinvoice_tenant_id_vendor_id_invoice_number" ON "vendor_invoices" ("tenant_id", "vendor_id", "invoice_number");
