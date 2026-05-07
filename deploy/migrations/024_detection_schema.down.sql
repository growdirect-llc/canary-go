-- 024_detection_schema.down.sql
-- Rollback detection schema.

DROP TABLE IF EXISTS detection.allow_list;
DROP TABLE IF EXISTS detection.lp_substrate;
DROP TABLE IF EXISTS detection.detections;
DROP TABLE IF EXISTS detection.detection_rules;
DROP SCHEMA IF EXISTS detection;
