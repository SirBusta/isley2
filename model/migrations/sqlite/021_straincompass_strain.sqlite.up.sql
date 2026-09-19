-- Provenance and cannabinoid data for strains imported from StrainCompass
-- (straincompass.com/api). straincompass_slug is the record's stable slug
-- and doubles as the dedupe/upsert key so a re-import updates in place
-- instead of creating a duplicate row.
ALTER TABLE strain ADD COLUMN straincompass_slug TEXT;
ALTER TABLE strain ADD COLUMN straincompass_updated_at TEXT;

ALTER TABLE strain ADD COLUMN thc_min REAL;
ALTER TABLE strain ADD COLUMN thc_max REAL;
ALTER TABLE strain ADD COLUMN cbd_min REAL;
ALTER TABLE strain ADD COLUMN cbd_max REAL;
ALTER TABLE strain ADD COLUMN cbn_max REAL;
ALTER TABLE strain ADD COLUMN cbg_max REAL;

-- Nullable 0/1: NULL means "no StrainCompass data", not "unverified".
ALTER TABLE strain ADD COLUMN straincompass_verified INTEGER;
ALTER TABLE strain ADD COLUMN straincompass_quality_score REAL;

-- Comma-joined list of upstream data sources StrainCompass itself cites
-- (e.g. "seedfinder"), display-only.
ALTER TABLE strain ADD COLUMN straincompass_sources TEXT;

-- Free-text lineage string as reported by StrainCompass (e.g.
-- "Biscotti x Sherb Bx"). Display-only — not parsed into strain_lineage,
-- since the grammar isn't guaranteed consistent enough to safely auto-split.
ALTER TABLE strain ADD COLUMN straincompass_lineage_note TEXT;

-- Partial-unique so manually-created rows (NULL slug) are unconstrained
-- while imported rows are unique per StrainCompass record.
CREATE UNIQUE INDEX idx_strain_straincompass_slug ON strain(straincompass_slug) WHERE straincompass_slug IS NOT NULL;
