-- Create index "auditlog_created_at" to table: "audit_logs"
CREATE INDEX "auditlog_created_at" ON "audit_logs" ("created_at");
-- Create index "documentaccesslog_created_at" to table: "document_access_logs"
CREATE INDEX "documentaccesslog_created_at" ON "document_access_logs" ("created_at");
-- Create index "patrolscan_scanned_at" to table: "patrol_scans"
CREATE INDEX "patrolscan_scanned_at" ON "patrol_scans" ("scanned_at");
-- Create index "visitorpass_valid_to" to table: "visitor_passes"
CREATE INDEX "visitorpass_valid_to" ON "visitor_passes" ("valid_to");
-- Create index "visitor_last_visit_at" to table: "visitors"
CREATE INDEX "visitor_last_visit_at" ON "visitors" ("last_visit_at");
