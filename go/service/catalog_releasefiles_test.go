package service

import (
	"testing"

	"github.com/openabstractions/abstraction-model/go/releasefiles"
)

// Every file the compiled-in catalogue names is a release model file with the
// same digest and size. The runtime lets operators read exactly the release
// model files through the storage inventory, so Setup can find a copy of a
// catalogue entry already on this computer; an entry outside them would be
// hidden from that lookup, and the two lists could drift apart.
func TestEveryCatalogEntryIsAReleaseModelFile(t *testing.T) {
	c, err := parseCatalog(catalogDocument)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.entries) == 0 {
		t.Fatal("the catalogue has no entry")
	}
	files := map[string]int64{}
	for digest, size := range releasefiles.Files() {
		files["sha256:"+digest] = size
	}
	for id, e := range c.entries {
		if size, ok := files[e.Digest]; !ok || size != e.SizeBytes {
			t.Errorf("catalogue entry %s (%s, %d bytes) is not a release model file", id, e.Digest, e.SizeBytes)
		}
	}
}
