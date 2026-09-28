-- Expected plant height and yield, indoor and outdoor. Free text with units
-- because sources report ranges like "90-150cm" and "450-550 g/m²".
ALTER TABLE strain ADD COLUMN height_indoor TEXT;
ALTER TABLE strain ADD COLUMN height_outdoor TEXT;
ALTER TABLE strain ADD COLUMN yield_indoor TEXT;
ALTER TABLE strain ADD COLUMN yield_outdoor TEXT;
