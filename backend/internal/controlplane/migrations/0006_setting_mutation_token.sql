-- MaestroVPN HA control-plane schema v6: request-unique setting CAS proof.
-- maestro:statement
ALTER TABLE cluster_settings
ADD COLUMN last_mutation_token TEXT NOT NULL DEFAULT '';
