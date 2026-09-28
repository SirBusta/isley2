-- The same imported strain can legitimately exist once per breeder: growers
-- buy the same strain from different seed companies. Imported-strain identity
-- therefore becomes (source key, breeder) instead of the source key alone,
-- so importing a strain for a second breeder adds a row instead of
-- overwriting the first one's breeder.
DROP INDEX IF EXISTS idx_strain_straincompass_slug;
DROP INDEX IF EXISTS idx_strain_cannadb_uri;

CREATE UNIQUE INDEX idx_strain_straincompass_slug_breeder ON strain(straincompass_slug, breeder_id) WHERE straincompass_slug IS NOT NULL;
CREATE UNIQUE INDEX idx_strain_cannadb_uri_breeder ON strain(cannadb_uri, breeder_id) WHERE cannadb_uri IS NOT NULL;
