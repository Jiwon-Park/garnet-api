# User dictionary

`userdict.txt.enc` is the user dictionary, encrypted with OpenSSL (AES-256-CBC, PBKDF2, 200000 iterations).
The release workflow decrypts it with the `USERDICT_PASSWORD` repository secret and writes it to
`hdtrans-data/user/userdict.txt`, where the engine reads it and reloads it when the file changes.

To update (the password is not stored in the repository):

    openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -in userdict.txt.enc -out userdict.txt
    # edit userdict.txt (UTF-8, tab-separated: 일본어<TAB>한국어<TAB>품사)
    openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -in userdict.txt -out userdict.txt.enc

Set the secret: GitHub repository Settings → Secrets and variables → Actions → New repository secret → `USERDICT_PASSWORD`.
