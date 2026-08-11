// Package sqlite3simple statically registers the bundled simple FTS5 tokenizer
// on every SQLite connection opened by github.com/mattn/go-sqlite3.
package sqlite3simple

/*
#cgo CXXFLAGS: -std=c++14 -Wno-deprecated-declarations

int llm_wiki_register_simple_auto_extension(void);
*/
import "C"

import (
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DriverName       = "sqlite3"
	TokenizerName    = "simple"
	TokenizerVersion = "v0.7.1"
	TokenizerCommit  = "4ed008934495fc55ff4bf6620bba58311988b23e"
)

var registrationErr error

func init() {
	if rc := int(C.llm_wiki_register_simple_auto_extension()); rc != 0 {
		registrationErr = fmt.Errorf("register simple SQLite auto-extension: error code %d", rc)
	}
}

func RegistrationError() error { return registrationErr }
