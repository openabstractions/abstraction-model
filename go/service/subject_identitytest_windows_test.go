//go:build oa_identitytest && windows

package service

import (
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/identitytest"
	"golang.org/x/sys/windows"
)

func TestSubjectIdentityDOSAliasCredential(t *testing.T) {
	path := subjectImage(t, filepath.Join(t.TempDir(), "subject program with spaces"))
	query := func(short bool) string {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		f := windows.GetLongPathName
		if short {
			f = windows.GetShortPathName
		}
		n, err := f(p, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 || n >= uint32(len(buf)) {
			t.Fatalf("path query: %d %v", n, err)
		}
		return windows.UTF16ToString(buf[:n])
	}
	short, canonical := query(true), query(false)
	if strings.EqualFold(short, canonical) {
		t.Fatal("mandatory DOS alias fixture requires a distinct existing 8.3 name")
	}
	peer := identitytest.New(t, identitytest.Facts{User: identity.User{Kind: "windows", SID: "fixture-owner"}, Path: short})[0]
	resolveSubjectCredential(t, peer, "fixture-owner", canonical)
}
