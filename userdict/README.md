# User dictionary

Place the user dictionary at `userdict/userdict.txt` (UTF-8, tab-separated: `일본어<TAB>한국어<TAB>품사`).

The release workflow copies it into the package as `hdtrans-data/user/userdict.txt`. The engine reads it
automatically and reloads it when the file changes. When the file is absent, the release ships without a user dictionary.
