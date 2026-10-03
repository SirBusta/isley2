-- How the strain was acquired: "clone", "feminized" (seed), "regular" (seed),
-- or NULL when not set (every strain created before this column existed).
ALTER TABLE strain ADD COLUMN seed_type TEXT;
