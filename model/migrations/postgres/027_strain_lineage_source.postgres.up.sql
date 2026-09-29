-- Where a strain's parent strains came from: 'straincompass' or 'cannadb'
-- when filled in by an import, NULL when entered or edited by the user.
-- lineage_source_uri points at the source record (a CannaDB AT-URI) so the
-- Strain page can link to it.
ALTER TABLE strain ADD COLUMN lineage_source TEXT;
ALTER TABLE strain ADD COLUMN lineage_source_uri TEXT;
