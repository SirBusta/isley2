-- Effects, flavors, terpenes, and medical uses imported from StrainCompass.
-- Only "sourced" (strain-specific, not generic category filler) data is
-- ever inserted here by the importer, so there is no provenance/trust
-- column to carry on these rows.
CREATE TABLE strain_effect (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strain_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    intensity REAL,
    FOREIGN KEY (strain_id) REFERENCES strain(id) ON DELETE CASCADE
);
CREATE INDEX idx_strain_effect_strain_id ON strain_effect(strain_id);

CREATE TABLE strain_flavor (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strain_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY (strain_id) REFERENCES strain(id) ON DELETE CASCADE
);
CREATE INDEX idx_strain_flavor_strain_id ON strain_flavor(strain_id);

CREATE TABLE strain_terpene (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strain_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    level TEXT, -- qualitative: "low"/"medium"/"high"
    FOREIGN KEY (strain_id) REFERENCES strain(id) ON DELETE CASCADE
);
CREATE INDEX idx_strain_terpene_strain_id ON strain_terpene(strain_id);

CREATE TABLE strain_medical_use (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strain_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY (strain_id) REFERENCES strain(id) ON DELETE CASCADE
);
CREATE INDEX idx_strain_medical_use_strain_id ON strain_medical_use(strain_id);
