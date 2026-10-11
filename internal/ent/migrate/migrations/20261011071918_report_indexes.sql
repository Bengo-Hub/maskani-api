-- Create index "incident_tenant_id_property_id_occurred_at" to table: "incidents"
CREATE INDEX "incident_tenant_id_property_id_occurred_at" ON "incidents" ("tenant_id", "property_id", "occurred_at");
-- Create index "manualpayment_tenant_id_status_reviewed_at" to table: "manual_payments"
CREATE INDEX "manualpayment_tenant_id_status_reviewed_at" ON "manual_payments" ("tenant_id", "status", "reviewed_at");
