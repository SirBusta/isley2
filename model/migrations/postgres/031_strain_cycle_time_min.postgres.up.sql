-- Short end of a flowering-time / seed-to-harvest range, in days (e.g.
-- StrainCompass's 10-12 weeks -> 70). cycle_time stays the long end and is
-- what the harvest estimate and sorting use. NULL = no range, one value.
ALTER TABLE strain ADD COLUMN cycle_time_min INTEGER;
