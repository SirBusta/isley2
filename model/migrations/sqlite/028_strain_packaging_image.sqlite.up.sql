-- Seed-pack (packaging) image for a strain: a file under uploads/strains/,
-- either uploaded by the user or downloaded from CannaDB with their consent.
ALTER TABLE strain ADD COLUMN packaging_image TEXT;
