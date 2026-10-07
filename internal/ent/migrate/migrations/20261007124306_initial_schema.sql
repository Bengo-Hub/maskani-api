-- Create "service_visits" table
CREATE TABLE "service_visits" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "schedule_id" uuid NULL, "contract_id" uuid NOT NULL, "vendor_id" uuid NOT NULL, "property_id" uuid NOT NULL, "due_at" timestamptz NOT NULL, "checked_in_at" timestamptz NULL, "checked_out_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'scheduled', "photos" jsonb NULL, "checklist_result" jsonb NULL, "resident_rating" bigint NULL, PRIMARY KEY ("id"));
-- Create index "servicevisit_tenant_id_contract_id_status" to table: "service_visits"
CREATE INDEX "servicevisit_tenant_id_contract_id_status" ON "service_visits" ("tenant_id", "contract_id", "status");
-- Create index "servicevisit_tenant_id_property_id_due_at" to table: "service_visits"
CREATE INDEX "servicevisit_tenant_id_property_id_due_at" ON "service_visits" ("tenant_id", "property_id", "due_at");
-- Create "occurrence_entries" table
CREATE TABLE "occurrence_entries" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "post_id" uuid NULL, "kind" character varying NOT NULL DEFAULT 'note', "body" text NOT NULL, "author_id" uuid NULL, "author_kind" character varying NULL, PRIMARY KEY ("id"));
-- Create index "occurrenceentry_tenant_id_property_id_created_at" to table: "occurrence_entries"
CREATE INDEX "occurrenceentry_tenant_id_property_id_created_at" ON "occurrence_entries" ("tenant_id", "property_id", "created_at");
-- Create "audit_logs" table
CREATE TABLE "audit_logs" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "actor_user_id" uuid NOT NULL, "actor_email" character varying NULL, "action" character varying NOT NULL, "target_type" character varying NOT NULL, "target_id" uuid NOT NULL, "before" jsonb NULL, "after" jsonb NULL, "ip" character varying NULL, "request_id" character varying NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "auditlog_tenant_id_created_at" to table: "audit_logs"
CREATE INDEX "auditlog_tenant_id_created_at" ON "audit_logs" ("tenant_id", "created_at");
-- Create index "auditlog_tenant_id_target_type_target_id" to table: "audit_logs"
CREATE INDEX "auditlog_tenant_id_target_type_target_id" ON "audit_logs" ("tenant_id", "target_type", "target_id");
-- Create "bill_queries" table
CREATE TABLE "bill_queries" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_account_id" uuid NOT NULL, "treasury_invoice_id" uuid NULL, "party_id" uuid NULL, "subject" character varying NOT NULL, "body" text NOT NULL, "status" character varying NOT NULL DEFAULT 'open', "assigned_to" uuid NULL, "due_by" timestamptz NULL, "resolution" text NULL, PRIMARY KEY ("id"));
-- Create index "billquery_tenant_id_status_due_by" to table: "bill_queries"
CREATE INDEX "billquery_tenant_id_status_due_by" ON "bill_queries" ("tenant_id", "status", "due_by");
-- Create "billing_runs" table
CREATE TABLE "billing_runs" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "fund_id" uuid NOT NULL, "period" character varying NOT NULL, "run_kind" character varying NOT NULL DEFAULT 'regular', "status" character varying NOT NULL DEFAULT 'draft', "invoice_date" timestamptz NOT NULL, "due_date" timestamptz NOT NULL, "unit_count" bigint NOT NULL DEFAULT 0, "line_count" bigint NOT NULL DEFAULT 0, "total_amount" numeric(18,2) NOT NULL, "issued_count" bigint NOT NULL DEFAULT 0, "failed_count" bigint NOT NULL DEFAULT 0, "skipped_count" bigint NOT NULL DEFAULT 0, "started_by" uuid NULL, "issued_at" timestamptz NULL, "error" text NULL, PRIMARY KEY ("id"));
-- Create index "billingrun_tenant_id_property_id_fund_id_period_run_kind" to table: "billing_runs"
CREATE UNIQUE INDEX "billingrun_tenant_id_property_id_fund_id_period_run_kind" ON "billing_runs" ("tenant_id", "property_id", "fund_id", "period", "run_kind") WHERE ((status)::text <> 'cancelled'::text);
-- Create index "billingrun_tenant_id_status" to table: "billing_runs"
CREATE INDEX "billingrun_tenant_id_status" ON "billing_runs" ("tenant_id", "status");
-- Create "visitor_passes" table
CREATE TABLE "visitor_passes" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "unit_id" uuid NULL, "host_party_id" uuid NULL, "created_by_kind" character varying NOT NULL DEFAULT 'resident', "created_by_id" uuid NULL, "pass_type" character varying NOT NULL DEFAULT 'guest_single', "visitor_name" character varying NOT NULL, "visitor_phone" character varying NULL, "vehicle_plate" character varying NULL, "code_hash" character varying NOT NULL, "code_hint" character varying NULL, "qr_token_hash" character varying NULL, "valid_from" timestamptz NOT NULL, "valid_to" timestamptz NOT NULL, "recurrence" jsonb NULL, "max_entries" bigint NOT NULL DEFAULT 1, "entries_used" bigint NOT NULL DEFAULT 0, "work_order_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'active', "notes" text NULL, PRIMARY KEY ("id"));
-- Create index "visitorpass_tenant_id_code_hash" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_code_hash" ON "visitor_passes" ("tenant_id", "code_hash");
-- Create index "visitorpass_tenant_id_host_party_id" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_host_party_id" ON "visitor_passes" ("tenant_id", "host_party_id");
-- Create index "visitorpass_tenant_id_property_id_status_valid_to" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_property_id_status_valid_to" ON "visitor_passes" ("tenant_id", "property_id", "status", "valid_to");
-- Create index "visitorpass_tenant_id_qr_token_hash" to table: "visitor_passes"
CREATE INDEX "visitorpass_tenant_id_qr_token_hash" ON "visitor_passes" ("tenant_id", "qr_token_hash");
-- Create "vehicles" table
CREATE TABLE "vehicles" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_id" uuid NOT NULL, "party_id" uuid NULL, "plate" character varying NOT NULL, "make" character varying NULL, "model" character varying NULL, "colour" character varying NULL, "sticker_number" character varying NULL, "status" character varying NOT NULL DEFAULT 'active', "removed_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "vehicle_tenant_id_plate" to table: "vehicles"
CREATE UNIQUE INDEX "vehicle_tenant_id_plate" ON "vehicles" ("tenant_id", "plate");
-- Create index "vehicle_tenant_id_unit_id" to table: "vehicles"
CREATE INDEX "vehicle_tenant_id_unit_id" ON "vehicles" ("tenant_id", "unit_id");
-- Create "catalog_entries" table
CREATE TABLE "catalog_entries" ("id" uuid NOT NULL, "tenant_id" uuid NULL, "kind" character varying NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "parent_code" character varying NULL, "sort" bigint NOT NULL DEFAULT 0, "active" boolean NOT NULL DEFAULT true, "attrs" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "catalogentry_kind_code" to table: "catalog_entries"
CREATE UNIQUE INDEX "catalogentry_kind_code" ON "catalog_entries" ("kind", "code") WHERE (tenant_id IS NULL);
-- Create index "catalogentry_tenant_id_kind_code" to table: "catalog_entries"
CREATE UNIQUE INDEX "catalogentry_tenant_id_kind_code" ON "catalog_entries" ("tenant_id", "kind", "code") WHERE (tenant_id IS NOT NULL);
-- Create "unit_charges" table
CREATE TABLE "unit_charges" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_id" uuid NOT NULL, "charge_type_id" uuid NOT NULL, "party_id" uuid NULL, "quantity" numeric(18,3) NOT NULL, "amount_override" numeric(18,2) NULL, "start_date" timestamptz NOT NULL, "end_date" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'active', PRIMARY KEY ("id"));
-- Create index "unitcharge_tenant_id_unit_id_status" to table: "unit_charges"
CREATE INDEX "unitcharge_tenant_id_unit_id_status" ON "unit_charges" ("tenant_id", "unit_id", "status");
-- Create "outbox_events" table
CREATE TABLE "outbox_events" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "aggregate_type" character varying NOT NULL, "aggregate_id" character varying NOT NULL, "event_type" character varying NOT NULL, "payload" jsonb NOT NULL, "status" character varying NOT NULL DEFAULT 'PENDING', "attempts" bigint NOT NULL DEFAULT 0, "last_attempt_at" timestamptz NULL, "published_at" timestamptz NULL, "error_message" text NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "outboxevent_status_created_at" to table: "outbox_events"
CREATE INDEX "outboxevent_status_created_at" ON "outbox_events" ("status", "created_at");
-- Create index "outboxevent_tenant_id_status" to table: "outbox_events"
CREATE INDEX "outboxevent_tenant_id_status" ON "outbox_events" ("tenant_id", "status");
-- Create "consumed_events" table
CREATE TABLE "consumed_events" ("id" uuid NOT NULL, "event_id" uuid NOT NULL, "consumer" character varying NOT NULL, "tenant_id" uuid NULL, "subject" character varying NULL, "processed_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "consumedevent_event_id_consumer" to table: "consumed_events"
CREATE UNIQUE INDEX "consumedevent_event_id_consumer" ON "consumed_events" ("event_id", "consumer");
-- Create index "consumedevent_processed_at" to table: "consumed_events"
CREATE INDEX "consumedevent_processed_at" ON "consumed_events" ("processed_at");
-- Create "custom_field_defs" table
CREATE TABLE "custom_field_defs" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "entity" character varying NOT NULL, "key" character varying NOT NULL, "label" character varying NOT NULL, "field_type" character varying NOT NULL, "options" jsonb NULL, "required" boolean NOT NULL DEFAULT false, "show_in_list" boolean NOT NULL DEFAULT false, "sort" bigint NOT NULL DEFAULT 0, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "customfielddef_tenant_id_entity_key" to table: "custom_field_defs"
CREATE UNIQUE INDEX "customfielddef_tenant_id_entity_key" ON "custom_field_defs" ("tenant_id", "entity", "key");
-- Create "daily_stats" table
CREATE TABLE "daily_stats" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "day" date NOT NULL, "billed" numeric(18,2) NOT NULL, "collected" numeric(18,2) NOT NULL, "invoices_issued" bigint NOT NULL DEFAULT 0, "payments_count" bigint NOT NULL DEFAULT 0, "arrears_total" numeric(18,2) NOT NULL, "arrears_0_30" numeric(18,2) NOT NULL, "arrears_31_60" numeric(18,2) NOT NULL, "arrears_61_90" numeric(18,2) NOT NULL, "arrears_90_plus" numeric(18,2) NOT NULL, "units_total" bigint NOT NULL DEFAULT 0, "units_occupied" bigint NOT NULL DEFAULT 0, "units_vacant" bigint NOT NULL DEFAULT 0, "units_sold" bigint NOT NULL DEFAULT 0, "water_supplied_m3" numeric(18,3) NOT NULL, "water_billed_m3" numeric(18,3) NOT NULL, "wo_opened" bigint NOT NULL DEFAULT 0, "wo_closed" bigint NOT NULL DEFAULT 0, "wo_sla_breaches" bigint NOT NULL DEFAULT 0, "visitors" bigint NOT NULL DEFAULT 0, "incidents" bigint NOT NULL DEFAULT 0, PRIMARY KEY ("id"));
-- Create index "dailystat_tenant_id_property_id_day" to table: "daily_stats"
CREATE UNIQUE INDEX "dailystat_tenant_id_property_id_day" ON "daily_stats" ("tenant_id", "property_id", "day");
-- Create "documents" table
CREATE TABLE "documents" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "number" character varying NOT NULL, "template_id" uuid NULL, "template_version" bigint NULL, "kind" character varying NOT NULL, "title" character varying NOT NULL, "entity_type" character varying NOT NULL, "entity_id" uuid NOT NULL, "unit_id" uuid NULL, "party_ids" jsonb NULL, "file_key" character varying NULL, "sha256" character varying NULL, "verification_code" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'draft', "issued_at" timestamptz NULL, "executed_at" timestamptz NULL, "expires_at" timestamptz NULL, "key_dates" jsonb NULL, "legal_hold" boolean NOT NULL DEFAULT false, "retention_until" timestamptz NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "document_tenant_id_entity_type_entity_id" to table: "documents"
CREATE INDEX "document_tenant_id_entity_type_entity_id" ON "documents" ("tenant_id", "entity_type", "entity_id");
-- Create index "document_tenant_id_number" to table: "documents"
CREATE UNIQUE INDEX "document_tenant_id_number" ON "documents" ("tenant_id", "number");
-- Create index "document_verification_code" to table: "documents"
CREATE UNIQUE INDEX "document_verification_code" ON "documents" ("verification_code");
-- Create "document_access_logs" table
CREATE TABLE "document_access_logs" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "document_id" uuid NOT NULL, "actor_id" uuid NULL, "actor_kind" character varying NULL, "action" character varying NOT NULL, "ip" character varying NULL, PRIMARY KEY ("id"));
-- Create index "documentaccesslog_tenant_id_document_id_created_at" to table: "document_access_logs"
CREATE INDEX "documentaccesslog_tenant_id_document_id_created_at" ON "document_access_logs" ("tenant_id", "document_id", "created_at");
-- Create "document_sequences" table
CREATE TABLE "document_sequences" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "kind" character varying NOT NULL, "prefix" character varying NULL, "next_value" bigint NOT NULL DEFAULT 1, "pad_width" bigint NOT NULL DEFAULT 5, "format" character varying NULL, "reset_period" character varying NOT NULL DEFAULT 'none', "period_key" character varying NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "documentsequence_tenant_id_kind" to table: "document_sequences"
CREATE UNIQUE INDEX "documentsequence_tenant_id_kind" ON "document_sequences" ("tenant_id", "kind");
-- Create "document_signatures" table
CREATE TABLE "document_signatures" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "document_id" uuid NOT NULL, "party_id" uuid NULL, "method" character varying NOT NULL, "signed_at" timestamptz NOT NULL, "document_hash" character varying NULL, "device" jsonb NULL, "ip" character varying NULL, "scan_file_key" character varying NULL, PRIMARY KEY ("id"));
-- Create index "documentsignature_tenant_id_document_id" to table: "document_signatures"
CREATE INDEX "documentsignature_tenant_id_document_id" ON "document_signatures" ("tenant_id", "document_id");
-- Create "document_templates" table
CREATE TABLE "document_templates" ("id" uuid NOT NULL, "tenant_id" uuid NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "category" character varying NOT NULL, "tenant_type" character varying NULL, "version" bigint NOT NULL DEFAULT 1, "body" text NOT NULL, "merge_fields" jsonb NULL, "conditions" jsonb NULL, "status" character varying NOT NULL DEFAULT 'draft', "approved_by" uuid NULL, "approved_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "documenttemplate_tenant_id_code_version" to table: "document_templates"
CREATE UNIQUE INDEX "documenttemplate_tenant_id_code_version" ON "document_templates" ("tenant_id", "code", "version");
-- Create "enquiries" table
CREATE TABLE "enquiries" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "unit_id" uuid NULL, "listing_id" uuid NULL, "name" character varying NOT NULL, "phone_enc" character varying NULL, "phone_hash" character varying NULL, "email" character varying NULL, "message" text NULL, "interest" character varying NOT NULL DEFAULT 'buy', "preferred_contact" character varying NOT NULL DEFAULT 'call', "consent_to_share" boolean NOT NULL DEFAULT false, "source" character varying NOT NULL DEFAULT 'showcase', "status" character varying NOT NULL DEFAULT 'new', "assigned_to" uuid NULL, "crm_lead_id" character varying NULL, "ip_hash" character varying NULL, PRIMARY KEY ("id"));
-- Create index "enquiry_tenant_id_phone_hash" to table: "enquiries"
CREATE INDEX "enquiry_tenant_id_phone_hash" ON "enquiries" ("tenant_id", "phone_hash");
-- Create index "enquiry_tenant_id_property_id" to table: "enquiries"
CREATE INDEX "enquiry_tenant_id_property_id" ON "enquiries" ("tenant_id", "property_id");
-- Create index "enquiry_tenant_id_status_created_at" to table: "enquiries"
CREATE INDEX "enquiry_tenant_id_status_created_at" ON "enquiries" ("tenant_id", "status", "created_at");
-- Create "tenant_settings" table
CREATE TABLE "tenant_settings" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "tenant_type" character varying NOT NULL DEFAULT 'estate_operator', "use_case_preset" character varying NOT NULL DEFAULT 'estate_developer', "currency" character varying NOT NULL DEFAULT 'KES', "timezone" character varying NOT NULL DEFAULT 'Africa/Nairobi', "billing_day" bigint NOT NULL DEFAULT 1, "due_day" bigint NOT NULL DEFAULT 10, "reading_window_start" bigint NOT NULL DEFAULT 25, "reading_window_end" bigint NOT NULL DEFAULT 28, "quiet_hours_start" character varying NOT NULL DEFAULT '21:00', "quiet_hours_end" character varying NOT NULL DEFAULT '07:00', "allocation_order" character varying NOT NULL DEFAULT 'oldest_first', "bill_to_revert_days" bigint NOT NULL DEFAULT 30, "water_loss_alert_pct" double precision NOT NULL DEFAULT 15, "arrears_steps" jsonb NULL, "terms_version" character varying NULL, "privacy_version" character varying NULL, "portal_support_phone" character varying NULL, "portal_support_email" character varying NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "tenantsetting_tenant_id" to table: "tenant_settings"
CREATE UNIQUE INDEX "tenantsetting_tenant_id" ON "tenant_settings" ("tenant_id");
-- Create "gate_devices" table
CREATE TABLE "gate_devices" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "name" character varying NOT NULL, "gate_name" character varying NOT NULL DEFAULT 'Main gate', "device_key_hash" character varying NOT NULL, "registered_by" uuid NULL, "last_seen_at" timestamptz NULL, "app_version" character varying NULL, "offline_alerted" boolean NOT NULL DEFAULT false, "status" character varying NOT NULL DEFAULT 'active', PRIMARY KEY ("id"));
-- Create index "gatedevice_device_key_hash" to table: "gate_devices"
CREATE UNIQUE INDEX "gatedevice_device_key_hash" ON "gate_devices" ("device_key_hash");
-- Create index "gatedevice_tenant_id_property_id_status" to table: "gate_devices"
CREATE INDEX "gatedevice_tenant_id_property_id_status" ON "gate_devices" ("tenant_id", "property_id", "status");
-- Create "gate_events" table
CREATE TABLE "gate_events" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "device_id" uuid NULL, "pass_id" uuid NULL, "kind" character varying NOT NULL, "visitor_name" character varying NULL, "visitor_phone" character varying NULL, "host_unit_id" uuid NULL, "vehicle_plate" character varying NULL, "id_sighted" boolean NOT NULL DEFAULT false, "occurred_at" timestamptz NOT NULL, "offline" boolean NOT NULL DEFAULT false, "client_event_id" character varying NOT NULL, "guard_personnel_id" uuid NULL, "decision" character varying NOT NULL DEFAULT 'none', "decided_at" timestamptz NULL, "notes" text NULL, PRIMARY KEY ("id"));
-- Create index "gateevent_tenant_id_device_id_client_event_id" to table: "gate_events"
CREATE UNIQUE INDEX "gateevent_tenant_id_device_id_client_event_id" ON "gate_events" ("tenant_id", "device_id", "client_event_id");
-- Create index "gateevent_tenant_id_property_id_occurred_at" to table: "gate_events"
CREATE INDEX "gateevent_tenant_id_property_id_occurred_at" ON "gate_events" ("tenant_id", "property_id", "occurred_at");
-- Create "guard_posts" table
CREATE TABLE "guard_posts" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "contract_id" uuid NULL, "name" character varying NOT NULL, "guards_day" bigint NOT NULL DEFAULT 1, "guards_night" bigint NOT NULL DEFAULT 1, "shift_pattern" jsonb NULL, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "guardpost_tenant_id_property_id" to table: "guard_posts"
CREATE INDEX "guardpost_tenant_id_property_id" ON "guard_posts" ("tenant_id", "property_id");
-- Create "handovers" table
CREATE TABLE "handovers" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "contract_id" uuid NOT NULL, "unit_id" uuid NOT NULL, "scheduled_at" timestamptz NULL, "completed_at" timestamptz NULL, "keys" jsonb NULL, "readings" jsonb NULL, "checklist" jsonb NULL, "snag_items" jsonb NULL, "signed_document_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'scheduled', PRIMARY KEY ("id"));
-- Create index "handover_tenant_id_contract_id" to table: "handovers"
CREATE UNIQUE INDEX "handover_tenant_id_contract_id" ON "handovers" ("tenant_id", "contract_id");
-- Create index "handover_tenant_id_status" to table: "handovers"
CREATE INDEX "handover_tenant_id_status" ON "handovers" ("tenant_id", "status");
-- Create "import_jobs" table
CREATE TABLE "import_jobs" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "kind" character varying NOT NULL, "property_id" uuid NULL, "dry_run" boolean NOT NULL DEFAULT true, "status" character varying NOT NULL DEFAULT 'validating', "file_name" character varying NULL, "rows_total" bigint NOT NULL DEFAULT 0, "rows_valid" bigint NOT NULL DEFAULT 0, "rows_failed" bigint NOT NULL DEFAULT 0, "rows_committed" bigint NOT NULL DEFAULT 0, "errors" jsonb NULL, "summary" jsonb NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "importjob_tenant_id_created_at" to table: "import_jobs"
CREATE INDEX "importjob_tenant_id_created_at" ON "import_jobs" ("tenant_id", "created_at");
-- Create "incidents" table
CREATE TABLE "incidents" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "number" character varying NOT NULL, "property_id" uuid NOT NULL, "unit_id" uuid NULL, "category" character varying NOT NULL, "severity" character varying NOT NULL DEFAULT 'medium', "title" character varying NOT NULL, "description" text NULL, "occurred_at" timestamptz NOT NULL, "reported_by_kind" character varying NULL, "reported_by_id" uuid NULL, "photos" jsonb NULL, "status" character varying NOT NULL DEFAULT 'open', "notified_at" timestamptz NULL, "resolution" text NULL, PRIMARY KEY ("id"));
-- Create index "incident_tenant_id_number" to table: "incidents"
CREATE UNIQUE INDEX "incident_tenant_id_number" ON "incidents" ("tenant_id", "number");
-- Create index "incident_tenant_id_property_id_status" to table: "incidents"
CREATE INDEX "incident_tenant_id_property_id_status" ON "incidents" ("tenant_id", "property_id", "status");
-- Create "tenant_modules" table
CREATE TABLE "tenant_modules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "module" character varying NOT NULL, "enabled" boolean NOT NULL DEFAULT true, "enabled_at" timestamptz NULL, "disabled_at" timestamptz NULL, "changed_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "tenantmodule_tenant_id_module" to table: "tenant_modules"
CREATE UNIQUE INDEX "tenantmodule_tenant_id_module" ON "tenant_modules" ("tenant_id", "module");
-- Create "adjustments" table
CREATE TABLE "adjustments" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_account_id" uuid NOT NULL, "kind" character varying NOT NULL, "amount" numeric(18,2) NOT NULL, "reason" text NOT NULL, "treasury_invoice_id" uuid NULL, "treasury_credit_note_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'pending_approval', "requested_by" uuid NOT NULL, "approvals" jsonb NULL, PRIMARY KEY ("id"));
-- Create index "adjustment_tenant_id_status" to table: "adjustments"
CREATE INDEX "adjustment_tenant_id_status" ON "adjustments" ("tenant_id", "status");
-- Create "maintenance_schedules" table
CREATE TABLE "maintenance_schedules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "asset_ref" character varying NULL, "title" character varying NOT NULL, "category" character varying NOT NULL, "frequency" character varying NOT NULL, "next_due_at" timestamptz NOT NULL, "default_vendor_id" uuid NULL, "default_employee_id" character varying NULL, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "maintenanceschedule_tenant_id_active_next_due_at" to table: "maintenance_schedules"
CREATE INDEX "maintenanceschedule_tenant_id_active_next_due_at" ON "maintenance_schedules" ("tenant_id", "active", "next_due_at");
-- Create "service_schedules" table
CREATE TABLE "service_schedules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "contract_id" uuid NOT NULL, "property_id" uuid NOT NULL, "name" character varying NOT NULL, "frequency" character varying NOT NULL, "days" jsonb NULL, "time_window" character varying NULL, "zone" character varying NULL, "checklist" jsonb NULL, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "serviceschedule_tenant_id_contract_id" to table: "service_schedules"
CREATE INDEX "serviceschedule_tenant_id_contract_id" ON "service_schedules" ("tenant_id", "contract_id");
-- Create "rosters" table
CREATE TABLE "rosters" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "post_id" uuid NOT NULL, "personnel_id" uuid NOT NULL, "shift_date" timestamptz NOT NULL, "shift" character varying NOT NULL, "starts_at" timestamptz NOT NULL, "ends_at" timestamptz NOT NULL, "signed_on_at" timestamptz NULL, "signed_off_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'scheduled', PRIMARY KEY ("id"));
-- Create index "roster_tenant_id_post_id_personnel_id_shift_date_shift" to table: "rosters"
CREATE UNIQUE INDEX "roster_tenant_id_post_id_personnel_id_shift_date_shift" ON "rosters" ("tenant_id", "post_id", "personnel_id", "shift_date", "shift");
-- Create "reservations" table
CREATE TABLE "reservations" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_id" uuid NOT NULL, "party_id" uuid NOT NULL, "price_list_item_id" uuid NULL, "price" numeric(18,2) NOT NULL, "fee_amount" numeric(18,2) NOT NULL, "fee_treasury_invoice_id" uuid NULL, "fee_paid_at" timestamptz NULL, "reserved_at" timestamptz NOT NULL, "expires_at" timestamptz NOT NULL, "status" character varying NOT NULL DEFAULT 'pending_payment', "sale_contract_id" uuid NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "reservation_tenant_id_status_expires_at" to table: "reservations"
CREATE INDEX "reservation_tenant_id_status_expires_at" ON "reservations" ("tenant_id", "status", "expires_at");
-- Create index "reservation_tenant_id_unit_id" to table: "reservations"
CREATE UNIQUE INDEX "reservation_tenant_id_unit_id" ON "reservations" ("tenant_id", "unit_id") WHERE ((status)::text = ANY ((ARRAY['pending_payment'::character varying, 'active'::character varying])::text[]));
-- Create "maskani_user_outlets" table
CREATE TABLE "maskani_user_outlets" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "user_id" uuid NOT NULL, "outlet_id" uuid NOT NULL, "is_home_outlet" boolean NOT NULL DEFAULT false, "property_role" character varying NOT NULL DEFAULT 'other', "erp_employee_id" character varying NULL, "ends_at" timestamptz NULL, "assigned_by" uuid NULL, "assigned_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "maskaniuseroutlet_tenant_id_outlet_id" to table: "maskani_user_outlets"
CREATE INDEX "maskaniuseroutlet_tenant_id_outlet_id" ON "maskani_user_outlets" ("tenant_id", "outlet_id");
-- Create index "maskaniuseroutlet_tenant_id_user_id_outlet_id" to table: "maskani_user_outlets"
CREATE UNIQUE INDEX "maskaniuseroutlet_tenant_id_user_id_outlet_id" ON "maskani_user_outlets" ("tenant_id", "user_id", "outlet_id");
-- Create "reminder_schedules" table
CREATE TABLE "reminder_schedules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "kind" character varying NOT NULL, "offsets_days" jsonb NOT NULL, "channels" jsonb NOT NULL, "template_code" character varying NULL, "respect_quiet_hours" boolean NOT NULL DEFAULT true, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "reminderschedule_tenant_id_kind" to table: "reminder_schedules"
CREATE UNIQUE INDEX "reminderschedule_tenant_id_kind" ON "reminder_schedules" ("tenant_id", "kind");
-- Create "reading_rounds" table
CREATE TABLE "reading_rounds" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "period" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'open', "assigned_to" uuid NULL, "opened_at" timestamptz NULL, "closed_at" timestamptz NULL, "totals" jsonb NULL, PRIMARY KEY ("id"));
-- Create index "readinground_tenant_id_property_id_period" to table: "reading_rounds"
CREATE UNIQUE INDEX "readinground_tenant_id_property_id_period" ON "reading_rounds" ("tenant_id", "property_id", "period");
-- Create "privacy_requests" table
CREATE TABLE "privacy_requests" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "party_id" uuid NULL, "requester_name" character varying NULL, "requester_phone" character varying NULL, "kind" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'received', "received_at" timestamptz NOT NULL, "due_at" timestamptz NOT NULL, "completed_at" timestamptz NULL, "export_file_key" character varying NULL, "notes" text NULL, PRIMARY KEY ("id"));
-- Create index "privacyrequest_tenant_id_status_due_at" to table: "privacy_requests"
CREATE INDEX "privacyrequest_tenant_id_status_due_at" ON "privacy_requests" ("tenant_id", "status", "due_at");
-- Create "approval_rules" table
CREATE TABLE "approval_rules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "action" character varying NOT NULL, "min_amount" numeric(18,2) NOT NULL, "max_amount" numeric(18,2) NULL, "levels" bigint NOT NULL DEFAULT 1, "approver_roles" jsonb NULL, "require_otp" boolean NOT NULL DEFAULT false, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "approvalrule_tenant_id_action_active" to table: "approval_rules"
CREATE INDEX "approvalrule_tenant_id_action_active" ON "approval_rules" ("tenant_id", "action", "active");
-- Create "patrol_scans" table
CREATE TABLE "patrol_scans" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "checkpoint_id" uuid NOT NULL, "personnel_id" uuid NULL, "device_id" uuid NULL, "scanned_at" timestamptz NOT NULL, "round_ref" character varying NULL, "client_event_id" character varying NOT NULL, PRIMARY KEY ("id"));
-- Create index "patrolscan_tenant_id_device_id_client_event_id" to table: "patrol_scans"
CREATE UNIQUE INDEX "patrolscan_tenant_id_device_id_client_event_id" ON "patrol_scans" ("tenant_id", "device_id", "client_event_id");
-- Create index "patrolscan_tenant_id_property_id_scanned_at" to table: "patrol_scans"
CREATE INDEX "patrolscan_tenant_id_property_id_scanned_at" ON "patrol_scans" ("tenant_id", "property_id", "scanned_at");
-- Create "patrol_checkpoints" table
CREATE TABLE "patrol_checkpoints" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "name" character varying NOT NULL, "code" character varying NOT NULL, "location_note" character varying NULL, "sort" bigint NOT NULL DEFAULT 0, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "patrolcheckpoint_tenant_id_code" to table: "patrol_checkpoints"
CREATE UNIQUE INDEX "patrolcheckpoint_tenant_id_code" ON "patrol_checkpoints" ("tenant_id", "code");
-- Create "billing_run_lines" table
CREATE TABLE "billing_run_lines" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_id" uuid NOT NULL, "unit_account_id" uuid NOT NULL, "party_id" uuid NULL, "unit_code" character varying NOT NULL, "lines" jsonb NOT NULL, "subtotal" numeric(18,2) NOT NULL, "tax_total" numeric(18,2) NOT NULL, "total" numeric(18,2) NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "skip_reason" character varying NULL, "treasury_invoice_id" uuid NULL, "invoice_number" character varying NULL, "attempts" bigint NOT NULL DEFAULT 0, "last_error" text NULL, "run_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "billing_run_lines_billing_runs_lines" FOREIGN KEY ("run_id") REFERENCES "billing_runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "billingrunline_run_id_unit_account_id" to table: "billing_run_lines"
CREATE UNIQUE INDEX "billingrunline_run_id_unit_account_id" ON "billing_run_lines" ("run_id", "unit_account_id");
-- Create index "billingrunline_tenant_id_status" to table: "billing_run_lines"
CREATE INDEX "billingrunline_tenant_id_status" ON "billing_run_lines" ("tenant_id", "status");
-- Create index "billingrunline_tenant_id_unit_account_id" to table: "billing_run_lines"
CREATE INDEX "billingrunline_tenant_id_unit_account_id" ON "billing_run_lines" ("tenant_id", "unit_account_id");
-- Create "portfolios" table
CREATE TABLE "portfolios" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "kind" character varying NOT NULL DEFAULT 'own', "landlord_party_id" uuid NULL, "mandate" jsonb NULL, "status" character varying NOT NULL DEFAULT 'active', "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "portfolio_tenant_id_code" to table: "portfolios"
CREATE UNIQUE INDEX "portfolio_tenant_id_code" ON "portfolios" ("tenant_id", "code");
-- Create "properties" table
CREATE TABLE "properties" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "outlet_id" uuid NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "property_type" character varying NOT NULL DEFAULT 'estate', "use_case" character varying NOT NULL DEFAULT 'estate_developer', "description" text NULL, "address_line" character varying NULL, "area" character varying NULL, "town" character varying NULL, "county" character varying NULL, "country" character varying NOT NULL DEFAULT 'KE', "latitude" double precision NULL, "longitude" double precision NULL, "plot_number" character varying NULL, "title_number" character varying NULL, "land_size_sqm" numeric(18,3) NULL, "year_built" bigint NULL, "phases" jsonb NULL, "amenities" jsonb NULL, "photos" jsonb NULL, "module_overrides" jsonb NULL, "management_since" timestamptz NULL, "published" boolean NOT NULL DEFAULT false, "public_slug" character varying NULL, "status" character varying NOT NULL DEFAULT 'active', "custom_fields" jsonb NULL, "created_by" uuid NULL, "portfolio_id" uuid NULL, PRIMARY KEY ("id"), CONSTRAINT "properties_portfolios_properties" FOREIGN KEY ("portfolio_id") REFERENCES "portfolios" ("id") ON UPDATE NO ACTION ON DELETE SET NULL);
-- Create index "property_public_slug" to table: "properties"
CREATE INDEX "property_public_slug" ON "properties" ("public_slug");
-- Create index "property_tenant_id_code" to table: "properties"
CREATE UNIQUE INDEX "property_tenant_id_code" ON "properties" ("tenant_id", "code");
-- Create index "property_tenant_id_outlet_id" to table: "properties"
CREATE INDEX "property_tenant_id_outlet_id" ON "properties" ("tenant_id", "outlet_id");
-- Create index "property_tenant_id_status" to table: "properties"
CREATE INDEX "property_tenant_id_status" ON "properties" ("tenant_id", "status");
-- Create "blocks" table
CREATE TABLE "blocks" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "phase" character varying NULL, "floors" bigint NULL, "sort" bigint NOT NULL DEFAULT 0, "description" text NULL, "status" character varying NOT NULL DEFAULT 'active', "property_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "blocks_properties_blocks" FOREIGN KEY ("property_id") REFERENCES "properties" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "block_tenant_id_property_id_code" to table: "blocks"
CREATE UNIQUE INDEX "block_tenant_id_property_id_code" ON "blocks" ("tenant_id", "property_id", "code");
-- Create "charge_types" table
CREATE TABLE "charge_types" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "charge_group" character varying NOT NULL, "basis" character varying NOT NULL, "frequency" character varying NOT NULL DEFAULT 'monthly', "event_trigger" character varying NULL, "applies_to" jsonb NULL, "bill_to" character varying NOT NULL DEFAULT 'owner', "reassignable" boolean NOT NULL DEFAULT false, "fund_code" character varying NOT NULL DEFAULT 'estate', "ledger_account_code" character varying NULL, "cost_center_code" character varying NULL, "vat_rate" double precision NOT NULL DEFAULT 0, "tax_exempt" boolean NOT NULL DEFAULT true, "etims_item_code" character varying NULL, "wht_applicable" boolean NOT NULL DEFAULT false, "proration" character varying NOT NULL DEFAULT 'days', "penalty" jsonb NULL, "allocation_priority" bigint NOT NULL DEFAULT 100, "percentage_of_code" character varying NULL, "tariff_kind" character varying NOT NULL DEFAULT 'flat', "seeded_from" character varying NULL, "sort" bigint NOT NULL DEFAULT 0, "active" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "chargetype_tenant_id_code" to table: "charge_types"
CREATE UNIQUE INDEX "chargetype_tenant_id_code" ON "charge_types" ("tenant_id", "code");
-- Create "charge_rates" table
CREATE TABLE "charge_rates" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "scope" character varying NOT NULL DEFAULT 'tenant', "property_id" uuid NULL, "unit_type" character varying NULL, "unit_id" uuid NULL, "amount" numeric(18,2) NOT NULL, "tariff" jsonb NULL, "fixed_meter_charge" numeric(18,2) NOT NULL, "effective_from" timestamptz NOT NULL, "effective_to" timestamptz NULL, "notes" text NULL, "created_by" uuid NULL, "charge_type_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "charge_rates_charge_types_rates" FOREIGN KEY ("charge_type_id") REFERENCES "charge_types" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "chargerate_tenant_id_charge_type_id_scope_effective_from" to table: "charge_rates"
CREATE INDEX "chargerate_tenant_id_charge_type_id_scope_effective_from" ON "charge_rates" ("tenant_id", "charge_type_id", "scope", "effective_from");
-- Create "sale_contracts" table
CREATE TABLE "sale_contracts" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "contract_number" character varying NOT NULL, "property_id" uuid NOT NULL, "unit_id" uuid NOT NULL, "primary_buyer_id" uuid NOT NULL, "buyers" jsonb NULL, "reservation_id" uuid NULL, "price" numeric(18,2) NOT NULL, "discount" numeric(18,2) NOT NULL, "discount_reason" character varying NULL, "net_price" numeric(18,2) NOT NULL, "reservation_credit" numeric(18,2) NOT NULL, "deposit_amount" numeric(18,2) NOT NULL, "payment_option" character varying NOT NULL DEFAULT 'instalments', "financier" jsonb NULL, "term_months" bigint NOT NULL DEFAULT 0, "frequency" character varying NOT NULL DEFAULT 'monthly', "interest_rate" double precision NOT NULL DEFAULT 0, "late_charge" jsonb NULL, "grace_days" bigint NOT NULL DEFAULT 30, "buyer_advocate" jsonb NULL, "seller_advocate" jsonb NULL, "signed_at" timestamptz NULL, "agreement_document_id" uuid NULL, "unit_account_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'draft', "default_since" timestamptz NULL, "terminated_at" timestamptz NULL, "termination" jsonb NULL, "invoiced_total" numeric(18,2) NOT NULL, "paid_total" numeric(18,2) NOT NULL, "notes" text NULL, "custom_fields" jsonb NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "salecontract_tenant_id_contract_number" to table: "sale_contracts"
CREATE UNIQUE INDEX "salecontract_tenant_id_contract_number" ON "sale_contracts" ("tenant_id", "contract_number");
-- Create index "salecontract_tenant_id_primary_buyer_id" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_primary_buyer_id" ON "sale_contracts" ("tenant_id", "primary_buyer_id");
-- Create index "salecontract_tenant_id_status" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_status" ON "sale_contracts" ("tenant_id", "status");
-- Create index "salecontract_tenant_id_unit_id" to table: "sale_contracts"
CREATE INDEX "salecontract_tenant_id_unit_id" ON "sale_contracts" ("tenant_id", "unit_id");
-- Create "instalment_schedules" table
CREATE TABLE "instalment_schedules" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "version" bigint NOT NULL DEFAULT 1, "status" character varying NOT NULL DEFAULT 'active', "reason" text NULL, "approved_by" uuid NULL, "approved_at" timestamptz NULL, "buyer_accepted_at" timestamptz NULL, "contract_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "instalment_schedules_sale_contracts_schedules" FOREIGN KEY ("contract_id") REFERENCES "sale_contracts" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "instalmentschedule_tenant_id_contract_id_version" to table: "instalment_schedules"
CREATE UNIQUE INDEX "instalmentschedule_tenant_id_contract_id_version" ON "instalment_schedules" ("tenant_id", "contract_id", "version");
-- Create "instalments" table
CREATE TABLE "instalments" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "contract_id" uuid NOT NULL, "seq" bigint NOT NULL, "kind" character varying NOT NULL, "due_date" timestamptz NOT NULL, "amount" numeric(18,2) NOT NULL, "milestone_label" character varying NULL, "released_at" timestamptz NULL, "released_by" uuid NULL, "evidence_key" character varying NULL, "status" character varying NOT NULL DEFAULT 'scheduled', "treasury_invoice_id" uuid NULL, "invoice_number" character varying NULL, "paid_amount" numeric(18,2) NOT NULL, "paid_at" timestamptz NULL, "schedule_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "instalments_instalment_schedules_instalments" FOREIGN KEY ("schedule_id") REFERENCES "instalment_schedules" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "instalment_schedule_id_seq" to table: "instalments"
CREATE UNIQUE INDEX "instalment_schedule_id_seq" ON "instalments" ("schedule_id", "seq");
-- Create index "instalment_tenant_id_contract_id" to table: "instalments"
CREATE INDEX "instalment_tenant_id_contract_id" ON "instalments" ("tenant_id", "contract_id");
-- Create index "instalment_tenant_id_status_due_date" to table: "instalments"
CREATE INDEX "instalment_tenant_id_status_due_date" ON "instalments" ("tenant_id", "status", "due_date");
-- Create "tenants" table
CREATE TABLE "tenants" ("id" uuid NOT NULL, "name" character varying NOT NULL, "slug" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'active', "use_case" character varying NULL, "sync_status" character varying NOT NULL DEFAULT 'synced', "last_sync_at" timestamptz NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "tenant_status" to table: "tenants"
CREATE INDEX "tenant_status" ON "tenants" ("status");
-- Create index "tenants_slug_key" to table: "tenants"
CREATE UNIQUE INDEX "tenants_slug_key" ON "tenants" ("slug");
-- Create "maskani_users" table
CREATE TABLE "maskani_users" ("id" uuid NOT NULL, "auth_service_user_id" uuid NOT NULL, "email" character varying NULL, "phone" character varying NULL, "name" character varying NULL, "kind" character varying NOT NULL DEFAULT 'staff', "status" character varying NOT NULL DEFAULT 'active', "sync_status" character varying NOT NULL DEFAULT 'synced', "last_sync_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tenant_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "maskani_users_tenants_users" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "maskaniuser_tenant_id_auth_service_user_id" to table: "maskani_users"
CREATE UNIQUE INDEX "maskaniuser_tenant_id_auth_service_user_id" ON "maskani_users" ("tenant_id", "auth_service_user_id");
-- Create index "maskaniuser_tenant_id_kind_status" to table: "maskani_users"
CREATE INDEX "maskaniuser_tenant_id_kind_status" ON "maskani_users" ("tenant_id", "kind", "status");
-- Create "meters" table
CREATE TABLE "meters" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "unit_id" uuid NULL, "kind" character varying NOT NULL DEFAULT 'unit', "utility" character varying NOT NULL DEFAULT 'water', "serial" character varying NOT NULL, "make" character varying NULL, "location_note" character varying NULL, "installed_at" timestamptz NULL, "multiplier" numeric(18,3) NOT NULL, "initial_reading" numeric(18,3) NOT NULL, "closing_reading" numeric(18,3) NULL, "walking_order" bigint NOT NULL DEFAULT 0, "status" character varying NOT NULL DEFAULT 'active', "replaced_by_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "meter_tenant_id_property_id_kind_status" to table: "meters"
CREATE INDEX "meter_tenant_id_property_id_kind_status" ON "meters" ("tenant_id", "property_id", "kind", "status");
-- Create index "meter_tenant_id_serial" to table: "meters"
CREATE UNIQUE INDEX "meter_tenant_id_serial" ON "meters" ("tenant_id", "serial");
-- Create index "meter_tenant_id_unit_id" to table: "meters"
CREATE INDEX "meter_tenant_id_unit_id" ON "meters" ("tenant_id", "unit_id");
-- Create "meter_readings" table
CREATE TABLE "meter_readings" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "round_id" uuid NULL, "unit_id" uuid NULL, "period" character varying NOT NULL, "reading" numeric(18,3) NOT NULL, "previous_reading" numeric(18,3) NULL, "consumption" numeric(18,3) NOT NULL, "read_at" timestamptz NOT NULL, "read_by" uuid NULL, "photo_key" character varying NULL, "source" character varying NOT NULL DEFAULT 'round', "is_estimated" boolean NOT NULL DEFAULT false, "flags" jsonb NULL, "status" character varying NOT NULL DEFAULT 'pending', "verified_by" uuid NULL, "notes" text NULL, "meter_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "meter_readings_meters_readings" FOREIGN KEY ("meter_id") REFERENCES "meters" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "meterreading_tenant_id_meter_id_period_source" to table: "meter_readings"
CREATE UNIQUE INDEX "meterreading_tenant_id_meter_id_period_source" ON "meter_readings" ("tenant_id", "meter_id", "period", "source");
-- Create index "meterreading_tenant_id_round_id_status" to table: "meter_readings"
CREATE INDEX "meterreading_tenant_id_round_id_status" ON "meter_readings" ("tenant_id", "round_id", "status");
-- Create index "meterreading_tenant_id_unit_id_period" to table: "meter_readings"
CREATE INDEX "meterreading_tenant_id_unit_id_period" ON "meter_readings" ("tenant_id", "unit_id", "period");
-- Create "notices" table
CREATE TABLE "notices" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NULL, "audience" jsonb NOT NULL, "channels" jsonb NOT NULL, "category" character varying NOT NULL DEFAULT 'general', "priority" character varying NOT NULL DEFAULT 'routine', "title" character varying NOT NULL, "body" text NOT NULL, "scheduled_at" timestamptz NULL, "sent_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'draft', "recipients_count" bigint NOT NULL DEFAULT 0, "delivered_count" bigint NOT NULL DEFAULT 0, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "notice_tenant_id_created_at" to table: "notices"
CREATE INDEX "notice_tenant_id_created_at" ON "notices" ("tenant_id", "created_at");
-- Create index "notice_tenant_id_status_scheduled_at" to table: "notices"
CREATE INDEX "notice_tenant_id_status_scheduled_at" ON "notices" ("tenant_id", "status", "scheduled_at");
-- Create "notice_deliveries" table
CREATE TABLE "notice_deliveries" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "party_id" uuid NOT NULL, "channel" character varying NOT NULL, "destination" character varying NULL, "notification_message_id" character varying NULL, "status" character varying NOT NULL DEFAULT 'queued', "error" text NULL, "sent_at" timestamptz NULL, "notice_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "notice_deliveries_notices_deliveries" FOREIGN KEY ("notice_id") REFERENCES "notices" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "noticedelivery_notice_id_party_id_channel" to table: "notice_deliveries"
CREATE UNIQUE INDEX "noticedelivery_notice_id_party_id_channel" ON "notice_deliveries" ("notice_id", "party_id", "channel");
-- Create index "noticedelivery_tenant_id_status" to table: "notice_deliveries"
CREATE INDEX "noticedelivery_tenant_id_status" ON "notice_deliveries" ("tenant_id", "status");
-- Create "outlets" table
CREATE TABLE "outlets" ("id" uuid NOT NULL, "tenant_slug" character varying NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "address_json" jsonb NULL, "status" character varying NOT NULL DEFAULT 'active', "use_case" character varying NULL, "is_hq" boolean NOT NULL DEFAULT false, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tenant_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "outlets_tenants_outlets" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "outlet_tenant_id_code" to table: "outlets"
CREATE UNIQUE INDEX "outlet_tenant_id_code" ON "outlets" ("tenant_id", "code");
-- Create index "outlet_tenant_slug" to table: "outlets"
CREATE INDEX "outlet_tenant_slug" ON "outlets" ("tenant_slug");
-- Create "price_lists" table
CREATE TABLE "price_lists" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "name" character varying NOT NULL, "phase" character varying NULL, "currency" character varying NOT NULL DEFAULT 'KES', "effective_from" timestamptz NOT NULL, "effective_to" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'draft', "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "pricelist_tenant_id_property_id_status" to table: "price_lists"
CREATE INDEX "pricelist_tenant_id_property_id_status" ON "price_lists" ("tenant_id", "property_id", "status");
-- Create "price_list_items" table
CREATE TABLE "price_list_items" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_type" character varying NULL, "unit_id" uuid NULL, "price" numeric(18,2) NOT NULL, "reservation_fee" numeric(18,2) NOT NULL, "deposit_pct" double precision NOT NULL DEFAULT 20, "min_deposit" numeric(18,2) NULL, "max_term_months" bigint NOT NULL DEFAULT 24, "interest_rate" double precision NOT NULL DEFAULT 0, "discount_rules" jsonb NULL, "price_list_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "price_list_items_price_lists_items" FOREIGN KEY ("price_list_id") REFERENCES "price_lists" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "pricelistitem_tenant_id_price_list_id" to table: "price_list_items"
CREATE INDEX "pricelistitem_tenant_id_price_list_id" ON "price_list_items" ("tenant_id", "price_list_id");
-- Create "maskani_permissions" table
CREATE TABLE "maskani_permissions" ("id" uuid NOT NULL, "permission_code" character varying NOT NULL, "name" character varying NOT NULL, "module" character varying NOT NULL, "action" character varying NOT NULL, "resource" character varying NULL, "description" text NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "maskani_permissions_permission_code_key" to table: "maskani_permissions"
CREATE UNIQUE INDEX "maskani_permissions_permission_code_key" ON "maskani_permissions" ("permission_code");
-- Create index "maskanipermission_module_action" to table: "maskani_permissions"
CREATE INDEX "maskanipermission_module_action" ON "maskani_permissions" ("module", "action");
-- Create "maskani_roles" table
CREATE TABLE "maskani_roles" ("id" uuid NOT NULL, "tenant_id" uuid NULL, "role_code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "is_system_role" boolean NOT NULL DEFAULT false, "is_customer_role" boolean NOT NULL DEFAULT false, "cloned_from_role_id" uuid NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "maskanirole_role_code" to table: "maskani_roles"
CREATE UNIQUE INDEX "maskanirole_role_code" ON "maskani_roles" ("role_code") WHERE (tenant_id IS NULL);
-- Create index "maskanirole_tenant_id" to table: "maskani_roles"
CREATE INDEX "maskanirole_tenant_id" ON "maskani_roles" ("tenant_id");
-- Create index "maskanirole_tenant_id_role_code" to table: "maskani_roles"
CREATE UNIQUE INDEX "maskanirole_tenant_id_role_code" ON "maskani_roles" ("tenant_id", "role_code") WHERE (tenant_id IS NOT NULL);
-- Create "role_permissions" table
CREATE TABLE "role_permissions" ("id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY, "role_id" uuid NOT NULL, "permission_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "role_permissions_maskani_permissions_permission" FOREIGN KEY ("permission_id") REFERENCES "maskani_permissions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "role_permissions_maskani_roles_role" FOREIGN KEY ("role_id") REFERENCES "maskani_roles" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "rolepermission_permission_id" to table: "role_permissions"
CREATE INDEX "rolepermission_permission_id" ON "role_permissions" ("permission_id");
-- Create index "rolepermission_role_id_permission_id" to table: "role_permissions"
CREATE UNIQUE INDEX "rolepermission_role_id_permission_id" ON "role_permissions" ("role_id", "permission_id");
-- Create "title_stages" table
CREATE TABLE "title_stages" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "unit_id" uuid NOT NULL, "stage" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "stage_date" timestamptz NULL, "reference" character varying NULL, "document_id" uuid NULL, "notes" text NULL, "contract_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "title_stages_sale_contracts_title_stages" FOREIGN KEY ("contract_id") REFERENCES "sale_contracts" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "titlestage_tenant_id_contract_id_stage" to table: "title_stages"
CREATE UNIQUE INDEX "titlestage_tenant_id_contract_id_stage" ON "title_stages" ("tenant_id", "contract_id", "stage");
-- Create "funds" table
CREATE TABLE "funds" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "kind" character varying NOT NULL DEFAULT 'estate', "treasury_bank_account_id" uuid NULL, "paybill_shortcode" character varying NULL, "account_prefix" character varying NULL, "cost_center_code" character varying NULL, "income_account_code" character varying NULL, "receivable_account_code" character varying NULL, "is_default" boolean NOT NULL DEFAULT false, "status" character varying NOT NULL DEFAULT 'active', PRIMARY KEY ("id"));
-- Create index "fund_tenant_id_code" to table: "funds"
CREATE UNIQUE INDEX "fund_tenant_id_code" ON "funds" ("tenant_id", "code");
-- Create "units" table
CREATE TABLE "units" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "code" character varying NOT NULL, "unit_type" character varying NOT NULL DEFAULT 'apartment', "use" character varying NOT NULL DEFAULT 'residential', "bedrooms" bigint NULL, "bathrooms" bigint NULL, "size_sqm" numeric(18,3) NULL, "plot_size_sqm" numeric(18,3) NULL, "floor" character varying NULL, "entitlement" numeric(18,3) NOT NULL, "parking_bays" bigint NOT NULL DEFAULT 0, "furnished" boolean NOT NULL DEFAULT false, "phase" character varying NULL, "sale_status" character varying NOT NULL DEFAULT 'not_for_sale', "occupancy_status" character varying NOT NULL DEFAULT 'vacant', "rentable" boolean NOT NULL DEFAULT false, "handed_over_at" timestamptz NULL, "features" jsonb NULL, "photos" jsonb NULL, "walking_order" bigint NOT NULL DEFAULT 0, "status" character varying NOT NULL DEFAULT 'active', "custom_fields" jsonb NULL, "created_by" uuid NULL, "block_id" uuid NULL, "property_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "units_blocks_units" FOREIGN KEY ("block_id") REFERENCES "blocks" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT "units_properties_units" FOREIGN KEY ("property_id") REFERENCES "properties" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "unit_tenant_id_block_id" to table: "units"
CREATE INDEX "unit_tenant_id_block_id" ON "units" ("tenant_id", "block_id");
-- Create index "unit_tenant_id_property_id_code" to table: "units"
CREATE UNIQUE INDEX "unit_tenant_id_property_id_code" ON "units" ("tenant_id", "property_id", "code");
-- Create index "unit_tenant_id_property_id_occupancy_status" to table: "units"
CREATE INDEX "unit_tenant_id_property_id_occupancy_status" ON "units" ("tenant_id", "property_id", "occupancy_status");
-- Create index "unit_tenant_id_property_id_sale_status" to table: "units"
CREATE INDEX "unit_tenant_id_property_id_sale_status" ON "units" ("tenant_id", "property_id", "sale_status");
-- Create "unit_accounts" table
CREATE TABLE "unit_accounts" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "account_ref" character varying NOT NULL, "primary_party_id" uuid NULL, "customer_name" character varying NULL, "customer_phone" character varying NULL, "c2b_route_registered_at" timestamptz NULL, "balance" numeric(18,2) NOT NULL, "opening_balance" numeric(18,2) NOT NULL, "balance_synced_at" timestamptz NULL, "last_payment_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'active', "fund_id" uuid NOT NULL, "unit_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "unit_accounts_funds_accounts" FOREIGN KEY ("fund_id") REFERENCES "funds" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "unit_accounts_units_accounts" FOREIGN KEY ("unit_id") REFERENCES "units" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "unitaccount_tenant_id_account_ref" to table: "unit_accounts"
CREATE UNIQUE INDEX "unitaccount_tenant_id_account_ref" ON "unit_accounts" ("tenant_id", "account_ref");
-- Create index "unitaccount_tenant_id_primary_party_id" to table: "unit_accounts"
CREATE INDEX "unitaccount_tenant_id_primary_party_id" ON "unit_accounts" ("tenant_id", "primary_party_id");
-- Create index "unitaccount_tenant_id_unit_id_fund_id" to table: "unit_accounts"
CREATE UNIQUE INDEX "unitaccount_tenant_id_unit_id_fund_id" ON "unit_accounts" ("tenant_id", "unit_id", "fund_id");
-- Create "parties" table
CREATE TABLE "parties" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "kind" character varying NOT NULL DEFAULT 'person', "display_name" character varying NOT NULL, "first_name" character varying NULL, "last_name" character varying NULL, "company_name" character varying NULL, "registration_number" character varying NULL, "phone" character varying NULL, "phone_hash" character varying NULL, "alt_phone" character varying NULL, "email" character varying NULL, "id_type" character varying NOT NULL DEFAULT 'none', "national_id_enc" character varying NULL, "kra_pin_enc" character varying NULL, "nationality" character varying NULL, "is_tax_resident" boolean NOT NULL DEFAULT true, "is_diaspora" boolean NOT NULL DEFAULT false, "postal_address" character varying NULL, "auth_user_id" uuid NULL, "crm_contact_id" uuid NULL, "preferred_channel" character varying NOT NULL DEFAULT 'sms', "language" character varying NOT NULL DEFAULT 'en', "consents" jsonb NULL, "terms_accepted_version" character varying NULL, "terms_accepted_at" timestamptz NULL, "invited_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'active', "notes" text NULL, "custom_fields" jsonb NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "party_tenant_id_auth_user_id" to table: "parties"
CREATE INDEX "party_tenant_id_auth_user_id" ON "parties" ("tenant_id", "auth_user_id");
-- Create index "party_tenant_id_display_name" to table: "parties"
CREATE INDEX "party_tenant_id_display_name" ON "parties" ("tenant_id", "display_name");
-- Create index "party_tenant_id_phone_hash" to table: "parties"
CREATE INDEX "party_tenant_id_phone_hash" ON "parties" ("tenant_id", "phone_hash");
-- Create "unit_parties" table
CREATE TABLE "unit_parties" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "role" character varying NOT NULL, "ownership_share" numeric(18,3) NOT NULL, "is_primary" boolean NOT NULL DEFAULT false, "start_date" timestamptz NOT NULL, "end_date" timestamptz NULL, "bill_to" jsonb NULL, "revert_after_days" bigint NULL, "source" character varying NOT NULL DEFAULT 'manual', "status" character varying NOT NULL DEFAULT 'active', "created_by" uuid NULL, "party_id" uuid NOT NULL, "unit_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "unit_parties_parties_unit_links" FOREIGN KEY ("party_id") REFERENCES "parties" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "unit_parties_units_parties" FOREIGN KEY ("unit_id") REFERENCES "units" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "unitparty_tenant_id_party_id_status" to table: "unit_parties"
CREATE INDEX "unitparty_tenant_id_party_id_status" ON "unit_parties" ("tenant_id", "party_id", "status");
-- Create index "unitparty_tenant_id_unit_id_role_status" to table: "unit_parties"
CREATE INDEX "unitparty_tenant_id_unit_id_role_status" ON "unit_parties" ("tenant_id", "unit_id", "role", "status");
-- Create "user_role_assignments" table
CREATE TABLE "user_role_assignments" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "assigned_by" uuid NOT NULL, "assigned_at" timestamptz NOT NULL, "expires_at" timestamptz NULL, "user_id" uuid NOT NULL, "role_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "user_role_assignments_maskani_roles_role" FOREIGN KEY ("role_id") REFERENCES "maskani_roles" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "user_role_assignments_maskani_users_user" FOREIGN KEY ("user_id") REFERENCES "maskani_users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "userroleassignment_role_id" to table: "user_role_assignments"
CREATE INDEX "userroleassignment_role_id" ON "user_role_assignments" ("role_id");
-- Create index "userroleassignment_tenant_id_user_id_role_id" to table: "user_role_assignments"
CREATE UNIQUE INDEX "userroleassignment_tenant_id_user_id_role_id" ON "user_role_assignments" ("tenant_id", "user_id", "role_id");
-- Create "vendors" table
CREATE TABLE "vendors" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "treasury_vendor_id" uuid NULL, "name" character varying NOT NULL, "categories" jsonb NULL, "kra_pin_enc" character varying NULL, "registration_number" character varying NULL, "contact_name" character varying NULL, "phone" character varying NULL, "email" character varying NULL, "payment_details" jsonb NULL, "payment_details_verified_at" timestamptz NULL, "rating" double precision NULL, "status" character varying NOT NULL DEFAULT 'active', "custom_fields" jsonb NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "vendor_tenant_id_name" to table: "vendors"
CREATE INDEX "vendor_tenant_id_name" ON "vendors" ("tenant_id", "name");
-- Create index "vendor_tenant_id_status" to table: "vendors"
CREATE INDEX "vendor_tenant_id_status" ON "vendors" ("tenant_id", "status");
-- Create "vendor_contracts" table
CREATE TABLE "vendor_contracts" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "contract_number" character varying NULL, "service_category" character varying NOT NULL, "scope" text NULL, "fee_basis" character varying NOT NULL DEFAULT 'fixed_monthly', "fee_amount" numeric(18,2) NOT NULL, "sla" jsonb NULL, "service_credits" jsonb NULL, "starts_on" timestamptz NOT NULL, "ends_on" timestamptz NULL, "renewal_notice_days" bigint NOT NULL DEFAULT 30, "auto_renew" boolean NOT NULL DEFAULT false, "budget_line_code" character varying NULL, "document_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'active', "vendor_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "vendor_contracts_vendors_contracts" FOREIGN KEY ("vendor_id") REFERENCES "vendors" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "vendorcontract_tenant_id_ends_on" to table: "vendor_contracts"
CREATE INDEX "vendorcontract_tenant_id_ends_on" ON "vendor_contracts" ("tenant_id", "ends_on");
-- Create index "vendorcontract_tenant_id_property_id_status" to table: "vendor_contracts"
CREATE INDEX "vendorcontract_tenant_id_property_id_status" ON "vendor_contracts" ("tenant_id", "property_id", "status");
-- Create "vendor_documents" table
CREATE TABLE "vendor_documents" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "doc_type" character varying NOT NULL, "number" character varying NULL, "issued_at" timestamptz NULL, "expires_at" timestamptz NULL, "file_key" character varying NULL, "status" character varying NOT NULL DEFAULT 'valid', "verified_by" uuid NULL, "last_alert_days" bigint NULL, "vendor_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "vendor_documents_vendors_documents" FOREIGN KEY ("vendor_id") REFERENCES "vendors" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "vendordocument_tenant_id_expires_at" to table: "vendor_documents"
CREATE INDEX "vendordocument_tenant_id_expires_at" ON "vendor_documents" ("tenant_id", "expires_at");
-- Create index "vendordocument_tenant_id_vendor_id" to table: "vendor_documents"
CREATE INDEX "vendordocument_tenant_id_vendor_id" ON "vendor_documents" ("tenant_id", "vendor_id");
-- Create "vendor_personnels" table
CREATE TABLE "vendor_personnels" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "full_name" character varying NOT NULL, "role" character varying NULL, "phone" character varying NULL, "badge_number" character varying NOT NULL, "pin_hash" character varying NULL, "photo_key" character varying NULL, "property_ids" jsonb NULL, "status" character varying NOT NULL DEFAULT 'active', "deactivated_at" timestamptz NULL, "vendor_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "vendor_personnels_vendors_personnel" FOREIGN KEY ("vendor_id") REFERENCES "vendors" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "vendorpersonnel_tenant_id_badge_number" to table: "vendor_personnels"
CREATE UNIQUE INDEX "vendorpersonnel_tenant_id_badge_number" ON "vendor_personnels" ("tenant_id", "badge_number");
-- Create index "vendorpersonnel_tenant_id_vendor_id_status" to table: "vendor_personnels"
CREATE INDEX "vendorpersonnel_tenant_id_vendor_id_status" ON "vendor_personnels" ("tenant_id", "vendor_id", "status");
-- Create "work_orders" table
CREATE TABLE "work_orders" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "number" character varying NOT NULL, "property_id" uuid NOT NULL, "unit_id" uuid NULL, "area" character varying NULL, "category" character varying NOT NULL, "priority" character varying NOT NULL DEFAULT 'normal', "title" character varying NOT NULL, "description" text NULL, "source" character varying NOT NULL DEFAULT 'staff', "requested_by_party_id" uuid NULL, "requested_by_user_id" uuid NULL, "assignee_kind" character varying NOT NULL DEFAULT 'none', "vendor_id" uuid NULL, "erp_employee_id" character varying NULL, "assigned_user_id" uuid NULL, "response_due_at" timestamptz NULL, "resolution_due_at" timestamptz NULL, "responded_at" timestamptz NULL, "completed_at" timestamptz NULL, "confirmed_at" timestamptz NULL, "reopened_count" bigint NOT NULL DEFAULT 0, "sla_breached" boolean NOT NULL DEFAULT false, "quote_amount" numeric(18,2) NULL, "quote_status" character varying NOT NULL DEFAULT 'none', "cost_amount" numeric(18,2) NOT NULL, "recharge" boolean NOT NULL DEFAULT false, "recharge_unit_account_id" uuid NULL, "recharge_invoice_id" uuid NULL, "photos_before" jsonb NULL, "photos_after" jsonb NULL, "parts" jsonb NULL, "minutes_on_site" bigint NOT NULL DEFAULT 0, "asset_ref" character varying NULL, "maintenance_schedule_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'requested', "custom_fields" jsonb NULL, "created_by" uuid NULL, PRIMARY KEY ("id"));
-- Create index "workorder_tenant_id_number" to table: "work_orders"
CREATE UNIQUE INDEX "workorder_tenant_id_number" ON "work_orders" ("tenant_id", "number");
-- Create index "workorder_tenant_id_property_id_status_priority" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_property_id_status_priority" ON "work_orders" ("tenant_id", "property_id", "status", "priority");
-- Create index "workorder_tenant_id_requested_by_party_id" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_requested_by_party_id" ON "work_orders" ("tenant_id", "requested_by_party_id");
-- Create index "workorder_tenant_id_resolution_due_at" to table: "work_orders"
CREATE INDEX "workorder_tenant_id_resolution_due_at" ON "work_orders" ("tenant_id", "resolution_due_at") WHERE ((status)::text <> ALL ((ARRAY['completed'::character varying, 'confirmed'::character varying, 'closed'::character varying, 'cancelled'::character varying])::text[]));
-- Create "work_order_events" table
CREATE TABLE "work_order_events" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "kind" character varying NOT NULL, "from_status" character varying NULL, "to_status" character varying NULL, "note" text NULL, "actor_id" uuid NULL, "actor_kind" character varying NULL, "work_order_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "work_order_events_work_orders_events" FOREIGN KEY ("work_order_id") REFERENCES "work_orders" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "workorderevent_tenant_id_work_order_id_created_at" to table: "work_order_events"
CREATE INDEX "workorderevent_tenant_id_work_order_id_created_at" ON "work_order_events" ("tenant_id", "work_order_id", "created_at");
