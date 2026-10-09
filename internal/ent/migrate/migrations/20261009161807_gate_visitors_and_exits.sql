-- Modify "gate_events" table
ALTER TABLE "gate_events" ADD COLUMN "decided_by" character varying NULL, ADD COLUMN "visitor_id" uuid NULL, ADD COLUMN "exited_at" timestamptz NULL, ADD COLUMN "entry_event_id" uuid NULL;
-- Create index "gateevent_tenant_id_property_id_exited_at_occurred_at" to table: "gate_events"
CREATE INDEX "gateevent_tenant_id_property_id_exited_at_occurred_at" ON "gate_events" ("tenant_id", "property_id", "exited_at", "occurred_at");
-- Create index "gateevent_tenant_id_visitor_id_occurred_at" to table: "gate_events"
CREATE INDEX "gateevent_tenant_id_visitor_id_occurred_at" ON "gate_events" ("tenant_id", "visitor_id", "occurred_at");
-- Modify "visitor_passes" table
ALTER TABLE "visitor_passes" ADD COLUMN "visitor_id" uuid NULL;
-- Create "visitors" table
CREATE TABLE "visitors" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "property_id" uuid NOT NULL, "name" character varying NOT NULL, "phone" character varying NULL, "id_number_hash" character varying NULL, "id_number_hint" character varying NULL, "vehicle_plate" character varying NULL, "vehicle_plates" jsonb NULL, "company" character varying NULL, "visits" bigint NOT NULL DEFAULT 0, "last_visit_at" timestamptz NULL, "last_host_unit_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'active', "notes" text NULL, PRIMARY KEY ("id"));
-- Create index "visitor_tenant_id_property_id_id_number_hash" to table: "visitors"
CREATE INDEX "visitor_tenant_id_property_id_id_number_hash" ON "visitors" ("tenant_id", "property_id", "id_number_hash");
-- Create index "visitor_tenant_id_property_id_last_visit_at" to table: "visitors"
CREATE INDEX "visitor_tenant_id_property_id_last_visit_at" ON "visitors" ("tenant_id", "property_id", "last_visit_at");
-- Create index "visitor_tenant_id_property_id_phone" to table: "visitors"
CREATE INDEX "visitor_tenant_id_property_id_phone" ON "visitors" ("tenant_id", "property_id", "phone");
-- Create index "visitor_tenant_id_property_id_vehicle_plate" to table: "visitors"
CREATE INDEX "visitor_tenant_id_property_id_vehicle_plate" ON "visitors" ("tenant_id", "property_id", "vehicle_plate");
