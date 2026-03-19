# Repo LVS Schema

`ndnd` consumes Light VerSec schemas in compiled binary `.tlv` form.
This repository does not include a native `.trust -> .tlv` compiler, so use
the helper script in this folder with `python-ndn`.

## Compile the repo schema

```bash
pip install python-ndn
python3 repo/config/compile_schema.py
```

This reads [schema.trust](/Users/0225n/Documents/UCLA/26Winter/ndnd/repo/config/schema.trust)
and writes `repo/config/schema.tlv`.

You can also choose explicit paths:

```bash
python3 repo/config/compile_schema.py repo/config/schema.trust -o repo/config/schema.tlv
```

The resulting `.tlv` file is the one you should reference from
[repo.sample.yml](/Users/0225n/Documents/UCLA/26Winter/ndnd/repo/repo.sample.yml)
via `schema_file`.
