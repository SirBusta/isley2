-- Stock tracking: wanted = on the user's wish list (only meaningful while
-- seed_count is 0; cleared when seeds are added). seeds_added_on = the date
-- (YYYY-MM-DD) the current seeds were added to stock; NULL when unknown
-- (strains stocked before this column existed) or when out of stock.
ALTER TABLE strain ADD COLUMN wanted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strain ADD COLUMN seeds_added_on TEXT;
