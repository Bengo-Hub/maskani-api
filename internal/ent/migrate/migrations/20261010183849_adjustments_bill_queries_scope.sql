-- Modify "adjustments" table
ALTER TABLE "adjustments" ADD COLUMN "property_id" uuid NOT NULL;
-- Create index "adjustment_tenant_id_property_id_status_created_at" to table: "adjustments"
CREATE INDEX "adjustment_tenant_id_property_id_status_created_at" ON "adjustments" ("tenant_id", "property_id", "status", "created_at");
-- Create index "adjustment_unit_account_id_created_at" to table: "adjustments"
CREATE INDEX "adjustment_unit_account_id_created_at" ON "adjustments" ("unit_account_id", "created_at");
-- Modify "bill_queries" table
ALTER TABLE "bill_queries" ADD COLUMN "property_id" uuid NOT NULL;
-- Create index "billquery_tenant_id_property_id_status_created_at" to table: "bill_queries"
CREATE INDEX "billquery_tenant_id_property_id_status_created_at" ON "bill_queries" ("tenant_id", "property_id", "status", "created_at");
-- Create index "billquery_unit_account_id_created_at" to table: "bill_queries"
CREATE INDEX "billquery_unit_account_id_created_at" ON "bill_queries" ("unit_account_id", "created_at");
