# Third-Party Notices

Sawmill is licensed under the Apache License 2.0 (see [LICENSE](LICENSE)).
Binary distributions of Sawmill statically link the open-source components
below. Each is used under its upstream licence; full licence texts ship with
each module's source (`LICENSE`/`COPYING` file in the module root, viewable
at `https://pkg.go.dev/<module>?tab=licenses`).

## Linked Go modules

| Module | Licence |
|---|---|
| dario.cat/mergo | BSD-3-Clause |
| github.com/ProtonMail/go-crypto | BSD-3-Clause |
| github.com/cloudflare/circl | BSD-3-Clause |
| github.com/cyphar/filepath-securejoin | BSD-3-Clause |
| github.com/dustin/go-humanize | MIT |
| github.com/emirpasic/gods | BSD-2-Clause |
| github.com/fsnotify/fsnotify | BSD-3-Clause |
| github.com/go-git/gcfg | BSD-3-Clause |
| github.com/go-git/go-billy/v5 | Apache-2.0 |
| github.com/go-git/go-git/v5 | Apache-2.0 |
| github.com/golang/groupcache | Apache-2.0 |
| github.com/google/jsonschema-go | MIT |
| github.com/google/uuid | BSD-3-Clause |
| github.com/jbenet/go-context | MIT |
| github.com/kevinburke/ssh_config | MIT |
| github.com/marcelocantos/claudia | Apache-2.0 |
| github.com/mark3labs/mcp-go | MIT |
| github.com/mattn/go-isatty | MIT |
| github.com/ncruces/go-strftime | MIT |
| github.com/odvcencio/gotreesitter | MIT (see grammar note below) |
| github.com/pjbgf/sha1cd | Apache-2.0 |
| github.com/pmezard/go-difflib | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | BSD-3-Clause |
| github.com/sergi/go-diff | MIT |
| github.com/skeema/knownhosts | Apache-2.0 (NOTICE reproduced below) |
| github.com/spf13/cast | MIT |
| github.com/xanzy/ssh-agent | Apache-2.0 |
| github.com/yosida95/uritemplate/v3 | BSD-3-Clause |
| golang.org/x/crypto | BSD-3-Clause |
| golang.org/x/net | BSD-3-Clause |
| golang.org/x/sys | BSD-3-Clause |
| gopkg.in/warnings.v0 | BSD-2-Clause |
| gopkg.in/yaml.v3 | MIT and Apache-2.0 |
| modernc.org/libc | BSD-3-Clause |
| modernc.org/libquickjs | BSD-3-Clause (bundles QuickJS, MIT — see below) |
| modernc.org/mathutil | BSD-3-Clause |
| modernc.org/memory | BSD-3-Clause |
| modernc.org/quickjs | BSD-3-Clause |
| modernc.org/sqlite | BSD-3-Clause (bundles SQLite, public domain) |

## Tree-sitter grammars

`github.com/odvcencio/gotreesitter` embeds parse tables derived from the
upstream tree-sitter grammar projects (tree-sitter-python, tree-sitter-go,
tree-sitter-rust, tree-sitter-typescript, tree-sitter-cpp, tree-sitter-java,
tree-sitter-c-sharp, tree-sitter-javascript, tree-sitter-ruby,
tree-sitter-php, tree-sitter-kotlin, tree-sitter-swift, tree-sitter-c, and
others). The upstream grammars are MIT-licensed (a small number are
Apache-2.0); the full source list with upstream repositories is recorded in
gotreesitter's `grammars/languages.manifest`. Copyright the respective
tree-sitter grammar authors.

## QuickJS

`modernc.org/libquickjs` bundles the QuickJS JavaScript engine,
Copyright (c) 2017-2021 Fabrice Bellard and Charlie Gordon, MIT licence.

## SQLite

`modernc.org/sqlite` is a Go translation of SQLite, which is in the
public domain.

## Reproduced NOTICE files

### github.com/skeema/knownhosts

```
Copyright 2025 Skeema LLC and the Skeema Knownhosts authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
