# openmensa-parser-berlin
openmensa parser for Berlin's university cafeterias

## Command-line arguments

By default the parser will use ids stored in `berlin/ids.json` and update
metadata and full feed for every canteen represented by those ids.

### `-u`: update ids and index files
This will update the ids and index file at `berlin/{ids,index}.json` while
making a backup of the old version at `berlin/{ids,index}.json.old`. Note for
some canteens the openmensa identifier may change slightly from time to time
as it is generated in an automatic fashion. Hence one might want to manually
edit both file to avoid such change.
