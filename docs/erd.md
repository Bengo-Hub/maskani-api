# maskani-api data model

Every tenant-owned table carries `id uuid`, `tenant_id uuid`, `created_at`, `updated_at` and
`metadata jsonb` (default `{}`). Records users edit also carry `created_by` and, where listed,
`custom_fields jsonb` driven by `custom_field_defs`. Money is `numeric(18,2)`, quantities
`numeric(18,3)`. Enumerations are Ent enums (text with a check), so adding a value is a migration,
but most per-tenant variety lives in catalogues and `metadata`, never in new columns.

Data owned elsewhere is referenced by ID only:

| Data | Owner | Held here |
|---|---|---|
| Login identity, sessions, roles | auth-api | `auth_user_id`, outlet IDs |
| Invoices, payments, balances, statements, vendor bills, ledger | treasury-api | invoice, vendor, bank account, cost centre IDs |
| Staff, attendance, payroll, casual payments, assets | erp-api | employee and asset IDs |
| Messages and delivery status | notifications-api | message ID where audit needs it |
| Leads (R3) | marketflow-api | lead ID |

## Platform and identity (template shared with other services)

| Table | Purpose and key fields |
|---|---|
| `tenants` | Projection of auth-api tenant: id, slug, name, status, use_case, tenant_type, synced_at |
| `outlets` | Projection of auth-api branches; each property is one. id, tenant_id, name, code, status |
| `maskani_users` | Local user projection: auth user id, tenant, email, phone, full name, kind (staff, owner, occupant, vendor_supervisor, guard), status |
| `maskani_user_outlets` | Staff assignment to properties: user, outlet (property), property_role (property_manager, caretaker, finance, letting, sales), erp_employee_id, is_primary, from, to |
| `maskani_roles`, `maskani_permissions`, `role_permissions`, `user_role_assignments` | RBAC catalogue (platform wide, never tenant scoped) and per-tenant assignments |
| `audit_logs` | Financial, contract, access and configuration changes: actor, action, entity, before, after, ip, request id. Index (tenant_id, entity_type, entity_id), (tenant_id, created_at) |
| `outbox_events` | Transactional outbox drained to NATS |
| `consumed_events` | Event ID, consumer, processed_at. Primary key (event_id, consumer) |
| `document_sequences` | Numbering per tenant and document type with prefix |

## Configuration

| Table | Key fields | Keys and indexes |
|---|---|---|
| `tenant_settings` | tenant_type (estate_operator, property_manager, landlord, agent, owners_association), use_case_preset, currency, timezone, billing_day, reading_window (start, end day), due_day, quiet_hours, allocation_order (oldest_first, priority), arrears_steps, terms_version, privacy_version; metadata holds branding overrides, walk_in_policy and late_charge | unique tenant_id |
| `tenant_modules` | module, enabled, enabled_at, disabled_at, enabled_by | unique (tenant_id, module) |
| `catalog_entries` | tenant_id nullable (null = platform default), kind (property_type, unit_use, unit_type, amenity, wo_category, vendor_category, vendor_doc_type, pass_type, incident_type, notice_category, title_stage, checklist), code, name, description, parent_code, sort, active, attrs jsonb | unique (kind, code) where tenant null; unique (tenant_id, kind, code) |
| `custom_field_defs` | entity (property, unit, party, sale_contract, work_order, vendor), key, label, field_type, options, required, show_in_list, sort, active | unique (tenant_id, entity, key) |
| `approval_rules` | module (credit_note, adjustment, manual_payment, restructure, vendor_bill, refund, work_order_quote, deposit_deduction, remittance, write_off), name, min_amount, max_amount, steps (JSON: sequence, name, approver_role), require_otp, is_active | index (tenant_id, module, is_active) |
| `approval_requests` | module, object_id, object_reference, amount, property_id, rule_id, status (pending, approved, rejected, cancelled), current_sequence, current_approver, actions (JSON: each step with status, acted_by, acted_by_name, acted_at, comment), submitted_by, submitted_by_name, decided_at; metadata for the inbox label | index (tenant_id, object_id, created_at), (tenant_id, status, created_at), (tenant_id, property_id, status, created_at), (tenant_id, status, current_approver) |
| `reminder_schedules` | kind (bill, instalment, document_expiry, licence, lease_end), offsets_days, channels, template_code, active | unique (tenant_id, kind) |

## Register

| Table | Key fields | Keys and indexes |
|---|---|---|
| `portfolios` | code, name, kind (own, client), landlord_party_id, mandate (R2, jsonb until then), status | unique (tenant_id, code) |
| `properties` | portfolio_id, outlet_id, code, name, property_type, use_case, description, address_line, area, town, county, country, latitude, longitude, plot_number, title_number, land_size_sqm, year_built, phases, amenities, photos, module_overrides, status, management_since, custom_fields | unique (tenant_id, code); index (tenant_id, status) |
| `blocks` | property_id, code, name, phase, floors, description, status | unique (property_id, code) |
| `units` | property_id, block_id, code, unit_type, use (residential, office, retail, industrial, parking, land, storage, other), bedrooms, bathrooms, size_sqm, plot_size_sqm, floor, entitlement, parking_bays, furnished, phase, sale_status (not_for_sale, available, reserved, under_agreement, fully_paid, handed_over, titled, in_default), occupancy_status (vacant, owner_occupied, tenanted, under_renovation, coming_vacant), rentable, handed_over_at, features, photos, status, custom_fields | unique (tenant_id, property_id, code); index (tenant_id, property_id, sale_status), (tenant_id, property_id, occupancy_status), (tenant_id, block_id) |

## Parties

| Table | Key fields | Keys and indexes |
|---|---|---|
| `parties` | kind (person, company), display_name, first_name, last_name, company_name, registration_number, phone (E.164), phone_hash, alt_phone, email, id_type, national_id_enc, kra_pin_enc, nationality, is_tax_resident, is_diaspora, postal_address, auth_user_id, crm_contact_id, preferred_channel, language, consents, terms_accepted_version, terms_accepted_at, status, notes, custom_fields | index (tenant_id, phone_hash), (tenant_id, auth_user_id), (tenant_id, lower(display_name)) |
| `unit_parties` | unit_id, party_id, role (owner, joint_owner, buyer, occupant, household_member, domestic_staff, emergency_contact), ownership_share, is_primary, start_date, end_date, bill_to (charge codes this party receives), revert_after_days, source (import, manual, sale, transfer), status (active, ended) | index (tenant_id, unit_id, role, status), (tenant_id, party_id, status) |
| `vehicles` | unit_id, party_id, plate, make, model, colour, sticker_number, status | unique (tenant_id, plate) |

Identity numbers and KRA PINs are AES-GCM encrypted at field level (`FIELD_ENCRYPTION_KEY`); phones
are stored for messaging and hashed (HMAC) for matching.

## Billing

| Table | Key fields | Keys and indexes |
|---|---|---|
| `funds` | code (estate, sales, deposits, client_rent, sinking), name, kind, treasury_bank_account_id, paybill_shortcode, account_prefix ("" estate, "S-" sales), cost_center_code, income_account_code, receivable_account_code, is_default, status | unique (tenant_id, code) |
| `charge_types` | code, name, charge_group (occupancy, services, utilities, reserves, amenities, recoveries, sales), basis (fixed, per_unit_type, per_sqm, entitlement, metered, percentage, one_off), frequency (monthly, quarterly, half_yearly, annual, on_event, one_off), event_trigger, applies_to (scope and ids), bill_to (owner, occupant, landlord), reassignable, fund_code, ledger_account_code, cost_center_code, vat_rate, tax_exempt, etims_item_code, wht_applicable, proration (none, days, full_month), penalty (enabled, grace_days, rate_pct, flat_amount, cap, compounding false), allocation_priority, percentage_of_code, seeded_from, active, sort | unique (tenant_id, code) |
| `charge_rates` | charge_type_id, scope (tenant, property, unit_type, unit), property_id, unit_type, unit_id, amount, tariff (blocks: from, to, rate), fixed_meter_charge, effective_from, effective_to, notes | index (tenant_id, charge_type_id, scope, effective_from DESC) |
| `unit_charges` | Opt-in or per-unit charges (second parking bay): unit_id, charge_type_id, party_id, quantity, amount_override, start_date, end_date, status | index (tenant_id, unit_id, status) |
| `unit_accounts` | unit_id, fund_id, account_ref ("B07", "S-B07"), primary_party_id, treasury_customer_name, treasury_customer_phone, c2b_route_registered_at, balance (display cache), balance_synced_at, last_payment_at, status | unique (tenant_id, account_ref); unique (tenant_id, unit_id, fund_id) |
| `billing_runs` | property_id, fund_id, period (YYYY-MM), run_kind (regular, adhoc), status (draft, previewed, issuing, issued, partially_failed, cancelled), invoice_date, due_date, unit_count, line_count, total_amount, issued_count, failed_count, started_by, issued_at, error | unique (tenant_id, property_id, fund_id, period, run_kind) where status <> cancelled |
| `billing_run_lines` | run_id, unit_id, unit_account_id, party_id, lines (charge_code, description, quantity, rate, amount, tax_rate, tax), subtotal, tax_total, total, status (pending, issued, failed, skipped), skip_reason, treasury_invoice_id, invoice_number, attempts, last_error | unique (run_id, unit_account_id); index (tenant_id, status) |
| `adjustments` | unit_account_id, kind (credit_note, debit, waiver, write_off), amount, reason, treasury_invoice_id, treasury_credit_note_id, status (pending_approval, approved, rejected, applied), requested_by, approvals | index (tenant_id, status) |
| `bill_queries` | unit_account_id, treasury_invoice_id, party_id, subject, body, status (open, in_review, resolved, rejected), assigned_to, due_by, resolution | index (tenant_id, status, due_by) |

The suspense queue is not copied locally: it is the treasury C2B inbox (`unreconciled`), read and
assigned through treasury S2S.

## Utilities

| Table | Key fields | Keys and indexes |
|---|---|---|
| `meters` | property_id, unit_id, kind (unit, bulk_supply, borehole, common_area), utility (water, gas, electricity_info), serial, make, location_note, installed_at, multiplier, initial_reading, closing_reading, walking_order, status (active, replaced, faulty, removed), replaced_by_id | unique (tenant_id, serial); index (tenant_id, property_id, kind, status) |
| `reading_rounds` | property_id, period, status (open, validating, closed), assigned_to, opened_at, closed_at, totals | unique (tenant_id, property_id, period) |
| `meter_readings` | round_id, meter_id, unit_id, period, reading, previous_reading, consumption, read_at, read_by, photo_key, source (round, estimate, import, handover, move_out, replacement), is_estimated, flags, status (pending, accepted, recheck, rejected), verified_by, notes | unique (tenant_id, meter_id, period, source); index (tenant_id, round_id, status) |

## Sales

| Table | Key fields | Keys and indexes |
|---|---|---|
| `price_lists` | property_id, name, phase, currency, effective_from, effective_to, status (draft, active, retired) | index (tenant_id, property_id, status) |
| `price_list_items` | price_list_id, unit_type, unit_id, price, reservation_fee, deposit_pct, min_deposit, max_term_months, discount_rules | index (price_list_id) |
| `reservations` | unit_id, party_id, price_list_item_id, price, fee_amount, fee_treasury_invoice_id, fee_paid_at, reserved_at, expires_at, status (pending_payment, active, converted, expired, cancelled), sale_contract_id | index (tenant_id, status, expires_at); one active per unit (partial unique) |
| `sale_contracts` | contract_number, property_id, unit_id, primary_buyer_id, buyers (party, share), price, discount, discount_reason, net_price, reservation_credit, deposit_amount, payment_option (outright, instalments, milestone, financed), financier, term_months, frequency (monthly, quarterly, milestone), interest_rate, late_charge, grace_days, buyer_advocate, seller_advocate, signed_at, agreement_document_id, unit_account_id, status (draft, active, fully_paid, handed_over, titled, in_default, terminated, cancelled), default_since, terminated_at, termination, invoiced_total, paid_total, custom_fields | unique (tenant_id, contract_number); index (tenant_id, status), (tenant_id, unit_id) |
| `instalment_schedules` | contract_id, version, status (proposed, active, superseded), reason, approved_by, approved_at, buyer_accepted_at | unique (contract_id, version) |
| `instalments` | schedule_id, contract_id, seq, kind (reservation, deposit, instalment, milestone, balance, financier), due_date, amount, milestone_label, released_at, released_by, evidence_key, status (scheduled, invoiced, partially_paid, paid, overdue, waived), treasury_invoice_id, invoice_number, paid_amount, paid_at | unique (schedule_id, seq); index (tenant_id, status, due_date) |
| `handovers` | contract_id, unit_id, scheduled_at, completed_at, keys, readings, checklist, snag_items, signed_document_id, status | index (tenant_id, status) |
| `title_stages` | contract_id, unit_id, stage (catalog title_stage), status, stage_date, reference, document_id, notes | unique (contract_id, stage) |

## Providers and works

| Table | Key fields | Keys and indexes |
|---|---|---|
| `vendors` | treasury_vendor_id, name, categories, kra_pin_enc, registration_number, contact_name, phone, email, payment_details (locked, verified_at), rating, status, custom_fields | index (tenant_id, status) |
| `vendor_documents` | vendor_id, doc_type, number, issued_at, expires_at, file_key, status (valid, expiring, expired), verified_by | index (tenant_id, expires_at) |
| `vendor_contracts` | vendor_id, property_id, contract_number, service_category, scope, fee_basis (fixed_monthly, per_visit, per_post, rate_card), fee_amount, sla, service_credits, starts_on, ends_on, renewal_notice_days, auto_renew, budget_line_code, document_id, status | index (tenant_id, property_id, status) |
| `service_schedules` | contract_id, property_id, name, frequency, days, time_window, zone, checklist, active | index (tenant_id, contract_id) |
| `service_visits` | schedule_id, contract_id, vendor_id, property_id, due_at, checked_in_at, checked_out_at, status (scheduled, completed, partial, missed), photos, checklist_result, resident_rating | index (tenant_id, property_id, due_at) |
| `vendor_personnel` | vendor_id, full_name, role, phone, badge_number, photo_key, status (active, deactivated), property_ids, deactivated_at | unique (tenant_id, badge_number) |
| `maintenance_schedules` | property_id, asset_ref, title, category, frequency, next_due_at, default_vendor_id, default_employee_id, active | index (tenant_id, next_due_at) |
| `work_orders` | number, property_id, unit_id, area, category, priority (emergency, high, normal, low), title, description, source (resident, staff, schedule, inspection), requested_by_party_id, requested_by_user_id, assignee_kind (vendor, staff), vendor_id, erp_employee_id, assigned_user_id, response_due_at, resolution_due_at, responded_at, completed_at, confirmed_at, reopened_count, sla_breached, quote_amount, quote_status, cost_amount, recharge, recharge_unit_account_id, recharge_invoice_id, photos_before, photos_after, parts, minutes_on_site, asset_ref, maintenance_schedule_id, status (requested, triaged, assigned, quoted, approved, in_progress, completed, confirmed, reopened, closed, cancelled), custom_fields | unique (tenant_id, number); index (tenant_id, property_id, status, priority), (tenant_id, resolution_due_at) where open |
| `work_order_events` | work_order_id, kind, from_status, to_status, note, actor_id, actor_kind | index (work_order_id, created_at) |

## Gate and security

| Table | Key fields | Keys and indexes |
|---|---|---|
| `gate_devices` | property_id, name, gate_name, device_key_hash, registered_by, last_seen_at, app_version, status | index (tenant_id, property_id) |
| `guard_posts` | property_id, contract_id, name, guards_day, guards_night, shift_pattern, active | index (tenant_id, property_id) |
| `rosters` | post_id, personnel_id, shift_date, shift (day, night), starts_at, ends_at, signed_on_at, signed_off_at, status | unique (post_id, personnel_id, shift_date, shift) |
| `patrol_checkpoints` | property_id, name, code, location_note, sort, active | unique (tenant_id, code) |
| `patrol_scans` | property_id, checkpoint_id, personnel_id, device_id, scanned_at, round_ref, client_event_id | unique (tenant_id, device_id, client_event_id); (tenant_id, property_id, scanned_at); BRIN in S6 |
| `visitor_passes` | property_id, unit_id, host_party_id, created_by_kind (resident, staff, guard), created_by_id, pass_type (guest_single, guest_recurring, domestic_staff, delivery, contractor, agency), visitor_name, visitor_phone, vehicle_plate, code_hash, code_hint, qr_token_hash, valid_from, valid_to, recurrence, max_entries, entries_used, work_order_id, status (active, used, expired, cancelled) | index (tenant_id, property_id, status, valid_to), (tenant_id, code_hash) |
| `gate_events` | property_id, device_id, pass_id, kind (entry, exit, denied, walk_in_request, walk_in_approved, walk_in_declined), visitor_name, visitor_phone, host_unit_id, vehicle_plate, id_sighted, occurred_at, offline, client_event_id, guard_personnel_id, notes | unique (tenant_id, device_id, client_event_id); index (tenant_id, property_id, occurred_at); BRIN in S6 |
| `incidents` | number, property_id, unit_id, category, severity (low, medium, high, critical), title, description, occurred_at, reported_by_kind, reported_by_id, photos, status (open, investigating, resolved, closed), notified_at, resolution | unique (tenant_id, number); index (tenant_id, property_id, status) |
| `occurrence_entries` | property_id, post_id, kind (handover, note), body, author_id, author_kind | index (tenant_id, property_id, created_at DESC) |

## Communication and documents

| Table | Key fields | Keys and indexes |
|---|---|---|
| `notices` | property_id, audience (scope, block ids, unit ids, party roles), channels, category, priority (routine, emergency), title, body, scheduled_at, sent_at, status (draft, scheduled, sending, sent, failed), recipients_count, delivered_count, created_by | index (tenant_id, status, scheduled_at) |
| `notice_deliveries` | notice_id, party_id, channel, destination, notification_message_id, status (queued, sent, delivered, failed), error, sent_at | unique (notice_id, party_id, channel) |
| `document_templates` | tenant_id nullable (platform starter), code, name, category, tenant_type, version, body, merge_fields, conditions, status (draft, approved, retired), approved_by, approved_at | unique (tenant_id, code, version) |
| `documents` | number, template_id, template_version, kind, title, entity_type, entity_id, unit_id, party_ids, file_key, sha256, verification_code, status (draft, issued, partly_signed, executed, expired, superseded), issued_at, executed_at, expires_at, key_dates, legal_hold, retention_until | unique (tenant_id, number); unique (verification_code); index (tenant_id, entity_type, entity_id) |
| `document_signatures` | document_id, party_id, method (otp_acceptance, wet_upload, e_signature), signed_at, document_hash, device, ip, scan_file_key | index (document_id) |
| `document_access_logs` | document_id, actor_id, actor_kind, action (view, download), ip | index (tenant_id, document_id, created_at) |
| `privacy_requests` | party_id, kind (access, correction, deletion), status, received_at, due_at, completed_at, export_file_key, notes | index (tenant_id, status, due_at) |

## Reporting

| Table | Key fields | Keys and indexes |
|---|---|---|
| `daily_stats` | property_id, day, billed, collected, invoices_issued, payments_count, arrears_total, arrears_0_30, arrears_31_60, arrears_61_90, arrears_90_plus, units_total, units_occupied, units_vacant, units_sold, water_supplied_m3, water_billed_m3, wo_opened, wo_closed, wo_sla_breaches, visitors, incidents | unique (tenant_id, property_id, day) |

## Release 2 and 3 entities (designed, added in their sprints)

| Release | Tables |
|---|---|
| R2 | `mandates`, `landlord_statements`, `remittance_runs`, `remittance_lines`, `rental_applications`, `leases`, `lease_parties`, `lease_charges`, `rent_reviews`, `inspections`, `inspection_items`, `deposit_settlements`, `turnovers` |
| R3 | `lister_profiles`, `listings`, `listing_media`, `listing_moderation`, `enquiries`, `viewings`, `saved_searches`, `favourites`, `listing_daily_stats` |

These attach to existing `units`, `parties`, `unit_accounts` and `documents`, so no R1 table needs to
change when they arrive.

## Relationships

```
portfolios 1:n properties 1:n blocks 1:n units
properties 1:n units
units 1:n unit_parties n:1 parties
units 1:n unit_accounts n:1 funds
unit_accounts 1:n billing_run_lines n:1 billing_runs n:1 properties
charge_types 1:n charge_rates
meters 1:n meter_readings n:1 reading_rounds
price_lists 1:n price_list_items
units 1:n reservations, 1:n sale_contracts 1:n instalment_schedules 1:n instalments
sale_contracts 1:1 handovers, 1:n title_stages
vendors 1:n vendor_documents, vendor_contracts, vendor_personnel
vendor_contracts 1:n service_schedules 1:n service_visits
properties 1:n work_orders 1:n work_order_events
properties 1:n visitor_passes, gate_events, incidents, gate_devices, guard_posts, patrol_checkpoints
notices 1:n notice_deliveries
document_templates 1:n documents 1:n document_signatures
```
