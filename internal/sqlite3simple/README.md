# Bundled simple tokenizer

This package statically registers the `simple` SQLite FTS5 tokenizer on every
`github.com/mattn/go-sqlite3` connection.

- Upstream: https://github.com/wangfenjin/simple
- Official release: `v0.7.1`
- Commit: `4ed008934495fc55ff4bf6620bba58311988b23e`
- License: MIT; see `LICENSE.simple`

The bundled build intentionally contains only the tokenizer and `simple_query`
paths needed by llm-wiki. Jieba, pinyin indexing/query expansion, custom pinyin
dictionaries, and highlight helpers are omitted. The configured forms are
always `tokenize='simple 0'` and `simple_query(..., 0)`.

This source-only static integration avoids a Homebrew/system SQLite dependency,
does not ship or load a user-selectable dynamic extension, and keeps release
artifacts self-contained for macOS Apple Silicon.
