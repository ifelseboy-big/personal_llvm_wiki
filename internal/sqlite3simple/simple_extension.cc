// Derived from wangfenjin/simple v0.7.1, commit
// 4ed008934495fc55ff4bf6620bba58311988b23e.
//
// The upstream implementation is MIT licensed. This build intentionally omits
// Jieba, pinyin data, and highlight helpers because llm-wiki uses `simple 0`
// and `simple_query(..., 0)` exclusively.

#define SQLITE_CORE 1
#include <sqlite3ext.h>

#include <algorithm>
#include <cctype>
#include <cstdlib>
#include <cstring>
#include <new>
#include <string>
#include <vector>

namespace {

using TokenCallback = int (*)(void *, int, const char *, int, int, int);

enum class TokenCategory {
  Space,
  ASCIIAlphabetic,
  Digit,
  Other,
};

TokenCategory category_from_byte(unsigned char value) {
  if (value > 127) return TokenCategory::Other;
  if (std::isdigit(value)) return TokenCategory::Digit;
  if (std::isspace(value) || std::iscntrl(value)) return TokenCategory::Space;
  if (std::isalpha(value)) return TokenCategory::ASCIIAlphabetic;
  return TokenCategory::Other;
}

int utf8_sequence_length(unsigned char value, int remaining) {
  int length = 1;
  if (value >= 0xF0) {
    length = 4;
  } else if (value >= 0xE0) {
    length = 3;
  } else if (value >= 0xC0) {
    length = 2;
  }
  return std::min(length, remaining);
}

std::string quote_fts5(const std::string &value) {
  std::string quoted = "\"";
  for (char c : value) {
    quoted.push_back(c);
    if (c == '"') quoted.push_back('"');
  }
  quoted.push_back('"');
  return quoted;
}

struct QueryPart {
  std::string text;
  TokenCategory category;
};

std::vector<QueryPart> split_parts(const char *text, int text_length) {
  std::vector<QueryPart> parts;
  int start = 0;
  while (start < text_length) {
    TokenCategory category = category_from_byte(static_cast<unsigned char>(text[start]));
    int end = start;
    if (category == TokenCategory::Other) {
      end += utf8_sequence_length(static_cast<unsigned char>(text[end]), text_length - end);
    } else {
      ++end;
      while (end < text_length &&
             category_from_byte(static_cast<unsigned char>(text[end])) == category) {
        ++end;
      }
    }
    if (category != TokenCategory::Space) {
      std::string value(text + start, text + end);
      if (category == TokenCategory::ASCIIAlphabetic) {
        std::transform(value.begin(), value.end(), value.begin(),
                       [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
      }
      parts.push_back({std::move(value), category});
    }
    start = end;
  }
  return parts;
}

class SimpleTokenizer {
 public:
  SimpleTokenizer(const char **arguments, int argument_count) {
    // Keep upstream argument parsing so `tokenize='simple 0'` has the expected
    // contract. Pinyin support is intentionally not compiled into this binary.
    if (argument_count >= 1) (void)std::atoi(arguments[0]);
  }

  int tokenize(void *context, const char *text, int text_length, TokenCallback callback) const {
    int rc = SQLITE_OK;
    int start = 0;
    while (start < text_length && rc == SQLITE_OK) {
      TokenCategory category = category_from_byte(static_cast<unsigned char>(text[start]));
      int end = start;
      if (category == TokenCategory::Other) {
        end += utf8_sequence_length(static_cast<unsigned char>(text[end]), text_length - end);
      } else {
        ++end;
        while (end < text_length &&
               category_from_byte(static_cast<unsigned char>(text[end])) == category) {
          ++end;
        }
      }
      if (category != TokenCategory::Space) {
        std::string token(text + start, text + end);
        if (category == TokenCategory::ASCIIAlphabetic) {
          std::transform(token.begin(), token.end(), token.begin(),
                         [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
        }
        rc = callback(context, 0, token.c_str(), static_cast<int>(token.size()), start, end);
      }
      start = end;
    }
    return rc;
  }
};

int simple_create(void *, const char **arguments, int argument_count, Fts5Tokenizer **output) {
  auto *tokenizer = new (std::nothrow) SimpleTokenizer(arguments, argument_count);
  if (tokenizer == nullptr) return SQLITE_NOMEM;
  *output = reinterpret_cast<Fts5Tokenizer *>(tokenizer);
  return SQLITE_OK;
}

void simple_delete(Fts5Tokenizer *tokenizer) {
  delete reinterpret_cast<SimpleTokenizer *>(tokenizer);
}

int simple_tokenize(Fts5Tokenizer *tokenizer, void *context, int, const char *text,
                    int text_length, TokenCallback callback) {
  return reinterpret_cast<SimpleTokenizer *>(tokenizer)->tokenize(context, text, text_length, callback);
}

int fts5_api_from_db(sqlite3 *db, fts5_api **api) {
  sqlite3_stmt *statement = nullptr;
  *api = nullptr;
  int rc = sqlite3_prepare_v2(db, "SELECT fts5(?1)", -1, &statement, nullptr);
  if (rc == SQLITE_OK) {
    rc = sqlite3_bind_pointer(statement, 1, reinterpret_cast<void *>(api), "fts5_api_ptr", nullptr);
  }
  if (rc == SQLITE_OK) (void)sqlite3_step(statement);
  if (statement != nullptr) {
    int finalize_rc = sqlite3_finalize(statement);
    if (rc == SQLITE_OK) rc = finalize_rc;
  }
  return rc;
}

void simple_query(sqlite3_context *context, int value_count, sqlite3_value **values) {
  if (value_count < 1 || sqlite3_value_type(values[0]) == SQLITE_NULL) {
    sqlite3_result_null(context);
    return;
  }
  const char *text = reinterpret_cast<const char *>(sqlite3_value_text(values[0]));
  int flags = 1;
  if (value_count >= 2) flags = sqlite3_value_int(values[1]);
  (void)flags;  // Pinyin query expansion is intentionally disabled.

  std::vector<QueryPart> parts = split_parts(text, static_cast<int>(std::strlen(text)));
  std::string result;
  for (const QueryPart &part : parts) {
    if (!result.empty()) result.append(" AND ");
    if (part.category == TokenCategory::ASCIIAlphabetic) {
      result.append(part.text);
    } else {
      result.append(quote_fts5(part.text));
    }
    if (part.category != TokenCategory::Other) result.push_back('*');
  }
  sqlite3_result_text(context, result.c_str(), static_cast<int>(result.size()), SQLITE_TRANSIENT);
}

extern "C" int sqlite3_simple_init(sqlite3 *db, char **, const sqlite3_api_routines *) {
  int rc = sqlite3_create_function(db, "simple_query", -1,
                                   SQLITE_UTF8 | SQLITE_DETERMINISTIC, nullptr,
                                   &simple_query, nullptr, nullptr);
  if (rc != SQLITE_OK) return rc;

  fts5_api *api = nullptr;
  rc = fts5_api_from_db(db, &api);
  if (rc != SQLITE_OK) return rc;
  if (api == nullptr || api->iVersion < 2) return SQLITE_ERROR;

  fts5_tokenizer tokenizer = {simple_create, simple_delete, simple_tokenize};
  return api->xCreateTokenizer(api, "simple", api, &tokenizer, nullptr);
}

}  // namespace

extern "C" int llm_wiki_register_simple_auto_extension(void) {
  return sqlite3_auto_extension(
      reinterpret_cast<void (*)(void)>(sqlite3_simple_init));
}
