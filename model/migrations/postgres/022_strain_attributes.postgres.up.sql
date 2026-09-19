-- Effects, flavors, terpenes, and medical uses imported from StrainCompass.
-- Only "sourced" (strain-specific, not generic category filler) data is
-- ever inserted here by the importer, so there is no provenance/trust
-- column to carry on these rows.
CREATE TABLE strain_effect (
    id SERIAL PRIMARY KEY,
    strain_id INTEGER NOT NULL REFERENCES strain(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    intensity REAL
);
CREATE INDEX idx_strain_effect_strain_id ON strain_effect(strain_id);

CREATE TABLE strain_flavor (
    id SERIAL PRIMARY KEY,
    strain_id INTEGER NOT NULL REFERENCES strain(id) ON DELETE CASCADE,
    name TEXT NOT NULL
);
CREATE INDEX idx_strain_flavor_strain_id ON strain_flavor(strain_id);

CREATE TABLE strain_terpene (
    id SERIAL PRIMARY KEY,
    strain_id INTEGER NOT NULL REFERENCES strain(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    level TEXT -- qualitative: "low"/"medium"/"high"
);
CREATE INDEX idx_strain_terpene_strain_id ON strain_terpene(strain_id);

CREATE TABLE strain_medical_use (
    id SERIAL PRIMARY KEY,
    strain_id INTEGER NOT NULL REFERENCES strain(id) ON DELETE CASCADE,
    name TEXT NOT NULL
);
CREATE INDEX idx_strain_medical_use_strain_id ON strain_medical_use(strain_id);
