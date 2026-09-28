-- Fails if the same slug/uri was imported for more than one breeder; remove
-- the extra rows first.
DROP INDEX IF EXISTS idx_strain_cannadb_uri_breeder;
DROP INDEX IF EXISTS idx_strain_straincompass_slug_breeder;

CREATE UNIQUE INDEX idx_strain_straincompass_slug ON strain(straincompass_slug) WHERE straincompass_slug IS NOT NULL;
CREATE UNIQUE INDEX idx_strain_cannadb_uri ON strain(cannadb_uri) WHERE cannadb_uri IS NOT NULL;
