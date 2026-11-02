-- 004_drop_dynamic_providers.sql
-- Drop legacy dynamic provider tables cleanly in reverse dependency order.

DROP TABLE IF EXISTS cookie_partition_rules;
DROP TABLE IF EXISTS payload_templates;
DROP TABLE IF EXISTS dynamic_endpoints;
DROP TABLE IF EXISTS provider_features;
DROP TABLE IF EXISTS account_secrets;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS dynamic_providers;
