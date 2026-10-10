-- Legacy approval fields replaced by the central engine (rules carry ordered steps, requests carry
-- the decisions); the tables held no rows when this ran.
DROP INDEX IF EXISTS "approvalrule_tenant_id_action_active";
ALTER TABLE "approval_rules" DROP COLUMN IF EXISTS "action", DROP COLUMN IF EXISTS "levels", DROP COLUMN IF EXISTS "approver_roles", DROP COLUMN IF EXISTS "active";
ALTER TABLE "adjustments" DROP COLUMN IF EXISTS "approvals";
-- Modify "approval_rules" table
ALTER TABLE "approval_rules" ADD COLUMN "module" character varying NOT NULL, ADD COLUMN "name" character varying NOT NULL DEFAULT '', ADD COLUMN "steps" jsonb NOT NULL, ADD COLUMN "is_active" boolean NOT NULL DEFAULT true;
-- Create index "approvalrule_tenant_id_module_is_active" to table: "approval_rules"
CREATE INDEX "approvalrule_tenant_id_module_is_active" ON "approval_rules" ("tenant_id", "module", "is_active");
-- Create "approval_requests" table
CREATE TABLE "approval_requests" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "metadata" jsonb NULL, "module" character varying NOT NULL, "object_id" uuid NOT NULL, "object_reference" character varying NOT NULL DEFAULT '', "amount" numeric(18,2) NOT NULL, "property_id" uuid NULL, "rule_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'pending', "current_sequence" bigint NOT NULL DEFAULT 1, "current_approver" character varying NOT NULL DEFAULT '', "actions" jsonb NOT NULL, "submitted_by" uuid NULL, "submitted_by_name" character varying NOT NULL DEFAULT '', "decided_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "approvalrequest_tenant_id_object_id_created_at" to table: "approval_requests"
CREATE INDEX "approvalrequest_tenant_id_object_id_created_at" ON "approval_requests" ("tenant_id", "object_id", "created_at");
-- Create index "approvalrequest_tenant_id_property_id_status_created_at" to table: "approval_requests"
CREATE INDEX "approvalrequest_tenant_id_property_id_status_created_at" ON "approval_requests" ("tenant_id", "property_id", "status", "created_at");
-- Create index "approvalrequest_tenant_id_status_created_at" to table: "approval_requests"
CREATE INDEX "approvalrequest_tenant_id_status_created_at" ON "approval_requests" ("tenant_id", "status", "created_at");
-- Create index "approvalrequest_tenant_id_status_current_approver" to table: "approval_requests"
CREATE INDEX "approvalrequest_tenant_id_status_current_approver" ON "approval_requests" ("tenant_id", "status", "current_approver");
