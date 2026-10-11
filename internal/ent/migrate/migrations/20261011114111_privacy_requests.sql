-- Create index "privacyrequest_tenant_id_created_at_id" to table: "privacy_requests"
CREATE INDEX "privacyrequest_tenant_id_created_at_id" ON "privacy_requests" ("tenant_id", "created_at", "id");
-- Create index "privacyrequest_tenant_id_party_id_created_at" to table: "privacy_requests"
CREATE INDEX "privacyrequest_tenant_id_party_id_created_at" ON "privacy_requests" ("tenant_id", "party_id", "created_at");
-- Exports are built on demand from the live records; no stored copy, so no file key
ALTER TABLE "privacy_requests" DROP COLUMN IF EXISTS "export_file_key";
