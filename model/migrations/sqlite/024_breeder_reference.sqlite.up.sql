-- Known breeder names used only as type-ahead suggestions, refreshed monthly
-- from StrainCompass's public breeder directory. Empty until the first
-- successful refresh; until then the snapshot bundled with the app is used.
-- Separate from `breeder` so ~1,200 never-used names don't show up in breeder
-- filters and dropdowns.
CREATE TABLE breeder_reference (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);
