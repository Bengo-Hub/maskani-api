-- Create index "billingrun_tenant_id_created_at_id" to table: "billing_runs"
CREATE INDEX "billingrun_tenant_id_created_at_id" ON "billing_runs" ("tenant_id", "created_at", "id");
-- Create index "billingrun_tenant_id_property_id_created_at_id" to table: "billing_runs"
CREATE INDEX "billingrun_tenant_id_property_id_created_at_id" ON "billing_runs" ("tenant_id", "property_id", "created_at", "id");
-- Create index "enquiry_tenant_id_created_at_id" to table: "enquiries"
CREATE INDEX "enquiry_tenant_id_created_at_id" ON "enquiries" ("tenant_id", "created_at", "id");
-- Create index "enquiry_tenant_id_property_id_created_at_id" to table: "enquiries"
CREATE INDEX "enquiry_tenant_id_property_id_created_at_id" ON "enquiries" ("tenant_id", "property_id", "created_at", "id");
-- Create index "incident_tenant_id_created_at_id" to table: "incidents"
CREATE INDEX "incident_tenant_id_created_at_id" ON "incidents" ("tenant_id", "created_at", "id");
-- Create index "incident_tenant_id_property_id_created_at_id" to table: "incidents"
CREATE INDEX "incident_tenant_id_property_id_created_at_id" ON "incidents" ("tenant_id", "property_id", "created_at", "id");
-- Drop index "notice_tenant_id_created_at" from table: "notices" (superseded by the keyset index below)
DROP INDEX IF EXISTS "notice_tenant_id_created_at";
-- Create index "notice_tenant_id_created_at_id" to table: "notices"
CREATE INDEX "notice_tenant_id_created_at_id" ON "notices" ("tenant_id", "created_at", "id");
-- Create index "notice_tenant_id_property_id_created_at_id" to table: "notices"
CREATE INDEX "notice_tenant_id_property_id_created_at_id" ON "notices" ("tenant_id", "property_id", "created_at", "id");
-- Drop index "reservation_tenant_id_unit_id" from table: "reservations"
DROP INDEX "reservation_tenant_id_unit_id";
-- Create index "reservation_tenant_id_unit_id" to table: "reservations"
CREATE UNIQUE INDEX "reservation_tenant_id_unit_id" ON "reservations" ("tenant_id", "unit_id") WHERE ((status)::text = ANY ((ARRAY['pending_payment'::character varying, 'active'::character varying])::text[]));
-- Create index "reservation_tenant_id_created_at_id" to table: "reservations"
CREATE INDEX "reservation_tenant_id_created_at_id" ON "reservations" ("tenant_id", "created_at", "id");
-- Create index "salecontract_tenant_id_created_at_id" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_created_at_id" ON "sale_contracts" ("tenant_id", "created_at", "id");
-- Create index "salecontract_tenant_id_property_id_created_at_id" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_property_id_created_at_id" ON "sale_contracts" ("tenant_id", "property_id", "created_at", "id");
-- Create index "unitaccount_tenant_id_balance_id" to table: "unit_accounts"
CREATE INDEX "unitaccount_tenant_id_balance_id" ON "unit_accounts" ("tenant_id", "balance", "id");
-- Create index "unitaccount_tenant_id_created_at_id" to table: "unit_accounts"
CREATE INDEX "unitaccount_tenant_id_created_at_id" ON "unit_accounts" ("tenant_id", "created_at", "id");
-- Create index "vendor_tenant_id_created_at_id" to table: "vendors"
CREATE INDEX "vendor_tenant_id_created_at_id" ON "vendors" ("tenant_id", "created_at", "id");
-- Create index "visitorpass_tenant_id_created_at_id" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_created_at_id" ON "visitor_passes" ("tenant_id", "created_at", "id");
-- Create index "visitorpass_tenant_id_property_id_created_at_id" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_property_id_created_at_id" ON "visitor_passes" ("tenant_id", "property_id", "created_at", "id");
-- Drop index "workorder_tenant_id_resolution_due_at" from table: "work_orders"
DROP INDEX "workorder_tenant_id_resolution_due_at";
-- Create index "workorder_tenant_id_resolution_due_at" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_resolution_due_at" ON "work_orders" ("tenant_id", "resolution_due_at") WHERE ((status)::text <> ALL ((ARRAY['completed'::character varying, 'confirmed'::character varying, 'closed'::character varying, 'cancelled'::character varying])::text[]));
