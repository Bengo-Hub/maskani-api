-- Create index "billingrunline_tenant_id_treasury_invoice_id" to table: "billing_run_lines"
CREATE INDEX "billingrunline_tenant_id_treasury_invoice_id" ON "billing_run_lines" ("tenant_id", "treasury_invoice_id");
-- Create index "billingrun_updated_at" to table: "billing_runs"
CREATE INDEX "billingrun_updated_at" ON "billing_runs" ("updated_at") WHERE ((status)::text = 'issuing'::text);
-- Create index "gatedevice_last_seen_at" to table: "gate_devices"
CREATE INDEX "gatedevice_last_seen_at" ON "gate_devices" ("last_seen_at") WHERE (((status)::text = 'active'::text) AND (offline_alerted = false));
-- Create index "gateevent_occurred_at" to table: "gate_events"
CREATE INDEX "gateevent_occurred_at" ON "gate_events" ("occurred_at");
-- Create index "gateevent_tenant_id_property_id_created_at_id" to table: "gate_events"
CREATE INDEX "gateevent_tenant_id_property_id_created_at_id" ON "gate_events" ("tenant_id", "property_id", "created_at", "id");
-- Create index "instalment_tenant_id_treasury_invoice_id" to table: "instalments"
CREATE INDEX "instalment_tenant_id_treasury_invoice_id" ON "instalments" ("tenant_id", "treasury_invoice_id");
-- Create index "notice_scheduled_at" to table: "notices"
CREATE INDEX "notice_scheduled_at" ON "notices" ("scheduled_at") WHERE ((status)::text = 'scheduled'::text);
-- Create index "party_tenant_id_created_at_id" to table: "parties"
CREATE INDEX "party_tenant_id_created_at_id" ON "parties" ("tenant_id", "created_at", "id");
-- Create index "reservation_expires_at" to table: "reservations"
CREATE INDEX "reservation_expires_at" ON "reservations" ("expires_at") WHERE ((status)::text = ANY ((ARRAY['pending_payment'::character varying, 'active'::character varying])::text[]));
-- Create index "salecontract_tenant_id_unit_account_id" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_unit_account_id" ON "sale_contracts" ("tenant_id", "unit_account_id");
-- Create index "unitaccount_created_at" to table: "unit_accounts"
CREATE INDEX "unitaccount_created_at" ON "unit_accounts" ("created_at") WHERE ((c2b_route_registered_at IS NULL) AND ((status)::text = 'active'::text));
-- Create index "unit_tenant_id_created_at_id" to table: "units"
CREATE INDEX "unit_tenant_id_created_at_id" ON "units" ("tenant_id", "created_at", "id");
-- Create index "unit_tenant_id_property_id_created_at_id" to table: "units"
CREATE INDEX "unit_tenant_id_property_id_created_at_id" ON "units" ("tenant_id", "property_id", "created_at", "id");
-- Create index "workorder_resolution_due_at" to table: "work_orders"
CREATE INDEX "workorder_resolution_due_at" ON "work_orders" ("resolution_due_at") WHERE ((sla_breached = false) AND ((status)::text <> ALL ((ARRAY['completed'::character varying, 'confirmed'::character varying, 'closed'::character varying, 'cancelled'::character varying])::text[])));
-- Create index "workorder_tenant_id_created_at_id" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_created_at_id" ON "work_orders" ("tenant_id", "created_at", "id");
-- Create index "workorder_tenant_id_property_id_created_at_id" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_property_id_created_at_id" ON "work_orders" ("tenant_id", "property_id", "created_at", "id");
